package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/migration"
	"UnifiedInfraTopology/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func roundFixture(t *testing.T) (InventoryRoundRepository, InventoryRepository, *Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/round.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, migration.Apply(context.Background(), db))
	require.NoError(t, db.Create(&model.Source{ID: strings.Repeat("a", 32), Name: "primary", Enabled: true}).Error)
	r := NewRepository(nil, db)
	return NewInventoryRoundRepository(r), NewInventoryRepository(r), r, db
}

func roundRequest(id string) *model.SyncRound {
	return &model.SyncRound{ID: strings.Repeat(id, 32), SourceID: strings.Repeat("a", 32), Status: "queued", Mode: "full", RequestHash: strings.Repeat(id, 64), IdempotencyKey: id, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
}

func TestInventoryRoundLifecycleAndDiagnostics(t *testing.T) {
	rounds, runs, r, db := roundFixture(t)
	ctx := context.Background()
	request := roundRequest("1")
	created, err := rounds.Enqueue(ctx, request)
	require.NoError(t, err)
	require.Equal(t, request.ID, created.ID)
	_, phases, err := rounds.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, phases, len(model.SyncRoundPhaseNames()))
	now := time.Now().UTC().Truncate(time.Microsecond)
	round, source, err := rounds.ClaimNext(ctx, now)
	require.NoError(t, err)
	require.Equal(t, request.SourceID, source.ID)
	require.Equal(t, now, *round.SnapshotAt)
	require.Equal(t, now.Add(15*time.Minute), *round.LeaseExpiresAt)
	require.ErrorIs(t, rounds.StartPhase(ctx, round.ID, model.SyncRoundPhaseNames()[1], now), ErrInventoryConflict)
	require.ErrorIs(t, rounds.Finish(ctx, round.ID, "succeeded", "", now), ErrInventoryConflict)
	for i, name := range model.SyncRoundPhaseNames() {
		at := now.Add(time.Duration(i) * time.Second)
		require.NoError(t, rounds.StartPhase(ctx, round.ID, name, at))
		stage := WithRoundStage(ctx, round.ID, name)
		child := &model.SyncRun{ID: fmt.Sprintf("%032x", i+2), SourceID: request.SourceID, Status: "queued", Mode: "full", IdempotencyKey: name, RequestHash: strings.Repeat("2", 64), CreatedAt: at}
		_, err = runs.Enqueue(stage, child)
		require.NoError(t, err)
		_, _, err = NewInventoryRunRepository(r, child.ID).ClaimNext(stage, at)
		require.NoError(t, err)
		for _, transition := range [][2]string{{"running", "validating"}, {"validating", "publishing"}, {"publishing", "succeeded"}} {
			_, err = runs.TransitionRun(stage, child.ID, transition[0], transition[1], at, "")
			require.NoError(t, err)
		}
		require.NoError(t, rounds.CompletePhase(ctx, round.ID, name, PhaseResult{RunID: child.ID, Nodes: 2, Edges: 3, Objects: 4, Skipped: 1, MissingEndpoints: 1, Reasons: map[string]int{"missing_endpoint": 1, "raw secret / credential": 7}}, at))
	}
	require.NoError(t, rounds.Finish(ctx, round.ID, "succeeded", "", now.Add(time.Minute)))
	round, phases, err = rounds.Get(ctx, round.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", round.Status)
	require.Nil(t, round.LeaseExpiresAt)
	for _, p := range phases {
		require.Equal(t, "succeeded", p.Status)
		require.EqualValues(t, 2, p.Nodes)
		require.EqualValues(t, 3, p.Edges)
	}
	diagnostics, err := rounds.Diagnostics(ctx, round.ID, true, InventoryListQuery{Limit: 10})
	require.NoError(t, err)
	require.True(t, diagnostics.HasMore)
	rows := diagnostics.Items.([]model.SyncDiagnostic)
	require.Len(t, rows, 10)
	for _, d := range rows {
		require.Equal(t, "missing_endpoint", d.Code)
		require.Equal(t, "1", d.Detail)
		require.Empty(t, d.ObjectRef)
	}
	var n int64
	require.NoError(t, db.Table("sync_diagnostics").Count(&n).Error)
	require.EqualValues(t, len(model.SyncRoundPhaseNames()), n)
}

func TestInventoryRoundChildOwnership(t *testing.T) {
	rounds, runs, r, db := roundFixture(t)
	ctx := context.Background()
	request := roundRequest("1")
	_, err := rounds.Enqueue(ctx, request)
	require.NoError(t, err)
	child := &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: request.SourceID, Status: "queued", Mode: "full", RequestHash: strings.Repeat("2", 64), IdempotencyKey: "child", CreatedAt: time.Now().UTC()}
	_, err = runs.Enqueue(ctx, child)
	require.ErrorIs(t, err, ErrInventoryConflict)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, _, err = rounds.ClaimNext(ctx, now)
	require.NoError(t, err)
	name := model.SyncRoundPhaseNames()[0]
	stage := WithRoundStage(ctx, request.ID, name)
	_, err = runs.Enqueue(stage, child)
	require.ErrorIs(t, err, ErrInventoryConflict)
	require.NoError(t, rounds.StartPhase(ctx, request.ID, name, now))
	_, err = runs.Enqueue(stage, child)
	require.NoError(t, err)
	_, phases, err := rounds.Get(ctx, request.ID)
	require.NoError(t, err)
	require.Equal(t, child.ID, phases[0].RunID)
	_, _, err = runs.ClaimNext(ctx, now)
	require.ErrorIs(t, err, ErrInventoryNoRun)
	_, _, err = NewInventoryRunRepository(r, child.ID).ClaimNext(ctx, now)
	require.ErrorIs(t, err, ErrInventoryConflict)
	claimed, _, err := NewInventoryRunRepository(r, child.ID).ClaimNext(stage, now)
	require.NoError(t, err)
	require.Equal(t, child.ID, claimed.ID)
	_, accepted, err := rounds.Cancel(ctx, request.ID)
	require.NoError(t, err)
	require.True(t, accepted)
	canceled, err := runs.Run(ctx, child.ID)
	require.NoError(t, err)
	require.NotNil(t, canceled.CancelRequestedAt)
	_, err = runs.Enqueue(stage, &model.SyncRun{ID: strings.Repeat("3", 32), SourceID: request.SourceID, Status: "queued", Mode: "full", RequestHash: "hash", IdempotencyKey: "another"})
	require.ErrorIs(t, err, ErrInventoryConflict)
	require.NoError(t, rounds.Finish(ctx, request.ID, "canceled", "", now.Add(time.Second)))
	_, phases, err = rounds.Get(ctx, request.ID)
	require.NoError(t, err)
	require.Equal(t, "canceled", phases[0].Status)
	for _, p := range phases[1:] {
		require.Equal(t, "not_run", p.Status)
	}
	require.True(t, db.Migrator().HasTable("sync_rounds"))
}

