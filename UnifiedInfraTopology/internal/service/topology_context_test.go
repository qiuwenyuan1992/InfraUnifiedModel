package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/stretchr/testify/require"
)

type contextReadStub struct {
	topologyDirectoryStub
	root             *repository.TopologyNode
	read             func(context.Context, model.EntityIdentity, repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error)
	sceneNetwork     func(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error)
	networkPeers     func(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error)
	roomPower        func(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error)
	queries          []repository.TopologyNeighborQuery
	identities       []model.EntityIdentity
	networkCalls     int
	networkPeerSeeds []repository.TopologyNode
	roomPowerCalls   int
	roomPowerSeeds   []repository.TopologyNode
	roomPowerLimit   int
}

func (s *contextReadStub) SceneNetwork(ctx context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
	if s.sceneNetwork == nil {
		panic("unexpected scene network read")
	}
	return s.sceneNetwork(ctx, seeds, limit)
}

func (s *contextReadStub) SceneNetworkPeers(ctx context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
	s.networkCalls++
	s.networkPeerSeeds = seeds
	if s.networkPeers == nil {
		panic("unexpected network peers read")
	}
	return s.networkPeers(ctx, seeds, limit)
}

func (s *contextReadStub) SceneRoomPower(ctx context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
	s.roomPowerCalls++
	s.roomPowerSeeds = seeds
	if s.roomPower == nil {
		panic("unexpected room power read")
	}
	return s.roomPower(ctx, seeds, limit)
}

func (s *contextReadStub) Node(_ context.Context, _ model.EntityIdentity) (*repository.TopologyNode, error) {
	return s.root, s.err
}
func (s *contextReadStub) Neighbors(ctx context.Context, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
	s.queries = append(s.queries, q)
	s.identities = append(s.identities, i)
	return s.read(ctx, i, q)
}
func contextNode(kind model.EntityType, stable string) repository.TopologyNode {
	i := model.EntityIdentity{SourceID: strings.Repeat("a", 32), EntityType: kind, StableID: stable}
	id, _ := i.VID()
	return repository.TopologyNode{ID: id, SourceID: i.SourceID, EntityType: string(kind), StableID: stable}
}

