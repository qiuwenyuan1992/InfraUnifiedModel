package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

type powerTestEdge struct {
	from, to   model.EntityType
	a, b, path string
	kind       model.RelationKind
	at         time.Time
}

func (e powerTestEdge) relation() model.Relation {
	r := model.Relation{Identity: model.RelationIdentity{SourceID: "source", EdgeType: model.EdgePower, Kind: e.kind, From: model.EntityIdentity{SourceID: "source", EntityType: e.from, StableID: e.a}, To: model.EntityIdentity{SourceID: "source", EntityType: e.to, StableID: e.b}}, CreatedAt: e.at, SyncedAt: e.at}
	if e.path != "" {
		r.Properties = map[string]any{"power_path": e.path}
	}
	r, _ = model.NormalizeRelation(r)
	return r
}
func powerTestEdges(at time.Time) []powerTestEdge {
	return []powerTestEdge{{model.EntityCabinet, model.EntityRPP, "cab", "101", "A", model.RelationPowerUpstream, at}, {model.EntityCabinet, model.EntityRPP, "cab", "101", "B", model.RelationPowerUpstream, at}, {model.EntityRPP, model.EntityUPSGroup, "101", "201", "", model.RelationPowerUpstream, at}, {model.EntityUPS, model.EntityUPSGroup, "301", "201", "", model.RelationMemberOf, at}, {model.EntityUPSGroup, model.EntityTransformer, "201", "401", "", model.RelationPowerUpstream, at}, {model.EntityTransformer, model.EntityTransformer, "401", "402", "", model.RelationHasStandby, at}}
}

type powerTestClient struct {
	t       *testing.T
	edges   []powerTestEdge
	failure string
}

