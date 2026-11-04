package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type recordedRoundFailure struct{ message string }

func (e *recordedRoundFailure) Error() string { return e.message }

type SyncRoundStageExecutor func(context.Context, model.Source, string) (repository.PhaseResult, error)

type SyncRoundWorker struct {
	repo      repository.InventoryRoundRepository
	execute   SyncRoundStageExecutor
	now       func() time.Time
	heartbeat time.Duration
}

func NewSyncRoundWorker(repo repository.InventoryRoundRepository, execute SyncRoundStageExecutor) *SyncRoundWorker {
	return &SyncRoundWorker{repo: repo, execute: execute, now: time.Now, heartbeat: 30 * time.Second}
}

func (w *SyncRoundWorker) ProcessNext(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	round, source, err := w.repo.ClaimNext(ctx, w.now().UTC().Truncate(time.Microsecond))
	if errors.Is(err, repository.ErrInventoryNoRun) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("round claim failed")
	}
	if round == nil || source == nil || round.SnapshotAt == nil || round.SnapshotAt.IsZero() {
		return true, errors.New("round claim has invalid snapshot")
	}
	execution, cancel := context.WithCancel(WithSyncSnapshot(ctx, *round.SnapshotAt))
	defer cancel()
	stop := make(chan struct{})
	stopped := make(chan struct{})
	monitorError := make(chan error, 1)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-execution.Done():
				return
			case <-ticker.C:
				requested, e := w.repo.CancellationRequested(execution, round.ID)
				if e == nil && requested {
					e = context.Canceled
				}
				if e == nil {
					e = w.repo.Renew(execution, round.ID, w.now().UTC().Truncate(time.Microsecond))
				}
				if e != nil {
					select {
					case <-stop:
						return
					default:
					}
					monitorError <- e
					cancel()
					return
				}
			}
		}
	}()
	var stopOnce sync.Once
	finish := func(status, code string) error {
		stopOnce.Do(func() { close(stop); cancel(); <-stopped })
		terminalCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		return w.repo.Finish(terminalCtx, round.ID, status, code, w.now().UTC().Truncate(time.Microsecond))
	}
	failed := func(phase string, cause error) (bool, error) {
		status, code := "failed", "round_stage_failed"
		checkCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		requested, e := w.repo.CancellationRequested(checkCtx, round.ID)
		done()
		if e == nil && requested {
			status, code = "canceled", ""
		} else if ctx.Err() != nil {
			code = "round_worker_interrupted"
		}
		select {
		case <-monitorError:
			if status != "canceled" {
				code = "round_lease_lost"
			}
		default:
		}
		_ = cause
		if e := finish(status, code); e != nil {
			checkCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			stored, _, readErr := w.repo.Get(checkCtx, round.ID)
			done()
			if readErr != nil || stored == nil || stored.ID != round.ID || stored.SourceID != round.SourceID || (stored.Status != "failed" && stored.Status != "canceled") {
				return true, fmt.Errorf("round %s stopped at %s; terminal state could not be recorded", round.ID, phase)
			}
			status = stored.Status
		}
		return true, &recordedRoundFailure{message: fmt.Sprintf("round %s stopped at %s; status=%s; prior graph writes remain", round.ID, phase, status)}
	}
	for _, phase := range model.SyncRoundPhaseNames() {
		if e := execution.Err(); e != nil {
			return failed(phase, e)
		}
		requested, e := w.repo.CancellationRequested(execution, round.ID)
		if e != nil || requested {
			return failed(phase, e)
		}
		if e = w.repo.StartPhase(execution, round.ID, phase, w.now().UTC().Truncate(time.Microsecond)); e != nil {
			return failed(phase, e)
		}
		phaseCtx := repository.WithRoundStage(execution, round.ID, phase)
		result, e := w.execute(phaseCtx, *source, phase)
		if e != nil {
			return failed(phase, e)
		}
		if e = execution.Err(); e != nil {
			return failed(phase, e)
		}
		if e = w.repo.CompletePhase(execution, round.ID, phase, result, w.now().UTC().Truncate(time.Microsecond)); e != nil {
			return failed(phase, e)
		}
	}
	if err = finish("succeeded", ""); err != nil {
		return failed("completion", err)
	}
	return true, nil
}

func (w *SyncRoundWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		processed, err := w.ProcessNext(ctx)
		if err != nil {
			var recorded *recordedRoundFailure
			if !errors.As(err, &recorded) {
				return err
			}
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
