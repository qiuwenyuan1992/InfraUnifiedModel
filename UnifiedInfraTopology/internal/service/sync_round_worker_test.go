package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/stretchr/testify/require"
)

type roundWorkerControl struct {
	repository.InventoryRoundRepository
	round                           *model.SyncRound
	phaseNames                      []string
	results                         []repository.PhaseResult
	finishStatus, finishError       string
	cancellation                    bool
	startErr, completeErr, renewErr error
}

func (r *roundWorkerControl) ClaimNext(context.Context, time.Time) (*model.SyncRound, *model.Source, error) {
	if r.round == nil {
		return nil, nil, repository.ErrInventoryNoRun
	}
	return r.round, &model.Source{ID: r.round.SourceID, Enabled: true, ConfigRef: "test", AdapterKind: "cmdb"}, nil
}
func (r *roundWorkerControl) Renew(context.Context, string, time.Time) error { return r.renewErr }
func (r *roundWorkerControl) CancellationRequested(context.Context, string) (bool, error) {
	return r.cancellation, nil
}
func (r *roundWorkerControl) StartPhase(_ context.Context, _ string, name string, _ time.Time) error {
	r.phaseNames = append(r.phaseNames, name)
	return r.startErr
}
func (r *roundWorkerControl) CompletePhase(_ context.Context, _, _ string, result repository.PhaseResult, _ time.Time) error {
	r.results = append(r.results, result)
	return r.completeErr
}
func (r *roundWorkerControl) Finish(_ context.Context, _, status, code string, _ time.Time) error {
	r.finishStatus, r.finishError = status, code
	return nil
}

func TestQueuedRoundWorkerOrdersPersistsAndSharesSnapshot(t *testing.T) {
	at := time.Date(2026, 11, 3, 1, 2, 3, 456789000, time.UTC)
	control := &roundWorkerControl{round: &model.SyncRound{ID: "round", SourceID: "source", SnapshotAt: &at, StartedAt: &at, Status: "running"}}
	names := []string{}
	worker := NewSyncRoundWorker(control, func(ctx context.Context, source model.Source, phase string) (repository.PhaseResult, error) {
		names = append(names, phase)
		require.Equal(t, "source", source.ID)
		require.Equal(t, at, SyncSnapshot(ctx, time.Time{}))
		round, name := repository.RoundStage(ctx)
		require.Equal(t, "round", round)
		require.Equal(t, phase, name)
		return repository.PhaseResult{RunID: fmt.Sprintf("%032x", len(names)), Nodes: 1, Edges: 2, Skipped: 3}, nil
	})
	processed, err := worker.ProcessNext(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, model.SyncRoundPhaseNames(), names)
	require.Equal(t, names, control.phaseNames)
	require.Len(t, control.results, 15)
	require.Equal(t, "succeeded", control.finishStatus)
	require.Empty(t, control.finishError)
}

func TestQueuedRoundWorkerStopsOnFailureOrCancellation(t *testing.T) {
	for _, failure := range []string{"stage", "start", "complete", "cancel", "shutdown"} {
		t.Run(failure, func(t *testing.T) {
			at := time.Now().UTC().Truncate(time.Microsecond)
			r := &roundWorkerControl{round: &model.SyncRound{ID: "round", SourceID: "source", SnapshotAt: &at, StartedAt: &at, Status: "running"}}
			if failure == "start" {
				r.startErr = errors.New("internalsecret")
			}
			if failure == "complete" {
				r.completeErr = errors.New("internalsecret")
			}
			if failure == "cancel" {
				r.cancellation = true
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			worker := NewSyncRoundWorker(r, func(context.Context, model.Source, string) (repository.PhaseResult, error) {
				calls++
				if failure == "shutdown" {
					cancel()
					return repository.PhaseResult{}, context.Canceled
				}
				if failure == "stage" {
					return repository.PhaseResult{}, errors.New("internalsecret")
				}
				return repository.PhaseResult{RunID: "run"}, nil
			})
			_, err := worker.ProcessNext(ctx)
			require.Error(t, err)
			require.LessOrEqual(t, calls, 1)
			require.NotContains(t, err.Error(), "internalsecret")
			if failure == "cancel" {
				require.Equal(t, "canceled", r.finishStatus)
				require.Zero(t, calls)
			} else {
				require.Equal(t, "failed", r.finishStatus)
			}
		})
	}
}

func TestQueuedRoundWorkerNoRunAndCanceledParent(t *testing.T) {
	worker := NewSyncRoundWorker(&roundWorkerControl{}, func(context.Context, model.Source, string) (repository.PhaseResult, error) {
		t.Fatal("unexpected stage")
		return repository.PhaseResult{}, nil
	})
	processed, err := worker.ProcessNext(context.Background())
	require.NoError(t, err)
	require.False(t, processed)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processed, err = worker.ProcessNext(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, processed)
}

type recoveringRoundControl struct {
	roundWorkerControl
	claims   int
	statuses []string
	cancel   context.CancelFunc
}

func (r *recoveringRoundControl) ClaimNext(_ context.Context, at time.Time) (*model.SyncRound, *model.Source, error) {
	r.claims++
	id := fmt.Sprintf("round-%d", r.claims)
	return &model.SyncRound{ID: id, SourceID: "source", SnapshotAt: &at, StartedAt: &at, Status: "running"}, &model.Source{ID: "source"}, nil
}
func (r *recoveringRoundControl) Finish(_ context.Context, _, status, _ string, _ time.Time) error {
	r.statuses = append(r.statuses, status)
	if len(r.statuses) == 2 {
		r.cancel()
	}
	return nil
}
func TestQueuedRoundDaemonContinuesAfterRecordedStageFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	control := &recoveringRoundControl{cancel: cancel}
	worker := NewSyncRoundWorker(control, func(ctx context.Context, _ model.Source, _ string) (repository.PhaseResult, error) {
		id, _ := repository.RoundStage(ctx)
		if id == "round-1" {
			return repository.PhaseResult{}, errors.New("failed source")
		}
		return repository.PhaseResult{RunID: "run"}, nil
	})
	require.NoError(t, worker.Run(ctx))
	require.Equal(t, []string{"failed", "succeeded"}, control.statuses)
	require.Equal(t, 2, control.claims)
}

