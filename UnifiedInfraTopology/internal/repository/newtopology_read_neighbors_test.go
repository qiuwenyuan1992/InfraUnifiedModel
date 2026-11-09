package repository

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

func TestTopologyReadNeighborsMergesGlobalLimitAndPreservesPlane(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	pod := model.EntityIdentity{SourceID: "s", EntityType: model.EntityPod, StableID: "p"}
	gpu := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
	composition := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: root, To: gpu}
	spatial := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeSpatial, Kind: model.RelationMemberOf, From: root, To: pod, Discriminator: "1"}
	secondPlane := spatial
	secondPlane.Discriminator = "2"
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	compositionID, _ := composition.ID()
	spatialID, _ := spatial.ID()
	secondID, _ := secondPlane.ID()
	wantID, wantPeer := compositionID, gpu
	if spatialID < wantID {
		wantID, wantPeer = spatialID, pod
	}
	if secondID < wantID {
		wantID, wantPeer = secondID, pod
	}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}),
		readResult(t, columns, readEdge(t, composition, nil)),
		readResult(t, columns), readResult(t, columns),
		readResult(t, columns, readEdge(t, spatial, map[string]*nebula.Value{"plane": readInt(1)}), readEdge(t, secondPlane, map[string]*nebula.Value{"plane": readInt(2)})),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, wantPeer)}),
	}}
	result, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 1, Direction: "out"})
	require.NoError(t, err)
	require.True(t, result.Truncated)
	require.Len(t, result.Edges, 1)
	require.Equal(t, wantID, result.Edges[0].ID)
	require.Len(t, result.Nodes, 2)
	require.Len(t, fake.calls, 6)
	for _, call := range fake.calls[1:5] {
		require.Contains(t, call.statement, "LIMIT 2")
		require.Contains(t, call.statement, "id(a) == $vid")
	}
	fake.results = []*nebulago.ResultSet{
		readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}),
		readResult(t, columns, readEdge(t, spatial, map[string]*nebula.Value{"plane": readInt(1)}), readEdge(t, secondPlane, map[string]*nebula.Value{"plane": readInt(2)})),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, pod)}),
	}
	result, err = (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 2, Direction: "both", EdgeType: model.EdgeSpatial})
	require.NoError(t, err)
	require.Len(t, result.Edges, 2)
	require.NotEqual(t, result.Edges[0].Properties["plane"], result.Edges[1].Properties["plane"])
	for _, edge := range result.Edges {
		rank, err := strconv.ParseInt(edge.Rank, 10, 64)
		require.NoError(t, err)
		require.Positive(t, rank)
	}
}

func TestTopologyReadNeighborsRejectsInvalidStoredData(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "a"}
	peer := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "b"}
	identity := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: peer, To: root}
	for _, name := range []string{"rank", "edge source", "peer source", "peer vid", "peer tag", "multiple tags", "direction", "columns", "too many rows", "duplicate peer"} {
		t.Run(name, func(t *testing.T) {
			row := readEdge(t, identity, nil)
			peerValue := readPeer(t, peer)
			columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
			rows := [][]*nebula.Value{row}
			peerRows := [][]*nebula.Value{{peerValue}}
			direction := "both"
			switch name {
			case "rank":
				row[2] = readInt(1)
			case "edge source":
				row[3].MVal.Kvs["source_id"] = readString("other")
			case "peer source":
				other := peer
				other.SourceID = "other"
				peerValue.VVal.Tags[0].Props = readNodeProps(other)
			case "peer vid":
				vid, _ := root.VID()
				peerValue.VVal.Vid = readString(vid)
			case "peer tag":
				peerValue.VVal.Tags[0].Name = []byte("arbitrary")
			case "multiple tags":
				peerValue.VVal.Tags = append(peerValue.VVal.Tags, &nebula.Tag{Name: []byte("device")})
			case "direction":
				direction = "out"
			case "columns":
				columns[0] = "invalid"
			case "too many rows":
				rows = append(rows, row, row)
			case "duplicate peer":
				peerRows = append(peerRows, []*nebula.Value{peerValue})
			}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}), readResult(t, columns, rows...), readResult(t, []string{"node"}, peerRows...)}}
			_, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 1, Direction: direction, EdgeType: model.EdgeNetwork})
			require.ErrorIs(t, err, ErrInventoryNotReady)
		})
	}
}