func TestDeviceContextFixedPathsAndRetainedEdges(t *testing.T) {
	device := contextNode(model.EntityDevice, "server")
	peers := map[model.EntityType]repository.TopologyNode{
		model.EntityCabinet: contextNode(model.EntityCabinet, "cab"), model.EntityDataCenter: contextNode(model.EntityDataCenter, "dc"),
		model.EntityRoom: contextNode(model.EntityRoom, "room-uuid"),
		model.EntityRPP:  contextNode(model.EntityRPP, "1"), model.EntityUPSGroup: contextNode(model.EntityUPSGroup, "2"),
		model.EntityTransformer: contextNode(model.EntityTransformer, "3"), model.EntityUPS: contextNode(model.EntityUPS, "4"),
		model.EntityGPU: contextNode(model.EntityGPU, "gpu"), model.EntityInterface: contextNode(model.EntityInterface, "port"),
		model.EntityDevice: contextNode(model.EntityDevice, "tor"), model.EntityPod: contextNode(model.EntityPod, "pod"),
	}
	r := &contextReadStub{root: &device}
	r.read = func(_ context.Context, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		require.Equal(t, 50, q.Limit)
		require.NotEmpty(t, q.RelationKind)
		require.NotEmpty(t, q.PeerEntityType)
		fromType, toType := i.EntityType, q.PeerEntityType
		if q.Direction == "in" {
			fromType, toType = toType, fromType
		}
		require.True(t, (model.RelationSpec{EdgeType: q.EdgeType, Kind: q.RelationKind, From: fromType, To: toType}).Valid())
		require.NotEqual(t, model.RelationHasStandby, q.RelationKind)
		require.NotEqual(t, model.RelationLinksTo, q.RelationKind)
		root := contextNode(i.EntityType, i.StableID)
		peer := peers[q.PeerEntityType]
		from, to := root.ID, peer.ID
		if q.Direction == "in" {
			from, to = to, from
		}
		edge := repository.TopologyEdge{ID: fmt.Sprintf("%s/%s/%s", from, to, q.RelationKind), From: from, To: to, Rank: "1", EdgeType: string(q.EdgeType), RelationKind: string(q.RelationKind)}
		edges := []repository.TopologyEdge{edge}
		if i.EntityType == model.EntityCabinet && q.PeerEntityType == model.EntityRPP {
			edge.ID += "B"
			edge.Rank = "2"
			edges = append(edges, edge)
		}
		return &repository.TopologyNeighborhood{Root: &root, Nodes: []repository.TopologyNode{peer}, Edges: edges}, nil
	}
	s := NewTopologyBrowseService(r, topologyConf())
	p, err := s.DeviceContext(context.Background(), "reader", model.EntityIdentity{SourceID: device.SourceID, EntityType: model.EntityDevice, StableID: device.StableID})
	require.NoError(t, err)
	require.False(t, p.Truncated)
	require.Empty(t, p.Warnings)
	require.Equal(t, 200, p.Limit)
	require.Equal(t, device.ID, p.Root.ID)
	ids := map[string]bool{}
	for _, n := range p.Nodes {
		ids[n.ID] = true
	}
	for _, e := range p.Edges {
		require.True(t, ids[e.From])
		require.True(t, ids[e.To])
	}
	var powerRanks []string
	for _, e := range p.Edges {
		if e.From == peers[model.EntityCabinet].ID && e.To == peers[model.EntityRPP].ID {
			powerRanks = append(powerRanks, e.Rank)
		}
	}
	require.ElementsMatch(t, []string{"1", "2"}, powerRanks)
	require.Contains(t, ids, peers[model.EntityRoom].ID)
	roomEdges := 0
	for _, e := range p.Edges {
		if e.From == peers[model.EntityRoom].ID || e.To == peers[model.EntityRoom].ID {
			require.Equal(t, string(model.EdgeSpatial), e.EdgeType)
			require.Equal(t, string(model.RelationLocatedIn), e.RelationKind)
			roomEdges++
		}
	}
	require.Equal(t, 2, roomEdges, "room is reached from the cabinet, never inferred as a power feed")
	for index, i := range r.identities {
		require.NotContains(t, []model.EntityType{model.EntityTransformer, model.EntityUPS}, i.EntityType)
		if i.EntityType == model.EntityDevice {
			require.Equal(t, device.StableID, i.StableID)
		}
		if i.EntityType == model.EntityInterface {
			require.Equal(t, "in", r.queries[index].Direction)
			require.Equal(t, model.RelationOwnsInterface, r.queries[index].RelationKind)
		}
	}
	// GPU 和服务器上联共享接口时，只读取一次归属。
	owners := 0
	for _, q := range r.queries {
		if q.RelationKind == model.RelationOwnsInterface {
			owners++
		}
	}
	require.Equal(t, 1, owners)
}

