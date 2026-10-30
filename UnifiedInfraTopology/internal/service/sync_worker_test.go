package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/pkg/log"
	"github.com/glebarez/sqlite"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func TestSyncWorkerCheckpointRejectsInvalidPartition(t *testing.T) {
	logger := &log.Logger{Logger: zap.NewNop()}
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.AutoMigrate(&model.SyncCheckpoint{}))
	control := repository.NewInventoryRepository(repository.NewRepository(logger, db))
	for _, tc := range []struct {
		name      string
		resource  string
		partition int64
	}{
		{name: "negative interface partition", resource: "interfaces", partition: -1},
		{name: "partition on device", resource: "devices", partition: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := fmt.Sprintf(`{"version":1,"run_id":"run-1","next_offset":1,"last_instance_id":1,"last_partition":%d,"snapshot_at":"2026-10-29T00:00:00Z","total":1}`, tc.partition)
			stored := model.SyncCheckpoint{SourceID: tc.name, Resource: tc.resource, Cursor: cursor}
			require.NoError(t, db.Create(&stored).Error)
			_, err := control.LoadCheckpoint(context.Background(), stored.SourceID, stored.Resource)
			require.ErrorIs(t, err, repository.ErrInventoryConflict)
		})
	}
}

func TestSyncWorkerInterfaceCheckpointAdvancesAcrossPartitions(t *testing.T) {
	logger := &log.Logger{Logger: zap.NewNop()}
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.AutoMigrate(&model.Source{}, &model.SyncRun{}, &model.SyncCheckpoint{}))
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := now.Add(time.Minute)
	source := model.Source{ID: "source-1", Name: "test", AdapterKind: "cmdb", Enabled: true}
	run := model.SyncRun{ID: "run-1", SourceID: source.ID, Status: model.SyncRunStatusRunning, CreatedAt: now, StartedAt: &now, LeaseExpiresAt: &lease}
	require.NoError(t, db.Create(&source).Error)
	require.NoError(t, db.Create(&run).Error)
	control := repository.NewInventoryRepository(repository.NewRepository(logger, db))
	checkpoint, err := control.BeginCheckpoint(context.Background(), source.ID, "interfaces", run.ID, now, now)
	require.NoError(t, err)
	legacyJSON, err := json.Marshal(checkpoint.Cursor)
	require.NoError(t, err)
	require.NotContains(t, string(legacyJSON), "last_partition")
	cursor := checkpoint.Cursor
	cursor.NextOffset, cursor.Total, cursor.LastInstanceID, cursor.LastPartition = 1, 4, 901, 10
	_, err = control.AdvanceCheckpoint(context.Background(), source.ID, "interfaces", run.ID, cursor, now)
	require.NoError(t, err)
	cursor.NextOffset, cursor.LastInstanceID, cursor.LastPartition = 2, 1, 20
	checkpoint, err = control.AdvanceCheckpoint(context.Background(), source.ID, "interfaces", run.ID, cursor, now)
	require.NoError(t, err)
	require.Equal(t, cursor, checkpoint.Cursor)
	for _, tc := range []struct {
		name      string
		partition int64
		id        int64
		resource  string
	}{
		{name: "same partition decreasing", partition: 20, id: 1, resource: "interfaces"},
		{name: "partition regression", partition: 10, id: 1000, resource: "interfaces"},
		{name: "negative partition", partition: -1, id: 2, resource: "interfaces"},
		{name: "non-interface partition", partition: 30, id: 2, resource: "devices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := cursor
			invalid.NextOffset, invalid.LastInstanceID, invalid.LastPartition = 3, tc.id, tc.partition
			_, err := control.AdvanceCheckpoint(context.Background(), source.ID, tc.resource, run.ID, invalid, now)
			require.ErrorIs(t, err, repository.ErrInventoryConflict)
		})
	}
}

func TestSyncWorkerProcessClaimedRejectsUnownedRun(t *testing.T) {
	worker := newWorkerForTest(&fakeWorkerRuns{}, &fakeDeviceGraph{}, &fakeDeviceSource{}, time.Now())
	for _, run := range []*model.SyncRun{nil, {ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued}, {ID: "run-1", SourceID: "wrong-source", Status: model.SyncRunStatusRunning}} {
		require.Error(t, worker.ProcessClaimed(context.Background(), run, model.Source{ID: "source-1"}))
	}
}

type workerTransition struct {
	from      string
	to        string
	at        time.Time
	errorCode string
}

type checkpointAdvance struct {
	cursor repository.DeviceSyncCursor
	at     time.Time
}

type fakeWorkerRuns struct {
	run               *model.SyncRun
	source            *model.Source
	claimed           bool
	claimAt           time.Time
	cancelChecks      []bool
	transitions       []workerTransition
	transitionErrFrom string
	checkpoint        *repository.DeviceSyncCheckpoint
	beginErr          error
	advanceErr        error
	completeErr       error
	beginRunID        string
	beginAt           time.Time
	advances          []checkpointAdvance
	completeCalled    bool
	completeStatus    string
	completeAt        time.Time
	renewErr          error
	renewCalls        int
}

func (f *fakeWorkerRuns) ClaimNext(_ context.Context, at time.Time) (*model.SyncRun, *model.Source, error) {
	if f.claimed || f.run == nil || f.source == nil {
		return nil, nil, repository.ErrInventoryNoRun
	}
	f.claimed = true
	f.claimAt = at
	f.run.Status = model.SyncRunStatusRunning
	f.run.StartedAt = &at
	return f.run, f.source, nil
}

func (f *fakeWorkerRuns) TransitionRun(_ context.Context, runID, from, to string, at time.Time, errorCode string) (*model.SyncRun, error) {
	if f.transitionErrFrom == from {
		return nil, errors.New("transition failed")
	}
	if f.run == nil || f.run.ID != runID || f.run.Status != from {
		return nil, repository.ErrInventoryConflict
	}
	f.transitions = append(f.transitions, workerTransition{from: from, to: to, at: at, errorCode: errorCode})
	f.run.Status = to
	f.run.ErrorCode = errorCode
	return f.run, nil
}

func (f *fakeWorkerRuns) RenewRunLease(context.Context, string, time.Time) error {
	f.renewCalls++
	return f.renewErr
}

func (f *fakeWorkerRuns) RunCancellationRequested(context.Context, string) (bool, error) {
	if len(f.cancelChecks) == 0 {
		return false, nil
	}
	requested := f.cancelChecks[0]
	f.cancelChecks = f.cancelChecks[1:]
	return requested, nil
}

func (f *fakeWorkerRuns) BeginCheckpoint(_ context.Context, sourceID, resource, runID string, startedAt, _ time.Time) (*repository.DeviceSyncCheckpoint, error) {
	f.beginRunID = runID
	f.beginAt = startedAt
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	if f.checkpoint == nil {
		f.checkpoint = &repository.DeviceSyncCheckpoint{
			SourceID: sourceID,
			Resource: resource,
			Cursor: repository.DeviceSyncCursor{
				Version: repository.DeviceSyncCursorVersion, RunID: runID, SnapshotAt: startedAt,
			},
		}
	}
	checkpoint := *f.checkpoint
	if checkpoint.Complete || checkpoint.Cursor.RunID != runID {
		checkpoint.Cursor = repository.DeviceSyncCursor{
			Version: repository.DeviceSyncCursorVersion, RunID: runID, SnapshotAt: startedAt,
		}
		checkpoint.Complete = false
		checkpoint.CompletedAt = nil
	} else {
		checkpoint.Cursor.RunID = runID
	}
	f.checkpoint = &checkpoint
	return &checkpoint, nil
}

func (f *fakeWorkerRuns) AdvanceCheckpoint(_ context.Context, _, _, runID string, cursor repository.DeviceSyncCursor, updatedAt time.Time) (*repository.DeviceSyncCheckpoint, error) {
	if f.advanceErr != nil {
		return nil, f.advanceErr
	}
	requireRunID := runID
	if cursor.RunID != requireRunID {
		return nil, repository.ErrInventoryConflict
	}
	f.advances = append(f.advances, checkpointAdvance{cursor: cursor, at: updatedAt})
	checkpoint := &repository.DeviceSyncCheckpoint{SourceID: f.source.ID, Resource: "devices", Cursor: cursor}
	f.checkpoint = checkpoint
	return checkpoint, nil
}

