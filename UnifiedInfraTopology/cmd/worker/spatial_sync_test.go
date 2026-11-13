package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestSpatialGraphVerifiesAllKindsAndTypedPaths(t *testing.T) {
	at := serverUplinkNow()
	stamp := at.Format(time.RFC3339Nano)
	identities := []model.RelationIdentity{
		{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "device-sn"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityCabinet, StableID: "cabinet-uuid"}},
		{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityCabinet, StableID: "cabinet-uuid"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityDataCenter, StableID: "idc-uuid"}},
		{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationTaggedWith, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityCabinet, StableID: "cabinet-uuid"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityPod, StableID: "pod-uuid"}},
		{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityRoom, StableID: "room-uuid"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityDataCenter, StableID: "idc-uuid"}},
		{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityCabinet, StableID: "cabinet-uuid"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityRoom, StableID: "room-uuid"}},
		{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityRPP, StableID: "42"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityRoom, StableID: "room-uuid"}},
	}
	for _, failure := range []string{"", "count", "orphan", "duplicate path", "duplicate edge", "source", "identity", "rank", "vid", "timestamp", "created", "wrong kind", "double tag", "UUID", "edge source"} {
		t.Run(failure, func(t *testing.T) {
			client := &relationCLIClient{t: t, query: func(q string, p map[string]interface{}) (int, [][]any) {
				require.Equal(t, "source", p["source_id"])
				require.NotContains(t, q, "member_of")
				require.NotContains(t, q, "LIMIT")
				require.NotContains(t, q, "OPTIONAL")
				require.NotContains(t, q, " AS go")
				if strings.Contains(q, "count(*)") {
					return 1, [][]any{{int64(len(identities))}}
				}
				var rows [][]any
				for index, identity := range identities {
					id, _ := identity.ID()
					rank, _ := identity.Rank()
					from, _ := identity.From.VID()
					to, _ := identity.To.VID()
					if strings.Contains(q, "LOOKUP") {
						row := []any{from, to, rank, id, string(identity.Kind), stamp, stamp, "source"}
						if index == 0 {
							switch failure {
							case "identity":
								row[3] = "wrong"
							case "rank":
								row[2] = rank + 1
							case "vid":
								row[0] = "wrong"
							case "timestamp":
								row[6] = at.Add(time.Second).Format(time.RFC3339Nano)
							case "created":
								row[5] = at.Add(time.Second).Format(time.RFC3339Nano)
							case "wrong kind":
								row[4] = "tagged_with"
							case "edge source":
								row[7] = "other"
							}
						}
						rows = append(rows, row)
						if index == 0 && failure == "duplicate edge" {
							rows = append(rows, row)
						}
						continue
					}
					require.Contains(t, q, "ORDER BY relation_id ASC")
					pattern := "(f:" + string(identity.From.EntityType) + ")-[e:spatial_relation]->(t:" + string(identity.To.EntityType) + ")"
					if !strings.Contains(q, pattern) {
						continue
					}
					if identity.From.EntityType == model.EntityRPP {
						require.Contains(t, q, "f.rpp.inst_id AS from_identity")
					}
					if identity.To.EntityType == model.EntityRoom {
						require.Contains(t, q, "t.room.uuid AS to_identity")
					}
					if index == 0 && failure == "orphan" {
						continue
					}
					row := []any{id, rank, from, to, identity.From.StableID, "source", identity.To.StableID, "source", int64(1), int64(1)}
					if index == 0 && failure == "source" {
						row[7] = "other"
					}
					if index == 0 && failure == "double tag" {
						row[8] = int64(2)
					}
					if index == 0 && failure == "UUID" {
						row[6] = "wrong-uuid"
					}
					rows = append(rows, row)
					if index == 0 && failure == "duplicate path" {
						rows = append(rows, row)
					}
				}
				if strings.Contains(q, "LOOKUP") {
					return 8, rows
				}
				return 10, rows
			}}
			expected := int64(len(identities))
			if failure == "count" {
				expected++
			}
			report, err := verifySpatialGraph(context.Background(), client, "source", at, expected)
			if failure != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(len(identities)), report.ValidEndpointCount)
			require.Equal(t, map[string]int64{"located_in": 5, "tagged_with": 1}, report.KindCounts)
			require.Equal(t, map[string]int64{"device_cabinet": 1, "cabinet_data_center": 1, "cabinet_pod": 1, "room_data_center": 1, "cabinet_room": 1, "rpp_room": 1}, report.PathCounts)
		})
	}
}

func TestSpatialGraphPreflightAndEmptySnapshot(t *testing.T) {
	for _, at := range []time.Time{{}, serverUplinkNow()} {
		client := &relationCLIClient{t: t, query: func(q string, _ map[string]interface{}) (int, [][]any) {
			if strings.Contains(q, "count(*)") {
				return 1, [][]any{{int64(0)}}
			}
			require.False(t, at.IsZero(), "preflight must not inspect a fake snapshot")
			if strings.Contains(q, "LOOKUP") {
				return 8, nil
			}
			return 10, nil
		}}
		r, err := verifySpatialGraph(context.Background(), client, "source", at, 0)
		require.NoError(t, err)
		require.Zero(t, r.Total)
		require.Zero(t, r.Current)
	}
}

type spatialRunnerTest func(context.Context, model.Source, time.Time) (service.SpatialSyncResult, error)

func (f spatialRunnerTest) Run(c context.Context, s model.Source, at time.Time) (service.SpatialSyncResult, error) {
	return f(c, s, at)
}

func TestSpatialExecutionUsesEdgeCheckpoint(t *testing.T) {
	for _, fail := range []bool{false, true} {
		at := serverUplinkNow()
		lease := at.Add(time.Minute)
		run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
		control := &relationResourceControlTest{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16)}}
		want := service.SpatialSyncResult{Edges: 3, Objects: 7, Skipped: 2, MissingEndpoints: 1, Reasons: map[string]int{"missing": 1}}
		result, err := executeSpatialSync(context.Background(), control, spatialRunnerTest(func(context.Context, model.Source, time.Time) (service.SpatialSyncResult, error) {
			if fail {
				return want, errors.New("collect failed")
			}
			return want, nil
		}), run, model.Source{ID: "source"}, time.Hour, func(_ context.Context, _ time.Time, n int64) error { require.Equal(t, int64(3), n); return nil })
		require.Equal(t, want, result)
		if fail {
			require.Error(t, err)
			require.Equal(t, []string{spatialSyncResource}, control.seen)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, 3, control.cursor.Total)
		require.Zero(t, control.cursor.LastInstanceID)
		require.Equal(t, []string{spatialSyncResource, spatialSyncResource, spatialSyncResource}, control.seen)
	}
}

func TestSpatialEntryRejectsInvalidConfiguration(t *testing.T) {
	conf := viper.New()
	require.Error(t, runSpatialSync(context.Background(), conf, nil, "bad", io.Discard))
	require.Error(t, verifySpatialSync(context.Background(), conf, "bad", "bad", io.Discard))
}
