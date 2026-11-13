package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSpatialNormalizerAcceptsRoomPaths(t *testing.T) {
	for _, pair := range [][2]model.EntityType{{model.EntityRoom, model.EntityDataCenter}, {model.EntityCabinet, model.EntityRoom}, {model.EntityRPP, model.EntityRoom}} {
		from := "from"
		if pair[0] == model.EntityRPP {
			from = "10934"
		}
		relation := model.Relation{Identity: model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: model.EntityIdentity{SourceID: "s", EntityType: pair[0], StableID: from}, To: model.EntityIdentity{SourceID: "s", EntityType: pair[1], StableID: "to"}}}
		rows, err := normalizeSpatialRelations([]model.Relation{relation}, "s", time.Now().UTC())
		require.NoError(t, err)
		require.Len(t, rows, 1)
	}
}

func TestEntityWorkerPublishesRoomCheckpoint(t *testing.T) {
	at := time.Now().UTC()
	lease := at.Add(time.Minute)
	runs := &fakeWorkerRuns{source: &model.Source{ID: "s", AdapterKind: "cmdb"}, checkpoint: &repository.DeviceSyncCheckpoint{}, run: &model.SyncRun{ID: "room-run", SourceID: "s", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}}
	source := &fakeDeviceSource{selected: model.EntityRoom, filtered: true, entityPages: []adapter.EntityPage{{Total: 1, Done: true, NextOffset: 1, Records: []adapter.EntityRecord{{SourceInstanceID: 3101, Identity: "room-uuid", Properties: map[string]any{"full_name": "A4_204", "inst_id": int64(3101), "idc_id": int64(1030)}}}}}}
	graph := &fakeDeviceGraph{}
	worker := NewSyncWorker(nil, runs, graph, source, nil)
	require.NoError(t, worker.ProcessClaimed(context.Background(), runs.run, model.Source{ID: "s", AdapterKind: "cmdb"}))
	require.Equal(t, model.SyncRunStatusSucceeded, runs.run.Status)
	require.Len(t, graph.vertices, 1)
	require.Equal(t, model.EntityRoom, graph.vertices[0].Identity.EntityType)
	require.NotEmpty(t, runs.advances)
	require.True(t, runs.completeCalled)
}