// 房间供电视图：按房间批量读取主供电链；无供电边的机柜如实标记缺失，不猜测补边。
func TestRoomPowerContextMarksCabinetsWithoutPowerFeed(t *testing.T) {
	room := contextNode(model.EntityRoom, "room-uuid")
	fed := contextNode(model.EntityCabinet, "c-fed")
	unfed := contextNode(model.EntityCabinet, "c-unfed")
	rpp := contextNode(model.EntityRPP, "6204")
	group := contextNode(model.EntityUPSGroup, "7")
	transformer := contextNode(model.EntityTransformer, "8")
	r := &contextReadStub{root: &room}
	r.roomPower = func(_ context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
		require.Equal(t, []repository.TopologyNode{room}, seeds)
		require.Positive(t, limit)
		return &repository.TopologyNeighborhood{Nodes: []repository.TopologyNode{room, fed, unfed, rpp, group, transformer}, Edges: []repository.TopologyEdge{
			{ID: "e1", SourceID: room.SourceID, From: fed.ID, To: room.ID, Rank: "1", EdgeType: string(model.EdgeSpatial), RelationKind: string(model.RelationLocatedIn)},
			{ID: "e2", SourceID: room.SourceID, From: unfed.ID, To: room.ID, Rank: "1", EdgeType: string(model.EdgeSpatial), RelationKind: string(model.RelationLocatedIn)},
			{ID: "e3", SourceID: room.SourceID, From: fed.ID, To: rpp.ID, Rank: "1", EdgeType: string(model.EdgePower), RelationKind: string(model.RelationPowerUpstream)},
			{ID: "e4", SourceID: room.SourceID, From: rpp.ID, To: group.ID, Rank: "1", EdgeType: string(model.EdgePower), RelationKind: string(model.RelationPowerUpstream)},
			{ID: "e5", SourceID: room.SourceID, From: group.ID, To: transformer.ID, Rank: "1", EdgeType: string(model.EdgePower), RelationKind: string(model.RelationPowerUpstream)},
		}}, nil
	}
	s := NewTopologyBrowseService(r, topologyConf())
	p, err := s.RoomPowerContext(context.Background(), "reader", model.EntityIdentity{SourceID: room.SourceID, EntityType: model.EntityRoom, StableID: room.StableID})
	require.NoError(t, err)
	require.Equal(t, room.ID, p.Root.ID)
	require.Equal(t, 1, r.roomPowerCalls)
	require.Len(t, p.Nodes, 6)
	require.Len(t, p.Edges, 5)
	// 供电缺失按节点稳定身份标记，前端据此打缺失标记。
	require.Equal(t, []string{unfed.ID}, p.PowerMissing)
	require.Empty(t, p.Warnings)
}

func TestRoomPowerContextRejectsNonRoomAndBudgets(t *testing.T) {
	r := &contextReadStub{root: func() *repository.TopologyNode { n := contextNode(model.EntityRoom, "r"); return &n }()}
	s := NewTopologyBrowseService(r, topologyConf())
	_, err := s.RoomPowerContext(context.Background(), "reader", model.EntityIdentity{SourceID: strings.Repeat("a", 32), EntityType: model.EntityCabinet, StableID: "c"})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	_, err = s.RoomPowerContext(context.Background(), "writer", model.EntityIdentity{SourceID: strings.Repeat("a", 32), EntityType: model.EntityRoom, StableID: "r"})
	require.ErrorIs(t, err, ErrInventoryForbidden)
	require.Zero(t, r.roomPowerCalls)
	// 图读取截断时返回截断标记，不静默当作完整供电链。
	room := contextNode(model.EntityRoom, "r")
	r2 := &contextReadStub{root: &room}
	r2.roomPower = func(_ context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
		return &repository.TopologyNeighborhood{Truncated: true, Nodes: []repository.TopologyNode{room}, Edges: []repository.TopologyEdge{}}, nil
	}
	p, err := NewTopologyBrowseService(r2, topologyConf()).RoomPowerContext(context.Background(), "reader", model.EntityIdentity{SourceID: room.SourceID, EntityType: model.EntityRoom, StableID: room.StableID})
	require.NoError(t, err)
	require.True(t, p.Truncated)
}