func TestQueuedRoundHeartbeatLossStopsStageAndRecordsFailure(t *testing.T) {
	at := time.Now()
	control := &roundWorkerControl{round: &model.SyncRound{ID: "round", SourceID: "source", StartedAt: &at, SnapshotAt: &at}, renewErr: errors.New("database secret")}
	worker := NewSyncRoundWorker(control, func(ctx context.Context, _ model.Source, _ string) (repository.PhaseResult, error) {
		<-ctx.Done()
		return repository.PhaseResult{}, ctx.Err()
	})
	worker.heartbeat = time.Millisecond
	_, err := worker.ProcessNext(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
	require.Equal(t, "failed", control.finishStatus)
	require.Equal(t, "round_lease_lost", control.finishError)
}

type cancelOnCompletionControl struct{ roundWorkerControl }

func (r *cancelOnCompletionControl) Finish(ctx context.Context, id, status, code string, at time.Time) error {
	if status == "succeeded" {
		r.cancellation = true
		return repository.ErrInventoryConflict
	}
	return r.roundWorkerControl.Finish(ctx, id, status, code, at)
}
func TestQueuedRoundCancellationAtCompletionIsPersisted(t *testing.T) {
	at := time.Now()
	control := &cancelOnCompletionControl{roundWorkerControl: roundWorkerControl{round: &model.SyncRound{ID: "round", SourceID: "source", SnapshotAt: &at, StartedAt: &at}}}
	worker := NewSyncRoundWorker(control, func(context.Context, model.Source, string) (repository.PhaseResult, error) {
		return repository.PhaseResult{RunID: "run"}, nil
	})
	_, err := worker.ProcessNext(context.Background())
	require.Error(t, err)
	require.Equal(t, "canceled", control.finishStatus)
}

type expiredRoundControl struct{ roundWorkerControl }

func (r *expiredRoundControl) Renew(context.Context, string, time.Time) error {
	return repository.ErrInventoryConflict
}
func (r *expiredRoundControl) Finish(context.Context, string, string, string, time.Time) error {
	return repository.ErrInventoryConflict
}
func (r *expiredRoundControl) Get(_ context.Context, id string) (*model.SyncRound, []model.SyncRoundPhase, error) {
	return &model.SyncRound{ID: id, SourceID: r.round.SourceID, Status: "failed", ErrorCode: "worker_lease_expired"}, nil, nil
}
func TestQueuedRoundExpiredLeaseAlreadyRecordedIsRecognized(t *testing.T) {
	at := time.Now()
	control := &expiredRoundControl{roundWorkerControl: roundWorkerControl{round: &model.SyncRound{ID: "round", SourceID: "source", StartedAt: &at, SnapshotAt: &at}}}
	worker := NewSyncRoundWorker(control, func(ctx context.Context, _ model.Source, _ string) (repository.PhaseResult, error) {
		<-ctx.Done()
		return repository.PhaseResult{}, ctx.Err()
	})
	worker.heartbeat = time.Millisecond
	_, err := worker.ProcessNext(context.Background())
	var recorded *recordedRoundFailure
	require.ErrorAs(t, err, &recorded)
}

type blockedHeartbeatControl struct {
	roundWorkerControl
	entered chan struct{}
}

func (r *blockedHeartbeatControl) Renew(ctx context.Context, _ string, _ time.Time) error {
	close(r.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestQueuedRoundFailureCancelsInflightHeartbeatBeforeJoining(t *testing.T) {
	at := time.Now()
	control := &blockedHeartbeatControl{roundWorkerControl: roundWorkerControl{round: &model.SyncRound{ID: "round", SourceID: "source", StartedAt: &at, SnapshotAt: &at}}, entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := NewSyncRoundWorker(control, func(context.Context, model.Source, string) (repository.PhaseResult, error) {
		<-control.entered
		return repository.PhaseResult{}, errors.New("source failed")
	})
	worker.heartbeat = time.Millisecond
	done := make(chan error, 1)
	go func() { _, err := worker.ProcessNext(ctx); done <- err }()
	select {
	case err := <-done:
		require.Error(t, err)
		require.Equal(t, "failed", control.finishStatus)
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("结束任务必须先取消在途续租，不能等待到根context退出")
	}
}