func (f *fakeWorkerRuns) CompleteCheckpointAndRun(_ context.Context, _, _, runID, expectedStatus string, completedAt time.Time) (*model.SyncRun, error) {
	f.completeCalled = true
	f.completeStatus = expectedStatus
	f.completeAt = completedAt
	if f.completeErr != nil {
		return nil, f.completeErr
	}
	if f.run == nil || f.run.ID != runID || f.run.Status != expectedStatus {
		return nil, repository.ErrInventoryConflict
	}
	f.run.Status = model.SyncRunStatusSucceeded
	return f.run, nil
}

type fakeDevicePage struct {
	offset int
	page   adapter.DevicePage
	err    error
}

type fakeDeviceSource struct {
	pages           []fakeDevicePage
	originalPages   []fakeDevicePage
	validationPages []fakeDevicePage
	completedPasses int
	validateErr     error
	offsets         []int
	selected        model.EntityType
	selectionErr    error
	filtered        bool
	coverageErr     error
	coverageChecks  int
	coverageChanged bool
	entityPages     []adapter.EntityPage
	entityReplay    []adapter.EntityPage
	entityPass      int
	passOpenCalls   int
	passOpenErrAt   int
	passPageHook    func(context.Context) error
	passOpenHook    func(context.Context, int) error
}

func (f *fakeDeviceSource) SelectedEntity(model.Source) (model.EntityType, error) {
	if f.selected == "" {
		return model.EntityDevice, f.selectionErr
	}
	return f.selected, f.selectionErr
}

func (f *fakeDeviceSource) CoversFullSource(model.Source, model.EntityType) (bool, error) {
	f.coverageChecks++
	return !f.filtered && !(f.coverageChanged && f.coverageChecks > 1), f.coverageErr
}

func (f *fakeDeviceSource) CollectEntityPage(_ context.Context, _ model.Source, entity model.EntityType, offset int) (adapter.EntityPage, error) {
	f.offsets = append(f.offsets, offset)
	if entity != f.selected || len(f.entityPages) == 0 {
		return adapter.EntityPage{}, errors.New("unexpected entity collection")
	}
	page := f.entityPages[0]
	f.entityPages = f.entityPages[1:]
	if page.Done && f.entityPass == 0 {
		f.entityPages = append([]adapter.EntityPage(nil), f.entityReplay...)
		f.entityPass++
	}
	return page, nil
}

type fakeEntityPass struct {
	source *fakeDeviceSource
	pages  []adapter.EntityPage
}

func (f *fakeDeviceSource) OpenEntityPass(ctx context.Context, _ model.Source, entity model.EntityType) (adapter.EntityPageStream, error) {
	f.passOpenCalls++
	if f.passOpenHook != nil {
		if err := f.passOpenHook(ctx, f.passOpenCalls); err != nil {
			return nil, err
		}
	}
	if entity != model.EntityInterface || f.passOpenCalls == f.passOpenErrAt {
		return nil, errors.New("manifest construction failed")
	}
	pages := f.entityPages
	if f.passOpenCalls > 1 {
		pages = f.entityReplay
	}
	return &fakeEntityPass{source: f, pages: append([]adapter.EntityPage(nil), pages...)}, nil
}

func (p *fakeEntityPass) Page(ctx context.Context, offset int) (adapter.EntityPage, error) {
	p.source.offsets = append(p.source.offsets, offset)
	if p.source.passPageHook != nil {
		if err := p.source.passPageHook(ctx); err != nil {
			return adapter.EntityPage{}, err
		}
	}
	if len(p.pages) == 0 {
		return adapter.EntityPage{}, errors.New("unexpected pass page")
	}
	page := p.pages[0]
	p.pages = p.pages[1:]
	return page, nil
}

func (f *fakeDeviceSource) Validate(context.Context, model.Source) error { return f.validateErr }

func (f *fakeDeviceSource) CollectDevicePage(_ context.Context, _ model.Source, offset int) (adapter.DevicePage, error) {
	f.offsets = append(f.offsets, offset)
	if f.originalPages == nil {
		f.originalPages = append([]fakeDevicePage(nil), f.pages...)
	}
	if len(f.pages) == 0 {
		return adapter.DevicePage{Done: true}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	if page.offset != offset {
		return adapter.DevicePage{}, fmt.Errorf("unexpected offset: got %d want %d", offset, page.offset)
	}
	if page.err == nil && page.page.Done && f.completedPasses == 0 {
		replay := f.originalPages
		if f.validationPages != nil {
			replay = f.validationPages
		}
		f.pages = append([]fakeDevicePage(nil), replay...)
		f.completedPasses++
	}
	return page.page, page.err
}

type fakeDeviceGraph struct {
	mu            sync.Mutex
	vertices      []repository.TopologyVertex
	upsertErrAt   int
	upsertCalls   int
	activeWrites  int
	maxActive     int
	writeDelay    time.Duration
	cleanupErr    error
	cleanup       bool
	cleanupID     string
	cleanupType   model.EntityType
	cleanupAt     time.Time
	ownerMissing  bool
	ownerReadErr  error
	ownerLookups  []model.EntityIdentity
	relations     []model.Relation
	relationCalls int
	relationErrAt int
}

func (f *fakeDeviceGraph) VertexExists(_ context.Context, identity model.EntityIdentity) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ownerLookups = append(f.ownerLookups, identity)
	return !f.ownerMissing, f.ownerReadErr
}

func (f *fakeDeviceGraph) UpsertRelation(_ context.Context, relation model.Relation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relationCalls++
	if f.relationErrAt > 0 && f.relationCalls == f.relationErrAt {
		return errors.New("relation write failed")
	}
	if err := model.ValidateRelation(relation); err != nil {
		return err
	}
	for _, vertex := range f.vertices {
		if vertex.Identity == relation.Identity.To {
			f.relations = append(f.relations, relation)
			return nil
		}
	}
	return errors.New("interface vertex was not written before relation")
}

