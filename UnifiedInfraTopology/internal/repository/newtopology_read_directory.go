package repository

import (
	"context"
	"strconv"

	"UnifiedInfraTopology/internal/model"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

func topologyDirectoryQueryValid(query TopologyDirectoryQuery) bool {
	return query.Limit >= 1 && query.Limit <= 100 && (query.After == "" || topologyVIDPattern.MatchString(query.After))
}

func (r *topologyReadRepository) Roots(ctx context.Context, source string, entityType model.EntityType, query TopologyDirectoryQuery) (*TopologyDirectoryPage, error) {
	if source == "" || (entityType != model.EntityDataCenter && entityType != model.EntityPod) || !topologyDirectoryQueryValid(query) {
		return nil, ErrInventoryInvalid
	}
	statement := "MATCH (n:" + string(entityType) + ") WHERE n." + string(entityType) + ".source_id == $source_id AND id(n) > $after " +
		"WITH n AS node, id(n) AS vid ORDER BY vid LIMIT " + strconv.Itoa(query.Limit+1) + " RETURN node;"
	rows, err := r.execute(ctx, statement, map[string]interface{}{"source_id": source, "after": query.After})
	if err != nil {
		return nil, err
	}
	return topologyReadDirectoryPage(rows, source, entityType, query)
}

func (r *topologyReadRepository) Children(ctx context.Context, identity model.EntityIdentity, query TopologyDirectoryQuery) (*TopologyDirectoryPage, error) {
	if !topologyDirectoryQueryValid(query) {
		return nil, ErrInventoryInvalid
	}
	var childType model.EntityType
	var kind model.RelationKind
	switch identity.EntityType {
	case model.EntityDataCenter:
		childType, kind = model.EntityRoom, model.RelationLocatedIn
	case model.EntityRoom:
		childType, kind = model.EntityCabinet, model.RelationLocatedIn
	case model.EntityCabinet:
		childType, kind = model.EntityDevice, model.RelationLocatedIn
	case model.EntityPod:
		childType, kind = model.EntityDevice, model.RelationMemberOf
	default:
		return nil, ErrInventoryInvalid
	}
	parent, err := r.Node(ctx, identity)
	if err != nil {
		return nil, err
	}
	statement := "MATCH (n:" + string(childType) + ")-[e:" + string(model.EdgeSpatial) + "]->(p:" + string(identity.EntityType) + ") WHERE id(p) == $vid " +
		"AND properties(e).relation_kind == $relation_kind AND properties(e).source_id == $source_id " +
		"AND n." + string(childType) + ".source_id == $source_id AND p." + string(identity.EntityType) + ".source_id == $source_id AND id(n) > $after " +
		"WITH DISTINCT n AS node, id(n) AS vid ORDER BY vid LIMIT " + strconv.Itoa(query.Limit+1) + " RETURN node;"
	rows, err := r.execute(ctx, statement, map[string]interface{}{"vid": parent.ID, "source_id": identity.SourceID, "relation_kind": string(kind), "after": query.After})
	if err != nil {
		return nil, err
	}
	return topologyReadDirectoryPage(rows, identity.SourceID, childType, query)
}

func topologyReadDirectoryPage(rows *nebulago.ResultSet, source string, entityType model.EntityType, query TopologyDirectoryQuery) (*TopologyDirectoryPage, error) {
	if !topologyReadColumns(rows, "node") || rows.GetRowSize() > query.Limit+1 {
		return nil, ErrInventoryNotReady
	}
	page := &TopologyDirectoryPage{Items: make([]TopologyNode, 0, rows.GetRowSize())}
	previous := query.After
	for index := 0; index < rows.GetRowSize(); index++ {
		values, err := topologyReadRow(rows, index)
		if err != nil {
			return nil, err
		}
		node, err := topologyReadSDKNode(values[0])
		if err != nil {
			return nil, err
		}
		if node.SourceID != source || model.EntityType(node.EntityType) != entityType || node.ID <= previous {
			return nil, ErrInventoryNotReady
		}
		previous = node.ID
		page.Items = append(page.Items, *node)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		page.NextAfter = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}