func TestInventoryRoundLeaseExpiryFailsParentAndChildren(t *testing.T) {
	rounds, runs, r, _ := roundFixture(t)
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
	late := now.Add(15 * time.Minute)
	require.ErrorIs(t, rounds.Renew(ctx, request.ID, late), ErrInventoryConflict)
	require.ErrorIs(t, rounds.CompletePhase(ctx, request.ID, name, PhaseResult{}, late), ErrInventoryConflict)
	_, _, err = rounds.ClaimNext(ctx, late)
	require.ErrorIs(t, err, ErrInventoryNoRun)
	round, phases, err := rounds.Get(ctx, request.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", round.Status)
	require.Equal(t, "worker_lease_expired", round.ErrorCode)
	require.Equal(t, "failed", phases[0].Status)
	for _, p := range phases[1:] {
		require.Equal(t, "not_run", p.Status)
	}
	run, err := runs.Run(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", run.Status)
}

func TestInventoryRoundEnqueueReplayCancelAndList(t *testing.T) {
	rounds, runs, _, db := roundFixture(t)
	ctx := context.Background()
	first := roundRequest("1")
	_, err := rounds.Enqueue(ctx, first)
	require.NoError(t, err)
	_, err = rounds.Enqueue(ctx, roundRequest("2"))
	require.ErrorIs(t, err, ErrInventoryConflict)
	require.NoError(t, db.Model(&model.Source{}).Where("id = ?", first.SourceID).Update("enabled", false).Error)
	replay, err := rounds.Enqueue(ctx, first)
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	changed := *first
	changed.RequestHash = "different"
	_, err = rounds.Enqueue(ctx, &changed)
	require.ErrorIs(t, err, ErrInventoryIdempotency)
	round, accepted, err := rounds.Cancel(ctx, first.ID)
	require.NoError(t, err)
	require.False(t, accepted, "排队任务已立即取消，不是等待worker处理的异步请求")
	require.Equal(t, "canceled", round.Status)
	_, accepted, err = rounds.Cancel(ctx, first.ID)
	require.NoError(t, err)
	require.False(t, accepted)
	_, err = rounds.Enqueue(ctx, roundRequest("2"))
	require.ErrorIs(t, err, ErrInventorySource)
	require.NoError(t, db.Model(&model.Source{}).Where("id = ?", first.SourceID).Update("enabled", true).Error)
	for i := 2; i < 5; i++ {
		request := roundRequest(fmt.Sprint(i))
		_, err = rounds.Enqueue(ctx, request)
		require.NoError(t, err)
		_, _, err = rounds.Cancel(ctx, request.ID)
		require.NoError(t, err)
	}
	page, err := rounds.List(ctx, InventoryListQuery{Limit: 2, SourceID: first.SourceID, Status: "canceled"})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	next, err := rounds.List(ctx, InventoryListQuery{Limit: 2, LastID: page.LastID, LastCreatedAt: page.LastCreatedAt})
	require.NoError(t, err)
	require.Len(t, next.Items.([]model.SyncRound), 2)
	require.False(t, next.HasMore)
	child := &model.SyncRun{ID: strings.Repeat("5", 32), SourceID: first.SourceID, Status: "queued", Mode: "full", RequestHash: strings.Repeat("5", 64), IdempotencyKey: "standalone", CreatedAt: time.Now().UTC()}
	_, err = runs.Enqueue(ctx, child)
	require.NoError(t, err)
	_, err = rounds.Enqueue(ctx, roundRequest("6"))
	require.ErrorIs(t, err, ErrInventoryConflict)
}