func (*powerTestClient) Close() {}
func (c *powerTestClient) ExecuteParameter(_ context.Context, q string, p map[string]interface{}) (*nebulago.ResultSet, error) {
	require.Equal(c.t, "source", p["source_id"])
	require.NotContains(c.t, q, "LIMIT")
	if c.failure == "query" {
		return nil, errors.New("query failed")
	}
	var rows [][]any
	columns := 1
	switch {
	case strings.Contains(q, "count(*)"):
		require.NotContains(c.t, q, "relation_kind ==")
		rows = [][]any{{int64(len(c.edges))}}
	case strings.Contains(q, "LOOKUP"):
		columns = 9
		require.NotContains(c.t, q, "relation_kind ==")
		require.NotContains(c.t, q, "power_path ==")
		for i, e := range c.edges {
			r := e.relation()
			id, _ := r.Identity.ID()
			rank, _ := r.Identity.Rank()
			a, _ := r.Identity.From.VID()
			b, _ := r.Identity.To.VID()
			row := []any{a, b, rank, id, string(e.kind), e.path, e.at.Format(time.RFC3339Nano), e.at.Format(time.RFC3339Nano), "source"}
			if i == 0 {
				switch c.failure {
				case "rank":
					row[2] = rank + 1
				case "id":
					row[3] = "wrong"
				case "vid":
					row[0] = "wrong"
				case "kind":
					row[4] = "unknown"
				case "path":
					row[5] = "C"
				case "source":
					row[8] = "other"
				case "time":
					row[6] = e.at.Add(time.Second).Format(time.RFC3339Nano)
				}
			}
			if i == 2 && c.failure == "extra path" {
				row[5] = "A"
			}
			rows = append(rows, row)
		}
	default:
		columns = 10
		require.NotContains(c.t, q, "a."+"source_id ==")
		for i, e := range c.edges {
			if !strings.Contains(q, "(a:"+string(e.from)+")-[e:power_relation]->(b:"+string(e.to)+")") {
				continue
			}
			if i == 0 && (c.failure == "missing" || c.failure == "invalid type") {
				continue
			}
			r := e.relation()
			id, _ := r.Identity.ID()
			rank, _ := r.Identity.Rank()
			a, _ := r.Identity.From.VID()
			b, _ := r.Identity.To.VID()
			row := []any{id, rank, a, b, e.a, "source", "CODE-" + e.a, e.b, "source", "CODE-" + e.b}
			for _, endpoint := range []struct {
				kind     model.EntityType
				column   int
				identity string
				alias    string
			}{{e.from, 4, e.a, "a"}, {e.to, 7, e.b, "b"}} {
				field := "inst_id"
				if endpoint.kind == model.EntityCabinet {
					field = "uuid"
				} else {
					number, err := strconv.ParseInt(endpoint.identity, 10, 64)
					require.NoError(c.t, err)
					row[endpoint.column] = number
					require.NotContains(c.t, q, endpoint.alias+"."+string(endpoint.kind)+".uuid")
				}
				require.Contains(c.t, q, endpoint.alias+"."+string(endpoint.kind)+"."+field+" AS "+endpoint.alias+"_identity")
			}
			if i == 0 && c.failure == "endpoint source" {
				row[8] = "other"
			}
			if i == 0 && c.failure == "identity" {
				row[7] = int64(999)
			}
			rows = append(rows, row)
			if i == 0 && c.failure == "multiple" {
				rows = append(rows, row)
			}
		}
	}
	data := &nebula.DataSet{}
	for i := 0; i < columns; i++ {
		data.ColumnNames = append(data.ColumnNames, []byte(fmt.Sprint(i)))
	}
	for _, row := range rows {
		r := &nebula.Row{}
		for _, v := range row {
			switch x := v.(type) {
			case string:
				r.Values = append(r.Values, &nebula.Value{SVal: []byte(x)})
			case int64:
				n := x
				r.Values = append(r.Values, &nebula.Value{IVal: &n})
			}
		}
		data.Rows = append(data.Rows, r)
	}
	return nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
}
func TestPowerGraphAllModeledPathsAndRejectsCorruption(t *testing.T) {
	at := serverUplinkNow()
	for _, failure := range []string{"", "query", "rank", "id", "vid", "kind", "path", "extra path", "source", "time", "missing", "invalid type", "endpoint source", "identity", "multiple"} {
		t.Run(failure, func(t *testing.T) {
			r, err := verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: powerTestEdges(at), failure: failure}, "source", at, 6)
			if failure != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(6), r.Total)
			require.Equal(t, int64(6), r.Current)
			require.Equal(t, int64(6), r.ValidEndpointCount)
			require.Equal(t, int64(4), r.ByKind["power_upstream"])
			require.Equal(t, int64(1), r.ByKind["member_of"])
			require.Len(t, r.Samples, 5)
		})
	}
}
func TestPowerGraphStandbyCyclesAcceptedAndSelfRejected(t *testing.T) {
	at := serverUplinkNow()
	for _, size := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("nodes_%d", size), func(t *testing.T) {
			edges := []powerTestEdge{}
			for i := 0; i < size; i++ {
				edges = append(edges, powerTestEdge{model.EntityTransformer, model.EntityTransformer, strconv.Itoa(401 + i), strconv.Itoa(401 + (i+1)%size), "", model.RelationHasStandby, at})
			}
			report, err := verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: edges}, "source", at, int64(size))
			if size == 1 {
				require.Error(t, err, "自引用备用必须被拒绝")
				return
			}
			require.NoError(t, err, "多变压器备用环仅为 CMDB 配置，不代表主供电链路")
			require.Equal(t, int64(size), report.ValidEndpointCount)
			require.Equal(t, int64(size), report.ByKind["has_standby"])
			require.Zero(t, report.ByKind["power_upstream"])
		})
	}
}

func TestPowerGraphChecksFullStandbyNetworkAndPreflight(t *testing.T) {
	at := serverUplinkNow()
	edges := powerTestEdges(at)
	r, err := verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: edges}, "source", time.Time{}, 0)
	require.NoError(t, err)
	require.Equal(t, int64(6), r.Total)
	require.Zero(t, r.Current)
	edges = append(edges, powerTestEdge{model.EntityTransformer, model.EntityTransformer, "402", "401", "", model.RelationHasStandby, at.Add(-time.Hour)})
	_, err = verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: edges}, "source", at, 6)
	require.NoError(t, err, "保留的反向备用边允许构成二节点互备")
	edges = append(edges, powerTestEdge{model.EntityTransformer, model.EntityTransformer, "401", "403", "", model.RelationHasStandby, at.Add(-time.Hour)}, powerTestEdge{model.EntityTransformer, model.EntityTransformer, "403", "401", "", model.RelationHasStandby, at.Add(-time.Hour)})
	_, err = verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: edges}, "source", at, 6)
	require.NoError(t, err, "重叠互备对不构成三节点简单环")
	edges = append(edges, powerTestEdge{model.EntityTransformer, model.EntityTransformer, "402", "403", "", model.RelationHasStandby, at.Add(-time.Hour)})
	_, err = verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: edges}, "source", at, 6)
	require.NoError(t, err, "重叠互备和三节点备用环均为允许的 CMDB 配置")
	r, err = verifyPowerGraph(context.Background(), &powerTestClient{t: t}, "source", at, 0)
	require.NoError(t, err)
	require.Zero(t, r.Total)
	require.Empty(t, r.Samples)
	_, err = verifyPowerGraph(context.Background(), &powerTestClient{t: t, edges: powerTestEdges(at)}, "source", at, 7)
	require.Error(t, err)
}

