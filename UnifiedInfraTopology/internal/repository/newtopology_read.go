package repository

import (
	"context"
	"errors"
	"strconv"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

var ErrInventoryInvalid = errors.New("invalid inventory argument")

type TopologyReadRepository interface {
	Node(context.Context, model.EntityIdentity) (*TopologyNode, error)
	Neighbors(context.Context, model.EntityIdentity, TopologyNeighborQuery) (*TopologyNeighborhood, error)
	Roots(context.Context, string, model.EntityType, TopologyDirectoryQuery) (*TopologyDirectoryPage, error)
	Children(context.Context, model.EntityIdentity, TopologyDirectoryQuery) (*TopologyDirectoryPage, error)
	SceneNetwork(context.Context, []TopologyNode, int) (*TopologyNeighborhood, error)
	SceneNetworkPeers(context.Context, []TopologyNode, int) (*TopologyNeighborhood, error)
	SceneRoomPower(context.Context, []TopologyNode, int) (*TopologyNeighborhood, error)
}

type TopologyDirectoryQuery struct {
	Limit int
	After string
}

type TopologyDirectoryPage struct {
	Items     []TopologyNode `json:"items"`
	NextAfter string         `json:"next_after"`
}

type TopologyNeighborQuery struct {
	Limit          int
	Direction      string
	EdgeType       model.EdgeType
	RelationKind   model.RelationKind
	PeerEntityType model.EntityType
}

type TopologyNode struct {
	ID         string         `json:"id"`
	SourceID   string         `json:"source_id"`
	EntityType string         `json:"entity_type"`
	StableID   string         `json:"stable_id"`
	Label      string         `json:"label"`
	Properties map[string]any `json:"properties"`
	CreatedAt  *time.Time     `json:"created_at"`
	SyncedAt   *time.Time     `json:"synced_at"`
}

type TopologyEdge struct {
	ID           string         `json:"id"`
	SourceID     string         `json:"source_id"`
	EdgeType     string         `json:"edge_type"`
	RelationKind string         `json:"relation_kind"`
	From         string         `json:"from"`
	To           string         `json:"to"`
	Rank         string         `json:"rank"`
	Properties   map[string]any `json:"properties"`
	CreatedAt    *time.Time     `json:"created_at"`
	SyncedAt     *time.Time     `json:"synced_at"`
}

type TopologyNeighborhood struct {
	Root      *TopologyNode  `json:"root"`
	Nodes     []TopologyNode `json:"nodes"`
	Edges     []TopologyEdge `json:"edges"`
	Truncated bool           `json:"truncated"`
	Limit     int            `json:"limit"`
}

type topologyReadRepository struct{ client graphclient.Client }

func NewTopologyReadRepository(conf *viper.Viper) (TopologyReadRepository, func(), error) {
	return newTopologyReadRepository(conf, graphclient.New)
}

func newTopologyReadRepository(conf *viper.Viper, open func(graphclient.Config) (graphclient.Client, error)) (TopologyReadRepository, func(), error) {
	config, err := graphclient.ParseConfig(conf)
	if err != nil {
		return &topologyReadRepository{}, func() {}, nil
	}
	if len(config.Hosts) == 0 || config.Space == "" {
		return &topologyReadRepository{}, func() {}, nil
	}
	client, err := open(config)
	if err != nil {
		return &topologyReadRepository{}, func() {}, nil
	}
	return &topologyReadRepository{client: client}, client.Close, nil
}

func (r *topologyReadRepository) execute(ctx context.Context, query string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.client == nil {
		return nil, ErrInventoryNotReady
	}
	if err := consumeTopologyQuery(ctx); err != nil {
		return nil, err
	}
	result, err := r.client.ExecuteParameter(ctx, query, params)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || result == nil || !result.IsSucceed() {
		return nil, ErrInventoryNotReady
	}
	return result, nil
}

func (r *topologyReadRepository) Node(ctx context.Context, identity model.EntityIdentity) (*TopologyNode, error) {
	vid, err := identity.VID()
	if err != nil {
		return nil, ErrInventoryInvalid
	}
	if _, ok := topologyEntitySchemas[identity.EntityType]; !ok {
		return nil, ErrInventoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node := cachedTopologyNode(ctx, vid); node != nil {
		return node, nil
	}
	result, err := r.execute(ctx, "MATCH (n:"+string(identity.EntityType)+") WHERE id(n) == $vid RETURN properties(n) AS props LIMIT 2;", map[string]interface{}{"vid": vid})
	if err != nil {
		return nil, err
	}
	if !topologyReadColumns(result, "props") {
		return nil, ErrInventoryNotReady
	}
	if result.GetRowSize() == 0 {
		return nil, ErrInventoryNotFound
	}
	if result.GetRowSize() != 1 {
		return nil, ErrInventoryNotReady
	}
	values, err := topologyReadRow(result, 0)
	if err != nil {
		return nil, err
	}
	if !values[0].IsMap() {
		return nil, ErrInventoryNotReady
	}
	raw, err := values[0].AsMap()
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	stable, present := raw[topologyEntitySchemas[identity.EntityType].stableField]
	if topologyEntitySchemas[identity.EntityType].stableField == "inst_id" && (!present || !stable.IsInt()) {
		return nil, ErrInventoryNotReady
	}
	props, err := topologyReadProperties(values[0])
	if err != nil {
		return nil, err
	}
	node, err := topologyReadNode(vid, identity.EntityType, props)
	if err != nil {
		return nil, err
	}
	if node.SourceID != identity.SourceID || node.StableID != identity.StableID {
		return nil, ErrInventoryNotReady
	}
	cacheTopologyNode(ctx, node)
	return node, nil
}

func topologyReadNode(vid string, entityType model.EntityType, props map[string]any) (*TopologyNode, error) {
	schema, ok := topologyEntitySchemas[entityType]
	if !ok {
		return nil, ErrInventoryNotReady
	}
	source, ok := props["source_id"].(string)
	if !ok || source == "" {
		return nil, ErrInventoryNotReady
	}
	stable, ok := props[schema.stableField].(string)
	if schema.stableField == "inst_id" {
		if integer, isInt := props[schema.stableField].(int64); isInt {
			stable, ok = strconv.FormatInt(integer, 10), true
		}
		if ok {
			integer, err := strconv.ParseInt(stable, 10, 64)
			ok = err == nil && integer > 0 && strconv.FormatInt(integer, 10) == stable
		}
	}
	if !ok || stable == "" {
		return nil, ErrInventoryNotReady
	}
	identity := model.EntityIdentity{SourceID: source, EntityType: entityType, StableID: stable}
	expected, err := identity.VID()
	if err != nil || vid != expected {
		return nil, ErrInventoryNotReady
	}
	if err := topologyReadDeclaredLists(schema, props); err != nil {
		return nil, err
	}
	created, err := topologyReadTimestamp(props, "created_at")
	if err != nil {
		return nil, err
	}
	synced, err := topologyReadTimestamp(props, "synced_at")
	if err != nil {
		return nil, err
	}
	label := stable
	for _, key := range []string{"name", "full_name", "port_name", "parts_number", "code"} {
		if text, ok := props[key].(string); ok && text != "" {
			label = text
			break
		}
	}
	return &TopologyNode{ID: vid, SourceID: source, EntityType: string(entityType), StableID: stable, Label: label, Properties: props, CreatedAt: created, SyncedAt: synced}, nil
}

func topologyReadTimestamp(props map[string]any, key string) (*time.Time, error) {
	value := props[key]
	if value == nil {
		return nil, nil
	}
	stamp, ok := value.(time.Time)
	if !ok {
		return nil, ErrInventoryNotReady
	}
	return &stamp, nil
}

func topologyReadColumns(result *nebulago.ResultSet, expected ...string) bool {
	columns := result.GetColNames()
	if len(columns) != len(expected) || result.GetColSize() != len(expected) {
		return false
	}
	for i, name := range expected {
		if columns[i] != name {
			return false
		}
	}
	return true
}

func topologyReadRow(result *nebulago.ResultSet, index int) ([]nebulago.ValueWrapper, error) {
	row, err := result.GetRowValuesByIndex(index)
	if err != nil {
		return nil, ErrInventoryNotReady
	}
	values := make([]nebulago.ValueWrapper, result.GetColSize())
	for i := range values {
		value, err := row.GetValueByIndex(i)
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		values[i] = *value
	}
	return values, nil
}
