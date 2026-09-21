package repository

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

type topologyGraphCall struct {
	statement string
	params    map[string]interface{}
}

type topologyGraphFake struct {
	results []*nebulago.ResultSet
	err     error
	calls   []topologyGraphCall
}

func (f *topologyGraphFake) ExecuteParameter(_ context.Context, statement string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	cloned := make(map[string]interface{}, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	f.calls = append(f.calls, topologyGraphCall{statement: statement, params: cloned})
	if f.err != nil {
		return nil, f.err
	}
	if len(f.results) == 0 {
		return nil, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func (f *topologyGraphFake) Close() {}

func TestTopologySourceCleanupLockWaitsForWrites(t *testing.T) {
	unlockWrite := lockTopologySourceWrite("cmdb-primary")
	acquired := make(chan struct{})
	go func() {
		unlockCleanup := lockTopologySourceCleanup("cmdb-primary")
		close(acquired)
		unlockCleanup()
	}()

	select {
	case <-acquired:
		t.Fatal("cleanup lock acquired while a source write was active")
	case <-time.After(20 * time.Millisecond):
	}
	unlockWrite()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("cleanup lock did not acquire after source writes completed")
	}
}

func topologyGraphResult(t *testing.T) *nebulago.ResultSet {
	t.Helper()
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{
		ErrorCode: nebula.ErrorCode_SUCCEEDED,
		Data:      &nebula.DataSet{},
	})
	require.NoError(t, err)
	return result
}

func topologyGraphStringResult(t *testing.T, columns []string, rows ...[]string) *nebulago.ResultSet {
	t.Helper()
	data := &nebula.DataSet{}
	for _, column := range columns {
		data.ColumnNames = append(data.ColumnNames, []byte(column))
	}
	for _, row := range rows {
		values := make([]*nebula.Value, 0, len(row))
		for _, value := range row {
			values = append(values, &nebula.Value{SVal: []byte(value)})
		}
		data.Rows = append(data.Rows, &nebula.Row{Values: values})
	}
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
	require.NoError(t, err)
	return result
}

func TestTopologyGraphUpsertVertexUsesParameters(t *testing.T) {
	createdAt := time.Date(2026, 9, 20, 1, 2, 3, 987654321, time.UTC)
	syncedAt := createdAt.Add(time.Hour)
	injection := `device"; DROP SPACE production; --`
	identity := model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityDevice, StableID: "SN-001"}
	vid, err := identity.VID()
	require.NoError(t, err)
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"source_id", "stable_id"}, []string{"cmdb-primary", "SN-001"}),
		topologyGraphResult(t),
	}}
	repository := &topologyGraphRepository{client: fake}

	err = repository.UpsertVertex(context.Background(), TopologyVertex{
		Identity: identity,
		Properties: map[string]any{
			"name":           injection,
			"parent_type_id": int64(1),
			"all_ips":        []string{"192.0.2.1"},
		},
		CreatedAt: createdAt,
		SyncedAt:  syncedAt,
	})
	require.NoError(t, err)
	require.Len(t, fake.calls, 3)

	insert := fake.calls[0]
	require.Contains(t, insert.statement, "INSERT VERTEX IF NOT EXISTS device")
	require.Contains(t, insert.statement, "created_at")
	require.NotContains(t, insert.statement, injection)
	require.Contains(t, insert.statement, `"`+vid+`"`)
	require.NotContains(t, insert.params, "vid")
	require.Equal(t, "cmdb-primary", insert.params["source_id"])
	require.Equal(t, "SN-001", insert.params["stable_id"])
	require.Equal(t, injection, insert.params["name"])
	require.Equal(t, int64(1), insert.params["parent_type_id"])
	require.Equal(t, `["192.0.2.1"]`, insert.params["all_ips"])
	require.NotEmpty(t, insert.params["created_at"])
	require.NotEmpty(t, insert.params["synced_at"])

	identityCheck := fake.calls[1]
	require.Contains(t, identityCheck.statement, "FETCH PROP ON device "+`"`+vid+`"`)
	require.NotContains(t, identityCheck.params, "vid")
	require.Contains(t, identityCheck.statement, "AS source_id")
	require.Contains(t, identityCheck.statement, "AS stable_id")

	update := fake.calls[2]
	require.Contains(t, update.statement, "UPDATE VERTEX ON device")
	require.Contains(t, update.statement, "WHEN synced_at <= datetime($synced_at)")
	require.Contains(t, update.statement, "`role` = $role")
	require.Contains(t, update.params, "role")
	require.Nil(t, update.params["role"])
	require.NotContains(t, update.statement, "created_at")
	require.NotContains(t, update.statement, injection)
	require.Contains(t, update.statement, `"`+vid+`"`)
	require.NotContains(t, update.params, "vid")
	require.Equal(t, "2026-09-20T02:02:03.987654+00:00", update.params["synced_at"])
}

