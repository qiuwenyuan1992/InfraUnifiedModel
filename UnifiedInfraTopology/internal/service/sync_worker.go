package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"strings"
	"sync"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
)

const (
	defaultWorkerPollInterval     = time.Second
	defaultLeaseRenewInterval     = 30 * time.Second
	defaultDeviceWriteConcurrency = 8
	maximumDeviceWriteConcurrency = 16
	deviceCheckpointResource      = "devices"

	syncErrorSourceValidation  = "source_validation_failed"
	syncErrorDeviceCollection  = "device_collection_failed"
	syncErrorGraphWrite        = "graph_write_failed"
	syncErrorGraphCleanup      = "graph_cleanup_failed"
	syncErrorCheckpoint        = "checkpoint_failed"
	syncErrorCancellationCheck = "cancellation_check_failed"
)

var (
	errSyncCanceled          = errors.New("sync run cancellation requested")
	errSyncDevicePage        = errors.New("sync device page invalid")
	errSyncGraphWrite        = errors.New("sync graph write failed")
	errSyncCancellationCheck = errors.New("sync cancellation check failed")
	errSyncLeaseLost         = errors.New("sync run lease lost")
	errSyncSnapshotChanged   = errors.New("sync source snapshot changed")
)

type SyncRunRepository interface {
	ClaimNext(context.Context, time.Time) (*model.SyncRun, *model.Source, error)
	TransitionRun(context.Context, string, string, string, time.Time, string) (*model.SyncRun, error)
	RenewRunLease(context.Context, string, time.Time) error
	RunCancellationRequested(context.Context, string) (bool, error)
	BeginCheckpoint(context.Context, string, string, string, time.Time, time.Time) (*repository.DeviceSyncCheckpoint, error)
	AdvanceCheckpoint(context.Context, string, string, string, repository.DeviceSyncCursor, time.Time) (*repository.DeviceSyncCheckpoint, error)
	CompleteCheckpointAndRun(context.Context, string, string, string, string, time.Time) (*model.SyncRun, error)
}

type DeviceSource interface {
	adapter.SourceAdapter
	adapter.DevicePageCollector
}

type DeviceGraphRepository interface {
	UpsertVertex(context.Context, repository.TopologyVertex) error
	DeleteOrphanVerticesNotSeen(context.Context, string, model.EntityType, time.Time) error
}

type SyncWorker struct {
	runs                   SyncRunRepository
	graph                  DeviceGraphRepository
	source                 DeviceSource
	logger                 *log.Logger
	pollInterval           time.Duration
	leaseRenewInterval     time.Duration
	deviceWriteConcurrency int
	now                    func() time.Time
}

func NewSyncWorker(conf *viper.Viper, runs SyncRunRepository, graph DeviceGraphRepository, source DeviceSource, logger *log.Logger) *SyncWorker {
	pollInterval := defaultWorkerPollInterval
	deviceWriteConcurrency := defaultDeviceWriteConcurrency
	if conf != nil {
		if conf.GetInt("inventory.worker.poll_interval_seconds") > 0 {
			pollInterval = time.Duration(conf.GetInt("inventory.worker.poll_interval_seconds")) * time.Second
		}
		if conf.IsSet("inventory.worker.device_write_concurrency") {
			deviceWriteConcurrency = conf.GetInt("inventory.worker.device_write_concurrency")
		}
	}
	if deviceWriteConcurrency < 1 {
		deviceWriteConcurrency = 1
	}
	if deviceWriteConcurrency > maximumDeviceWriteConcurrency {
		deviceWriteConcurrency = maximumDeviceWriteConcurrency
	}
	return &SyncWorker{
		runs:                   runs,
		graph:                  graph,
		source:                 source,
		logger:                 logger,
		pollInterval:           pollInterval,
		leaseRenewInterval:     defaultLeaseRenewInterval,
		deviceWriteConcurrency: deviceWriteConcurrency,
		now:                    time.Now,
	}
}