func TestTopologyReadNeighborsRejectsInvalidRelationMatrix(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	peer := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
	identity := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: root, To: peer}
	for _, kind := range []model.RelationKind{model.RelationLinksTo, model.RelationGPUUplink} {
		row := readEdge(t, identity, nil)
		row[3].MVal.Kvs["relation_kind"] = readString(string(kind))
		fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}), readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, row), readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, peer)})}}
		_, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 100, Direction: "both", EdgeType: model.EdgeComposition})
		require.ErrorIs(t, err, ErrInventoryNotReady, "非法关系类型/端点组合不能当正常拓扑返回")
	}
}

func TestTopologyReadNeighborsFiltersBeforeLimitAndSelectsPeer(t *testing.T) {
	for _, direction := range []string{"in", "out", "both"} {
		t.Run(direction, func(t *testing.T) {
			device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
			gpu := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
			root, peer := device, gpu
			if direction == "in" {
				root, peer = gpu, device
			}
			edge := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: device, To: gpu}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{
				readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}),
				readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, readEdge(t, edge, nil)),
				readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, peer)}),
			}}
			page, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 1, Direction: direction, RelationKind: model.RelationContainsGPU, PeerEntityType: peer.EntityType})
			require.NoError(t, err)
			require.Len(t, page.Edges, 1)
			require.Len(t, fake.calls, 3, "关系类型不能扩散到其他边类型")
			call := fake.calls[1]
			require.Contains(t, call.statement, "[e:"+string(model.EdgeComposition)+"]")
			require.Contains(t, call.statement, "properties(e).relation_kind == $relation_kind")
			require.Less(t, strings.Index(call.statement, "properties(e).relation_kind == $relation_kind"), strings.Index(call.statement, "LIMIT 2"))
			require.Equal(t, string(model.RelationContainsGPU), call.params["relation_kind"])
			require.Equal(t, string(peer.EntityType), call.params["peer_entity_type"])
			if direction != "in" {
				require.Contains(t, call.statement, "id(a) == $vid AND $peer_entity_type IN labels(b)")
			}
			if direction != "out" {
				require.Contains(t, call.statement, "id(b) == $vid AND $peer_entity_type IN labels(a)")
			}
			if direction == "both" {
				require.Contains(t, call.statement, ") OR (")
			}
		})
	}
}

func TestTopologyReadNeighborsRejectsInvalidFilterCombinationsBeforeQuery(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	for _, query := range []TopologyNeighborQuery{
		{Limit: 1, Direction: "both", RelationKind: "injected kind"},
		{Limit: 1, Direction: "both", PeerEntityType: "device)"},
		{Limit: 1, Direction: "out", EdgeType: model.EdgePower, RelationKind: model.RelationContainsGPU},
		{Limit: 1, Direction: "out", RelationKind: model.RelationContainsGPU, PeerEntityType: model.EntityInterface},
		{Limit: 1, Direction: "in", RelationKind: model.RelationContainsGPU, PeerEntityType: model.EntityGPU},
	} {
		fake := &topologyGraphFake{}
		_, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, query)
		require.ErrorIs(t, err, ErrInventoryInvalid)
		require.Empty(t, fake.calls)
	}
}

func TestTopologyReadNeighborsRejectsStoredFilterMismatch(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	peer := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
	for _, name := range []string{"kind", "peer"} {
		t.Run(name, func(t *testing.T) {
			edge := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: root, To: peer}
			row := readEdge(t, edge, nil)
			peerValue := readPeer(t, peer)
			if name == "kind" {
				row[3].MVal.Kvs["relation_kind"] = readString(string(model.RelationOwnsInterface))
			}
			if name == "peer" {
				other := peer
				other.EntityType = model.EntityInterface
				peerValue = readPeer(t, other)
				row[1] = peerValue.VVal.Vid
			}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{
				readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}),
				readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, row),
				readResult(t, []string{"node"}, []*nebula.Value{peerValue}),
			}}
			_, err := (&topologyReadRepository{client: fake}).Neighbors(context.Background(), root, TopologyNeighborQuery{Limit: 1, Direction: "out", RelationKind: model.RelationContainsGPU, PeerEntityType: model.EntityGPU})
			require.ErrorIs(t, err, ErrInventoryNotReady)
		})
	}
}