func TestDeviceContextNetworkDeviceLinksToPeers(t *testing.T) {
	switchA := contextNode(model.EntityDevice, "switch-a")
	switchA.Properties = map[string]any{"parent_type_id": int64(54)}
	switchB := contextNode(model.EntityDevice, "switch-b")
	switchB.Properties = map[string]any{"parent_type_id": int64(54)}
	portA, portB := contextNode(model.EntityInterface, "port-a"), contextNode(model.EntityInterface, "port-b")
	peers := map[model.EntityType]repository.TopologyNode{
		model.EntityCabinet: contextNode(model.EntityCabinet, "cab"), model.EntityDataCenter: contextNode(model.EntityDataCenter, "dc"),
		model.EntityRoom: contextNode(model.EntityRoom, "room"),
		model.EntityRPP:  contextNode(model.EntityRPP, "1"), model.EntityUPSGroup: contextNode(model.EntityUPSGroup, "2"),
		model.EntityTransformer: contextNode(model.EntityTransformer, "3"), model.EntityUPS: contextNode(model.EntityUPS, "4"),
		model.EntityPod: contextNode(model.EntityPod, "pod"),
	}
	r := &contextReadStub{root: &switchA}
	r.read = func(_ context.Context, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		require.Equal(t, 50, q.Limit)
		fromType, toType := i.EntityType, q.PeerEntityType
		if q.Direction == "in" {
			fromType, toType = toType, fromType
		}
		require.True(t, (model.RelationSpec{EdgeType: q.EdgeType, Kind: q.RelationKind, From: fromType, To: toType}).Valid())
		// 网络设备分流：GPU、服务器上联与端口互联均由 SceneNetworkPeers 批量读取。
		require.NotEqual(t, model.RelationContainsGPU, q.RelationKind, "网络设备不查询GPU上联")
		require.NotEqual(t, model.RelationServerUplink, q.RelationKind, "网络设备不查询服务器上联")
		require.NotEqual(t, model.RelationGPUUplink, q.RelationKind)
		require.NotEqual(t, model.RelationLinksTo, q.RelationKind)
		root := contextNode(i.EntityType, i.StableID)
		peer := peers[q.PeerEntityType]
		from, to := root.ID, peer.ID
		if q.Direction == "in" {
			from, to = to, from
		}
		return &repository.TopologyNeighborhood{Root: &root, Nodes: []repository.TopologyNode{peer}, Edges: []repository.TopologyEdge{{ID: fmt.Sprintf("%s/%s/%s", from, to, q.RelationKind), From: from, To: to, Rank: "1", EdgeType: string(q.EdgeType), RelationKind: string(q.RelationKind)}}}, nil
	}
	r.networkPeers = func(_ context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
		require.Equal(t, []repository.TopologyNode{switchA}, seeds)
		require.Equal(t, topologyContextEdges, limit)
		return &repository.TopologyNeighborhood{Nodes: []repository.TopologyNode{portA, portB, switchB}, Edges: []repository.TopologyEdge{
			{ID: "own-a", From: switchA.ID, To: portA.ID, Rank: "1", EdgeType: string(model.EdgeComposition), RelationKind: string(model.RelationOwnsInterface)},
			{ID: "link", From: portA.ID, To: portB.ID, Rank: "1", EdgeType: string(model.EdgeNetwork), RelationKind: string(model.RelationLinksTo)},
			{ID: "own-b", From: switchB.ID, To: portB.ID, Rank: "1", EdgeType: string(model.EdgeComposition), RelationKind: string(model.RelationOwnsInterface)},
		}}, nil
	}
	s := NewTopologyBrowseService(r, topologyConf())
	p, err := s.DeviceContext(context.Background(), "reader", model.EntityIdentity{SourceID: switchA.SourceID, EntityType: model.EntityDevice, StableID: switchA.StableID})
	require.NoError(t, err)
	require.Empty(t, p.Warnings)
	ids := map[string]bool{}
	for _, n := range p.Nodes {
		ids[n.ID] = true
	}
	require.Contains(t, ids, portA.ID, "本机端口必须出现")
	require.Contains(t, ids, portB.ID, "对端端口必须出现")
	require.Contains(t, ids, switchB.ID, "对端网络设备必须出现")
	links, owned := 0, 0
	for _, e := range p.Edges {
		require.True(t, ids[e.From])
		require.True(t, ids[e.To])
		if e.RelationKind == string(model.RelationLinksTo) {
			links++
			require.Equal(t, portA.ID, e.From)
			require.Equal(t, portB.ID, e.To)
		}
		if e.RelationKind == string(model.RelationOwnsInterface) {
			owned++
		}
	}
	require.Equal(t, 1, links)
	require.Equal(t, 2, owned, "本机与对端端口的归属都要展示")
}

