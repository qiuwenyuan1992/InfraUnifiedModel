package repository

import (
	"context"
	"reflect"
	"sort"
	"strconv"

	"UnifiedInfraTopology/internal/model"
)

// topologySceneStage 描述一个批量遍历阶段：种子实体类型与该阶段执行的一跳查询。
type topologySceneStage struct {
	seedType model.EntityType
	queries  []TopologyNeighborQuery
}

// 场景网络是有界的一跳接口展开，不改变通用邻居分页契约。
func (r *topologyReadRepository) SceneNetwork(ctx context.Context, seeds []TopologyNode, limit int) (*TopologyNeighborhood, error) {
	return r.sceneNetworkStages(ctx, seeds, limit, model.EntityDevice, []topologySceneStage{
		{model.EntityDevice, []TopologyNeighborQuery{{Direction: "out", EdgeType: model.EdgeComposition, RelationKind: model.RelationOwnsInterface, PeerEntityType: model.EntityInterface},
			{Direction: "out", EdgeType: model.EdgeNetwork, RelationKind: model.RelationServerUplink, PeerEntityType: model.EntityInterface}}},
		{model.EntityInterface, []TopologyNeighborQuery{{Direction: "both", EdgeType: model.EdgeNetwork, RelationKind: model.RelationLinksTo, PeerEntityType: model.EntityInterface}}},
		{model.EntityInterface, []TopologyNeighborQuery{{Direction: "in", EdgeType: model.EdgeComposition, RelationKind: model.RelationOwnsInterface, PeerEntityType: model.EntityDevice}}},
	})
}

// SceneNetworkPeers 服务网络设备的设备上下文：首阶段只取端口归属，跳过服务器上联。
func (r *topologyReadRepository) SceneNetworkPeers(ctx context.Context, seeds []TopologyNode, limit int) (*TopologyNeighborhood, error) {
	return r.sceneNetworkStages(ctx, seeds, limit, model.EntityDevice, []topologySceneStage{
		{model.EntityDevice, []TopologyNeighborQuery{{Direction: "out", EdgeType: model.EdgeComposition, RelationKind: model.RelationOwnsInterface, PeerEntityType: model.EntityInterface}}},
		{model.EntityInterface, []TopologyNeighborQuery{{Direction: "both", EdgeType: model.EdgeNetwork, RelationKind: model.RelationLinksTo, PeerEntityType: model.EntityInterface}}},
		{model.EntityInterface, []TopologyNeighborQuery{{Direction: "in", EdgeType: model.EdgeComposition, RelationKind: model.RelationOwnsInterface, PeerEntityType: model.EntityDevice}}},
	})
}

// SceneRoomPower 从房间出发批量遍历主供电链：房间内机柜与列头柜→UPS组→变压器，
// 附带UPS组成员；默认链不查询备用关系（has_standby），与 power.md 查询语义一致。
// 机柜→列头柜 power_upstream 常因来源未维护而缺失，房间内列头柜直接作为起点保住主链。
func (r *topologyReadRepository) SceneRoomPower(ctx context.Context, seeds []TopologyNode, limit int) (*TopologyNeighborhood, error) {
	return r.sceneNetworkStages(ctx, seeds, limit, model.EntityRoom, []topologySceneStage{
		{model.EntityRoom, []TopologyNeighborQuery{
			{Direction: "in", EdgeType: model.EdgeSpatial, RelationKind: model.RelationLocatedIn, PeerEntityType: model.EntityCabinet},
			{Direction: "in", EdgeType: model.EdgeSpatial, RelationKind: model.RelationLocatedIn, PeerEntityType: model.EntityRPP},
		}},
		{model.EntityCabinet, []TopologyNeighborQuery{{Direction: "out", EdgeType: model.EdgePower, RelationKind: model.RelationPowerUpstream, PeerEntityType: model.EntityRPP}}},
		{model.EntityRPP, []TopologyNeighborQuery{{Direction: "out", EdgeType: model.EdgePower, RelationKind: model.RelationPowerUpstream, PeerEntityType: model.EntityUPSGroup}}},
		{model.EntityUPSGroup, []TopologyNeighborQuery{{Direction: "out", EdgeType: model.EdgePower, RelationKind: model.RelationPowerUpstream, PeerEntityType: model.EntityTransformer}}},
		{model.EntityUPSGroup, []TopologyNeighborQuery{{Direction: "in", EdgeType: model.EdgePower, RelationKind: model.RelationMemberOf, PeerEntityType: model.EntityUPS}}},
	})
}