func (f *fakeDeviceGraph) UpsertVertex(ctx context.Context, vertex repository.TopologyVertex) error {
	f.mu.Lock()
	f.upsertCalls++
	call := f.upsertCalls
	f.activeWrites++
	if f.activeWrites > f.maxActive {
		f.maxActive = f.activeWrites
	}
	f.mu.Unlock()

	if f.writeDelay > 0 {
		select {
		case <-ctx.Done():
			f.finishWrite()
			return ctx.Err()
		case <-time.After(f.writeDelay):
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeWrites--
	if f.upsertErrAt > 0 && call == f.upsertErrAt {
		return errors.New("graph write failed")
	}
	f.vertices = append(f.vertices, vertex)
	return nil
}

func (f *fakeDeviceGraph) finishWrite() {
	f.mu.Lock()
	f.activeWrites--
	f.mu.Unlock()
}

func (f *fakeDeviceGraph) DeleteOrphanVerticesNotSeen(_ context.Context, sourceID string, entityType model.EntityType, at time.Time) error {
	f.cleanup = true
	f.cleanupID = sourceID
	f.cleanupType = entityType
	f.cleanupAt = at
	return f.cleanupErr
}

func newWorkerForTest(runs *fakeWorkerRuns, graph *fakeDeviceGraph, source *fakeDeviceSource, now time.Time) *SyncWorker {
	return newWorkerWithConfigForTest(nil, runs, graph, source, now)
}

func newWorkerWithConfigForTest(conf *viper.Viper, runs *fakeWorkerRuns, graph *fakeDeviceGraph, source *fakeDeviceSource, now time.Time) *SyncWorker {
	worker := NewSyncWorker(conf, runs, graph, source, &log.Logger{Logger: zap.NewNop()})
	worker.now = func() time.Time { return now }
	return worker
}

func devicePage(offset, nextOffset, total int, done bool, records ...adapter.DeviceRecord) fakeDevicePage {
	return fakeDevicePage{offset: offset, page: adapter.DevicePage{Records: records, NextOffset: nextOffset, Total: total, Done: done}}
}

func TestSyncWorkerProcessNextPublishesPagedDeviceSnapshot(t *testing.T) {
	startedAt := time.Date(2026, 9, 22, 8, 9, 10, 123456789, time.FixedZone("test", 8*60*60))
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	graph := &fakeDeviceGraph{}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 1, 2, false, adapter.DeviceRecord{SourceInstanceID: 11, DeviceSN: "sn-1", Name: "device-1"}),
		devicePage(1, 2, 2, true, adapter.DeviceRecord{SourceInstanceID: 12, DeviceSN: "sn-2", Name: "device-2"}),
	}}

	processed, err := newWorkerForTest(runs, graph, source, startedAt).ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)

	wantAt := startedAt.UTC().Truncate(time.Microsecond)
	require.Equal(t, wantAt, runs.claimAt)
	require.Equal(t, "run-1", runs.beginRunID)
	require.Equal(t, wantAt, runs.beginAt)
	require.Equal(t, []int{0, 1, 0, 1}, source.offsets)
	require.Equal(t, []repository.DeviceSyncCursor{
		{Version: repository.DeviceSyncCursorVersion, RunID: "run-1", NextOffset: 1, LastInstanceID: 11, SnapshotAt: wantAt, Total: 2},
		{Version: repository.DeviceSyncCursorVersion, RunID: "run-1", NextOffset: 2, LastInstanceID: 12, SnapshotAt: wantAt, Total: 2},
	}, []repository.DeviceSyncCursor{runs.advances[0].cursor, runs.advances[1].cursor})
	require.Equal(t, []workerTransition{
		{from: model.SyncRunStatusRunning, to: model.SyncRunStatusValidating, at: wantAt},
		{from: model.SyncRunStatusValidating, to: model.SyncRunStatusPublishing, at: wantAt},
	}, runs.transitions)
	require.True(t, runs.completeCalled)
	require.Equal(t, model.SyncRunStatusPublishing, runs.completeStatus)
	require.Len(t, graph.vertices, 2)
	require.ElementsMatch(t, []string{"sn-1", "sn-2"}, []string{graph.vertices[0].Identity.StableID, graph.vertices[1].Identity.StableID})
	require.True(t, graph.cleanup)
	require.Equal(t, "source-1", graph.cleanupID)
	require.Equal(t, model.EntityDevice, graph.cleanupType)
	require.Equal(t, wantAt, graph.cleanupAt)
}

func TestSyncWorkerRejectsChangedIdentitySetBeforeCleanup(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	source := &fakeDeviceSource{
		pages: []fakeDevicePage{devicePage(0, 2, 2, true,
			adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"},
			adapter.DeviceRecord{SourceInstanceID: 2, DeviceSN: "sn-2"})},
		validationPages: []fakeDevicePage{devicePage(0, 2, 2, true,
			adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"},
			adapter.DeviceRecord{SourceInstanceID: 3, DeviceSN: "sn-3"})},
	}
	graph := &fakeDeviceGraph{}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.EqualError(t, err, syncErrorDeviceCollection)
	require.True(t, processed)
	require.False(t, graph.cleanup)
	require.False(t, runs.completeCalled)
	require.Equal(t, []workerTransition{
		{from: model.SyncRunStatusRunning, to: model.SyncRunStatusValidating, at: now},
		{from: model.SyncRunStatusValidating, to: model.SyncRunStatusFailed, at: now, errorCode: syncErrorDeviceCollection},
	}, runs.transitions)
}

func TestSyncWorkerRejectsDuplicateDeviceIdentityBeforeWriting(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{devicePage(0, 2, 2, true,
		adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "duplicate-sn"},
		adapter.DeviceRecord{SourceInstanceID: 2, DeviceSN: "duplicate-sn"})}}
	graph := &fakeDeviceGraph{}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.EqualError(t, err, syncErrorDeviceCollection)
	require.True(t, processed)
	require.Empty(t, graph.vertices)
	require.False(t, graph.cleanup)
}

func TestSyncWorkerRenewsLeaseDuringLongPage(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 1, 1, true, adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"}),
	}}
	worker := newWorkerForTest(runs, &fakeDeviceGraph{writeDelay: 20 * time.Millisecond}, source, now)
	worker.leaseRenewInterval = time.Millisecond

	processed, err := worker.ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Positive(t, runs.renewCalls)
}

func TestSyncWorkerStopsBeforeCleanupWhenLeaseRenewalFails(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:      &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source:   &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
		renewErr: errors.New("lease update failed"),
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 1, 1, true, adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"}),
	}}
	graph := &fakeDeviceGraph{writeDelay: 20 * time.Millisecond}
	worker := newWorkerForTest(runs, graph, source, now)
	worker.leaseRenewInterval = time.Millisecond

	processed, err := worker.ProcessNext(context.Background())
	require.ErrorIs(t, err, errSyncLeaseLost)
	require.True(t, processed)
	require.False(t, graph.cleanup)
	require.False(t, runs.completeCalled)
}

func TestSyncWorkerRejectsNonCMDBSourceBeforeCollection(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "other", ConfigRef: "test", Enabled: true},
	}
	source := &fakeDeviceSource{}

	processed, err := newWorkerForTest(runs, &fakeDeviceGraph{}, source, now).ProcessNext(context.Background())
	require.EqualError(t, err, syncErrorSourceValidation)
	require.True(t, processed)
	require.Empty(t, source.offsets)
	require.Equal(t, []workerTransition{{
		from: model.SyncRunStatusRunning, to: model.SyncRunStatusFailed, at: now, errorCode: syncErrorSourceValidation,
	}}, runs.transitions)
}

func TestSyncWorkerWriteFailureDoesNotCleanOldDevices(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	graph := &fakeDeviceGraph{upsertErrAt: 1, writeDelay: time.Millisecond}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 2, 2, true,
			adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"},
			adapter.DeviceRecord{SourceInstanceID: 2, DeviceSN: "sn-2"}),
	}}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.Error(t, err)
	require.True(t, processed)
	require.Empty(t, runs.advances)
	require.False(t, graph.cleanup)
	require.False(t, runs.completeCalled)
	require.Equal(t, []workerTransition{{
		from: model.SyncRunStatusRunning, to: model.SyncRunStatusFailed, at: now, errorCode: syncErrorGraphWrite,
	}}, runs.transitions)
}

func TestSyncWorkerCollectionFailureDoesNotCleanOldDevices(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	graph := &fakeDeviceGraph{}
	source := &fakeDeviceSource{pages: []fakeDevicePage{{offset: 0, err: errors.New("upstream failed")}}}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.Error(t, err)
	require.True(t, processed)
	require.False(t, graph.cleanup)
	require.Equal(t, syncErrorDeviceCollection, runs.transitions[0].errorCode)
	require.Equal(t, model.SyncRunStatusFailed, runs.transitions[0].to)
}

func TestSyncWorkerCancellationStopsBeforeCleanup(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:          &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source:       &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
		cancelChecks: []bool{false, false, true},
	}
	graph := &fakeDeviceGraph{}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 1, 2, false, adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"}),
		devicePage(1, 2, 2, true, adapter.DeviceRecord{SourceInstanceID: 2, DeviceSN: "sn-2"}),
	}}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Len(t, graph.vertices, 1)
	require.Empty(t, runs.advances)
	require.False(t, graph.cleanup)
	require.False(t, runs.completeCalled)
	require.Equal(t, []workerTransition{{
		from: model.SyncRunStatusRunning, to: model.SyncRunStatusCanceled, at: now,
	}}, runs.transitions)
}

func TestSyncWorkerRestartsIncompleteCheckpointFromBeginning(t *testing.T) {
	startedAt := time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC)
	oldSnapshotAt := startedAt.Add(-time.Hour)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-2", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
		checkpoint: &repository.DeviceSyncCheckpoint{SourceID: "source-1", Resource: "devices", Cursor: repository.DeviceSyncCursor{
			Version: repository.DeviceSyncCursorVersion, RunID: "old-run", NextOffset: 20, LastInstanceID: 100, SnapshotAt: oldSnapshotAt, Total: 21,
		}},
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 1, 1, true, adapter.DeviceRecord{SourceInstanceID: 101, DeviceSN: "sn-101"}),
	}}
	graph := &fakeDeviceGraph{}

	processed, err := newWorkerForTest(runs, graph, source, startedAt).ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, []int{0, 0}, source.offsets)
	require.Equal(t, startedAt, graph.vertices[0].SyncedAt)
	require.Equal(t, startedAt, graph.cleanupAt)
	require.Equal(t, startedAt, runs.advances[0].cursor.SnapshotAt)
	require.Equal(t, 1, runs.advances[0].cursor.NextOffset)
}