func (w *SyncWorker) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		processed, err := w.ProcessNext(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.logger.Error("sync run processing failed")
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(w.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (w *SyncWorker) ProcessNext(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	snapshotAt := w.currentTime()
	run, source, err := w.runs.ClaimNext(ctx, snapshotAt)
	if errors.Is(err, repository.ErrInventoryNoRun) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("sync run claim failed")
	}
	if run.StartedAt != nil {
		snapshotAt = run.StartedAt.UTC().Truncate(time.Microsecond)
	}
	return true, w.executeWithLease(ctx, run, *source, snapshotAt)
}

func (w *SyncWorker) executeWithLease(ctx context.Context, run *model.SyncRun, source model.Source, startedAt time.Time) error {
	leaseCtx, cancel := context.WithCancelCause(ctx)
	heartbeatDone := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(w.leaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(context.WithoutCancel(leaseCtx), 5*time.Second)
				err := w.runs.RenewRunLease(renewCtx, run.ID, w.currentTime())
				renewCancel()
				if err != nil {
					heartbeatErr <- err
					cancel(errSyncLeaseLost)
					return
				}
			}
		}
	}()

	err := w.executeClaimed(leaseCtx, run, source, startedAt)
	cancel(nil)
	<-heartbeatDone
	if err != nil {
		select {
		case <-heartbeatErr:
			return errSyncLeaseLost
		default:
		}
	}
	return err
}

func (w *SyncWorker) executeClaimed(ctx context.Context, run *model.SyncRun, source model.Source, startedAt time.Time) error {
	status := model.SyncRunStatusRunning
	canceled, err := w.cancellationRequested(ctx, run.ID)
	if err != nil {
		return w.failCancellationCheck(ctx, run.ID, status, startedAt, err)
	}
	if canceled {
		return w.cancelRun(ctx, run.ID, status, startedAt)
	}
	if source.AdapterKind != "cmdb" {
		return w.failRun(ctx, run.ID, status, startedAt, syncErrorSourceValidation)
	}
	if err := w.source.Validate(ctx, source); err != nil {
		return w.failOrCancel(ctx, run.ID, status, syncErrorSourceValidation)
	}

	checkpoint, err := w.runs.BeginCheckpoint(ctx, source.ID, deviceCheckpointResource, run.ID, startedAt, w.currentTime())
	if err != nil || checkpoint == nil {
		return w.failOrCancel(ctx, run.ID, status, syncErrorCheckpoint)
	}
	cursor := checkpoint.Cursor
	snapshotAt := cursor.SnapshotAt
	identityDigest := sha256.New()
	seenDeviceSN := make(map[string]struct{})

	for {
		canceled, err = w.cancellationRequested(ctx, run.ID)
		if err != nil {
			return w.failCancellationCheck(ctx, run.ID, status, startedAt, err)
		}
		if canceled {
			return w.cancelRun(ctx, run.ID, status, startedAt)
		}

		page, collectErr := w.source.CollectDevicePage(ctx, source, cursor.NextOffset)
		if collectErr != nil {
			return w.failOrCancel(ctx, run.ID, status, syncErrorDeviceCollection)
		}
		lastInstanceID, pageErr := validateDevicePage(cursor, page)
		if pageErr != nil || addDeviceIdentities(seenDeviceSN, page.Records) != nil {
			return w.failRun(ctx, run.ID, status, w.currentTime(), syncErrorDeviceCollection)
		}
		writeDeviceIdentityDigest(identityDigest, page.Records)
		if err := w.writeDevicePage(ctx, source.ID, page.Records, snapshotAt); err != nil {
			if errors.Is(err, errSyncLeaseLost) {
				return err
			}
			if errors.Is(err, errSyncCanceled) {
				return w.cancelRun(ctx, run.ID, status, w.currentTime())
			}
			return w.failRun(ctx, run.ID, status, w.currentTime(), syncErrorGraphWrite)
		}

		if len(page.Records) > 0 {
			canceled, err = w.cancellationRequested(ctx, run.ID)
			if err != nil {
				return w.failCancellationCheck(ctx, run.ID, status, startedAt, err)
			}
			if canceled {
				return w.cancelRun(ctx, run.ID, status, startedAt)
			}
			cursor = repository.DeviceSyncCursor{
				Version:        repository.DeviceSyncCursorVersion,
				RunID:          run.ID,
				NextOffset:     page.NextOffset,
				LastInstanceID: lastInstanceID,
				SnapshotAt:     snapshotAt,
				Total:          page.Total,
			}
			if _, err := w.runs.AdvanceCheckpoint(ctx, source.ID, deviceCheckpointResource, run.ID, cursor, w.currentTime()); err != nil {
				return w.failOrCancel(ctx, run.ID, status, syncErrorCheckpoint)
			}
		}
		if page.Done {
			break
		}
	}

	canceled, err = w.cancellationRequested(ctx, run.ID)
	if err != nil {
		return w.failCancellationCheck(ctx, run.ID, status, startedAt, err)
	}
	if canceled {
		return w.cancelRun(ctx, run.ID, status, startedAt)
	}
	if _, err := w.runs.TransitionRun(ctx, run.ID, status, model.SyncRunStatusValidating, w.currentTime(), ""); err != nil {
		return w.failOrCancel(ctx, run.ID, status, syncErrorCheckpoint)
	}
	status = model.SyncRunStatusValidating

	if err := w.validateDeviceSnapshot(ctx, run.ID, source, cursor.Total, identityDigest.Sum(nil)); err != nil {
		if errors.Is(err, errSyncLeaseLost) {
			return err
		}
		if errors.Is(err, errSyncCanceled) {
			return w.cancelRun(ctx, run.ID, status, w.currentTime())
		}
		return w.failRun(ctx, run.ID, status, w.currentTime(), syncErrorDeviceCollection)
	}

	canceled, err = w.cancellationRequested(ctx, run.ID)
	if err != nil {
		return w.failCancellationCheck(ctx, run.ID, status, startedAt, err)
	}
	if canceled {
		return w.cancelRun(ctx, run.ID, status, startedAt)
	}
	if _, err := w.runs.TransitionRun(ctx, run.ID, status, model.SyncRunStatusPublishing, w.currentTime(), ""); err != nil {
		return w.failOrCancel(ctx, run.ID, status, syncErrorCheckpoint)
	}
	status = model.SyncRunStatusPublishing

	if err := leaseContextError(ctx); err != nil {
		return err
	}
	if err := w.graph.DeleteOrphanVerticesNotSeen(ctx, source.ID, model.EntityDevice, snapshotAt); err != nil {
		if leaseErr := leaseContextError(ctx); leaseErr != nil {
			return leaseErr
		}
		return w.failRun(ctx, run.ID, status, w.currentTime(), syncErrorGraphCleanup)
	}
	if err := leaseContextError(ctx); err != nil {
		return err
	}
	if _, err := w.runs.CompleteCheckpointAndRun(ctx, source.ID, deviceCheckpointResource, run.ID, status, w.currentTime()); err != nil {
		return w.failRun(ctx, run.ID, status, w.currentTime(), syncErrorCheckpoint)
	}
	return nil
}