func TestNormalizeTopologyPropertiesEncodesStructuredLists(t *testing.T) {
	tests := []struct {
		name       string
		entityType model.EntityType
		property   string
		value      any
		want       string
	}{
		{name: "device IPs", entityType: model.EntityDevice, property: "all_ips", value: []string{"192.0.2.1", "2001:db8::1"}, want: `["192.0.2.1","2001:db8::1"]`},
		{name: "GPU ports", entityType: model.EntityGPU, property: "conn_ports", value: []string{"ib8", "ib9"}, want: `["ib8","ib9"]`},
		{name: "cabinet POD IDs", entityType: model.EntityCabinet, property: "pod_ids", value: []int64{914, 2289}, want: `[914,2289]`},
		{name: "empty list", entityType: model.EntityDevice, property: "all_ips", value: []string(nil), want: `[]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, _, err := normalizeTopologyProperties(
				map[string]any{test.property: test.value},
				topologyEntitySchemas[test.entityType].properties,
			)
			require.NoError(t, err)
			require.Equal(t, test.want, normalized[test.property])
		})
	}
}

func TestNormalizeTopologyPropertiesRejectsPreencodedList(t *testing.T) {
	_, _, err := normalizeTopologyProperties(
		map[string]any{"all_ips": `["192.0.2.1"]`},
		topologyEntitySchemas[model.EntityDevice].properties,
	)
	require.ErrorContains(t, err, "value must be a string list")
}

func TestTopologyGraphUpsertGPUQuotesReservedPropertyName(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 40, 0, 0, time.UTC)
	identity := model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityGPU, StableID: "gpu-uuid-001"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"source_id", "stable_id"}, []string{"cmdb-primary", "gpu-uuid-001"}),
		topologyGraphResult(t),
	}}

	err := (&topologyGraphRepository{client: fake}).UpsertVertex(context.Background(), TopologyVertex{
		Identity:   identity,
		Properties: map[string]any{"int8": "1000Tops"},
		CreatedAt:  now,
		SyncedAt:   now,
	})
	require.NoError(t, err)
	require.Len(t, fake.calls, 3)
	require.Contains(t, fake.calls[0].statement, "`int8`")
	require.Contains(t, fake.calls[2].statement, "`int8` = $int8")
}

func TestTopologyGraphRejectsVertexIdentityCollision(t *testing.T) {
	identity := model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityDevice, StableID: "SN-001"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"source_id", "stable_id"}, []string{"other-source", "SN-999"}),
	}}

	err := (&topologyGraphRepository{client: fake}).UpsertVertex(context.Background(), TopologyVertex{
		Identity: identity, CreatedAt: time.Now().UTC(), SyncedAt: time.Now().UTC(),
	})
	require.ErrorContains(t, err, "identity collision")
	require.Len(t, fake.calls, 2)
}

func TestTopologyGraphVertexAllowlistRejectsBeforeClient(t *testing.T) {
	now := time.Now().UTC()
	tests := []TopologyVertex{
		{
			Identity:   model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityDevice, StableID: "SN-001"},
			Properties: map[string]any{"unknown_field": "value"},
			CreatedAt:  now,
			SyncedAt:   now,
		},
		{
			Identity:   model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityType("address"), StableID: "id"},
			Properties: map[string]any{},
			CreatedAt:  now,
			SyncedAt:   now,
		},
		{
			Identity:   model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityDevice, StableID: "SN-001"},
			Properties: map[string]any{"device_sn": "replacement"},
			CreatedAt:  now,
			SyncedAt:   now,
		},
		{
			Identity:   model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityDevice, StableID: "SN-001"},
			Properties: map[string]any{"parent_type_id": "not-an-integer"},
			CreatedAt:  now,
			SyncedAt:   now,
		},
		{
			Identity:   model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityDevice, StableID: "SN-001"},
			Properties: map[string]any{},
			CreatedAt:  time.Time{},
			SyncedAt:   now,
		},
	}
	for index, vertex := range tests {
		fake := &topologyGraphFake{}
		err := (&topologyGraphRepository{client: fake}).UpsertVertex(context.Background(), vertex)
		require.Error(t, err, index)
		require.Empty(t, fake.calls, index)
	}
}

func TestTopologyGraphUpsertRelationUsesNormalizedIdentity(t *testing.T) {
	createdAt := time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC)
	relation := model.Relation{
		Identity: model.RelationIdentity{
			SourceID:         "cmdb-primary",
			EdgeType:         model.EdgeNetwork,
			Kind:             model.RelationGPUUplink,
			From:             model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityGPU, StableID: "gpu-uuid"},
			To:               model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityInterface, StableID: "port-uuid"},
			SourceRelationID: "uplink-uuid",
		},
		Properties: map[string]any{"tor_port": " Ethernet1/1 ", "gpu_port": "ib8"},
		CreatedAt:  createdAt,
		SyncedAt:   createdAt.Add(time.Hour),
	}
	normalized, err := model.NormalizeRelation(relation)
	require.NoError(t, err)
	relationID, err := normalized.Identity.ID()
	require.NoError(t, err)
	rank, err := normalized.Identity.Rank()
	require.NoError(t, err)
	fromVID, err := normalized.Identity.From.VID()
	require.NoError(t, err)
	toVID, err := normalized.Identity.To.VID()
	require.NoError(t, err)
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"relation_id", "relation_kind", "source_id"}, []string{relationID, "gpu_uplink", "cmdb-primary"}),
		topologyGraphResult(t),
	}}

	err = (&topologyGraphRepository{client: fake}).UpsertRelation(context.Background(), relation)
	require.NoError(t, err)
	require.Len(t, fake.calls, 3)
	insert := fake.calls[0]
	require.Contains(t, insert.statement, "INSERT EDGE IF NOT EXISTS network_relation")
	require.NotContains(t, insert.statement, "uplink-uuid")
	require.Equal(t, relationID, insert.params["relation_id"])
	require.Contains(t, insert.statement, `"`+fromVID+`" -> "`+toVID+`" @ `+strconv.FormatInt(rank, 10))
	require.NotContains(t, insert.params, "rank")
	require.NotContains(t, insert.params, "from_vid")
	require.NotContains(t, insert.params, "to_vid")
	require.Equal(t, "Ethernet1/1", insert.params["tor_port"])
	identityCheck := fake.calls[1]
	require.Contains(t, identityCheck.statement, "FETCH PROP ON network_relation")
	require.Contains(t, identityCheck.statement, "AS relation_id")

	update := fake.calls[2]
	require.Contains(t, update.statement, "UPDATE EDGE ON network_relation")
	require.Contains(t, update.statement, "WHEN synced_at <= datetime($synced_at)")
	require.Contains(t, update.statement, "`gpu_ip` = $gpu_ip")
	require.Contains(t, update.params, "gpu_ip")
	require.Nil(t, update.params["gpu_ip"])
	require.NotContains(t, update.statement, "created_at")
	require.Contains(t, update.statement, `"`+fromVID+`" -> "`+toVID+`" @ `+strconv.FormatInt(rank, 10))
	require.NotContains(t, update.params, "rank")
}

func TestTopologyGraphRejectsRelationRankCollision(t *testing.T) {
	now := time.Now().UTC()
	relation := model.Relation{
		Identity: model.RelationIdentity{
			SourceID: "cmdb-primary", EdgeType: model.EdgeNetwork, Kind: model.RelationGPUUplink,
			From:             model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityGPU, StableID: "gpu-uuid"},
			To:               model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityInterface, StableID: "port-uuid"},
			SourceRelationID: "uplink-uuid",
		},
		Properties: map[string]any{"tor_port": "Ethernet1/1"}, CreatedAt: now, SyncedAt: now,
	}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"relation_id", "relation_kind", "source_id"}, []string{strings.Repeat("f", 64), "gpu_uplink", "cmdb-primary"}),
	}}

	err := (&topologyGraphRepository{client: fake}).UpsertRelation(context.Background(), relation)
	require.ErrorContains(t, err, "rank collision")
	require.Len(t, fake.calls, 2)
}

func TestTopologyGraphRelationValidationRunsBeforeClient(t *testing.T) {
	now := time.Now().UTC()
	base := model.Relation{
		Identity: model.RelationIdentity{
			SourceID: "cmdb-primary",
			EdgeType: model.EdgeNetwork,
			Kind:     model.RelationGPUUplink,
			From:     model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityGPU, StableID: "gpu-uuid"},
			To:       model.EntityIdentity{SourceID: "cmdb-primary", EntityType: model.EntityInterface, StableID: "port-uuid"},
		},
		Properties: map[string]any{"tor_port": "Ethernet1/1"},
		CreatedAt:  now,
		SyncedAt:   now,
	}
	tests := []model.Relation{
		base,
		func() model.Relation {
			value := base
			value.Identity.SourceRelationID = "uplink-uuid"
			value.Identity.To.EntityType = model.EntityTransformer
			return value
		}(),
		func() model.Relation {
			value := base
			value.Identity.SourceRelationID = "uplink-uuid"
			value.Properties = map[string]any{}
			return value
		}(),
		func() model.Relation {
			value := base
			value.Identity.SourceRelationID = "uplink-uuid"
			value.Properties = map[string]any{"tor_port": "Ethernet1/1", "unknown_field": "value"}
			return value
		}(),
	}
	for index, relation := range tests {
		fake := &topologyGraphFake{}
		err := (&topologyGraphRepository{client: fake}).UpsertRelation(context.Background(), relation)
		require.Error(t, err, index)
		require.Empty(t, fake.calls, index)
	}
}

func TestTopologyGraphCleanupQueriesAreScoped(t *testing.T) {
	cutoff := time.Date(2026, 9, 20, 0, 0, 0, 987654321, time.UTC)
	firstVID := strings.Repeat("a", 64)
	secondVID := strings.Repeat("b", 64)
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"stale_vid"}, []string{firstVID}, []string{secondVID}),
		topologyGraphResult(t),
		topologyGraphStringResult(t, []string{"stale_vid"}),
	}}
	repository := &topologyGraphRepository{client: fake}

	require.NoError(t, repository.DeleteRelationsNotSeen(context.Background(), "cmdb-primary", model.EdgeNetwork, cutoff))
	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(context.Background(), "cmdb-primary", model.EntityGPU, cutoff))
	require.Len(t, fake.calls, 4)

	edges := fake.calls[0]
	require.Contains(t, edges.statement, "LOOKUP ON network_relation")
	require.Contains(t, edges.statement, "network_relation.source_id == $source_id")
	require.Contains(t, edges.statement, "network_relation.synced_at < datetime($run_started_at)")
	require.Contains(t, edges.statement, "DELETE EDGE network_relation")
	require.Equal(t, "cmdb-primary", edges.params["source_id"])
	require.Equal(t, "2026-09-20T00:00:00.987654+00:00", edges.params["run_started_at"])

	lookup := fake.calls[1]
	require.Contains(t, lookup.statement, "MATCH (v:gpu)")
	require.Contains(t, lookup.statement, "v.gpu.source_id == $source_id")
	require.Contains(t, lookup.statement, "v.gpu.synced_at < datetime($run_started_at)")
	require.Contains(t, lookup.statement, "NOT (v)-[]-()")
	require.Contains(t, lookup.statement, "LIMIT 200")
	require.NotContains(t, lookup.statement, "DELETE VERTEX")

	deletion := fake.calls[2]
	require.Equal(t, `DELETE VERTEX "`+firstVID+`", "`+secondVID+`";`, deletion.statement)
	require.Empty(t, deletion.params)
	require.Equal(t, lookup.statement, fake.calls[3].statement)
}

func TestTopologyGraphRejectsInvalidCleanupBeforeClient(t *testing.T) {
	now := time.Now().UTC()
	fake := &topologyGraphFake{}
	repository := &topologyGraphRepository{client: fake}
	require.Error(t, repository.DeleteRelationsNotSeen(context.Background(), "", model.EdgeNetwork, now))
	require.Error(t, repository.DeleteRelationsNotSeen(context.Background(), "source", model.EdgeType("unknown"), now))
	require.Error(t, repository.DeleteOrphanVerticesNotSeen(context.Background(), "source", model.EntityType("address"), now))
	require.Error(t, repository.DeleteOrphanVerticesNotSeen(context.Background(), "source", model.EntityGPU, time.Time{}))
	require.Empty(t, fake.calls)
}

func TestTopologyGraphLiveWriteRefreshAndCleanup(t *testing.T) {
	configPath := os.Getenv("TOPOLOGY_GRAPH_TEST_CONFIG")
	if configPath == "" {
		t.Skip("设置 TOPOLOGY_GRAPH_TEST_CONFIG 后运行真实 NebulaGraph 验收")
	}

	conf := viper.New()
	conf.SetConfigFile(configPath)
	require.NoError(t, conf.ReadInConfig())
	repositoryInterface, closeRepository, err := NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	t.Cleanup(closeRepository)
	repository, ok := repositoryInterface.(*topologyGraphRepository)
	require.True(t, ok, "真实验收必须启用 NebulaGraph 仓储")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const sourceID = "live-nebula-acceptance"
	device := model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityDevice, StableID: "LIVE-SN-001"}
	port := model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityInterface, StableID: "live-port-uuid-001"}
	gpu := model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityGPU, StableID: "live-gpu-uuid-001"}
	createdAt := time.Date(2026, 9, 21, 9, 41, 0, 123456000, time.UTC)
	refreshedAt := time.Date(2026, 9, 21, 9, 41, 0, 654321000, time.UTC)
	cleanupAt := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, repository.DeleteRelationsNotSeen(ctx, sourceID, model.EdgeComposition, cleanupAt))
	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(ctx, sourceID, model.EntityInterface, cleanupAt))
	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(ctx, sourceID, model.EntityGPU, cleanupAt))
	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(ctx, sourceID, model.EntityDevice, cleanupAt))

	require.NoError(t, repository.UpsertVertex(ctx, TopologyVertex{
		Identity: device, Properties: map[string]any{"name": "live-device-v1"},
		CreatedAt: createdAt, SyncedAt: createdAt,
	}))
	require.NoError(t, repository.UpsertVertex(ctx, TopologyVertex{
		Identity: port, Properties: map[string]any{"port_name": "Ethernet1/1"},
		CreatedAt: createdAt, SyncedAt: createdAt,
	}))
	require.NoError(t, repository.UpsertVertex(ctx, TopologyVertex{
		Identity: gpu, Properties: map[string]any{"int8": "1000Tops"},
		CreatedAt: createdAt, SyncedAt: createdAt,
	}))
	relation := model.Relation{
		Identity: model.RelationIdentity{
			SourceID: sourceID, EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface,
			From: device, To: port,
		},
		CreatedAt: createdAt, SyncedAt: createdAt,
	}
	require.NoError(t, repository.UpsertRelation(ctx, relation))

	require.NoError(t, repository.UpsertVertex(ctx, TopologyVertex{
		Identity: device, Properties: map[string]any{"name": "live-device-v2"},
		CreatedAt: refreshedAt, SyncedAt: refreshedAt,
	}))
	deviceVID, err := device.VID()
	require.NoError(t, err)
	result, err := repository.client.ExecuteParameter(ctx,
		"FETCH PROP ON device "+topologyVIDLiteral(deviceVID)+" YIELD properties(vertex).name AS name, properties(vertex).created_at AS created_at, properties(vertex).synced_at AS synced_at;",
		map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, result.IsSucceed(), result.GetErrorMsg())
	require.Equal(t, [][]string{
		{"name", "created_at", "synced_at"},
		{`"live-device-v2"`, "2026-09-21T09:41:00.123456", "2026-09-21T09:41:00.654321"},
	}, result.AsStringTable())
	gpuVID, err := gpu.VID()
	require.NoError(t, err)
	result, err = repository.client.ExecuteParameter(ctx,
		"FETCH PROP ON gpu "+topologyVIDLiteral(gpuVID)+" YIELD properties(vertex).`int8` AS int8_value;",
		map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, result.IsSucceed(), result.GetErrorMsg())
	require.Equal(t, [][]string{{"int8_value"}, {`"1000Tops"`}}, result.AsStringTable())

	require.NoError(t, repository.DeleteRelationsNotSeen(ctx, sourceID, model.EdgeComposition, refreshedAt))
	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(ctx, sourceID, model.EntityInterface, refreshedAt))
	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(ctx, sourceID, model.EntityGPU, refreshedAt))
	portVID, err := port.VID()
	require.NoError(t, err)
	result, err = repository.client.ExecuteParameter(ctx,
		"FETCH PROP ON interface "+topologyVIDLiteral(portVID)+" YIELD properties(vertex).port_uuid AS port_uuid;",
		map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, result.IsSucceed(), result.GetErrorMsg())
	require.Zero(t, result.GetRowSize())

	relationID, err := model.NormalizeRelation(relation)
	require.NoError(t, err)
	rank, err := relationID.Identity.Rank()
	require.NoError(t, err)
	result, err = repository.client.ExecuteParameter(ctx,
		"FETCH PROP ON composition_relation "+topologyVIDLiteral(deviceVID)+" -> "+topologyVIDLiteral(portVID)+" @ "+strconv.FormatInt(rank, 10)+" YIELD properties(edge).relation_id AS relation_id;",
		map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, result.IsSucceed(), result.GetErrorMsg())
	require.Zero(t, result.GetRowSize())

	require.NoError(t, repository.DeleteOrphanVerticesNotSeen(ctx, sourceID, model.EntityDevice, refreshedAt.Add(time.Microsecond)))
	result, err = repository.client.ExecuteParameter(ctx,
		"FETCH PROP ON device "+topologyVIDLiteral(deviceVID)+" YIELD properties(vertex).device_sn AS device_sn;",
		map[string]interface{}{})
	require.NoError(t, err)
	require.True(t, result.IsSucceed(), result.GetErrorMsg())
	require.Zero(t, result.GetRowSize())
}

func TestTopologyGraphErrorsDoNotLeakParameters(t *testing.T) {
	secret := "secret-source"
	now := time.Now().UTC()
	vertex := TopologyVertex{
		Identity:  model.EntityIdentity{SourceID: secret, EntityType: model.EntityDevice, StableID: "SN-001"},
		CreatedAt: now,
		SyncedAt:  now,
	}
	for _, fake := range []*topologyGraphFake{
		{err: errors.New("transport leaked secret-source")},
		{},
		{results: []*nebulago.ResultSet{topologyGraphRejectedResult(t, "server leaked secret-source")}},
	} {
		err := (&topologyGraphRepository{client: fake}).UpsertVertex(context.Background(), vertex)
		require.ErrorIs(t, err, ErrInventoryNotReady)
		require.NotContains(t, err.Error(), secret)
	}
}

func topologyGraphRejectedResult(t *testing.T, message string) *nebulago.ResultSet {
	t.Helper()
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{
		ErrorCode: nebula.ErrorCode_E_EXECUTION_ERROR,
		ErrorMsg:  []byte(message),
	})
	require.NoError(t, err)
	return result
}
