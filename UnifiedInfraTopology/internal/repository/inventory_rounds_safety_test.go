package repository

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestInventoryRoundCompletionRejectsUnboundHistoricalRun(t *testing.T) {
	for _, runID := range []string{"", strings.Repeat("2", 32)} {
		t.Run("run_id_"+runID, func(t *testing.T) {
			rounds, runs, _, db := roundFixture(t)
			ctx := context.Background()
			request := roundRequest("1")
			historical := &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: request.SourceID, Status: "succeeded", Mode: "full", RequestHash: strings.Repeat("2", 64), IdempotencyKey: "historical", CreatedAt: time.Now().UTC()}
			_, err := runs.Enqueue(ctx, historical)
			require.NoError(t, err)
			_, err = rounds.Enqueue(ctx, request)
			require.NoError(t, err)
			now := time.Now().UTC().Truncate(time.Microsecond)
			_, _, err = rounds.ClaimNext(ctx, now)
			require.NoError(t, err)
			name := model.SyncRoundPhaseNames()[0]
			require.NoError(t, rounds.StartPhase(ctx, request.ID, name, now))
			require.ErrorIs(t, rounds.CompletePhase(ctx, request.ID, name, PhaseResult{RunID: runID, Reasons: map[string]int{"missing_endpoint": 1}}, now), ErrInventoryConflict)
			_, phases, err := rounds.Get(ctx, request.ID)
			require.NoError(t, err)
			require.Equal(t, "running", phases[0].Status)
			require.Empty(t, phases[0].RunID)
			var count int64
			require.NoError(t, db.Table("sync_diagnostics").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestInventoryRoundExpiredMutationPersistsFailure(t *testing.T) {
	for _, operation := range []string{"renew", "start", "complete", "finish"} {
		t.Run(operation, func(t *testing.T) {
			rounds, _, _, _ := roundFixture(t)
			ctx := context.Background()
			request := roundRequest("1")
			_, err := rounds.Enqueue(ctx, request)
			require.NoError(t, err)
			now := time.Now().UTC().Truncate(time.Microsecond)
			_, _, err = rounds.ClaimNext(ctx, now)
			require.NoError(t, err)
			name := model.SyncRoundPhaseNames()[0]
			require.NoError(t, rounds.StartPhase(ctx, request.ID, name, now))
			late := now.Add(15 * time.Minute)
			switch operation {
			case "renew":
				err = rounds.Renew(ctx, request.ID, late)
			case "start":
				err = rounds.StartPhase(ctx, request.ID, model.SyncRoundPhaseNames()[1], late)
			case "complete":
				err = rounds.CompletePhase(ctx, request.ID, name, PhaseResult{}, late)
			case "finish":
				err = rounds.Finish(ctx, request.ID, "failed", "stage_failed", late)
			}
			require.ErrorIs(t, err, ErrInventoryConflict)
			round, phases, err := rounds.Get(ctx, request.ID)
			require.NoError(t, err)
			require.Equal(t, "failed", round.Status)
			require.Equal(t, "worker_lease_expired", round.ErrorCode)
			require.Equal(t, "failed", phases[0].Status)
		})
	}
}

func TestInventorySourceExclusionUsesCurrentExistenceReads(t *testing.T) {
	for _, operation := range []string{"round_enqueue", "run_enqueue", "round_claim"} {
		t.Run(operation, func(t *testing.T) {
			rounds, runs, _, db := roundFixture(t)
			ctx := context.Background()
			request := roundRequest("1")
			if operation == "round_claim" {
				_, err := rounds.Enqueue(ctx, request)
				require.NoError(t, err)
			}
			checks := 0
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:current_exclusion", func(query *gorm.DB) {
				if query.Statement.Table != "sync_rounds" && query.Statement.Table != "sync_runs" {
					return
				}
				sql := query.Statement.SQL.String()
				if !strings.Contains(sql, "idempotency_key") && !strings.Contains(sql, "status IN") {
					return
				}
				checks++
				locking, ok := query.Statement.Clauses["FOR"].Expression.(clause.Locking)
				require.True(t, ok, "exclusion/idempotency read must escape an outer repeatable-read snapshot: %s", sql)
				require.Equal(t, "UPDATE", locking.Strength)
				require.NotContains(t, strings.ToLower(sql), "count(")
				require.Contains(t, sql, "LIMIT 1")
			}))
			switch operation {
			case "round_enqueue":
				_, err := rounds.Enqueue(ctx, request)
				require.NoError(t, err)
			case "run_enqueue":
				_, err := runs.Enqueue(ctx, &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: request.SourceID, Status: "queued", Mode: "full", IdempotencyKey: "standalone", RequestHash: strings.Repeat("2", 64), CreatedAt: time.Now().UTC()})
				require.NoError(t, err)
			case "round_claim":
				_, _, err := rounds.ClaimNext(ctx, time.Now().UTC())
				require.NoError(t, err)
			}
			if operation == "round_claim" {
				require.Equal(t, 2, checks)
			} else {
				require.Equal(t, 3, checks)
			}
		})
	}
}

func TestInventoryRoundSourceConcurrency(t *testing.T) {
	rounds, runs, _, db := roundFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			if i%2 == 0 {
				request := roundRequest(string(rune(49 + i)))
				_, err := rounds.Enqueue(ctx, request)
				errs <- err
				return
			}
			id := string(rune(49 + i))
			_, err := runs.Enqueue(ctx, &model.SyncRun{ID: strings.Repeat(id, 32), SourceID: strings.Repeat("a", 32), Status: "queued", Mode: "full", IdempotencyKey: id, RequestHash: strings.Repeat(id, 64), CreatedAt: time.Now().UTC()})
			errs <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, ErrInventoryConflict)
		}
	}
	require.Equal(t, 1, succeeded)
	var roundsCount, runsCount int64
	require.NoError(t, db.Model(&model.SyncRound{}).Count(&roundsCount).Error)
	require.NoError(t, db.Model(&model.SyncRun{}).Count(&runsCount).Error)
	require.EqualValues(t, 1, roundsCount+runsCount)
}