func (w *SyncWorker) validateDeviceSnapshot(ctx context.Context, runID string, source model.Source, expectedTotal int, expectedDigest []byte) error {
	cursor := repository.DeviceSyncCursor{Version: repository.DeviceSyncCursorVersion, Total: expectedTotal}
	digest := sha256.New()
	for {
		canceled, err := w.cancellationRequested(ctx, runID)
		if err != nil {
			return err
		}
		if canceled {
			return errSyncCanceled
		}
		page, err := w.source.CollectDevicePage(ctx, source, cursor.NextOffset)
		if err != nil {
			if leaseErr := leaseContextError(ctx); leaseErr != nil {
				return leaseErr
			}
			return err
		}
		if page.Total != expectedTotal {
			return errSyncSnapshotChanged
		}
		lastInstanceID, err := validateDevicePage(cursor, page)
		if err != nil {
			return err
		}
		writeDeviceIdentityDigest(digest, page.Records)
		cursor.NextOffset = page.NextOffset
		cursor.LastInstanceID = lastInstanceID
		if page.Done {
			if cursor.NextOffset != expectedTotal || !bytes.Equal(digest.Sum(nil), expectedDigest) {
				return errSyncSnapshotChanged
			}
			return nil
		}
	}
}

func addDeviceIdentities(seen map[string]struct{}, records []adapter.DeviceRecord) error {
	for _, record := range records {
		deviceSN := strings.TrimSpace(record.DeviceSN)
		if deviceSN == "" || deviceSN != record.DeviceSN {
			return errSyncDevicePage
		}
		if _, exists := seen[deviceSN]; exists {
			return errSyncDevicePage
		}
		seen[deviceSN] = struct{}{}
	}
	return nil
}

func writeDeviceIdentityDigest(digest hash.Hash, records []adapter.DeviceRecord) {
	var encodedID [8]byte
	for _, record := range records {
		binary.BigEndian.PutUint64(encodedID[:], uint64(record.SourceInstanceID))
		_, _ = digest.Write(encodedID[:])
		_, _ = digest.Write([]byte(record.DeviceSN))
		_, _ = digest.Write([]byte{0})
	}
}

