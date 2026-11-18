package repository

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

// 仅互联模式（网络设备设备上下文）：首阶段只查 owns_interface，跳过 server_uplink。
func TestSceneNetworkPeersSkipsServerUplink(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	port := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "p"}
	own := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: device, To: port}
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns, readEdge(t, own, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, port)}),
		readResult(t, columns), readResult(t, columns),
		readResult(t, columns),
	}}
	page, err := (&topologyReadRepository{client: fake}).SceneNetworkPeers(context.Background(), []TopologyNode{sceneNetworkSeed(t, device)}, 600)
	require.NoError(t, err)
	require.Nil(t, page.Root)
	require.Len(t, page.Edges, 1)
	id, _ := own.ID()
	require.Equal(t, id, page.Edges[0].ID)
	for _, call := range fake.calls {
		require.NotEqual(t, string(model.RelationServerUplink), call.params["relation_kind"], "仅互联模式不得查询服务器上联")
	}
}

// 房间供电：从房间批量遍历主供电链（机柜→列头柜→UPS组→变压器）与UPS成员；默认链不含备用关系。
func TestSceneRoomPowerWalksMainChainAndUpsMembers(t *testing.T) {
	room := model.EntityIdentity{SourceID: "s", EntityType: model.EntityRoom, StableID: "r"}
	fed := model.EntityIdentity{SourceID: "s", EntityType: model.EntityCabinet, StableID: "c1"}
	unfed := model.EntityIdentity{SourceID: "s", EntityType: model.EntityCabinet, StableID: "c2"}
	rpp := model.EntityIdentity{SourceID: "s", EntityType: model.EntityRPP, StableID: "6204"}
	group := model.EntityIdentity{SourceID: "s", EntityType: model.EntityUPSGroup, StableID: "7"}
	transformer := model.EntityIdentity{SourceID: "s", EntityType: model.EntityTransformer, StableID: "8"}
	ups := model.EntityIdentity{SourceID: "s", EntityType: model.EntityUPS, StableID: "9"}
	fedIn := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: fed, To: room}
	unfedIn := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: unfed, To: room}
	rppIn := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeSpatial, Kind: model.RelationLocatedIn, From: rpp, To: room}
	feed := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgePower, Kind: model.RelationPowerUpstream, From: fed, To: rpp, Discriminator: "A"}
	rppUp := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgePower, Kind: model.RelationPowerUpstream, From: rpp, To: group}
	groupUp := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgePower, Kind: model.RelationPowerUpstream, From: group, To: transformer}
	member := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgePower, Kind: model.RelationMemberOf, From: ups, To: group}
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns, readEdge(t, fedIn, nil), readEdge(t, unfedIn, nil)),
		readResult(t, columns, readEdge(t, rppIn, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, fed)}, []*nebula.Value{readPeer(t, unfed)}, []*nebula.Value{readPeer(t, rpp)}),
		readResult(t, columns, readEdge(t, feed, nil)),
		readResult(t, columns, readEdge(t, rppUp, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, group)}),
		readResult(t, columns, readEdge(t, groupUp, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, transformer)}),
		readResult(t, columns, readEdge(t, member, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, ups)}),
	}}
	page, err := (&topologyReadRepository{client: fake}).SceneRoomPower(context.Background(), []TopologyNode{sceneNetworkSeed(t, room)}, 600)
	require.NoError(t, err)
	require.Nil(t, page.Root)
	require.Len(t, page.Nodes, 7)
	require.Len(t, page.Edges, 7)
	for _, edge := range page.Edges {
		require.NotEqual(t, string(model.RelationHasStandby), edge.RelationKind)
	}
	for _, call := range fake.calls {
		require.NotEqual(t, string(model.RelationHasStandby), call.params["relation_kind"], "默认供电链不得查询备用关系")
	}
	fedVID, _ := fed.VID()
	unfedVID, _ := unfed.VID()
	// 供电上游阶段必须以房间内机柜为种子批量查询。
	require.ElementsMatch(t, []interface{}{fedVID, unfedVID}, fake.calls[3].params["seed_vids"])
	// 列头柜在首阶段以房间为种子收取，即使机柜→列头柜边缺失也保住主链起点。
	roomVID, _ := room.VID()
	require.Equal(t, []interface{}{roomVID}, fake.calls[1].params["seed_vids"])
	require.Equal(t, string(model.EntityRPP), fake.calls[1].params["peer_entity_type"])
}

