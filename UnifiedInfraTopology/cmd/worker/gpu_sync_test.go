package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"github.com/stretchr/testify/require"
)

type gpuRunnerTest func(context.Context, model.Source, time.Time) (service.GPUSyncResult, error)

func (f gpuRunnerTest) Run(c context.Context, s model.Source, t time.Time) (service.GPUSyncResult, error) {
	return f(c, s, t)
}
func TestGPUSyncExecutionCheckpointCountsNodesNotEdges(t *testing.T) {
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	c := &relationResourceControlTest{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16)}}
	result, err := executeGPUSync(context.Background(), c, gpuRunnerTest(func(context.Context, model.Source, time.Time) (service.GPUSyncResult, error) {
		return service.GPUSyncResult{Nodes: 2, Edges: 5, Objects: 2}, nil
	}), run, model.Source{ID: "source"}, time.Hour, func(_ context.Context, _ time.Time, n int64) error { require.Equal(t, int64(2), n); return nil })
	require.NoError(t, err)
	require.Equal(t, 5, result.Edges)
	require.Equal(t, 2, c.cursor.Total)
	require.Equal(t, []string{gpuSyncResource, gpuSyncResource, gpuSyncResource}, c.seen)
}
func TestGPUGraphRequiresUniqueTypedOwnerForEveryNode(t *testing.T) {
	at := serverUplinkNow()
	stamp := at.Format(time.RFC3339Nano)
	gpu := model.EntityIdentity{SourceID: "source", EntityType: model.EntityGPU, StableID: "gpu"}
	server := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "server"}
	vid, _ := gpu.VID()
	serverVID, _ := server.VID()
	id := model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: server, To: gpu}
	rid, _ := id.ID()
	rank, _ := id.Rank()
	for _, failure := range []string{"", "missing", "duplicate", "type", "source", "uuid", "vid"} {
		t.Run(failure, func(t *testing.T) {
			c := &relationCLIClient{t: t, query: func(q string, p map[string]interface{}) (int, [][]any) {
				require.NotContains(t, q, "LIMIT")
				require.NotContains(t, q, "source_relation_id")
				if strings.Contains(q, "LOOKUP ON gpu") && !strings.Contains(q, "count(*)") {
					require.Contains(t, q, "gpu.parts_sn")
				}
				if strings.Contains(q, "MATCH") {
					require.Contains(t, q, "g.gpu.parts_sn")
				}
				switch {
				case strings.Contains(q, "LOOKUP ON gpu"):
					if strings.Contains(q, "count(*)") {
						return 1, [][]any{{int64(1)}}
					}
					row := []any{vid, "gpu", "source", stamp, stamp}
					if failure == "uuid" {
						row[1] = "bad"
					}
					if failure == "vid" {
						row[0] = "bad"
					}
					return 5, [][]any{row}
				case strings.Contains(q, "LOOKUP ON composition_relation"):
					return 8, [][]any{{serverVID, vid, rank, rid, stamp, stamp, "source", "contains_gpu"}}
				case strings.Contains(q, "LOOKUP ON network_relation"):
					return 9, nil
				default:
					require.NotContains(t, q, "device_type_id ==")
					require.NotContains(t, q, "parent_type_id ==")
					row := []any{rid, rank, serverVID, vid, "server", "source", "gpu", "source", int64(52), int64(58)}
					if failure == "missing" {
						return 10, nil
					}
					if failure == "type" {
						row[9] = int64(57)
					}
					if failure == "source" {
						row[5] = "other"
					}
					if failure == "duplicate" {
						return 10, [][]any{row, row}
					}
					return 10, [][]any{row}
				}
			}}
			r, err := verifyGPUGraph(context.Background(), c, "source", at, 1)
			if failure == "" {
				require.NoError(t, err)
				require.Equal(t, int64(1), r.ContainsGPU)
				require.False(t, r.UplinkSourceIdentityVerified)
			} else {
				require.Error(t, err)
			}
		})
	}
}
func TestGPUUplinksCheckAllTypedToROwnershipPaths(t *testing.T) {
	at := serverUplinkNow()
	stamp := at.Format(time.RFC3339Nano)
	gpu := model.EntityIdentity{SourceID: "source", EntityType: model.EntityGPU, StableID: "gpu"}
	server := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "server"}
	port := model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "port"}
	gv, _ := gpu.VID()
	sv, _ := server.VID()
	pv, _ := port.VID()
	contains := model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: server, To: gpu}
	cid, _ := contains.ID()
	crank, _ := contains.Rank()
	uplink := model.Relation{Identity: model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeNetwork, Kind: model.RelationGPUUplink, From: gpu, To: port, SourceRelationID: "upstream-uuid"}, Properties: map[string]any{"tor_port": "Ethernet1"}, CreatedAt: at, SyncedAt: at}
	normalized, err := model.NormalizeRelation(uplink)
	require.NoError(t, err)
	uid, _ := normalized.Identity.ID()
	urank, _ := normalized.Identity.Rank()
	for _, failure := range []string{"", "aggregation", "virtual", "logical", "owner type", "owner source", "port source", "ownership source", "duplicate", "missing", "port vid", "rank", "blank tor port", "port name mismatch"} {
		t.Run(failure, func(t *testing.T) {
			client := &relationCLIClient{t: t, query: func(q string, _ map[string]interface{}) (int, [][]any) {
				require.NotContains(t, q, "LIMIT")
				require.NotContains(t, q, "parent_type_id ==")
				require.NotContains(t, q, "port_type ==")
				switch {
				case strings.Contains(q, "LOOKUP ON gpu"):
					if strings.Contains(q, "count(*)") {
						return 1, [][]any{{int64(1)}}
					}
					return 5, [][]any{{gv, "gpu", "source", stamp, stamp}}
				case strings.Contains(q, "LOOKUP ON composition_relation"):
					return 8, [][]any{{sv, gv, crank, cid, stamp, stamp, "source", "contains_gpu"}}
				case strings.Contains(q, "LOOKUP ON network_relation"):
					row := []any{gv, pv, urank, uid, "Ethernet1", stamp, stamp, "source", "gpu_uplink"}
					if failure == "blank tor port" {
						row[4] = ""
					}
					return 9, [][]any{row}
				case strings.Contains(q, "MATCH (d:device)"):
					return 10, [][]any{{cid, crank, sv, gv, "server", "source", "gpu", "source", int64(52), int64(58)}}
				default:
					row := []any{uid, urank, gv, pv, "gpu", "source", "port", "source", "physics", "tor", "source", int64(54), "source", "Ethernet1", "Ethernet1"}
					switch failure {
					case "port name mismatch":
						row[14] = "Ethernet2"
					case "aggregation", "virtual", "logical":
						row[8] = failure
					case "owner type":
						row[11] = int64(52)
					case "owner source":
						row[10] = "other"
					case "port source":
						row[7] = "other"
					case "ownership source":
						row[12] = "other"
					case "port vid":
						row[3] = "wrong"
					case "rank":
						row[1] = urank + 1
					case "missing":
						return 15, nil
					case "duplicate":
						return 15, [][]any{row, row}
					}
					return 15, [][]any{row}
				}
			}}
			r, e := verifyGPUGraph(context.Background(), client, "source", at, 1)
			if failure == "" || failure == "aggregation" || failure == "virtual" || failure == "logical" {
				require.NoError(t, e)
				require.Equal(t, int64(1), r.GPUUplink)
				require.False(t, r.UplinkSourceIdentityVerified)
			} else {
				require.Error(t, e)
			}
		})
	}
}

func TestStandaloneTopologyModesRejectConflicts(t *testing.T) {
	require.True(t, validStandaloneTopologyModes(true, false, false, false, nil, "source", "", ""))
	require.True(t, validStandaloneTopologyModes(false, false, false, true, nil, "source", "", "run"))
	for _, args := range []struct {
		pod, vpod, gpu, vgpu bool
		other                []bool
		source, entity, run  string
	}{{true, true, false, false, nil, "source", "", ""}, {true, false, true, false, nil, "source", "", ""}, {false, false, true, false, []bool{true}, "source", "", ""}, {true, false, false, false, nil, "source", "device", ""}, {false, false, true, false, nil, "source", "", "run"}, {false, false, false, true, nil, "", "", ""}} {
		require.False(t, validStandaloneTopologyModes(args.pod, args.vpod, args.gpu, args.vgpu, args.other, args.source, args.entity, args.run))
	}
}
