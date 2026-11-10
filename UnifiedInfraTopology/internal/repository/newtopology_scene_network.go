package repository

import (
	"context"
	"reflect"
	"sort"
	"strconv"

	"UnifiedInfraTopology/internal/model"
)

// 场景网络是有界的一跳接口展开，不改变通用邻居分页契约。
type TopologySceneNetworkReader interface {
	SceneNetwork(context.Context, []TopologyNode, int) (*TopologyNeighborhood, error)
}

func (r *topologyReadRepository) SceneNetwork(ctx context.Context, seeds []TopologyNode, limit int) (*TopologyNeighborhood, error) {
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
		if err != nil || vid != seed.ID || seed.EntityType != string(model.EntityDevice) || (source != "" && seed.SourceID != source) {
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
	seedIDs := topologySceneNodeIDs(nodes, model.EntityDevice)
	stages := [][]TopologyNeighborQuery{
		{{Direction: "out", EdgeType: model.EdgeComposition, RelationKind: model.RelationOwnsInterface, PeerEntityType: model.EntityInterface},
			{Direction: "out", EdgeType: model.EdgeNetwork, RelationKind: model.RelationServerUplink, PeerEntityType: model.EntityInterface}},
		{{Direction: "both", EdgeType: model.EdgeNetwork, RelationKind: model.RelationLinksTo, PeerEntityType: model.EntityInterface}},
		{{Direction: "in", EdgeType: model.EdgeComposition, RelationKind: model.RelationOwnsInterface, PeerEntityType: model.EntityDevice}},
	}
	for stage, queries := range stages {
		if stage > 0 {
			seedIDs = topologySceneNodeIDs(nodes, model.EntityInterface)
		}
		if len(seedIDs) == 0 {
			break
		}
		selected := map[string]bool{}
		for _, id := range seedIDs {
			selected[id] = true
		}
		stageEdges := []TopologyEdge{}
		for queryIndex, query := range queries {
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
					if !reflect.DeepEqual(previous, edge) {
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
				result.Truncated = result.Truncated || stage < len(stages)-1 || queryIndex < len(queries)-1
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
				if previous, exists := unique[edge.ID]; exists && !reflect.DeepEqual(previous, edge) {
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