func TestSceneNetworkPreservesStoredServerUplinkAndDates(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	port := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "p"}
	uplink := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: device, To: port}
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns),
		readResult(t, columns, readEdge(t, uplink, map[string]*nebula.Value{"synced_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 5}}})),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, port)}),
		readResult(t, columns), readResult(t, columns), readResult(t, columns),
	}}
	page, err := (&topologyReadRepository{client: fake}).SceneNetwork(context.Background(), []TopologyNode{sceneNetworkSeed(t, device)}, 600)
	require.NoError(t, err)
	require.Len(t, page.Edges, 1)
	id, _ := uplink.ID()
	require.Equal(t, id, page.Edges[0].ID)
	require.Equal(t, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), *page.Edges[0].SyncedAt)
	require.Equal(t, string(model.RelationServerUplink), fake.calls[1].params["relation_kind"])
	require.False(t, page.Truncated)
}

func sceneNetworkSeed(t *testing.T, identity model.EntityIdentity) TopologyNode {
	t.Helper()
	vid, err := identity.VID()
	require.NoError(t, err)
	return TopologyNode{ID: vid, SourceID: identity.SourceID, EntityType: string(identity.EntityType), StableID: identity.StableID}
}

func TestSceneNetworkBatchesHundredDevicesPreservesIncomingAndParallelEdges(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	owner := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "owner"}
	local := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "local"}
	remote := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "remote"}
	own := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: device, To: local}
	otherOwn := own
	otherOwn.From, otherOwn.To = owner, remote
	link := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: remote, To: local, Discriminator: "one"}
	parallel := link
	parallel.Discriminator = "two"
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns, readEdge(t, own, nil)), readResult(t, columns),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, local)}),
		readResult(t, columns),
		readResult(t, columns, readEdge(t, link, nil), readEdge(t, parallel, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, remote)}),
		readResult(t, columns, readEdge(t, own, nil), readEdge(t, otherOwn, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, owner)}),
	}}
	seeds := []TopologyNode{sceneNetworkSeed(t, device)}
	for i := 1; i < 100; i++ {
		identity := device
		identity.StableID = fmt.Sprintf("d%d", i)
		seeds = append(seeds, sceneNetworkSeed(t, identity))
	}
	var reader TopologyReadRepository = &topologyReadRepository{client: fake}
	page, err := reader.SceneNetwork(context.Background(), seeds, 600)
	require.NoError(t, err)
	require.Nil(t, page.Root)
	require.False(t, page.Truncated)
	require.Len(t, page.Nodes, 103)
	require.Len(t, page.Edges, 4)
	require.Len(t, fake.calls, 8)
	require.Len(t, fake.calls[0].params["seed_vids"], 100)
	wantIDs := []string{}
	for _, identity := range []model.RelationIdentity{own, otherOwn, link, parallel} {
		id, err := identity.ID()
		require.NoError(t, err)
		wantIDs = append(wantIDs, id)
	}
	actualIDs := []string{}
	for _, edge := range page.Edges {
		actualIDs = append(actualIDs, edge.ID)
	}
	require.ElementsMatch(t, wantIDs, actualIDs)
	for _, node := range page.Nodes {
		if node.EntityType == string(model.EntityInterface) {
			require.NotNil(t, node.SyncedAt)
			require.NotNil(t, node.CreatedAt)
		}
	}
	for _, index := range []int{0, 1, 3, 4, 6} {
		require.Contains(t, fake.calls[index].statement, "MATCH (a) WHERE id(a) IN $seed_vids WITH a MATCH")
		require.NotContains(t, fake.calls[index].statement, " OR ")
		require.Contains(t, fake.calls[index].statement, "IN $seed_vids")
		require.Contains(t, fake.calls[index].statement, "properties(e).source_id == $source_id")
		require.Contains(t, fake.calls[index].statement, "properties(e).relation_kind == $relation_kind")
		require.Contains(t, fake.calls[index].statement, "LIMIT ")
	}
	require.Contains(t, fake.calls[3].statement, "MATCH (a)-[e:network_relation]->(b)")
	require.Contains(t, fake.calls[4].statement, "MATCH (b)-[e:network_relation]->(a)")
	require.Contains(t, fake.calls[6].statement, "MATCH (b)-[e:composition_relation]->(a)")
	for _, edge := range page.Edges {
		if edge.RelationKind == string(model.RelationLinksTo) {
			from, _ := remote.VID()
			to, _ := local.VID()
			require.Equal(t, from, edge.From)
			require.Equal(t, to, edge.To)
			require.NotEmpty(t, edge.Rank)
		}
	}
}