type powerRunnerTest func(context.Context, model.Source, time.Time) (service.PowerSyncResult, error)

func (f powerRunnerTest) Run(c context.Context, s model.Source, at time.Time) (service.PowerSyncResult, error) {
	return f(c, s, at)
}

type powerControlTest struct {
	*uplinkControlTest
	resources []string
}

func (c *powerControlTest) BeginCheckpoint(ctx context.Context, s, resource, run string, snapshot, at time.Time) (*repository.DeviceSyncCheckpoint, error) {
	c.resources = append(c.resources, resource)
	if resource != powerSyncResource {
		return nil, errors.New("wrong resource")
	}
	return c.uplinkControlTest.BeginCheckpoint(ctx, s, serverUplinkResource, run, snapshot, at)
}
func (c *powerControlTest) AdvanceCheckpoint(ctx context.Context, s, resource, run string, cur repository.DeviceSyncCursor, at time.Time) (*repository.DeviceSyncCheckpoint, error) {
	c.resources = append(c.resources, resource)
	if resource != powerSyncResource {
		return nil, errors.New("wrong resource")
	}
	return c.uplinkControlTest.AdvanceCheckpoint(ctx, s, serverUplinkResource, run, cur, at)
}
func (c *powerControlTest) CompleteCheckpointAndRun(ctx context.Context, s, resource, run, status string, at time.Time) (*model.SyncRun, error) {
	c.resources = append(c.resources, resource)
	if resource != powerSyncResource {
		return nil, errors.New("wrong resource")
	}
	return c.uplinkControlTest.CompleteCheckpointAndRun(ctx, s, resource, run, status, at)
}
func TestPowerExecutionMapsResourceAndPreservesCounts(t *testing.T) {
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	c := &powerControlTest{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16)}}
	want := service.PowerSyncResult{Edges: 6, Objects: 5, Skipped: 2, MissingEndpoints: 1, Reasons: map[string]int{"missing": 1}, ByKind: map[string]int{"power_upstream": 4}}
	got, err := executePowerSync(context.Background(), c, powerRunnerTest(func(_ context.Context, s model.Source, snapshot time.Time) (service.PowerSyncResult, error) {
		require.Equal(t, at, snapshot)
		require.Equal(t, "source", s.ID)
		return want, nil
	}), run, model.Source{ID: "source"}, time.Minute, func(_ context.Context, snapshot time.Time, n int64) error {
		require.Equal(t, int64(6), n)
		require.Equal(t, at, snapshot)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, []string{powerSyncResource, powerSyncResource, powerSyncResource}, c.resources)
	require.Zero(t, c.cursor.LastInstanceID)
	require.Zero(t, c.cursor.LastPartition)
	require.True(t, c.completed)
}
func TestPowerModesAndIDCSelection(t *testing.T) {
	require.True(t, validPowerModes(false, false, false, false, false, false, false, true, false, "source", "", ""))
	require.True(t, validPowerModes(false, false, false, false, false, false, false, false, true, "source", "", "run"))
	for _, args := range [][2]bool{{true, true}, {true, false}} {
		require.False(t, validPowerModes(args[1], false, false, false, false, false, false, args[0], args[1], "source", "", "run"))
	}
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"cabinet_condition.idc_id", 1030)
	for _, kind := range []string{"rpp", "ups", "ups_group", "transformer"} {
		conf.Set(prefix+kind+"_condition.idc", 1030)
	}
	require.NoError(t, validatePowerSelection(conf, prefix))
	conf.Set(prefix+"ups_condition.idc", 451)
	require.Error(t, validatePowerSelection(conf, prefix))
}