func TestSyncWorkerRejectsCrossPageNonIncreasingInstanceID(t *testing.T) {
	now := time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
		checkpoint: &repository.DeviceSyncCheckpoint{SourceID: "source-1", Resource: "devices", Cursor: repository.DeviceSyncCursor{
			Version: repository.DeviceSyncCursorVersion, RunID: "run-1", NextOffset: 10, LastInstanceID: 100, SnapshotAt: now, Total: 11,
		}},
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(10, 11, 11, true, adapter.DeviceRecord{SourceInstanceID: 100, DeviceSN: "sn-100"}),
	}}
	graph := &fakeDeviceGraph{}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.EqualError(t, err, syncErrorDeviceCollection)
	require.True(t, processed)
	require.Empty(t, graph.vertices)
	require.Empty(t, runs.advances)
	require.False(t, graph.cleanup)
	require.Equal(t, syncErrorDeviceCollection, runs.transitions[0].errorCode)
}

func TestSyncWorkerGraphWritesHonorConfiguredConcurrencyCap(t *testing.T) {
	now := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	records := make([]adapter.DeviceRecord, 64)
	for i := range records {
		records[i] = adapter.DeviceRecord{SourceInstanceID: int64(i + 1), DeviceSN: fmt.Sprintf("sn-%d", i+1)}
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{devicePage(0, 64, 64, true, records...)}}
	graph := &fakeDeviceGraph{writeDelay: 5 * time.Millisecond}
	conf := viper.New()
	conf.Set("inventory.worker.device_write_concurrency", 100)

	processed, err := newWorkerWithConfigForTest(conf, runs, graph, source, now).ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 16, graph.maxActive)
}

func TestNewSyncWorkerDefaultsAndClampsDeviceWriteConcurrency(t *testing.T) {
	runs := &fakeWorkerRuns{}
	graph := &fakeDeviceGraph{}
	source := &fakeDeviceSource{}
	logger := &log.Logger{Logger: zap.NewNop()}

	require.Equal(t, 8, NewSyncWorker(nil, runs, graph, source, logger).deviceWriteConcurrency)
	conf := viper.New()
	conf.Set("inventory.worker.device_write_concurrency", -1)
	require.Equal(t, 1, NewSyncWorker(conf, runs, graph, source, logger).deviceWriteConcurrency)
	conf.Set("inventory.worker.device_write_concurrency", 99)
	require.Equal(t, 16, NewSyncWorker(conf, runs, graph, source, logger).deviceWriteConcurrency)
}

func TestSyncWorkerCheckpointFailureDoesNotCleanOldDevices(t *testing.T) {
	now := time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:        &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source:     &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
		advanceErr: errors.New("checkpoint unavailable"),
	}
	source := &fakeDeviceSource{pages: []fakeDevicePage{
		devicePage(0, 1, 1, true, adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "sn-1"}),
	}}
	graph := &fakeDeviceGraph{}

	processed, err := newWorkerForTest(runs, graph, source, now).ProcessNext(context.Background())
	require.EqualError(t, err, syncErrorCheckpoint)
	require.True(t, processed)
	require.Empty(t, runs.advances)
	require.False(t, graph.cleanup)
	require.Equal(t, syncErrorCheckpoint, runs.transitions[0].errorCode)
}

func TestSyncWorkerCleanupFailureMarksPublishingRunFailed(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	runs := &fakeWorkerRuns{
		run:    &model.SyncRun{ID: "run-1", SourceID: "source-1", Status: model.SyncRunStatusQueued},
		source: &model.Source{ID: "source-1", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true},
	}
	graph := &fakeDeviceGraph{cleanupErr: errors.New("cleanup failed")}

	processed, err := newWorkerForTest(runs, graph, &fakeDeviceSource{}, now).ProcessNext(context.Background())
	require.Error(t, err)
	require.True(t, processed)
	require.Equal(t, []workerTransition{
		{from: model.SyncRunStatusRunning, to: model.SyncRunStatusValidating, at: now},
		{from: model.SyncRunStatusValidating, to: model.SyncRunStatusPublishing, at: now},
		{from: model.SyncRunStatusPublishing, to: model.SyncRunStatusFailed, at: now, errorCode: syncErrorGraphCleanup},
	}, runs.transitions)
}

func TestSyncWorkerDeviceViewVerticalFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/pub/api/v3/find/instance/object/device_view", r.URL.Path)
		fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":101,"device_id":101,"device_sn":"SN-101","device_name":"device-101","parent_type_id":52,"device_type_id":57,"role":"server","eth_ip":["10.0.0.1"]}]}}`)
	}))
	defer server.Close()

	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.page_size", 2)
	logger := &log.Logger{Logger: zap.NewNop()}
	db, err := gorm.Open(sqlite.Open("file:sync-worker-vertical?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.AutoMigrate(&model.Source{}, &model.SyncRun{}, &model.SyncCheckpoint{}))

	source := model.Source{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "test", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true}
	run := model.SyncRun{
		ID: "11111111111111111111111111111111", SourceID: source.ID, Status: model.SyncRunStatusQueued,
		CreatedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, db.Create(&source).Error)
	require.NoError(t, db.Create(&run).Error)

	control := repository.NewInventoryRepository(repository.NewRepository(logger, db))
	graph := &fakeDeviceGraph{}
	worker := NewSyncWorker(conf, control, graph, adapter.NewCMDBWithClient(conf, server.Client()), logger)
	worker.now = func() time.Time { return time.Date(2026, 9, 22, 8, 9, 10, 123456789, time.FixedZone("test", 8*60*60)) }

	processed, err := worker.ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	stored, err := control.Run(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, model.SyncRunStatusSucceeded, stored.Status)
	require.Empty(t, stored.ErrorCode)
	require.Len(t, graph.vertices, 1)
	require.Equal(t, "SN-101", graph.vertices[0].Identity.StableID)
	require.Equal(t, []string{"10.0.0.1"}, graph.vertices[0].Properties["all_ips"])
	require.True(t, graph.cleanup)
}

func TestSyncWorkerSelectedEntityVerticalFlow(t *testing.T) {
	for _, entity := range []model.EntityType{model.EntityDataCenter, model.EntityCabinet, model.EntityInterface, model.EntityTransformer} {
		for _, filtered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/filtered=%t", entity, filtered), func(t *testing.T) {
				object, resource := "idc", "data_centers"
				if entity == model.EntityCabinet {
					object, resource = "cabinet", "cabinets"
				} else if entity == model.EntityInterface {
					object, resource = "port_view", "interfaces"
				} else if entity == model.EntityTransformer {
					object, resource = "idc_transformer", "transformers"
				}
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Condition map[string]any `json:"condition"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					requests++
					if entity == model.EntityInterface && r.URL.Path == "/pub/api/v3/find/instance/object/device_view" {
						condition := map[string]any{"idc_id": float64(17), "parent_type_id": float64(54)}
						if filtered {
							condition["device_sn"] = "SN-101"
						}
						require.Equal(t, condition, body.Condition)
						fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":201,"device_sn":"SN-101","parent_type_id":54}]}}`)
						return
					}
					require.Equal(t, "/pub/api/v3/find/instance/object/"+object, r.URL.Path)
					if entity == model.EntityInterface {
						require.Equal(t, map[string]any{"idc_id": float64(17), "sn": "SN-101", "port_type": "physics"}, body.Condition)
					} else {
						require.Equal(t, filtered, len(body.Condition) > 0)
					}
					if entity == model.EntityInterface {
						fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":101,"uuid":"VIEW-101","port_uuid":"UUID-101","port_name":"Ethernet1","port_type":"physics","port_speed":10000,"port_operation_status":1,"port_group_name":"uplink","group_type":3,"port_group_type":6,"group_subtype":0}]}}`)
					} else if entity == model.EntityTransformer {
						fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":101,"uuid":"UUID-101","code":"code-101","idc":17,"standby_transformer":102,"transformer_brand":"brand","transformer_type":"model","transformer_capacity":2500}]}}`)
					} else {
						fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":101,"uuid":"UUID-101","code":"code-101","cn_name":"IDC-101","idc_id":17,"pod_ids":[3,2,3]}]}}`)
					}
				}))
				defer server.Close()
				conf := viper.New()
				conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
				conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
				conf.Set("inventory.cmdb.sources.test.page_size", 2)
				conf.Set("inventory.cmdb.sources.test.entity_type", string(entity))
				if entity == model.EntityInterface {
					condition := map[string]any{"idc_id": 17}
					if filtered {
						condition["sn"] = "SN-101"
					}
					conf.Set("inventory.cmdb.sources.test.interface_condition", condition)
				}
				if filtered && entity != model.EntityInterface {
					key := "idc_id"
					if entity == model.EntityDataCenter {
						key = "inst_id"
					} else if entity == model.EntityTransformer {
						key = "idc"
					}
					conf.Set("inventory.cmdb.sources.test."+string(entity)+"_condition", map[string]any{key: 17})
				}
				core, observed := observer.New(zap.InfoLevel)
				logger := &log.Logger{Logger: zap.New(core)}
				db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				defer sqlDB.Close()
				require.NoError(t, db.AutoMigrate(&model.Source{}, &model.SyncRun{}, &model.SyncCheckpoint{}))
				source := model.Source{ID: strings.Repeat("a", 32), Name: "test", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true}
				run := model.SyncRun{ID: strings.Repeat("1", 32), SourceID: source.ID, Status: model.SyncRunStatusQueued, CreatedAt: time.Now()}
				require.NoError(t, db.Create(&source).Error)
				require.NoError(t, db.Create(&run).Error)
				control := repository.NewInventoryRepository(repository.NewRepository(logger, db))
				graph := &fakeDeviceGraph{}
				worker := NewSyncWorker(conf, control, graph, adapter.NewCMDBWithClient(conf, server.Client()), logger)
				now := time.Now().UTC().Truncate(time.Microsecond)
				worker.now = func() time.Time { return now }
				processed, err := worker.ProcessNext(context.Background())
				require.NoError(t, err)
				require.True(t, processed)
				if entity == model.EntityInterface {
					require.Equal(t, 6, requests)
				} else {
					require.Equal(t, 2, requests)
				}
				stored, err := control.Run(context.Background(), run.ID)
				require.NoError(t, err)
				require.Equal(t, model.SyncRunStatusSucceeded, stored.Status)
				require.Nil(t, stored.LeaseExpiresAt)
				var checkpoint model.SyncCheckpoint
				require.NoError(t, db.Where("source_id = ? AND resource = ?", source.ID, resource).First(&checkpoint).Error)
				require.Len(t, graph.vertices, 1)
				vertex := graph.vertices[0]
				require.Equal(t, model.EntityIdentity{SourceID: source.ID, EntityType: entity, StableID: "UUID-101"}, vertex.Identity)
				if entity == model.EntityInterface {
					var cursor repository.DeviceSyncCursor
					require.NoError(t, json.Unmarshal([]byte(checkpoint.Cursor), &cursor))
					require.Equal(t, int64(201), cursor.LastPartition)
					require.Equal(t, int64(101), cursor.LastInstanceID)
					require.Equal(t, "Ethernet1", vertex.Properties["port_name"])
					require.Equal(t, "uplink", vertex.Properties["group_name"])
					require.Equal(t, "10000", vertex.Properties["port_speed"])
					require.Equal(t, "1", vertex.Properties["port_operation_status"])
					require.NotContains(t, vertex.Properties, "port_uuid")
					require.NotContains(t, vertex.Properties, "owner_device_sn")
					require.Equal(t, []model.EntityIdentity{{SourceID: source.ID, EntityType: model.EntityDevice, StableID: "SN-101"}}, graph.ownerLookups)
					require.Len(t, graph.relations, 1)
					require.Equal(t, model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: graph.ownerLookups[0], To: vertex.Identity}, graph.relations[0].Identity)
					require.Equal(t, now, graph.relations[0].CreatedAt)
					require.Equal(t, now, graph.relations[0].SyncedAt)
				} else {
					require.Empty(t, graph.ownerLookups)
					require.Empty(t, graph.relations)
					require.Equal(t, "code-101", vertex.Properties["code"])
				}
				require.NotContains(t, vertex.Properties, "uuid")
				require.Equal(t, now, vertex.CreatedAt)
				require.Equal(t, now, vertex.SyncedAt)
				if entity == model.EntityCabinet {
					require.Equal(t, int64(17), vertex.Properties["idc_id"])
					require.Equal(t, []int64{2, 3}, vertex.Properties["pod_ids"])
				} else if entity == model.EntityDataCenter {
					require.Equal(t, "IDC-101", vertex.Properties["cn_name"])
				}
				wantCleanup := !filtered && entity != model.EntityInterface
				require.Equal(t, wantCleanup, graph.cleanup)
				if !wantCleanup {
					require.NotEmpty(t, observed.FilterMessage("sync orphan cleanup skipped").All())
				} else {
					require.Equal(t, entity, graph.cleanupType)
					require.Equal(t, now, graph.cleanupAt)
				}
			})
		}
	}
}