func TestInventoryGenericClaimPersistsStaleRoundWithoutStandaloneQueue(t *testing.T) {
	rounds, runs, _, _ := roundFixture(t)
	ctx := context.Background()
	request := roundRequest("1")
	_, err := rounds.Enqueue(ctx, request)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, _, err = rounds.ClaimNext(ctx, now)
	require.NoError(t, err)
	_, _, err = runs.ClaimNext(ctx, now.Add(15*time.Minute))
	require.ErrorIs(t, err, ErrInventoryNoRun)
	round, _, err := rounds.Get(ctx, request.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", round.Status)
}

func TestInventoryGenericClaimDoesNotExpireOwnedChild(t *testing.T) {
	rounds, runs, r, db := roundFixture(t)
	ctx := context.Background()
	request := roundRequest("1")
	_, err := rounds.Enqueue(ctx, request)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, _, err = rounds.ClaimNext(ctx, now)
	require.NoError(t, err)
	name := model.SyncRoundPhaseNames()[0]
	require.NoError(t, rounds.StartPhase(ctx, request.ID, name, now))
	stage := WithRoundStage(ctx, request.ID, name)
	child := &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: request.SourceID, Status: "queued", Mode: "full", RequestHash: strings.Repeat("2", 64), IdempotencyKey: "child", CreatedAt: now}
	_, err = runs.Enqueue(stage, child)
	require.NoError(t, err)
	_, _, err = NewInventoryRunRepository(r, child.ID).ClaimNext(stage, now)
	require.NoError(t, err)
	require.NoError(t, rounds.Renew(ctx, request.ID, now.Add(14*time.Minute)))
	standalone := &model.SyncRun{ID: strings.Repeat("3", 32), SourceID: strings.Repeat("b", 32), Status: "queued", Mode: "full", RequestHash: strings.Repeat("3", 64), IdempotencyKey: "standalone", CreatedAt: now}
	// 使用另一来源保证通用领取事务提交，暴露子任务过期的越权更新。
	require.NoError(t, db.Create(&model.Source{ID: standalone.SourceID, Name: "other", Enabled: true}).Error)
	_, err = runs.Enqueue(ctx, standalone)
	require.NoError(t, err)
	claimed, _, err := runs.ClaimNext(ctx, now.Add(16*time.Minute))
	require.NoError(t, err)
	require.Equal(t, standalone.ID, claimed.ID)
	run, err := runs.Run(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "running", run.Status)
}

func TestInventoryRoundCallerRollbackIncludesChildBinding(t *testing.T) {
	rounds, runs, r, db := roundFixture(t)
	ctx := context.Background()
	request := roundRequest("1")
	rollback := errors.New("rollback")
	err := r.Transaction(ctx, func(txCtx context.Context) error {
		_, err := rounds.Enqueue(txCtx, request)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		_, _, err = rounds.ClaimNext(txCtx, now)
		if err != nil {
			return err
		}
		name := model.SyncRoundPhaseNames()[0]
		if err = rounds.StartPhase(txCtx, request.ID, name, now); err != nil {
			return err
		}
		_, err = runs.Enqueue(WithRoundStage(txCtx, request.ID, name), &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: request.SourceID, Status: "queued", Mode: "full", IdempotencyKey: "child", RequestHash: strings.Repeat("2", 64), CreatedAt: now})
		if err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	for _, table := range []string{"sync_rounds", "sync_round_phases", "sync_runs"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count, table)
	}
}

func TestInventoryRoundFailedPhaseRetainsCountsAndStopsOrder(t *testing.T) {
	rounds, runs, r, _ := roundFixture(t)
	ctx := context.Background()
	request := roundRequest("1")
	_, err := rounds.Enqueue(ctx, request)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, _, err = rounds.ClaimNext(ctx, now)
	require.NoError(t, err)
	names := model.SyncRoundPhaseNames()
	require.NoError(t, rounds.StartPhase(ctx, request.ID, names[0], now))
	stage := WithRoundStage(ctx, request.ID, names[0])
	child := &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: request.SourceID, Status: "queued", Mode: "full", IdempotencyKey: "child", RequestHash: strings.Repeat("2", 64), CreatedAt: now}
	_, err = runs.Enqueue(stage, child)
	require.NoError(t, err)
	_, _, err = NewInventoryRunRepository(r, child.ID).ClaimNext(stage, now)
	require.NoError(t, err)
	_, err = runs.TransitionRun(stage, child.ID, "running", "failed", now, "stage_failed")
	require.NoError(t, err)
	require.NoError(t, rounds.CompletePhase(ctx, request.ID, names[0], PhaseResult{RunID: child.ID, Objects: 9, ErrorCode: "stage_failed"}, now))
	require.ErrorIs(t, rounds.StartPhase(ctx, request.ID, names[1], now), ErrInventoryConflict)
	require.NoError(t, rounds.Finish(ctx, request.ID, "failed", "stage_failed", now))
	_, phases, err := rounds.Get(ctx, request.ID)
	require.NoError(t, err)
	require.EqualValues(t, 9, phases[0].Objects)
	require.Equal(t, "failed", phases[0].Status)
	for _, phase := range phases[1:] {
		require.Equal(t, "not_run", phase.Status)
	}
}
