package repository

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

func readString(s string) *nebula.Value { return &nebula.Value{SVal: []byte(s)} }
func readInt(i int64) *nebula.Value     { return &nebula.Value{IVal: &i} }
func readMap(props map[string]*nebula.Value) *nebula.Value {
	return &nebula.Value{MVal: &nebula.NMap{Kvs: props}}
}
func readResult(t *testing.T, columns []string, rows ...[]*nebula.Value) *nebulago.ResultSet {
	t.Helper()
	data := &nebula.DataSet{}
	for _, c := range columns {
		data.ColumnNames = append(data.ColumnNames, []byte(c))
	}
	for _, row := range rows {
		data.Rows = append(data.Rows, &nebula.Row{Values: row})
	}
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
	require.NoError(t, err)
	return result
}
func readNodeProps(identity model.EntityIdentity) map[string]*nebula.Value {
	stable := readString(identity.StableID)
	if topologyEntitySchemas[identity.EntityType].stableField == "inst_id" {
		integer, _ := strconv.ParseInt(identity.StableID, 10, 64)
		stable = readInt(integer)
	}
	return map[string]*nebula.Value{"source_id": readString(identity.SourceID), topologyEntitySchemas[identity.EntityType].stableField: stable, "name": readString("asset"), "created_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 4, Hour: 12, Microsec: 123456}}, "synced_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 5}}}
}

func TestTopologyReadNodeTypedProperties(t *testing.T) {
	id := model.EntityIdentity{SourceID: "source-'text", EntityType: model.EntityGPU, StableID: "SN-1"}
	props := readNodeProps(id)
	props["large"] = readInt(math.MaxInt64)
	props["list"] = &nebula.Value{LVal: &nebula.NList{Values: []*nebula.Value{readString("x"), readInt(2)}}}
	null := nebula.NullType___NULL__
	props["empty"] = &nebula.Value{NVal: &null}
	props["nested"] = readMap(map[string]*nebula.Value{"ok": {BVal: new(bool)}})
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(props)})}}
	node, err := (&topologyReadRepository{client: fake}).Node(context.Background(), id)
	require.NoError(t, err)
	vid, _ := id.VID()
	require.Equal(t, vid, node.ID)
	require.Equal(t, "9223372036854775807", node.Properties["large"])
	require.Equal(t, []any{"x", int64(2)}, node.Properties["list"])
	require.Nil(t, node.Properties["empty"])
	require.Equal(t, time.Date(2026, 11, 4, 12, 0, 0, 123456000, time.UTC), *node.CreatedAt)
	require.Contains(t, fake.calls[0].statement, "MATCH (n:gpu) WHERE id(n) == $vid RETURN properties(n) AS props LIMIT 2")
	require.NotContains(t, fake.calls[0].statement, id.SourceID)
	require.Equal(t, vid, fake.calls[0].params["vid"])
}

func TestTopologyReadNodeRejectsMissingCorruptAndUnavailable(t *testing.T) {
	id := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "SN"}
	bad := readNodeProps(id)
	bad["source_id"] = readString("other")
	badNull := nebula.NullType_BAD_TYPE
	special := readNodeProps(id)
	special["name"] = &nebula.Value{NVal: &badNull}
	for _, tc := range []struct {
		name   string
		result *nebulago.ResultSet
		want   error
	}{
		{"missing", readResult(t, []string{"props"}), ErrInventoryNotFound},
		{"source", readResult(t, []string{"props"}, []*nebula.Value{readMap(bad)}), ErrInventoryNotReady},
		{"special", readResult(t, []string{"props"}, []*nebula.Value{readMap(special)}), ErrInventoryNotReady},
		{"unavailable", nil, ErrInventoryNotReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&topologyReadRepository{client: &topologyGraphFake{results: []*nebulago.ResultSet{tc.result}}}).Node(context.Background(), id)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestTopologyReadArgumentsAndDisabledConfiguration(t *testing.T) {
	repo, cleanup, err := NewTopologyReadRepository(viper.New())
	require.NoError(t, err)
	cleanup()
	_, err = repo.Node(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "n"})
	require.ErrorIs(t, err, ErrInventoryNotReady)
	fake := &topologyGraphFake{}
	r := &topologyReadRepository{client: fake}
	_, err = r.Node(context.Background(), model.EntityIdentity{})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	id := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "n"}
	for _, q := range []TopologyNeighborQuery{{Limit: 0, Direction: "both"}, {Limit: 201, Direction: "out"}, {Limit: 1, Direction: "bad"}, {Limit: 1, Direction: "in", EdgeType: "bad"}} {
		_, err := r.Neighbors(context.Background(), id, q)
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	require.Empty(t, fake.calls)
}