func TestSceneNetworkRejectsMalformedStoredResults(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	port := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "p"}
	own := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: device, To: port}
	for _, name := range []string{"edge source", "rank", "unselected root", "peer source", "peer vid", "peer tags", "peer type", "peer timestamp", "edge timestamp", "columns", "duplicate peer", "too many rows", "kind"} {
		t.Run(name, func(t *testing.T) {
			row := readEdge(t, own, nil)
			peer := readPeer(t, port)
			columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
			edgeRows := [][]*nebula.Value{row}
			peerRows := [][]*nebula.Value{{peer}}
			switch name {
			case "edge source":
				row[3].MVal.Kvs["source_id"] = readString("other")
			case "rank":
				row[2] = readInt(1)
			case "unselected root":
				row[0] = readPeer(t, port).VVal.Vid
			case "peer source":
				peer.VVal.Tags[0].Props["source_id"] = readString("other")
			case "peer vid":
				peer.VVal.Vid = readString("bad")
			case "peer tags":
				peer.VVal.Tags = append(peer.VVal.Tags, peer.VVal.Tags[0])
			case "peer type":
				peer.VVal.Tags[0].Name = []byte(model.EntityDevice)
			case "peer timestamp":
				peer.VVal.Tags[0].Props["synced_at"] = readString("bad")
			case "edge timestamp":
				row[3].MVal.Kvs["created_at"] = readString("bad")
			case "columns":
				columns[0] = "bad"
			case "duplicate peer":
				peerRows = append(peerRows, []*nebula.Value{peer})
			case "too many rows":
				for i := 0; i < 4; i++ {
					edgeRows = append(edgeRows, row)
				}
			case "kind":
				row[3].MVal.Kvs["relation_kind"] = readString(string(model.RelationContainsGPU))
			}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{
				readResult(t, columns, edgeRows...), readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}), readResult(t, []string{"node"}, peerRows...),
			}}
			_, err := (&topologyReadRepository{client: fake}).SceneNetwork(context.Background(), []TopologyNode{sceneNetworkSeed(t, device)}, 3)
			require.ErrorIs(t, err, ErrInventoryNotReady)
		})
	}
}

func TestSceneNetworkValidatesSeedsAndHonorsQueryBudget(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	seed := sceneNetworkSeed(t, device)
	for _, name := range []string{"vid", "source", "type", "limit"} {
		t.Run(name, func(t *testing.T) {
			bad, limit := seed, 600
			switch name {
			case "vid":
				bad.ID = "bad"
			case "source":
				bad.SourceID = "other"
			case "type":
				bad.EntityType = string(model.EntityInterface)
			case "limit":
				limit = 601
			}
			fake := &topologyGraphFake{}
			_, err := (&topologyReadRepository{client: fake}).SceneNetwork(context.Background(), []TopologyNode{bad}, limit)
			require.ErrorIs(t, err, ErrInventoryInvalid)
			require.Empty(t, fake.calls)
		})
	}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"})}}
	_, err := (&topologyReadRepository{client: fake}).SceneNetwork(WithTopologyQueryBudget(context.Background(), 1), []TopologyNode{seed}, 600)
	require.ErrorIs(t, err, ErrTopologyQueryBudget)
	require.Len(t, fake.calls, 1)
}

func TestSceneNetworkGlobalLimitStopsExpansion(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	local := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "local"}
	other := local
	other.StableID = "other"
	own := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: device, To: local}
	second := own
	second.To = other
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, readEdge(t, own, nil), readEdge(t, second, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, local)}),
	}}
	var reader TopologyReadRepository = &topologyReadRepository{client: fake}
	page, err := reader.SceneNetwork(context.Background(), []TopologyNode{sceneNetworkSeed(t, device)}, 1)
	require.NoError(t, err)
	require.True(t, page.Truncated)
	require.Len(t, page.Edges, 1)
	require.Len(t, page.Nodes, 2)
	require.Len(t, fake.calls, 2)
	require.Contains(t, fake.calls[0].statement, "LIMIT 2")
}

func TestSceneNetworkBothDirectionsMergeBeforeGlobalTruncation(t *testing.T) {
	local := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "local"}
	remote := local
	remote.StableID = "remote"
	outgoing := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: local, To: remote}
	incoming := outgoing
	incoming.From, incoming.To = remote, local
	parallel := incoming
	parallel.Discriminator = "parallel"
	localID, err := local.VID()
	require.NoError(t, err)
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns, readEdge(t, outgoing, nil)),
		readResult(t, columns, readEdge(t, incoming, nil), readEdge(t, parallel, nil)),
	}}
	edges, err := (&topologyReadRepository{client: fake}).sceneNetworkEdges(context.Background(), "s", []string{localID}, TopologyNeighborQuery{Direction: "both", EdgeType: model.EdgeNetwork, RelationKind: model.RelationLinksTo, PeerEntityType: model.EntityInterface}, 1)
	require.NoError(t, err)
	want := []string{}
	for _, relation := range []model.RelationIdentity{outgoing, incoming, parallel} {
		id, err := relation.ID()
		require.NoError(t, err)
		want = append(want, id)
	}
	sort.Strings(want)
	require.Len(t, edges, 2)
	require.Equal(t, want[:2], []string{edges[0].ID, edges[1].ID}, "merge directions before retaining the global limit plus sentinel")
	require.Len(t, fake.calls, 2)
}