func TestSyncWorkerSequentialEntitiesPreserveSourceCheckpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, []string{"/pub/api/v3/find/instance/object/idc", "/pub/api/v3/find/instance/object/cabinet"}, r.URL.Path)
		fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[{"inst_id":101,"uuid":"UUID-101","code":"code-101"}]}}`)
	}))
	defer server.Close()
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"base_url", server.URL)
	conf.Set(prefix+"allow_insecure_http", true)
	conf.Set(prefix+"data_center_condition", map[string]any{"inst_id": 101})
	conf.Set(prefix+"cabinet_condition", map[string]any{"idc_id": 101})
	logger := &log.Logger{Logger: zap.NewNop()}
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.AutoMigrate(&model.Source{}, &model.SyncRun{}, &model.SyncCheckpoint{}))
	source := model.Source{ID: strings.Repeat("a", 32), Name: "test", AdapterKind: "cmdb", ConfigRef: "test", Enabled: true}
	require.NoError(t, db.Create(&source).Error)
	control := repository.NewInventoryRepository(repository.NewRepository(logger, db))
	graph := &fakeDeviceGraph{}
	worker := NewSyncWorker(conf, control, graph, adapter.NewCMDBWithClient(conf, server.Client()), logger)
	now := time.Now().UTC().Truncate(time.Microsecond)
	worker.now = func() time.Time { return now }
	var previousCursor string
	for index, entity := range []model.EntityType{model.EntityDataCenter, model.EntityCabinet} {
		conf.Set(prefix+"entity_type", string(entity))
		run := model.SyncRun{ID: strings.Repeat(fmt.Sprint(index+1), 32), SourceID: source.ID, Status: model.SyncRunStatusQueued, CreatedAt: now}
		require.NoError(t, db.Create(&run).Error)
		processed, err := worker.ProcessNext(context.Background())
		require.NoError(t, err)
		require.True(t, processed)
		stored, err := control.Run(context.Background(), run.ID)
		require.NoError(t, err)
		require.Equal(t, model.SyncRunStatusSucceeded, stored.Status)
		require.Nil(t, stored.LeaseExpiresAt)
		var checkpoint model.SyncCheckpoint
		require.NoError(t, db.Where("source_id = ? AND resource = ?", source.ID, "data_centers").First(&checkpoint).Error)
		require.True(t, checkpoint.Complete)
		if index == 0 {
			previousCursor = checkpoint.Cursor
		} else {
			require.Equal(t, previousCursor, checkpoint.Cursor)
		}
		now = now.Add(time.Minute)
	}
	var checkpoints []model.SyncCheckpoint
	require.NoError(t, db.Where("source_id = ?", source.ID).Find(&checkpoints).Error)
	require.Len(t, checkpoints, 2)
	for _, checkpoint := range checkpoints {
		require.True(t, checkpoint.Complete)
	}
	require.Len(t, graph.vertices, 2)
	require.ElementsMatch(t, []model.EntityType{model.EntityDataCenter, model.EntityCabinet}, []model.EntityType{graph.vertices[0].Identity.EntityType, graph.vertices[1].Identity.EntityType})
	require.False(t, graph.cleanup)
}

func TestSyncWorkerInterfacePassCancellationAndLeaseLoss(t *testing.T) {
	for _, phase := range []int{1, 2} {
		for _, duringOpen := range []bool{false, true} {
			for _, leaseLost := range []bool{false, true} {
				t.Run(fmt.Sprintf("pass=%d/open=%t/lease=%t", phase, duringOpen, leaseLost), func(t *testing.T) {
					ctx, cancel := context.WithCancelCause(context.Background())
					defer cancel(nil)
					runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-1"}, source: &model.Source{ID: "source-1", AdapterKind: "cmdb"}}
					page := adapter.EntityPage{Records: []adapter.EntityRecord{{Partition: 10, SourceInstanceID: 1, Identity: "PORT-1", OwnerDeviceSN: "SN-10"}}, NextOffset: 1, Total: 1, Done: true}
					source := &fakeDeviceSource{selected: model.EntityInterface, entityPages: []adapter.EntityPage{page}, entityReplay: []adapter.EntityPage{page}}
					interrupt := func(ctx context.Context) error {
						if source.passOpenCalls != phase {
							return nil
						}
						if leaseLost {
							cancel(errSyncLeaseLost)
						} else {
							cancel(context.Canceled)
						}
						return ctx.Err()
					}
					if duringOpen {
						source.passOpenHook = func(ctx context.Context, _ int) error { return interrupt(ctx) }
					} else {
						source.passPageHook = interrupt
					}
					graph := &fakeDeviceGraph{}
					_, err := newWorkerForTest(runs, graph, source, time.Now()).ProcessNext(ctx)
					if leaseLost {
						require.ErrorIs(t, err, errSyncLeaseLost)
						require.NotEqual(t, model.SyncRunStatusFailed, runs.run.Status)
						require.NotEqual(t, model.SyncRunStatusCanceled, runs.run.Status)
					} else {
						require.NoError(t, err)
						require.Equal(t, model.SyncRunStatusCanceled, runs.run.Status)
					}
					require.False(t, graph.cleanup)
					require.False(t, runs.completeCalled)
					if phase == 1 {
						require.Empty(t, graph.vertices)
						require.Empty(t, runs.advances)
					}
				})
			}
		}
	}
}

func TestSyncWorkerInterfaceOwnerRelations(t *testing.T) {
	now := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	record := func(partition, id int64, owner string) adapter.EntityRecord {
		return adapter.EntityRecord{Partition: partition, SourceInstanceID: id, Identity: fmt.Sprintf("PORT-%d-%d", partition, id), OwnerDeviceSN: owner, Properties: map[string]any{"port_name": "Ethernet1"}}
	}
	page := func(next, total int, records ...adapter.EntityRecord) adapter.EntityPage {
		return adapter.EntityPage{Records: records, NextOffset: next, Total: total, Done: next == total}
	}
	for _, tc := range []struct {
		name          string
		pages         []adapter.EntityPage
		replay        []adapter.EntityPage
		graph         *fakeDeviceGraph
		wantError     string
		wantVertices  int
		wantRelations int
		wantLookups   int
		wantAdvances  int
	}{
		{name: "writes device interface edges and caches owners across pages", pages: []adapter.EntityPage{page(2, 4, record(10, 1, "SN-A"), record(10, 2, "SN-A")), page(3, 4, record(10, 3, "SN-A")), page(4, 4, record(20, 1, "SN-B"))}, wantVertices: 4, wantRelations: 4, wantLookups: 2, wantAdvances: 3},
		{name: "missing owner device", pages: []adapter.EntityPage{page(2, 2, record(10, 1, "SN-A"), record(10, 2, "SN-A"))}, graph: &fakeDeviceGraph{ownerMissing: true}, wantError: syncErrorGraphWrite, wantLookups: 1},
		{name: "owner lookup failure", pages: []adapter.EntityPage{page(1, 1, record(10, 1, "SN-A"))}, graph: &fakeDeviceGraph{ownerReadErr: errors.New("read failed")}, wantError: syncErrorGraphWrite, wantLookups: 1},
		{name: "blank owner blocks whole page", pages: []adapter.EntityPage{page(2, 2, record(10, 1, "SN-A"), record(10, 2, ""))}, wantError: syncErrorGraphWrite},
		{name: "whitespace owner blocks whole page", pages: []adapter.EntityPage{page(1, 1, record(10, 1, " \t"))}, wantError: syncErrorGraphWrite},
		{name: "untrimmed owner blocks whole page", pages: []adapter.EntityPage{page(1, 1, record(10, 1, " SN-A "))}, wantError: syncErrorGraphWrite},
		{name: "mixed owners block whole partition page", pages: []adapter.EntityPage{page(2, 2, record(10, 1, "SN-A"), record(10, 2, "SN-B"))}, wantError: syncErrorGraphWrite},
		{name: "owner changes across pages of same partition", pages: []adapter.EntityPage{page(1, 2, record(10, 1, "SN-A")), page(2, 2, record(10, 2, "SN-B"))}, wantError: syncErrorGraphWrite, wantVertices: 1, wantRelations: 1, wantLookups: 1, wantAdvances: 1},
		{name: "vertex failure never writes relation", pages: []adapter.EntityPage{page(1, 1, record(10, 1, "SN-A"))}, graph: &fakeDeviceGraph{upsertErrAt: 1}, wantError: syncErrorGraphWrite, wantLookups: 1},
		{name: "relation failure leaves vertex but no checkpoint", pages: []adapter.EntityPage{page(1, 1, record(10, 1, "SN-A"))}, graph: &fakeDeviceGraph{relationErrAt: 1}, wantError: syncErrorGraphWrite, wantVertices: 1, wantLookups: 1},
		{name: "validation owner change fails digest without new writes", pages: []adapter.EntityPage{page(1, 2, record(10, 1, "SN-A")), page(2, 2, record(20, 1, "SN-B"))}, replay: []adapter.EntityPage{page(1, 2, record(10, 1, "SN-A")), page(2, 2, record(20, 1, "SN-C"))}, wantError: "entity_collection_failed", wantVertices: 2, wantRelations: 2, wantLookups: 2, wantAdvances: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-1"}, source: &model.Source{ID: "source-1", AdapterKind: "cmdb"}}
			replay := tc.replay
			if replay == nil {
				replay = tc.pages
			}
			source := &fakeDeviceSource{selected: model.EntityInterface, entityPages: tc.pages, entityReplay: replay, filtered: true}
			graph := tc.graph
			if graph == nil {
				graph = &fakeDeviceGraph{}
			}
			worker := newWorkerForTest(runs, graph, source, now)
			_, err := worker.ProcessNext(context.Background())
			if tc.wantError == "" {
				require.NoError(t, err)
				require.Equal(t, model.SyncRunStatusSucceeded, runs.run.Status)
				require.True(t, runs.completeCalled)
			} else {
				require.EqualError(t, err, tc.wantError)
				require.Equal(t, model.SyncRunStatusFailed, runs.run.Status)
				require.Equal(t, tc.wantError, runs.run.ErrorCode)
				require.False(t, runs.completeCalled)
			}
			require.False(t, graph.cleanup)
			require.Len(t, graph.vertices, tc.wantVertices)
			require.Len(t, graph.relations, tc.wantRelations)
			expectedRelations := make([]model.Relation, 0, tc.wantRelations)
			for _, page := range tc.pages {
				for _, record := range page.Records {
					if len(expectedRelations) == tc.wantRelations {
						break
					}
					expectedRelations = append(expectedRelations, model.Relation{
						Identity: model.RelationIdentity{
							SourceID: "source-1",
							EdgeType: model.EdgeComposition,
							Kind:     model.RelationOwnsInterface,
							From:     model.EntityIdentity{SourceID: "source-1", EntityType: model.EntityDevice, StableID: record.OwnerDeviceSN},
							To:       model.EntityIdentity{SourceID: "source-1", EntityType: model.EntityInterface, StableID: record.Identity},
						},
						CreatedAt: now,
						SyncedAt:  now,
					})
				}
			}
			require.ElementsMatch(t, expectedRelations, graph.relations)
			require.Len(t, graph.ownerLookups, tc.wantLookups)
			require.Len(t, runs.advances, tc.wantAdvances)
			for _, vertex := range graph.vertices {
				require.Equal(t, model.EntityInterface, vertex.Identity.EntityType, "owner devices must never be fabricated")
				require.NotContains(t, vertex.Properties, "owner_device_sn")
				require.NotContains(t, vertex.Properties, "OwnerDeviceSN")
			}
			if tc.wantVertices == 0 && graph.upsertErrAt == 0 {
				require.Zero(t, graph.upsertCalls, "owner validation must finish before any page vertex write")
			}
			for _, owner := range graph.ownerLookups {
				require.Equal(t, runs.source.ID, owner.SourceID)
				require.Equal(t, model.EntityDevice, owner.EntityType)
				require.Contains(t, []string{"SN-A", "SN-B"}, owner.StableID)
			}
			for _, relation := range graph.relations {
				require.NoError(t, model.ValidateRelation(relation))
				require.Equal(t, model.EdgeComposition, relation.Identity.EdgeType)
				require.Equal(t, model.RelationOwnsInterface, relation.Identity.Kind)
				require.Equal(t, runs.source.ID, relation.Identity.SourceID)
				require.Equal(t, runs.source.ID, relation.Identity.From.SourceID)
				require.Equal(t, runs.source.ID, relation.Identity.To.SourceID)
				require.Equal(t, model.EntityDevice, relation.Identity.From.EntityType)
				require.Equal(t, model.EntityInterface, relation.Identity.To.EntityType)
				owner := "SN-A"
				if strings.HasPrefix(relation.Identity.To.StableID, "PORT-20-") {
					owner = "SN-B"
				}
				require.Equal(t, owner, relation.Identity.From.StableID)
				require.Equal(t, now, relation.CreatedAt)
				require.Equal(t, now, relation.SyncedAt)
				require.Empty(t, relation.Properties)
				expectedIdentity := model.RelationIdentity{
					SourceID: "source-1",
					EdgeType: model.EdgeComposition,
					Kind:     model.RelationOwnsInterface,
					From:     model.EntityIdentity{SourceID: "source-1", EntityType: model.EntityDevice, StableID: owner},
					To:       model.EntityIdentity{SourceID: "source-1", EntityType: model.EntityInterface, StableID: relation.Identity.To.StableID},
				}
				expectedRank, err := expectedIdentity.Rank()
				require.NoError(t, err)
				rank, err := relation.Identity.Rank()
				require.NoError(t, err)
				require.Equal(t, expectedRank, rank)
			}
			if graph.upsertErrAt > 0 {
				require.Zero(t, graph.relationCalls)
			}
			if graph.relationErrAt > 0 {
				require.Equal(t, 1, graph.relationCalls)
			}
		})
	}
}

func TestSyncWorkerInterfacePartitionPasses(t *testing.T) {
	record := func(partition, id int64, uuid string) adapter.EntityRecord {
		return adapter.EntityRecord{Partition: partition, SourceInstanceID: id, Identity: uuid, OwnerDeviceSN: fmt.Sprintf("SN-%d", partition), Properties: map[string]any{"port_name": uuid}}
	}
	page := func(next, total int, done bool, records ...adapter.EntityRecord) adapter.EntityPage {
		return adapter.EntityPage{Records: records, NextOffset: next, Total: total, Done: done}
	}
	valid := []adapter.EntityPage{
		page(1, 3, false, record(10, 901, "PORT-901")),
		page(2, 3, false, record(10, 902, "PORT-902")),
		page(3, 3, true, record(20, 1, "PORT-1")),
	}
	for _, tc := range []struct {
		name        string
		pages       []adapter.EntityPage
		replay      []adapter.EntityPage
		openErrAt   int
		wantSuccess bool
	}{
		{name: "actual IDs descend across devices", pages: valid, wantSuccess: true},
		{name: "duplicate UUID across devices", pages: []adapter.EntityPage{page(1, 2, false, record(10, 901, "same")), page(2, 2, true, record(20, 1, "same"))}},
		{name: "changed validation partition", pages: valid, replay: []adapter.EntityPage{valid[0], valid[1], page(3, 3, true, record(21, 1, "PORT-1"))}},
		{name: "changed validation identity", pages: valid, replay: []adapter.EntityPage{valid[0], valid[1], page(3, 3, true, record(20, 1, "different"))}},
		{name: "changed validation actual ID", pages: valid, replay: []adapter.EntityPage{valid[0], valid[1], page(3, 3, true, record(20, 2, "PORT-1"))}},
		{name: "changed validation count", pages: valid, replay: []adapter.EntityPage{page(1, 4, false, record(10, 901, "PORT-901"))}},
		{name: "missing partition", pages: []adapter.EntityPage{page(1, 1, true, record(0, 1, "PORT-1"))}},
		{name: "negative partition", pages: []adapter.EntityPage{page(1, 1, true, record(-1, 1, "PORT-1"))}},
		{name: "partition regresses", pages: []adapter.EntityPage{page(1, 2, false, record(20, 1, "PORT-1")), page(2, 2, true, record(10, 2, "PORT-2"))}},
		{name: "mixed partition page", pages: []adapter.EntityPage{page(2, 2, true, record(10, 1, "PORT-1"), record(20, 2, "PORT-2"))}},
		{name: "same partition IDs decrease", pages: []adapter.EntityPage{page(1, 2, false, record(10, 2, "PORT-2")), page(2, 2, true, record(10, 1, "PORT-1"))}},
		{name: "same page IDs decrease", pages: []adapter.EntityPage{page(2, 2, true, record(10, 2, "PORT-2"), record(10, 1, "PORT-1"))}},
		{name: "nonpositive actual ID", pages: []adapter.EntityPage{page(1, 1, true, record(10, 0, "PORT-1"))}},
		{name: "manifest open fails", pages: valid, openErrAt: 1},
		{name: "validation manifest open fails", pages: valid, openErrAt: 2},
		{name: "empty manifest", pages: []adapter.EntityPage{page(0, 0, true)}, wantSuccess: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-1"}, source: &model.Source{ID: "source-1", AdapterKind: "cmdb"}}
			replay := tc.replay
			if replay == nil {
				replay = tc.pages
			}
			source := &fakeDeviceSource{selected: model.EntityInterface, entityPages: tc.pages, entityReplay: replay, passOpenErrAt: tc.openErrAt}
			graph := &fakeDeviceGraph{}
			_, err := newWorkerForTest(runs, graph, source, time.Now()).ProcessNext(context.Background())
			if !tc.wantSuccess {
				require.EqualError(t, err, "entity_collection_failed")
				require.Equal(t, model.SyncRunStatusFailed, runs.run.Status)
				require.False(t, graph.cleanup)
				require.False(t, runs.completeCalled)
				if tc.openErrAt == 1 {
					require.Empty(t, graph.vertices)
					require.Empty(t, source.offsets)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, model.SyncRunStatusSucceeded, runs.run.Status)
			require.True(t, graph.cleanup)
			require.True(t, runs.completeCalled)
			require.Equal(t, 2, source.passOpenCalls)
			if len(tc.pages[0].Records) == 0 {
				require.Empty(t, graph.vertices)
				return
			}
			require.Len(t, runs.advances, 3)
			require.Equal(t, int64(20), runs.advances[2].cursor.LastPartition)
			require.Equal(t, int64(1), runs.advances[2].cursor.LastInstanceID)
			identities := make([]string, 0, len(graph.vertices))
			for _, vertex := range graph.vertices {
				require.Equal(t, model.EntityInterface, vertex.Identity.EntityType)
				identities = append(identities, vertex.Identity.StableID)
			}
			require.ElementsMatch(t, []string{"PORT-901", "PORT-902", "PORT-1"}, identities)
			require.Equal(t, []int{0, 1, 2, 0, 1, 2}, source.offsets)
		})
	}
}

func TestSyncWorkerEntityFailuresNeverCleanup(t *testing.T) {
	record := func(id int64, uuid string) adapter.EntityRecord {
		return adapter.EntityRecord{SourceInstanceID: id, Identity: uuid, Properties: map[string]any{"code": uuid}}
	}
	page := func(next, total int, done bool, records ...adapter.EntityRecord) adapter.EntityPage {
		return adapter.EntityPage{Records: records, NextOffset: next, Total: total, Done: done}
	}
	valid := page(1, 1, true, record(1, "UUID-1"))
	for _, entity := range []model.EntityType{model.EntityDataCenter, model.EntityCabinet} {
		for _, tc := range []struct {
			name          string
			pages         []adapter.EntityPage
			replay        []adapter.EntityPage
			writeErr      bool
			checkpointErr bool
			cancel        bool
			want          string
		}{
			{name: "duplicate identity", pages: []adapter.EntityPage{page(2, 2, true, record(1, "same"), record(2, "same"))}, want: "entity_collection_failed"},
			{name: "cross page order", pages: []adapter.EntityPage{page(1, 2, false, record(2, "UUID-2")), page(2, 2, true, record(1, "UUID-1"))}, want: "entity_collection_failed"},
			{name: "changed identity", pages: []adapter.EntityPage{valid}, replay: []adapter.EntityPage{page(1, 1, true, record(2, "UUID-2"))}, want: "entity_collection_failed"},
			{name: "changed count", pages: []adapter.EntityPage{valid}, replay: []adapter.EntityPage{page(1, 2, false, record(1, "UUID-1"))}, want: "entity_collection_failed"},
			{name: "bad offset", pages: []adapter.EntityPage{page(0, 1, true, record(1, "UUID-1"))}, want: "entity_collection_failed"},
			{name: "graph error", pages: []adapter.EntityPage{valid}, writeErr: true, want: syncErrorGraphWrite},
			{name: "checkpoint error", pages: []adapter.EntityPage{valid}, checkpointErr: true, want: syncErrorCheckpoint},
			{name: "canceled", pages: []adapter.EntityPage{valid}, cancel: true},
		} {
			t.Run(string(entity)+"/"+tc.name, func(t *testing.T) {
				runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-1"}, source: &model.Source{ID: "source-1", AdapterKind: "cmdb"}}
				if tc.checkpointErr {
					runs.advanceErr = errors.New("checkpoint error")
				}
				if tc.cancel {
					runs.cancelChecks = []bool{false, true}
				}
				source := &fakeDeviceSource{selected: entity, entityPages: append([]adapter.EntityPage(nil), tc.pages...), entityReplay: tc.replay}
				graph := &fakeDeviceGraph{}
				if tc.writeErr {
					graph.upsertErrAt = 1
				}
				_, err := newWorkerForTest(runs, graph, source, time.Now()).ProcessNext(context.Background())
				if tc.cancel {
					require.NoError(t, err)
					require.Equal(t, model.SyncRunStatusCanceled, runs.run.Status)
				} else {
					require.EqualError(t, err, tc.want)
					require.Equal(t, model.SyncRunStatusFailed, runs.run.Status)
				}
				require.False(t, graph.cleanup)
				require.False(t, runs.completeCalled)
			})
		}
	}
}

func TestSyncWorkerRejectsInvalidSelectionBeforeWriting(t *testing.T) {
	for _, source := range []*fakeDeviceSource{
		{selected: model.EntityPod}, {selectionErr: errors.New("invalid config")}, {coverageErr: errors.New("invalid condition")},
	} {
		runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-1"}, source: &model.Source{ID: "source-1", AdapterKind: "cmdb"}}
		graph := &fakeDeviceGraph{}
		_, err := newWorkerForTest(runs, graph, source, time.Now()).ProcessNext(context.Background())
		require.EqualError(t, err, syncErrorSourceValidation)
		require.Empty(t, graph.vertices)
		require.Empty(t, source.offsets)
		require.False(t, graph.cleanup)
		require.Nil(t, runs.checkpoint)
	}
}

func TestSyncWorkerFilteredDeviceSkipsCleanup(t *testing.T) {
	for _, changed := range []bool{false, true} {
		runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-1"}, source: &model.Source{ID: "source-1", AdapterKind: "cmdb"}}
		source := &fakeDeviceSource{filtered: !changed, coverageChanged: changed, pages: []fakeDevicePage{
			devicePage(0, 1, 1, true, adapter.DeviceRecord{SourceInstanceID: 1, DeviceSN: "SN-1"}),
		}}
		graph := &fakeDeviceGraph{}
		_, err := newWorkerForTest(runs, graph, source, time.Now()).ProcessNext(context.Background())
		require.NoError(t, err)
		require.Equal(t, model.SyncRunStatusSucceeded, runs.run.Status)
		require.False(t, graph.cleanup)
	}
}

func TestSyncWorkerLiveCMDBToNebula(t *testing.T) {
	configPath := os.Getenv("SYNC_WORKER_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("设置 SYNC_WORKER_LIVE_CONFIG 后运行真实 CMDB、MySQL 和 NebulaGraph 纵向验收")
	}

	conf := viper.New()
	conf.SetConfigFile(configPath)
	require.NoError(t, conf.ReadInConfig())
	logger := &log.Logger{Logger: zap.NewNop()}
	db := repository.NewDB(conf, logger)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	control := repository.NewInventoryRepository(repository.NewRepository(logger, db))
	graph, closeGraph, err := repository.NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	t.Cleanup(closeGraph)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sourceID := strings.Repeat("f", 31) + "1"
	runID := strings.Repeat("f", 31) + "2"
	cleanupAt := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	cleanup := func(cleanupCtx context.Context) {
		require.NoError(t, graph.DeleteOrphanVerticesNotSeen(cleanupCtx, sourceID, model.EntityDevice, cleanupAt))
		require.NoError(t, db.WithContext(cleanupCtx).Where("source_id = ?", sourceID).Delete(&model.SyncCheckpoint{}).Error)
		require.NoError(t, db.WithContext(cleanupCtx).Where("id = ?", runID).Delete(&model.SyncRun{}).Error)
		require.NoError(t, db.WithContext(cleanupCtx).Where("id = ?", sourceID).Delete(&model.Source{}).Error)
	}
	cleanup(ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup(cleanupCtx)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	source := model.Source{
		ID: sourceID, Name: "cmdb-device-live-acceptance", AdapterKind: "cmdb", ConfigRef: "test",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	run := model.SyncRun{
		ID: runID, SourceID: sourceID, Status: model.SyncRunStatusQueued, Mode: "full",
		RequestHash: strings.Repeat("a", 64), IdempotencyKey: "cmdb-device-live-acceptance",
		RequestedBy: "live-test", CreatedAt: now,
	}
	require.NoError(t, db.WithContext(ctx).Create(&source).Error)
	require.NoError(t, db.WithContext(ctx).Create(&run).Error)

	worker := NewSyncWorker(conf, control, graph, adapter.NewCMDB(conf), logger)
	processed, err := worker.ProcessNext(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	stored, err := control.Run(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, model.SyncRunStatusSucceeded, stored.Status)
	require.NotNil(t, stored.StartedAt)
	require.NotNil(t, stored.FinishedAt)
	require.Empty(t, stored.ErrorCode)
}

func TestDevicePropertiesOmitMissingOptionalTypeFields(t *testing.T) {
	properties := deviceProperties(adapter.DeviceRecord{DeviceSN: "SN-1", Name: "device-1"})
	require.NotContains(t, properties, "parent_type_id")
	require.NotContains(t, properties, "device_type_id")
}

func TestSyncWorkerProcessNextReturnsIdleWhenQueueIsEmpty(t *testing.T) {
	worker := newWorkerForTest(&fakeWorkerRuns{}, &fakeDeviceGraph{}, &fakeDeviceSource{}, time.Now())
	processed, err := worker.ProcessNext(context.Background())
	require.NoError(t, err)
	require.False(t, processed)
}

func TestSyncWorkerRunStopsOnCancellation(t *testing.T) {
	worker := newWorkerForTest(&fakeWorkerRuns{}, &fakeDeviceGraph{}, &fakeDeviceSource{}, time.Now())
	worker.pollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	cancel()
	require.NoError(t, <-done)
}
