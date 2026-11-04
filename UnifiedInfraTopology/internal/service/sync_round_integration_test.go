package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/migration"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/glebarez/sqlite"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRoundAPIQueueToWorkerDurablePhases(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	defer pool.Close()
	ctx := context.Background()
	require.NoError(t, migration.Apply(ctx, db))
	source := model.Source{ID: "00000000000000000000000000000238", Name: "IDC238", ConfigRef: "test", AdapterKind: "cmdb", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.Create(&source).Error)
	config := viper.New()
	config.Set("inventory.cursor_key", strings.Repeat("x", 32))
	config.Set("inventory.grants", []map[string]any{{"user_id": "operator", "permissions": []string{"sync:read", "sync:write"}}})
	base := repository.NewRepository(nil, db)
	control := repository.NewInventoryRoundRepository(base)
	management := NewInventoryRoundService(control, config)
	round, err := management.Enqueue(ctx, "operator", "round-1", EnqueueInventoryRound{SourceID: source.ID, Mode: "full"})
	require.NoError(t, err)
	require.Equal(t, "queued", round.Status)
	replay, err := management.Enqueue(ctx, "operator", "round-1", EnqueueInventoryRound{SourceID: source.ID, Mode: "full"})
	require.NoError(t, err)
	require.Equal(t, round.ID, replay.ID)
	_, err = management.Enqueue(ctx, "operator", "round-2", EnqueueInventoryRound{SourceID: source.ID, Mode: "full"})
	require.ErrorIs(t, err, ErrInventoryConflict)
	calls := 0
	worker := NewSyncRoundWorker(control, func(stageCtx context.Context, actual model.Source, name string) (repository.PhaseResult, error) {
		calls++
		at := SyncSnapshot(stageCtx, time.Time{})
		require.False(t, at.IsZero())
		require.Equal(t, source.ID, actual.ID)
		run := &model.SyncRun{ID: fmt.Sprintf("%032x", calls), SourceID: source.ID, Mode: "full", Status: "queued", RequestHash: strings.Repeat("a", 64), IdempotencyKey: fmt.Sprintf("child-%d", calls), RequestedBy: "worker", CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
		ordinary := repository.NewInventoryRepository(base)
		_, e := ordinary.Enqueue(stageCtx, run)
		require.NoError(t, e)
		_, _, e = ordinary.ClaimNext(ctx, time.Now())
		require.ErrorIs(t, e, repository.ErrInventoryNoRun, "普通worker不可抢领整轮子任务")
		childRepo := repository.NewInventoryRunRepository(base, run.ID)
		claimed, _, e := childRepo.ClaimNext(stageCtx, time.Now().UTC().Truncate(time.Microsecond))
		require.NoError(t, e)
		require.Equal(t, run.ID, claimed.ID)
		for _, transition := range [][2]string{{"running", "validating"}, {"validating", "publishing"}, {"publishing", "succeeded"}} {
			_, e = childRepo.TransitionRun(stageCtx, run.ID, transition[0], transition[1], time.Now().UTC().Truncate(time.Microsecond), "")
			require.NoError(t, e)
		}
		_, phases, e := management.Get(ctx, "operator", round.ID)
		require.NoError(t, e)
		require.Equal(t, name, phases[calls-1].Name)
		require.Equal(t, "running", phases[calls-1].Status)
		require.Equal(t, run.ID, phases[calls-1].RunID)
		return repository.PhaseResult{RunID: run.ID, Nodes: 2, Edges: 3, Objects: 5, Skipped: 1, Reasons: map[string]int{"missing_reference": 1}}, nil
	})
	processed, err := worker.ProcessNext(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	current, phases, err := management.Get(ctx, "operator", round.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", current.Status)
	require.NotNil(t, current.SnapshotAt)
	require.Nil(t, current.LeaseExpiresAt)
	require.Len(t, phases, 15)
	for _, phase := range phases {
		require.Equal(t, "succeeded", phase.Status)
		require.NotEmpty(t, phase.RunID)
		require.EqualValues(t, 2, phase.Nodes)
		require.EqualValues(t, 1, phase.Skipped)
	}
	diagnostics, err := management.Diagnostics(ctx, "operator", round.ID, true, InventoryQuery{Limit: 200})
	require.NoError(t, err)
	require.Len(t, diagnostics.Items, 15)
}
