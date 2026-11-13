package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

var safeSyncErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

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
		var gpuFailure *GPUSyncError
		if errors.As(cause, &gpuFailure) && strings.HasPrefix(gpuFailure.Code, "gpu_") && safeSyncErrorCode.MatchString(gpuFailure.Code) {
			code = gpuFailure.Code
		}
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
	stored, phases, planErr := w.repo.Get(execution, round.ID)
	planValid := planErr == nil && stored != nil && stored.ID == round.ID && stored.SourceID == source.ID && stored.Status == "running" && len(phases) == len(model.SyncRoundPhaseNames())
	if planValid {
		for i, name := range model.SyncRoundPhaseNames() {
			if phases[i].RoundID != round.ID || phases[i].Name != name || phases[i].Position != i+1 || phases[i].Status != "queued" {
				planValid = false
				break
			}
		}
	}
	if !planValid {
		if err := finish("failed", "round_phase_plan_changed"); err != nil {
			return true, errors.New("round phase plan failure could not be recorded")
		}
		return true, &recordedRoundFailure{message: "round phase plan changed; create a new round for the current room stage"}
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
			var gpuFailure *GPUSyncError
			if phase == "gpu_sync" && execution.Err() == nil && errors.As(e, &gpuFailure) && result.RunID != "" && result.ErrorCode == gpuFailure.Code {
				if persistErr := w.repo.CompletePhase(execution, round.ID, phase, result, w.now().UTC().Truncate(time.Microsecond)); persistErr != nil {
					return failed(phase, fmt.Errorf("failure statistics persistence: %w", e))
				}
			}
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