func readPeer(t *testing.T, id model.EntityIdentity) *nebula.Value {
	t.Helper()
	vid, err := id.VID()
	require.NoError(t, err)
	return &nebula.Value{VVal: &nebula.Vertex{Vid: readString(vid), Tags: []*nebula.Tag{{Name: []byte(id.EntityType), Props: readNodeProps(id)}}}}
}

func readEdge(t *testing.T, identity model.RelationIdentity, extra map[string]*nebula.Value) []*nebula.Value {
	t.Helper()
	id, err := identity.ID()
	require.NoError(t, err)
	rank, err := identity.Rank()
	require.NoError(t, err)
	from, _ := identity.From.VID()
	to, _ := identity.To.VID()
	props := map[string]*nebula.Value{"relation_id": readString(id), "source_id": readString(identity.SourceID), "relation_kind": readString(string(identity.Kind))}
	for k, v := range extra {
		props[k] = v
	}
	return []*nebula.Value{readString(from), readString(to), readInt(rank), readMap(props)}
}

func TestTopologyReadNeighborsPreservesParallelIncomingAndTruncates(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityRPP, StableID: "42"}
	peer := model.EntityIdentity{SourceID: "s", EntityType: model.EntityCabinet, StableID: "42"}
	a := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgePower, Kind: model.RelationPowerUpstream, From: peer, To: root, Discriminator: "A"}
	b := a
	b.Discriminator = "B"
	rows := [][]*nebula.Value{readEdge(t, b, map[string]*nebula.Value{"power_path": readString("B")}), readEdge(t, a, map[string]*nebula.Value{"power_path": readString("A")})}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}),
		readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, rows...),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, peer)}),
	}}
	result, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 2, Direction: "both", EdgeType: model.EdgePower})
	require.NoError(t, err)
	require.Len(t, result.Edges, 2)
	require.Len(t, result.Nodes, 2)
	require.False(t, result.Truncated)
	require.Less(t, result.Edges[0].ID, result.Edges[1].ID)
	peerVID, _ := peer.VID()
	rootVID, _ := root.VID()
	for _, edge := range result.Edges {
		require.Equal(t, peerVID, edge.From)
		require.Equal(t, rootVID, edge.To)
		require.NotEmpty(t, edge.Rank)
	}
	require.Contains(t, fake.calls[1].statement, "id(a) == $vid OR id(b) == $vid")
	require.Contains(t, fake.calls[1].statement, "LIMIT 3")
	require.Contains(t, fake.calls[1].statement, "ORDER BY edge_id")
	require.NotContains(t, fake.calls[1].statement, "OPTIONAL")
	require.Equal(t, []any{peerVID}, fake.calls[2].params["vids"])
	fake.results = []*nebulago.ResultSet{
		readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}),
		readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, rows...),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, peer)}),
	}
	result, err = (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 1, Direction: "in", EdgeType: model.EdgePower})
	require.NoError(t, err)
	require.True(t, result.Truncated)
	require.Len(t, result.Edges, 1)
}

func TestTopologyReadNeighborsFourQueriesAndOrphanFailure(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	peer := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
	edge := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: root, To: peer}
	empty := readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"})
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}), empty, empty, empty, empty}}
	result, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 100, Direction: "out"})
	require.NoError(t, err)
	require.Len(t, fake.calls, 5)
	require.Empty(t, result.Edges)
	require.NotNil(t, result.Edges)
	require.Len(t, result.Nodes, 1)
	fake.results = []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}), readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, readEdge(t, edge, nil)), readResult(t, []string{"node"})}
	_, err = (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 100, Direction: "out", EdgeType: model.EdgeComposition})
	require.ErrorIs(t, err, ErrInventoryNotReady)
}

func TestTopologyReadPowerIdentityRequiresStoredInteger(t *testing.T) {
	id := model.EntityIdentity{SourceID: "s", EntityType: model.EntityUPS, StableID: "9223372036854775807"}
	props := readNodeProps(id)
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(props)})}}
	node, err := (&topologyReadRepository{client: fake}).Node(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, id.StableID, node.StableID)
	props["inst_id"] = readString(id.StableID)
	fake.results = []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(props)})}
	_, err = (&topologyReadRepository{client: fake}).Node(context.Background(), id)
	require.ErrorIs(t, err, ErrInventoryNotReady)
	peer := readPeer(t, id)
	peer.VVal.Tags[0].Props["inst_id"] = readString(id.StableID)
	values, err := topologyReadRow(readResult(t, []string{"node"}, []*nebula.Value{peer}), 0)
	require.NoError(t, err)
	_, err = topologyReadSDKNode(values[0])
	require.ErrorIs(t, err, ErrInventoryNotReady)
}
