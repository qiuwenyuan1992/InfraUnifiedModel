package repository

import (
	"context"
	"reflect"
	"sort"
	"strconv"

	"UnifiedInfraTopology/internal/model"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

func (r *topologyReadRepository) Neighbors(ctx context.Context, identity model.EntityIdentity, query TopologyNeighborQuery) (*TopologyNeighborhood, error) {
	if query.Limit < 1 || query.Limit > 200 || (query.Direction != "in" && query.Direction != "out" && query.Direction != "both") {
		return nil, ErrInventoryInvalid
	}
	if query.EdgeType != "" {
		if _, ok := topologyEdgeSchemas[query.EdgeType]; !ok {
			return nil, ErrInventoryInvalid
		}
	}
	if (query.RelationKind != "" && !query.RelationKind.Valid()) || (query.PeerEntityType != "" && !query.PeerEntityType.Valid()) {
		return nil, ErrInventoryInvalid
	}
	edgeTypes := make([]model.EdgeType, 0, len(topologyEdgeSchemas))
	for _, edgeType := range []model.EdgeType{model.EdgeComposition, model.EdgeNetwork, model.EdgePower, model.EdgeSpatial} {
		if query.EdgeType != "" && query.EdgeType != edgeType {
			continue
		}
		if topologyNeighborFilterMatches(identity.EntityType, edgeType, query) {
			edgeTypes = append(edgeTypes, edgeType)
		}
	}
	if len(edgeTypes) == 0 {
		return nil, ErrInventoryInvalid
	}
	root, err := r.Node(ctx, identity)
	if err != nil {
		return nil, err
	}
	result := &TopologyNeighborhood{Root: root, Nodes: []TopologyNode{*root}, Edges: []TopologyEdge{}, Limit: query.Limit}
	edges := make(map[string]TopologyEdge)
	for _, edgeType := range edgeTypes {
		outgoing, incoming := "id(a) == $vid", "id(b) == $vid"
		params := map[string]interface{}{"vid": root.ID, "source_id": identity.SourceID}
		if query.PeerEntityType != "" {
			outgoing += " AND $peer_entity_type IN labels(b)"
			incoming += " AND $peer_entity_type IN labels(a)"
			params["peer_entity_type"] = string(query.PeerEntityType)
		}
		condition := outgoing + " OR " + incoming
		if query.PeerEntityType != "" {
			condition = "(" + outgoing + ") OR (" + incoming + ")"
		}
		if query.Direction == "out" {
			condition = outgoing
		}
		if query.Direction == "in" {
			condition = incoming
		}
		condition = "(" + condition + ") AND properties(e).source_id == $source_id"
		if query.RelationKind != "" {
			condition += " AND properties(e).relation_kind == $relation_kind"
			params["relation_kind"] = string(query.RelationKind)
		}
		statement := "MATCH (a)-[e:" + string(edgeType) + "]->(b) WHERE " + condition + " " +
			"WITH src(e) AS src_vid, dst(e) AS dst_vid, rank(e) AS edge_rank, properties(e) AS props, properties(e).relation_id AS edge_id " +
			"ORDER BY edge_id, src_vid, dst_vid, edge_rank LIMIT " + strconv.Itoa(query.Limit+1) + " RETURN src_vid, dst_vid, edge_rank, props;"
		rows, err := r.execute(ctx, statement, params)
		if err != nil {
			return nil, err
		}
		if !topologyReadColumns(rows, "src_vid", "dst_vid", "edge_rank", "props") || rows.GetRowSize() > query.Limit+1 {
			return nil, ErrInventoryNotReady
		}
		for i := 0; i < rows.GetRowSize(); i++ {
			values, err := topologyReadRow(rows, i)
			if err != nil {
				return nil, err
			}
			edge, err := topologyReadEdge(values, edgeType)
			if err != nil {
				return nil, err
			}
			if edge.SourceID != root.SourceID || (edge.From != root.ID && edge.To != root.ID) || (query.Direction == "out" && edge.From != root.ID) || (query.Direction == "in" && edge.To != root.ID) {
				return nil, ErrInventoryNotReady
			}
			if query.RelationKind != "" && edge.RelationKind != string(query.RelationKind) {
				return nil, ErrInventoryNotReady
			}
			if existing, ok := edges[edge.ID]; ok && !reflect.DeepEqual(existing, *edge) {
				return nil, ErrInventoryNotReady
			}
			edges[edge.ID] = *edge
		}
	}
	for _, edge := range edges {
		result.Edges = append(result.Edges, edge)
	}
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	if len(result.Edges) > query.Limit {
		result.Truncated = true
		result.Edges = result.Edges[:query.Limit]
	}
	if len(result.Edges) == 0 {
		return result, nil
	}
	peers := make(map[string]bool)
	for _, edge := range result.Edges {
		if edge.From != root.ID {
			peers[edge.From] = true
		}
		if edge.To != root.ID {
			peers[edge.To] = true
		}
	}
	if len(peers) == 0 {
		if err := topologyReadRelationMatrix(result); err != nil {
			return nil, err
		}
		return result, nil
	}
	vids := make([]string, 0, len(peers))
	for vid := range peers {
		vids = append(vids, vid)
	}
	sort.Strings(vids)
	parameters := make([]any, len(vids))
	for index, vid := range vids {
		parameters[index] = vid
	}
	rows, err := r.execute(ctx, "MATCH (n) WHERE id(n) IN $vids RETURN n AS node LIMIT "+strconv.Itoa(len(vids)+1)+";", map[string]interface{}{"vids": parameters})
	if err != nil {
		return nil, err
	}
	if !topologyReadColumns(rows, "node") || rows.GetRowSize() != len(vids) {
		return nil, ErrInventoryNotReady
	}
	for i := 0; i < rows.GetRowSize(); i++ {
		values, err := topologyReadRow(rows, i)
		if err != nil {
			return nil, err
		}
		node, err := topologyReadSDKNode(values[0])
		if err != nil {
			return nil, err
		}
		if !peers[node.ID] || node.SourceID != root.SourceID || (query.PeerEntityType != "" && node.EntityType != string(query.PeerEntityType)) {
			return nil, ErrInventoryNotReady
		}
		delete(peers, node.ID)
		result.Nodes = append(result.Nodes, *node)
	}
	if len(peers) != 0 {
		return nil, ErrInventoryNotReady
	}
	if err := topologyReadRelationMatrix(result); err != nil {
		return nil, err
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	for i := range result.Nodes {
		cacheTopologyNode(ctx, &result.Nodes[i])
	}
	return result, nil
}

func topologyNeighborFilterMatches(root model.EntityType, edgeType model.EdgeType, query TopologyNeighborQuery) bool {
	if query.RelationKind == "" && query.PeerEntityType == "" {
		return true
	}
	for peer := range topologyEntitySchemas {
		if query.PeerEntityType != "" && peer != query.PeerEntityType {
			continue
		}
		for _, kind := range []model.RelationKind{model.RelationMemberOf, model.RelationLocatedIn, model.RelationTaggedWith, model.RelationOwnsInterface, model.RelationContainsGPU, model.RelationServerUplink, model.RelationGPUUplink, model.RelationLinksTo, model.RelationPowerUpstream, model.RelationHasStandby} {
			if query.RelationKind != "" && kind != query.RelationKind {
				continue
			}
			if query.Direction != "in" && (model.RelationSpec{EdgeType: edgeType, Kind: kind, From: root, To: peer}).Valid() {
				return true
			}
			if query.Direction != "out" && (model.RelationSpec{EdgeType: edgeType, Kind: kind, From: peer, To: root}).Valid() {
				return true
			}
		}
	}
	return false
}

func topologyReadRelationMatrix(result *TopologyNeighborhood) error {
	types := make(map[string]model.EntityType, len(result.Nodes))
	for _, node := range result.Nodes {
		types[node.ID] = model.EntityType(node.EntityType)
	}
	for _, edge := range result.Edges {
		if !(model.RelationSpec{EdgeType: model.EdgeType(edge.EdgeType), Kind: model.RelationKind(edge.RelationKind), From: types[edge.From], To: types[edge.To]}).Valid() {
			return ErrInventoryNotReady
		}
	}
	return nil
}

func topologyReadEdge(values []nebulago.ValueWrapper, edgeType model.EdgeType) (*TopologyEdge, error) {
	if len(values) != 4 || !values[0].IsString() || !values[1].IsString() || !values[2].IsInt() {
		return nil, ErrInventoryNotReady
	}
	from, err := values[0].AsString()
	if err != nil || !topologyVIDPattern.MatchString(from) {
		return nil, ErrInventoryNotReady
	}
	to, err := values[1].AsString()
	if err != nil || !topologyVIDPattern.MatchString(to) {
		return nil, ErrInventoryNotReady
	}
	rank, err := values[2].AsInt()
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	props, err := topologyReadProperties(values[3])
	if err != nil {
		return nil, err
	}
	id, ok := props["relation_id"].(string)
	if !ok || !topologyVIDPattern.MatchString(id) {
		return nil, ErrInventoryNotReady
	}
	expected, err := strconv.ParseUint(id[:16], 16, 64)
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	expected &= uint64(1<<63 - 1)
	if expected == 0 {
		expected = 1
	}
	if rank != int64(expected) {
		return nil, ErrInventoryNotReady
	}
	source, ok := props["source_id"].(string)
	if !ok || source == "" {
		return nil, ErrInventoryNotReady
	}
	kind, ok := props["relation_kind"].(string)
	if !ok || !model.RelationKind(kind).Valid() {
		return nil, ErrInventoryNotReady
	}
	created, err := topologyReadTimestamp(props, "created_at")
	if err != nil {
		return nil, err
	}
	synced, err := topologyReadTimestamp(props, "synced_at")
	if err != nil {
		return nil, err
	}
	return &TopologyEdge{ID: id, SourceID: source, EdgeType: string(edgeType), RelationKind: kind, From: from, To: to, Rank: strconv.FormatInt(rank, 10), Properties: props, CreatedAt: created, SyncedAt: synced}, nil
}

func topologyReadSDKNode(value nebulago.ValueWrapper) (*TopologyNode, error) {
	if !value.IsVertex() {
		return nil, ErrInventoryNotReady
	}
	vertex, err := value.AsNode()
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	tags := vertex.GetTags()
	if len(tags) != 1 {
		return nil, ErrInventoryNotReady
	}
	if _, ok := topologyEntitySchemas[model.EntityType(tags[0])]; !ok {
		return nil, ErrInventoryNotReady
	}
	idValue := vertex.GetID()
	if !idValue.IsString() {
		return nil, ErrInventoryNotReady
	}
	vid, err := idValue.AsString()
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	values, err := vertex.Properties(tags[0])
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	stableField := topologyEntitySchemas[model.EntityType(tags[0])].stableField
	if stableField == "inst_id" && (values[stableField] == nil || !values[stableField].IsInt()) {
		return nil, ErrInventoryNotReady
	}
	props := make(map[string]any, len(values))
	for key, value := range values {
		decoded, err := topologyReadValue(*value)
		if err != nil {
			return nil, err
		}
		props[key] = decoded
	}
	return topologyReadNode(vid, model.EntityType(tags[0]), props)
}
