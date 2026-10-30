package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

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
