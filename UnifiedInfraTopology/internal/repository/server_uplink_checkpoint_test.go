package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestPODAndGPUResourceCheckpointsDoNotInventInstanceIDs(t *testing.T) {
	for _, resource := range []string{"pod_membership", "gpu_sync", "spatial_relations"} {
		t.Run(resource, func(t *testing.T) {
			repo, _, db := inventoryRepositoryFixture(t)
			at := time.Now().UTC().Truncate(time.Microsecond)
			lease := at.Add(time.Minute)
			source, run := strings.Repeat("c", 32), strings.Repeat("3", 32)
			require.NoError(t, db.Create(&model.SyncRun{ID: run, SourceID: source, Status: model.SyncRunStatusRunning, CreatedAt: at, StartedAt: &at, LeaseExpiresAt: &lease}).Error)
			_, err := repo.BeginCheckpoint(context.Background(), source, resource, run, at, at)
			require.NoError(t, err)
			cursor := DeviceSyncCursor{Version: 1, RunID: run, SnapshotAt: at, NextOffset: 2, Total: 2}
			_, err = repo.AdvanceCheckpoint(context.Background(), source, resource, run, cursor, at.Add(time.Second))
			require.NoError(t, err)
			require.Error(t, validateDeviceSyncCursor(cursor))
		})
	}
}

func TestPowerCheckpointTracksEdgeOffsetWithoutSyntheticInstanceID(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	lease := at.Add(time.Minute)
	source, run := strings.Repeat("b", 32), strings.Repeat("2", 32)
	require.NoError(t, db.Create(&model.SyncRun{ID: run, SourceID: source, Status: model.SyncRunStatusRunning, CreatedAt: at, StartedAt: &at, LeaseExpiresAt: &lease}).Error)
	_, err := repo.BeginCheckpoint(context.Background(), source, "power_relations", run, at, at)
	require.NoError(t, err)
	cursor := DeviceSyncCursor{Version: 1, RunID: run, SnapshotAt: at, NextOffset: 1, Total: 1}
	_, err = repo.AdvanceCheckpoint(context.Background(), source, "power_relations", run, cursor, at.Add(time.Second))
	require.NoError(t, err)
	require.Error(t, validateDeviceSyncCursor(cursor))
}

func TestServerUplinkCheckpointTracksEdgeOffsetWithoutSyntheticInstanceID(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	lease := at.Add(time.Minute)
	source, run := strings.Repeat("a", 32), strings.Repeat("1", 32)
	require.NoError(t, db.Create(&model.SyncRun{ID: run, SourceID: source, Status: model.SyncRunStatusRunning, CreatedAt: at, StartedAt: &at, LeaseExpiresAt: &lease}).Error)
	_, err := repo.BeginCheckpoint(context.Background(), source, "server_uplinks", run, at, at)
	require.NoError(t, err)
	cursor := DeviceSyncCursor{Version: 1, RunID: run, SnapshotAt: at, NextOffset: 2, Total: 2}
	checkpoint, err := repo.AdvanceCheckpoint(context.Background(), source, "server_uplinks", run, cursor, at.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, cursor, checkpoint.Cursor)
	require.Zero(t, checkpoint.Cursor.LastInstanceID)
	_, err = repo.AdvanceCheckpoint(context.Background(), source, "server_uplinks", run, cursor, at.Add(time.Second))
	require.Error(t, err)
	require.Error(t, validateDeviceSyncCursor(cursor), "普通实体仍需真实 inst_id")
}

func TestSpatialCheckpointNonemptyThenEmptySnapshot(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	source := strings.Repeat("e", 32)
	for index, total := range []int{3, 0} {
		snapshot := at.Add(time.Duration(index) * time.Minute)
		lease := snapshot.Add(time.Minute)
		run := strings.Repeat(string(rune('4'+index)), 32)
		require.NoError(t, db.Create(&model.SyncRun{ID: run, SourceID: source, Status: model.SyncRunStatusRunning, CreatedAt: snapshot, StartedAt: &snapshot, LeaseExpiresAt: &lease}).Error)
		checkpoint, err := repo.BeginCheckpoint(ctx, source, "spatial_relations", run, snapshot, snapshot)
		require.NoError(t, err)
		require.Zero(t, checkpoint.Cursor.Total)
		require.Zero(t, checkpoint.Cursor.NextOffset)
		if total > 0 {
			cursor := checkpoint.Cursor
			cursor.Total, cursor.NextOffset = total, total
			for _, bad := range []DeviceSyncCursor{
				{Version: 1, RunID: run, SnapshotAt: snapshot, NextOffset: total, Total: total, LastInstanceID: 1},
				{Version: 1, RunID: run, SnapshotAt: snapshot, NextOffset: total, Total: total, LastPartition: 1},
			} {
				_, err = repo.AdvanceCheckpoint(ctx, source, "spatial_relations", run, bad, snapshot.Add(time.Second))
				require.ErrorIs(t, err, ErrInventoryConflict)
			}
			_, err = repo.AdvanceCheckpoint(ctx, source, "spatial_relations", run, cursor, snapshot.Add(time.Second))
			require.NoError(t, err)
			_, err = repo.AdvanceCheckpoint(ctx, source, "spatial_relations", run, cursor, snapshot.Add(2*time.Second))
			require.ErrorIs(t, err, ErrInventoryConflict)
		}
		require.NoError(t, db.Model(&model.SyncRun{}).Where("id = ?", run).Update("status", model.SyncRunStatusPublishing).Error)
		completed, err := repo.CompleteCheckpointAndRun(ctx, source, "spatial_relations", run, model.SyncRunStatusPublishing, snapshot.Add(3*time.Second))
		require.NoError(t, err)
		require.Equal(t, model.SyncRunStatusSucceeded, completed.Status)
		checkpoint, err = repo.LoadCheckpoint(ctx, source, "spatial_relations")
		require.NoError(t, err)
		require.True(t, checkpoint.Complete)
		require.Equal(t, total, checkpoint.Cursor.Total)
		require.Equal(t, total, checkpoint.Cursor.NextOffset)
		require.Zero(t, checkpoint.Cursor.LastInstanceID)
		require.True(t, checkpoint.Cursor.SnapshotAt.Equal(snapshot))
	}
}