func TestDeviceContextActualQueryBudgetPreservesCompletedPaths(t *testing.T) {
	root := contextNode(model.EntityDevice, "server")
	cabinet := contextNode(model.EntityCabinet, "cab")
	stub := &contextReadStub{root: &root}
	stub.read = func(_ context.Context, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		if i.EntityType == model.EntityDevice && q.RelationKind == model.RelationLocatedIn {
			return &repository.TopologyNeighborhood{Root: &root, Nodes: []repository.TopologyNode{root, cabinet}, Edges: []repository.TopologyEdge{{ID: "located", From: root.ID, To: cabinet.ID, SourceID: root.SourceID, RelationKind: "located_in", EdgeType: "spatial_relation"}}}, nil
		}
		return nil, repository.ErrTopologyQueryBudget
	}
	graph, err := NewTopologyBrowseService(stub, topologyConf()).DeviceContext(context.Background(), "reader", model.EntityIdentity{SourceID: root.SourceID, EntityType: model.EntityDevice, StableID: root.StableID})
	require.NoError(t, err)
	require.True(t, graph.Truncated)
	require.Contains(t, graph.Warnings, "query_budget_exhausted")
	require.Len(t, graph.Edges, 1)
	require.Len(t, graph.Nodes, 2)
	require.Len(t, stub.queries, 2, "预算耗尽后不能继续调用仓储")
}

func TestDeviceContextAtomicBudgetsAndStepCap(t *testing.T) {
	for _, budget := range []string{"node", "edge", "step"} {
		t.Run(budget, func(t *testing.T) {
			root := contextNode(model.EntityDevice, "root")
			r := &contextReadStub{root: &root}
			r.read = func(_ context.Context, _ model.EntityIdentity, _ repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
				page := &repository.TopologyNeighborhood{Root: &root}
				for index := 0; index < 51; index++ {
					n := contextNode(model.EntityGPU, fmt.Sprint(index))
					page.Nodes = append(page.Nodes, n)
					page.Edges = append(page.Edges, repository.TopologyEdge{ID: fmt.Sprintf("%03d", index), From: root.ID, To: n.ID})
				}
				return page, nil
			}
			w := &topologyContextWalk{service: NewTopologyBrowseService(r, topologyConf()).(*topologyBrowseService), ctx: context.Background(), result: &TopologyDeviceContext{}, nodes: map[string]repository.TopologyNode{root.ID: root}, edges: map[string]repository.TopologyEdge{}, cache: map[topologyContextRead][]repository.TopologyNode{}, warnings: map[string]bool{}}
			if budget == "node" {
				for index := 0; index < 198; index++ {
					n := contextNode(model.EntityPod, fmt.Sprint(index))
					w.nodes[n.ID] = n
				}
			}
			if budget == "edge" {
				for index := 0; index < 399; index++ {
					id := fmt.Sprintf("existing-%d", index)
					w.edges[id] = repository.TopologyEdge{ID: id, From: root.ID, To: root.ID}
				}
			}
			_, err := w.neighbors(root, model.EdgeComposition, model.RelationContainsGPU, model.EntityGPU, "out", "")
			require.NoError(t, err)
			require.True(t, w.result.Truncated)
			require.LessOrEqual(t, len(w.nodes), 200)
			require.LessOrEqual(t, len(w.edges), 400)
			for _, e := range w.edges {
				require.Contains(t, w.nodes, e.From)
				require.Contains(t, w.nodes, e.To)
			}
			if budget == "node" {
				require.Len(t, w.nodes, 200)
				require.Len(t, w.edges, 1)
				require.Contains(t, w.result.Warnings, "node_budget_exhausted")
			}
			if budget == "edge" {
				require.Len(t, w.edges, 400)
				require.Len(t, w.nodes, 2)
				require.Contains(t, w.result.Warnings, "edge_budget_exhausted")
			}
			if budget == "step" {
				require.Len(t, w.edges, 50)
				require.Contains(t, w.result.Warnings, "step_truncated")
			}
		})
	}
}

func TestDeviceContextCancellationAfterFinalRepositoryCall(t *testing.T) {
	root := contextNode(model.EntityDevice, "root")
	ctx, cancel := context.WithCancel(context.Background())
	r := &contextReadStub{root: &root}
	r.read = func(_ context.Context, _ model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		if q.RelationKind == model.RelationServerUplink {
			cancel()
		}
		return &repository.TopologyNeighborhood{Root: &root}, nil
	}
	s := NewTopologyBrowseService(r, topologyConf())
	_, err := s.DeviceContext(ctx, "reader", model.EntityIdentity{SourceID: root.SourceID, EntityType: model.EntityDevice, StableID: root.StableID})
	require.ErrorIs(t, err, context.Canceled)
}