func TestSceneNetworkRejectsWrongDirectionalScope(t *testing.T) {
	local := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "local"}
	remote := local
	remote.StableID = "remote"
	localID, err := local.VID()
	require.NoError(t, err)
	for _, direction := range []string{"out", "in"} {
		t.Run(direction, func(t *testing.T) {
			relation := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: remote, To: local}
			if direction == "in" {
				relation.From, relation.To = local, remote
			}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, readEdge(t, relation, nil))}}
			_, err := (&topologyReadRepository{client: fake}).sceneNetworkEdges(context.Background(), "s", []string{localID}, TopologyNeighborQuery{Direction: direction, EdgeType: model.EdgeNetwork, RelationKind: model.RelationLinksTo, PeerEntityType: model.EntityInterface}, 600)
			require.ErrorIs(t, err, ErrInventoryNotReady)
		})
	}
}

func TestSceneNetworkPeersToleratesSnapshotDivergenceAcrossStages(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	port := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "p"}
	own := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: device, To: port}
	// 同一条边在不同阶段（设备锚点出向 / 端口锚点入向）可能读到不同轮次写入的时间戳快照，
	// 身份以 relation_id 为准，快照差异不得使整个视图失败。
	first := map[string]*nebula.Value{
		"created_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 2, Minute: 21, Sec: 11}},
		"synced_at":  {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 2, Minute: 21, Sec: 11}},
	}
	second := map[string]*nebula.Value{
		"created_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 3, Minute: 11, Sec: 26}},
		"synced_at":  {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 3, Minute: 11, Sec: 26}},
	}
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns, readEdge(t, own, first)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, port)}),
		readResult(t, columns), readResult(t, columns),
		readResult(t, columns, readEdge(t, own, second)),
	}}
	page, err := (&topologyReadRepository{client: fake}).SceneNetworkPeers(context.Background(), []TopologyNode{sceneNetworkSeed(t, device)}, 600)
	require.NoError(t, err)
	require.Len(t, page.Edges, 1)
}

func TestSceneNetworkBothDirectionsMergeDivergentSnapshots(t *testing.T) {
	local := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "local"}
	remote := local
	remote.StableID = "remote"
	link := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: local, To: remote}
	first := map[string]*nebula.Value{
		"created_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 2, Minute: 21, Sec: 11}},
		"synced_at":  {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 2, Minute: 21, Sec: 11}},
	}
	second := map[string]*nebula.Value{
		"created_at": {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 3, Minute: 11, Sec: 26}},
		"synced_at":  {DtVal: &nebula.DateTime{Year: 2026, Month: 11, Day: 17, Hour: 3, Minute: 11, Sec: 26}},
	}
	localID, err := local.VID()
	require.NoError(t, err)
	remoteID, err := remote.VID()
	require.NoError(t, err)
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns, readEdge(t, link, first)),
		readResult(t, columns, readEdge(t, link, second)),
	}}
	// 种子包含两端：出向以 local 为锚点、入向以 remote 为锚点，两个子查询读到同一条边。
	edges, err := (&topologyReadRepository{client: fake}).sceneNetworkEdges(context.Background(), "s", []string{localID, remoteID}, TopologyNeighborQuery{Direction: "both", EdgeType: model.EdgeNetwork, RelationKind: model.RelationLinksTo, PeerEntityType: model.EntityInterface}, 600)
	require.NoError(t, err)
	require.Len(t, edges, 1)
}

func TestSceneNetworkExactFinalBudgetIsComplete(t *testing.T) {
	device := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	port := model.EntityIdentity{SourceID: "s", EntityType: model.EntityInterface, StableID: "p"}
	uplink := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: device, To: port}
	own := uplink
	own.EdgeType, own.Kind = model.EdgeComposition, model.RelationOwnsInterface
	columns := []string{"src_vid", "dst_vid", "edge_rank", "props"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{
		readResult(t, columns), readResult(t, columns, readEdge(t, uplink, nil)),
		readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, port)}),
		readResult(t, columns), readResult(t, columns), readResult(t, columns, readEdge(t, own, nil)),
	}}
	page, err := (&topologyReadRepository{client: fake}).SceneNetwork(context.Background(), []TopologyNode{sceneNetworkSeed(t, device)}, 2)
	require.NoError(t, err)
	require.Len(t, page.Edges, 2)
	require.False(t, page.Truncated)
}
