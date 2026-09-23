package service

import (
	"context"
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
	"gorm.io/gorm"
)

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
	mu           sync.Mutex
	vertices     []repository.TopologyVertex
	upsertErrAt  int
	upsertCalls  int
	activeWrites int
	maxActive    int
	writeDelay   time.Duration
	cleanupErr   error
	cleanup      bool
	cleanupID    string
	cleanupType  model.EntityType
	cleanupAt    time.Time
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