func TestDeviceContextBudgetMissingPowerAndCancellation(t *testing.T) {
	device := contextNode(model.EntityDevice, "server")
	i := model.EntityIdentity{SourceID: device.SourceID, EntityType: model.EntityDevice, StableID: device.StableID}
	r := &contextReadStub{root: &device}
	r.read = func(_ context.Context, identity model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		root := contextNode(identity.EntityType, identity.StableID)
		p := &repository.TopologyNeighborhood{Root: &root, Nodes: []repository.TopologyNode{}, Edges: []repository.TopologyEdge{}}
		if q.RelationKind == model.RelationContainsGPU {
			for index := 0; index < 50; index++ {
				n := contextNode(model.EntityGPU, fmt.Sprint(index))
				p.Nodes = append(p.Nodes, n)
				p.Edges = append(p.Edges, repository.TopologyEdge{ID: n.ID, From: root.ID, To: n.ID})
			}
		}
		return p, nil
	}
	s := NewTopologyBrowseService(r, topologyConf())
	p, err := s.DeviceContext(context.Background(), "reader", i)
	require.NoError(t, err)
	require.True(t, p.Truncated)
	require.Contains(t, p.Warnings, "query_budget_exhausted")
	require.Contains(t, p.Warnings, "missing_cabinet")
	require.LessOrEqual(t, len(r.queries)+1, 40)
	require.LessOrEqual(t, len(p.Nodes), 200)
	require.LessOrEqual(t, len(p.Edges), 400)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.queries = nil
	_, err = s.DeviceContext(ctx, "reader", i)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, r.queries)
	r.err = repository.ErrInventoryNotFound
	_, err = s.DeviceContext(context.Background(), "reader", i)
	require.ErrorIs(t, err, ErrInventoryNotFound)
}

func TestDeviceContextMissingCabinetIsNotFabricated(t *testing.T) {
	device := contextNode(model.EntityDevice, "server")
	r := &contextReadStub{root: &device}
	r.read = func(_ context.Context, _ model.EntityIdentity, _ repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		return &repository.TopologyNeighborhood{Root: &device}, nil
	}
	i := model.EntityIdentity{SourceID: device.SourceID, EntityType: model.EntityDevice, StableID: device.StableID}
	s := NewTopologyBrowseService(r, topologyConf())
	p, err := s.DeviceContext(context.Background(), "reader", i)
	require.NoError(t, err)
	require.False(t, p.Truncated)
	require.Equal(t, []string{"missing_cabinet"}, p.Warnings)
	require.Equal(t, []repository.TopologyNode{device}, p.Nodes)
	require.Empty(t, p.Edges)
	for _, q := range r.queries {
		require.NotEqual(t, model.EdgePower, q.EdgeType)
	}
	r.queries = nil
	_, err = s.DeviceContext(context.Background(), "writer", i)
	require.ErrorIs(t, err, ErrInventoryForbidden)
	require.Empty(t, r.queries)
	r.err = fmt.Errorf("backend unavailable")
	_, err = s.DeviceContext(context.Background(), "reader", i)
	require.ErrorIs(t, err, ErrInventoryNotReady)
}

func TestDeviceContextIntermediateRepositoryFailureIsUnavailable(t *testing.T) {
	root := contextNode(model.EntityDevice, "root")
	for _, err := range []error{repository.ErrInventoryNotFound, repository.ErrInventoryInvalid, repository.ErrInventoryNotReady} {
		r := &contextReadStub{root: &root}
		r.read = func(context.Context, model.EntityIdentity, repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
			return nil, err
		}
		s := NewTopologyBrowseService(r, topologyConf())
		_, got := s.DeviceContext(context.Background(), "reader", model.EntityIdentity{SourceID: root.SourceID, EntityType: model.EntityDevice, StableID: root.StableID})
		require.ErrorIs(t, got, ErrInventoryNotReady)
	}
}