func (r *topologyReadRepository) sceneNetworkStages(ctx context.Context, seeds []TopologyNode, limit int, seedType model.EntityType, stages []topologySceneStage) (*TopologyNeighborhood, error) {
	if limit < 1 || limit > 600 || len(seeds) > 400 {
		return nil, ErrInventoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &TopologyNeighborhood{Nodes: []TopologyNode{}, Edges: []TopologyEdge{}, Limit: limit}
	nodes := map[string]TopologyNode{}
	edges := map[string]TopologyEdge{}
	source := ""
	for _, seed := range seeds {
		vid, err := (model.EntityIdentity{SourceID: seed.SourceID, EntityType: model.EntityType(seed.EntityType), StableID: seed.StableID}).VID()
		if err != nil || vid != seed.ID || seed.EntityType != string(seedType) || (source != "" && seed.SourceID != source) {
			return nil, ErrInventoryInvalid
		}
		if previous, ok := nodes[seed.ID]; ok && !reflect.DeepEqual(previous, seed) {
			return nil, ErrInventoryInvalid
		}
		source = seed.SourceID
		nodes[seed.ID] = seed
	}
	if len(nodes) == 0 {
		return result, nil
	}
	for stageIndex, stage := range stages {
		seedIDs := topologySceneNodeIDs(nodes, stage.seedType)
		if len(seedIDs) == 0 {
			break
		}
		selected := map[string]bool{}
		for _, id := range seedIDs {
			selected[id] = true
		}
		stageEdges := []TopologyEdge{}
		for queryIndex, query := range stage.queries {
			rows, err := r.sceneNetworkEdges(ctx, source, seedIDs, query, limit)
			if err != nil {
				return nil, err
			}
			for _, edge := range rows {
				matches := selected[edge.From] || selected[edge.To]
				if query.Direction == "out" {
					matches = selected[edge.From]
				}
				if query.Direction == "in" {
					matches = selected[edge.To]
				}
				if !matches || edge.SourceID != source || edge.RelationKind != string(query.RelationKind) {
					return nil, ErrInventoryNotReady
				}
				if previous, exists := edges[edge.ID]; exists {
					if !topologyEdgeSameIdentity(previous, edge) {
						return nil, ErrInventoryNotReady
					}
					continue
				}
				if len(edges) == limit {
					result.Truncated = true
					continue
				}
				edges[edge.ID] = edge
				stageEdges = append(stageEdges, edge)
			}
			if len(edges) == limit {
				// 后续查询未执行，不能声明接口归属完整。
				result.Truncated = result.Truncated || stageIndex < len(stages)-1 || queryIndex < len(stage.queries)-1
				break
			}
		}
		if err := r.sceneNetworkNodes(ctx, source, nodes, stageEdges); err != nil {
			return nil, err
		}
		check := &TopologyNeighborhood{Edges: stageEdges}
		for _, node := range nodes {
			check.Nodes = append(check.Nodes, node)
		}
		if err := topologyReadRelationMatrix(check); err != nil {
			return nil, err
		}
		if result.Truncated {
			break
		}
	}
	for _, node := range nodes {
		result.Nodes = append(result.Nodes, node)
	}
	for _, edge := range edges {
		result.Edges = append(result.Edges, edge)
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result, nil
}

func topologySceneNodeIDs(nodes map[string]TopologyNode, entity model.EntityType) []string {
	ids := []string{}
	for id, node := range nodes {
		if node.EntityType == string(entity) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// topologyEdgeSameIdentity 判断同 relation_id 的两条边是否为同一结构身份：
// 端点、方向(rank)、边类型、关系种类、来源一致即同一条边。
// created_at/synced_at 等快照属性不参与身份判定——多轮同步后，
// 出向/入向两种查询形态可能读到不同轮次写入的属性快照，快照差异不构成边冲突。
func topologyEdgeSameIdentity(a, b TopologyEdge) bool {
	return a.From == b.From && a.To == b.To && a.Rank == b.Rank &&
		a.SourceID == b.SourceID && a.EdgeType == b.EdgeType && a.RelationKind == b.RelationKind
}

func (r *topologyReadRepository) sceneNetworkEdges(ctx context.Context, source string, seeds []string, query TopologyNeighborQuery, limit int) ([]TopologyEdge, error) {
	if query.Direction == "both" {
		// 分别从已知 VID 展开，避免双端 OR 使规划器退化为全边扫描。
		unique := map[string]TopologyEdge{}
		for _, direction := range []string{"out", "in"} {
			directed := query
			directed.Direction = direction
			edges, err := r.sceneNetworkEdges(ctx, source, seeds, directed, limit)
			if err != nil {
				return nil, err
			}
			for _, edge := range edges {
				if previous, exists := unique[edge.ID]; exists && !topologyEdgeSameIdentity(previous, edge) {
					return nil, ErrInventoryNotReady
				}
				unique[edge.ID] = edge
			}
		}
		edges := make([]TopologyEdge, 0, len(unique))
		for _, edge := range unique {
			edges = append(edges, edge)
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
		if len(edges) > limit+1 {
			edges = edges[:limit+1]
		}
		return edges, nil
	}
	parameters := make([]any, len(seeds))
	selected := map[string]bool{}
	for i, id := range seeds {
		parameters[i] = id
		selected[id] = true
	}
	pattern := "(a)-[e:" + string(query.EdgeType) + "]->(b)"
	if query.Direction == "in" {
		pattern = "(b)-[e:" + string(query.EdgeType) + "]->(a)"
	}
	statement := "MATCH (a) WHERE id(a) IN $seed_vids WITH a MATCH " + pattern + " WHERE $peer_entity_type IN labels(b) AND properties(e).source_id == $source_id AND properties(e).relation_kind == $relation_kind " +
		"WITH src(e) AS src_vid, dst(e) AS dst_vid, rank(e) AS edge_rank, properties(e) AS props, properties(e).relation_id AS edge_id " +
		"ORDER BY edge_id, src_vid, dst_vid, edge_rank LIMIT " + strconv.Itoa(limit+1) + " RETURN src_vid, dst_vid, edge_rank, props;"
	rows, err := r.execute(ctx, statement, map[string]interface{}{"seed_vids": parameters, "source_id": source, "relation_kind": string(query.RelationKind), "peer_entity_type": string(query.PeerEntityType)})
	if err != nil {
		return nil, err
	}
	if !topologyReadColumns(rows, "src_vid", "dst_vid", "edge_rank", "props") || rows.GetRowSize() > limit+1 {
		return nil, ErrInventoryNotReady
	}
	edges := make([]TopologyEdge, 0, rows.GetRowSize())
	for i := 0; i < rows.GetRowSize(); i++ {
		values, err := topologyReadRow(rows, i)
		if err != nil {
			return nil, err
		}
		edge, err := topologyReadEdge(values, query.EdgeType)
		if err != nil {
			return nil, err
		}
		matches := selected[edge.From]
		if query.Direction == "in" {
			matches = selected[edge.To]
		}
		if !matches || edge.SourceID != source || edge.RelationKind != string(query.RelationKind) {
			return nil, ErrInventoryNotReady
		}
		edges = append(edges, *edge)
	}
	return edges, nil
}

func (r *topologyReadRepository) sceneNetworkNodes(ctx context.Context, source string, nodes map[string]TopologyNode, edges []TopologyEdge) error {
	missing := map[string]bool{}
	for _, edge := range edges {
		for _, id := range []string{edge.From, edge.To} {
			if _, exists := nodes[id]; !exists {
				missing[id] = true
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	ids := make([]string, 0, len(missing))
	for id := range missing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parameters := make([]any, len(ids))
	for i, id := range ids {
		parameters[i] = id
	}
	rows, err := r.execute(ctx, "MATCH (n) WHERE id(n) IN $vids RETURN n AS node LIMIT "+strconv.Itoa(len(ids)+1)+";", map[string]interface{}{"vids": parameters})
	if err != nil {
		return err
	}
	if !topologyReadColumns(rows, "node") || rows.GetRowSize() != len(ids) {
		return ErrInventoryNotReady
	}
	for i := 0; i < rows.GetRowSize(); i++ {
		values, err := topologyReadRow(rows, i)
		if err != nil {
			return err
		}
		node, err := topologyReadSDKNode(values[0])
		if err != nil {
			return err
		}
		if !missing[node.ID] || node.SourceID != source {
			return ErrInventoryNotReady
		}
		delete(missing, node.ID)
		nodes[node.ID] = *node
		cacheTopologyNode(ctx, node)
	}
	if len(missing) != 0 {
		return ErrInventoryNotReady
	}
	return nil
}