func (w *SyncWorker) writeDevicePage(ctx context.Context, sourceID string, records []adapter.DeviceRecord, snapshotAt time.Time) error {
	pageCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	semaphore := make(chan struct{}, w.deviceWriteConcurrency)
	var waitGroup sync.WaitGroup
	var firstError error
	var errorOnce sync.Once

launch:
	for _, record := range records {
		select {
		case semaphore <- struct{}{}:
		case <-pageCtx.Done():
			break launch
		}
		waitGroup.Add(1)
		go func(record adapter.DeviceRecord) {
			defer waitGroup.Done()
			defer func() { <-semaphore }()
			vertex := repository.TopologyVertex{
				Identity:   model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityDevice, StableID: record.DeviceSN},
				Properties: deviceProperties(record),
				CreatedAt:  snapshotAt,
				SyncedAt:   snapshotAt,
			}
			if err := w.graph.UpsertVertex(pageCtx, vertex); err != nil {
				errorOnce.Do(func() {
					firstError = err
					cancel()
				})
			}
		}(record)
	}
	waitGroup.Wait()
	if err := leaseContextError(ctx); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return errSyncCanceled
	}
	if firstError != nil {
		return errSyncGraphWrite
	}
	return nil
}

func deviceProperties(record adapter.DeviceRecord) map[string]any {
	properties := map[string]any{
		"name":    record.Name,
		"role":    record.Role,
		"all_ips": record.AllIPs,
	}
	if record.ParentTypeID > 0 {
		properties["parent_type_id"] = record.ParentTypeID
	}
	if record.DeviceTypeID > 0 {
		properties["device_type_id"] = record.DeviceTypeID
	}
	return properties
}

func validateDevicePage(cursor repository.DeviceSyncCursor, page adapter.DevicePage) (int64, error) {
	expectedNextOffset := cursor.NextOffset + len(page.Records)
	if page.Total < 0 || page.NextOffset != expectedNextOffset || page.NextOffset > page.Total {
		return 0, errSyncDevicePage
	}
	if cursor.Total > 0 && page.Total != cursor.Total {
		return 0, errSyncDevicePage
	}
	if page.Done != (page.NextOffset >= page.Total) {
		return 0, errSyncDevicePage
	}
	lastInstanceID := cursor.LastInstanceID
	for _, record := range page.Records {
		if record.SourceInstanceID <= lastInstanceID {
			return 0, errSyncDevicePage
		}
		lastInstanceID = record.SourceInstanceID
	}
	return lastInstanceID, nil
}

func (w *SyncWorker) currentTime() time.Time {
	return w.now().UTC().Truncate(time.Microsecond)
}

func leaseContextError(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), errSyncLeaseLost) {
		return errSyncLeaseLost
	}
	return nil
}

func (w *SyncWorker) failCancellationCheck(ctx context.Context, runID, from string, at time.Time, err error) error {
	if errors.Is(err, errSyncLeaseLost) {
		return err
	}
	return w.failRun(ctx, runID, from, at, syncErrorCancellationCheck)
}

func (w *SyncWorker) failOrCancel(ctx context.Context, runID, from, code string) error {
	if err := leaseContextError(ctx); err != nil {
		return err
	}
	at := w.currentTime()
	if ctx.Err() != nil {
		return w.cancelRun(ctx, runID, from, at)
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	canceled, err := w.runs.RunCancellationRequested(checkCtx, runID)
	if err == nil && canceled {
		return w.cancelRun(ctx, runID, from, at)
	}
	return w.failRun(ctx, runID, from, at, code)
}

func (w *SyncWorker) cancellationRequested(ctx context.Context, runID string) (bool, error) {
	if err := leaseContextError(ctx); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return true, nil
	}
	return w.runs.RunCancellationRequested(ctx, runID)
}

func (w *SyncWorker) failRun(ctx context.Context, runID, from string, at time.Time, code string) error {
	transitionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := w.runs.TransitionRun(transitionCtx, runID, from, model.SyncRunStatusFailed, at, code); err != nil {
		return errors.New("sync run state transition failed")
	}
	return errors.New(code)
}

func (w *SyncWorker) cancelRun(ctx context.Context, runID, from string, at time.Time) error {
	transitionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := w.runs.TransitionRun(transitionCtx, runID, from, model.SyncRunStatusCanceled, at, ""); err != nil {
		return errors.New("sync run state transition failed")
	}
	return nil
}
