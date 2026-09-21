package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

const (
	topologyTimeFormat       = "2006-01-02T15:04:05.000000+00:00"
	topologyCleanupBatchSize = 200
)

var (
	topologyVIDPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	topologySourceLocks sync.Map
)

func lockTopologySourceWrite(sourceID string) func() {
	value, _ := topologySourceLocks.LoadOrStore(sourceID, &sync.RWMutex{})
	mutex := value.(*sync.RWMutex)
	mutex.RLock()
	return mutex.RUnlock
}

func lockTopologySourceCleanup(sourceID string) func() {
	value, _ := topologySourceLocks.LoadOrStore(sourceID, &sync.RWMutex{})
	mutex := value.(*sync.RWMutex)
	mutex.Lock()
	return mutex.Unlock
}

type TopologyVertex struct {
	Identity   model.EntityIdentity
	Properties map[string]any
	CreatedAt  time.Time
	SyncedAt   time.Time
}

type TopologyGraphRepository interface {
	UpsertVertex(context.Context, TopologyVertex) error
	UpsertRelation(context.Context, model.Relation) error
	DeleteRelationsNotSeen(context.Context, string, model.EdgeType, time.Time) error
	DeleteOrphanVerticesNotSeen(context.Context, string, model.EntityType, time.Time) error
}

type topologyGraphRepository struct {
	client graphclient.Client
}

type unavailableTopologyGraphRepository struct{}

func NewTopologyGraphRepository(conf *viper.Viper) (TopologyGraphRepository, func(), error) {
	return newTopologyGraphRepository(conf, graphclient.New)
}

func newTopologyGraphRepository(conf *viper.Viper, open func(graphclient.Config) (graphclient.Client, error)) (TopologyGraphRepository, func(), error) {
	config, err := graphclient.ParseConfig(conf)
	if err != nil {
		return nil, func() {}, err
	}
	if len(config.Hosts) == 0 || config.Space == "" {
		return unavailableTopologyGraphRepository{}, func() {}, nil
	}
	client, err := open(config)
	if err != nil {
		return nil, func() {}, err
	}
	return &topologyGraphRepository{client: client}, client.Close, nil
}

func (unavailableTopologyGraphRepository) UpsertVertex(context.Context, TopologyVertex) error {
	return ErrInventoryNotReady
}

func (unavailableTopologyGraphRepository) UpsertRelation(context.Context, model.Relation) error {
	return ErrInventoryNotReady
}

func (unavailableTopologyGraphRepository) DeleteRelationsNotSeen(context.Context, string, model.EdgeType, time.Time) error {
	return ErrInventoryNotReady
}

func (unavailableTopologyGraphRepository) DeleteOrphanVerticesNotSeen(context.Context, string, model.EntityType, time.Time) error {
	return ErrInventoryNotReady
}

type topologyPropertyType uint8

const (
	topologyString topologyPropertyType = iota + 1
	topologyInteger
	topologyDouble
	topologyBoolean
	topologyDateTime
	topologyStringList
	topologyIntegerList
)

type topologyEntitySchema struct {
	stableField string
	properties  map[string]topologyPropertyType
}

var topologyEntitySchemas = map[model.EntityType]topologyEntitySchema{
	model.EntityDevice: {
		stableField: "device_sn",
		properties: map[string]topologyPropertyType{
			"name": topologyString, "parent_type_id": topologyInteger, "device_type_id": topologyInteger,
			"role": topologyString, "all_ips": topologyStringList,
		},
	},
	model.EntityInterface: {
		stableField: "port_uuid",
		properties: map[string]topologyPropertyType{
			"port_name": topologyString, "port_type": topologyString, "port_speed": topologyString,
			"port_operation_status": topologyString, "group_uuid": topologyString, "port_group_uuid": topologyString,
			"group_name": topologyString, "group_type": topologyInteger, "port_group_type": topologyInteger,
			"group_subtype": topologyInteger,
		},
	},
	model.EntityGPU: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"parts_sn": topologyString, "parts_number": topologyString, "manufacturer": topologyString,
			"model": topologyString, "specification": topologyString, "card_index": topologyString,
			"slot": topologyString, "bmc_slot": topologyString, "bus_address": topologyString,
			"connector": topologyString, "conn_ports": topologyStringList, "vram": topologyString,
			"tdp": topologyString, "computer_perf": topologyString, "gpu_fp": topologyString,
			"gpu_fp64": topologyString, "fp16_tensor_sparsity": topologyString, "fp4": topologyString,
			"int4": topologyString, "int8": topologyString, "tf16": topologyString, "tf32": topologyString,
			"driver_ver": topologyString, "firmware_ver": topologyString, "npu_versions": topologyString,
			"is_domestic": topologyString, "maintenance_status": topologyString, "maintenance_end": topologyDateTime,
		},
	},
	model.EntityPod: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "basic_code": topologyString, "name": topologyString,
			"full_name": topologyString, "plane": topologyInteger, "mode": topologyString,
			"rdma": topologyInteger, "phy_building_id": topologyInteger, "idc_logic_id": topologyInteger,
			"is_delete": topologyInteger, "is_sync": topologyInteger, "source_created_at": topologyDateTime,
			"source_updated_at": topologyDateTime,
		},
	},
	model.EntityCabinet: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "code": topologyString, "dc_colo_rack": topologyString,
			"isp_rack_code": topologyString, "rack": topologyString, "rack_column_code": topologyString,
			"rack_row": topologyString, "idc_id": topologyInteger, "phy_building_id": topologyInteger,
			"phy_room_id": topologyInteger, "idc_module_id": topologyInteger,
			"idc_building_structure_id": topologyInteger, "idc_rows_code": topologyInteger,
			"pod_ids": topologyIntegerList, "row_switch_id_a": topologyInteger, "row_switch_id_b": topologyInteger,
			"source_created_at": topologyDateTime, "source_updated_at": topologyDateTime,
		},
	},
	model.EntityDataCenter: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "code": topologyString, "cn_name": topologyString,
			"address": topologyString, "location": topologyString, "source_created_at": topologyDateTime,
			"source_updated_at": topologyDateTime,
		},
	},
	model.EntityRPP: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "code": topologyString, "idc_id": topologyInteger,
			"ups_group_id": topologyInteger, "transformer_group_a_id": topologyInteger,
			"source_created_at": topologyDateTime, "source_updated_at": topologyDateTime,
		},
	},
	model.EntityUPSGroup: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "code": topologyString, "idc_id": topologyInteger,
			"transformer_id_up": topologyInteger, "source_created_at": topologyDateTime,
			"source_updated_at": topologyDateTime,
		},
	},
	model.EntityUPS: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "code": topologyString, "idc_id": topologyInteger,
			"ups_group_id": topologyInteger, "transformer_id_up": topologyInteger, "brand": topologyString,
			"model": topologyString, "rated_capacity": topologyInteger, "source_created_at": topologyDateTime,
			"source_updated_at": topologyDateTime,
		},
	},
	model.EntityTransformer: {
		stableField: "uuid",
		properties: map[string]topologyPropertyType{
			"inst_id": topologyInteger, "code": topologyString, "idc_id": topologyInteger,
			"standby_transformer_id": topologyInteger, "brand": topologyString, "model": topologyString,
			"rated_capacity": topologyInteger, "source_created_at": topologyDateTime,
			"source_updated_at": topologyDateTime,
		},
	},
}

var topologyEdgeSchemas = map[model.EdgeType]map[string]topologyPropertyType{
	model.EdgeSpatial:     {"plane": topologyInteger},
	model.EdgeComposition: {},
	model.EdgeNetwork: {
		"gpu_port": topologyString, "gpu_port_speed": topologyString, "gpu_ip": topologyString,
		"gpu_slot": topologyString, "server_port_speed": topologyString, "bond_name": topologyString,
		"tor_port": topologyString, "tor_port_speed": topologyString, "tor_role": topologyString,
		"source": topologyString,
	},
	model.EdgePower: {"power_path": topologyString},
}

func (r *topologyGraphRepository) UpsertVertex(ctx context.Context, vertex TopologyVertex) error {
	schema, ok := topologyEntitySchemas[vertex.Identity.EntityType]
	if !ok {
		return errors.New("unsupported topology entity type")
	}
	vid, err := vertex.Identity.VID()
	if err != nil {
		return fmt.Errorf("invalid topology vertex identity: %w", err)
	}
	if vertex.CreatedAt.IsZero() || vertex.SyncedAt.IsZero() {
		return errors.New("topology vertex timestamps are required")
	}
	properties, names, err := normalizeTopologyProperties(vertex.Properties, schema.properties)
	if err != nil {
		return err
	}
	unlockSource := lockTopologySourceWrite(vertex.Identity.SourceID)
	defer unlockSource()

	params := map[string]interface{}{
		"source_id":  vertex.Identity.SourceID,
		"stable_id":  vertex.Identity.StableID,
		"created_at": topologyTime(vertex.CreatedAt),
		"synced_at":  topologyTime(vertex.SyncedAt),
	}
	for name, value := range properties {
		params[name] = value
	}

	tag := string(vertex.Identity.EntityType)
	fields := []string{"source_id", schema.stableField}
	values := []string{"$source_id", "$stable_id"}
	for _, name := range names {
		fields = append(fields, topologyPropertyIdentifier(name))
		values = append(values, topologyValueExpression(name, schema.properties[name], properties[name]))
	}
	fields = append(fields, "created_at", "synced_at")
	values = append(values, "datetime($created_at)", "datetime($synced_at)")
	vidLiteral := topologyVIDLiteral(vid)
	insert := "INSERT VERTEX IF NOT EXISTS " + tag + " (" + strings.Join(fields, ", ") + ") VALUES " + vidLiteral + ":(" + strings.Join(values, ", ") + ");"
	if _, err := r.executeTopology(ctx, insert, params); err != nil {
		return err
	}
	identityQuery := "FETCH PROP ON " + tag + " " + vidLiteral + " YIELD properties(vertex).source_id AS source_id, properties(vertex)." + schema.stableField + " AS stable_id;"
	identityResult, err := r.executeTopology(ctx, identityQuery, map[string]interface{}{})
	if err != nil {
		return err
	}
	identityValues, err := topologySingleStringRow(identityResult, "source_id", "stable_id")
	if err != nil {
		return err
	}
	if identityValues[0] != vertex.Identity.SourceID || identityValues[1] != vertex.Identity.StableID {
		return errors.New("topology vertex identity collision")
	}

	assignments := make([]string, 0, len(names)+1)
	for _, name := range names {
		assignments = append(assignments, topologyPropertyIdentifier(name)+" = "+topologyValueExpression(name, schema.properties[name], properties[name]))
	}
	assignments = append(assignments, "synced_at = datetime($synced_at)")
	update := "UPDATE VERTEX ON " + tag + " " + vidLiteral + " SET " + strings.Join(assignments, ", ") + " WHEN synced_at <= datetime($synced_at);"
	_, err = r.executeTopology(ctx, update, withoutTopologyParameter(params, "created_at", "source_id", "stable_id"))
	return err
}

func (r *topologyGraphRepository) UpsertRelation(ctx context.Context, relation model.Relation) error {
	normalized, err := model.NormalizeRelation(relation)
	if err != nil {
		return fmt.Errorf("invalid topology relation: %w", err)
	}
	schema, ok := topologyEdgeSchemas[normalized.Identity.EdgeType]
	if !ok {
		return errors.New("unsupported topology edge type")
	}
	properties, names, err := normalizeTopologyProperties(normalized.Properties, schema)
	if err != nil {
		return err
	}
	relationID, err := normalized.Identity.ID()
	if err != nil {
		return err
	}
	rank, err := normalized.Identity.Rank()
	if err != nil {
		return err
	}
	fromVID, err := normalized.Identity.From.VID()
	if err != nil {
		return err
	}
	toVID, err := normalized.Identity.To.VID()
	if err != nil {
		return err
	}
	unlockSource := lockTopologySourceWrite(normalized.Identity.SourceID)
	defer unlockSource()

	params := map[string]interface{}{
		"relation_id":   relationID,
		"relation_kind": string(normalized.Identity.Kind),
		"source_id":     normalized.Identity.SourceID,
		"created_at":    topologyTime(normalized.CreatedAt),
		"synced_at":     topologyTime(normalized.SyncedAt),
	}
	for name, value := range properties {
		params[name] = value
	}

	edge := string(normalized.Identity.EdgeType)
	fields := []string{"relation_id", "relation_kind", "source_id"}
	values := []string{"$relation_id", "$relation_kind", "$source_id"}
	for _, name := range names {
		fields = append(fields, topologyPropertyIdentifier(name))
		values = append(values, topologyValueExpression(name, schema[name], properties[name]))
	}
	fields = append(fields, "created_at", "synced_at")
	values = append(values, "datetime($created_at)", "datetime($synced_at)")
	edgeLiteral := topologyVIDLiteral(fromVID) + " -> " + topologyVIDLiteral(toVID) + " @ " + strconv.FormatInt(rank, 10)
	insert := "INSERT EDGE IF NOT EXISTS " + edge + " (" + strings.Join(fields, ", ") + ") VALUES " + edgeLiteral + ":(" + strings.Join(values, ", ") + ");"
	if _, err := r.executeTopology(ctx, insert, params); err != nil {
		return err
	}
	identityQuery := "FETCH PROP ON " + edge + " " + edgeLiteral + " YIELD properties(edge).relation_id AS relation_id, properties(edge).relation_kind AS relation_kind, properties(edge).source_id AS source_id;"
	identityResult, err := r.executeTopology(ctx, identityQuery, map[string]interface{}{})
	if err != nil {
		return err
	}
	identityValues, err := topologySingleStringRow(identityResult, "relation_id", "relation_kind", "source_id")
	if err != nil {
		return err
	}
	if identityValues[0] != relationID || identityValues[1] != string(normalized.Identity.Kind) || identityValues[2] != normalized.Identity.SourceID {
		return errors.New("topology relation rank collision")
	}

	assignments := make([]string, 0, len(names)+1)
	for _, name := range names {
		assignments = append(assignments, topologyPropertyIdentifier(name)+" = "+topologyValueExpression(name, schema[name], properties[name]))
	}
	assignments = append(assignments, "synced_at = datetime($synced_at)")
	update := "UPDATE EDGE ON " + edge + " " + edgeLiteral + " SET " + strings.Join(assignments, ", ") + " WHEN synced_at <= datetime($synced_at);"
	_, err = r.executeTopology(ctx, update, withoutTopologyParameter(params, "created_at", "relation_id", "relation_kind", "source_id"))
	return err
}

func (r *topologyGraphRepository) DeleteRelationsNotSeen(ctx context.Context, sourceID string, edgeType model.EdgeType, runStartedAt time.Time) error {
	if sourceID == "" || !edgeType.Valid() || runStartedAt.IsZero() {
		return errors.New("invalid topology relation cleanup scope")
	}
	unlockSource := lockTopologySourceCleanup(sourceID)
	defer unlockSource()
	edge := string(edgeType)
	query := "LOOKUP ON " + edge + " WHERE " + edge + ".source_id == $source_id AND " + edge + ".synced_at < datetime($run_started_at) " +
		"YIELD src(edge) AS source_vid, dst(edge) AS target_vid, rank(edge) AS edge_rank | " +
		"DELETE EDGE " + edge + " $-.source_vid -> $-.target_vid @ $-.edge_rank;"
	_, err := r.executeTopology(ctx, query, map[string]interface{}{
		"source_id":      sourceID,
		"run_started_at": topologyTime(runStartedAt),
	})
	return err
}

func (r *topologyGraphRepository) DeleteOrphanVerticesNotSeen(ctx context.Context, sourceID string, entityType model.EntityType, runStartedAt time.Time) error {
	if sourceID == "" || !entityType.Valid() || runStartedAt.IsZero() {
		return errors.New("invalid topology vertex cleanup scope")
	}
	unlockSource := lockTopologySourceCleanup(sourceID)
	defer unlockSource()
	tag := string(entityType)
	query := fmt.Sprintf("MATCH (v:%s) WHERE v.%s.source_id == $source_id AND v.%s.synced_at < datetime($run_started_at) "+
		"AND NOT (v)-[]-() RETURN id(v) AS stale_vid LIMIT %d;", tag, tag, tag, topologyCleanupBatchSize)
	lookupParams := map[string]interface{}{
		"source_id":      sourceID,
		"run_started_at": topologyTime(runStartedAt),
	}
	for {
		result, err := r.executeTopology(ctx, query, lookupParams)
		if err != nil {
			return err
		}
		vids, err := topologyResultVIDs(result)
		if err != nil {
			return err
		}
		if len(vids) == 0 {
			return nil
		}
		vidLiterals := make([]string, len(vids))
		for index, vid := range vids {
			vidLiterals[index] = `"` + vid + `"`
		}
		if _, err := r.executeTopology(ctx, "DELETE VERTEX "+strings.Join(vidLiterals, ", ")+";", map[string]interface{}{}); err != nil {
			return err
		}
	}
}

func (r *topologyGraphRepository) executeTopology(ctx context.Context, statement string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := r.client.ExecuteParameter(ctx, statement, params)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || result == nil {
		return nil, fmt.Errorf("%w: topology graph query failed", ErrInventoryNotReady)
	}
	if !result.IsSucceed() {
		return nil, fmt.Errorf("%w: topology graph query rejected", ErrInventoryNotReady)
	}
	return result, nil
}

func normalizeTopologyProperties(properties map[string]any, schema map[string]topologyPropertyType) (map[string]any, []string, error) {
	normalized := make(map[string]any, len(properties))
	names := make([]string, 0, len(properties))
	for name, value := range properties {
		propertyType, ok := schema[name]
		if !ok {
			return nil, nil, fmt.Errorf("unsupported topology property %q", name)
		}
		normalizedValue, err := normalizeTopologyValue(propertyType, value)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid topology property %q: %w", name, err)
		}
		normalized[name] = normalizedValue
	}
	for name := range schema {
		if _, exists := normalized[name]; !exists {
			normalized[name] = nil
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return normalized, names, nil
}

func normalizeTopologyValue(propertyType topologyPropertyType, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch propertyType {
	case topologyString:
		text, ok := value.(string)
		if !ok {
			return nil, errors.New("value must be a string")
		}
		return text, nil
	case topologyInteger:
		return topologyIntegerValue(value)
	case topologyDouble:
		switch number := value.(type) {
		case float64:
			return number, nil
		case float32:
			return float64(number), nil
		case int:
			return float64(number), nil
		case int64:
			return float64(number), nil
		default:
			return nil, errors.New("value must be numeric")
		}
	case topologyBoolean:
		boolean, ok := value.(bool)
		if !ok {
			return nil, errors.New("value must be boolean")
		}
		return boolean, nil
	case topologyDateTime:
		valueTime, ok := value.(time.Time)
		if !ok || valueTime.IsZero() {
			return nil, errors.New("value must be a non-zero time")
		}
		return topologyTime(valueTime), nil
	case topologyStringList:
		return topologyStringListValue(value)
	case topologyIntegerList:
		return topologyIntegerListValue(value)
	default:
		return nil, errors.New("unsupported property type")
	}
}

func topologyStringListValue(value any) (string, error) {
	items, ok := value.([]string)
	if !ok {
		return "", errors.New("value must be a string list")
	}
	if items == nil {
		items = []string{}
	}
	seen := make(map[string]struct{}, len(items))
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			return "", errors.New("string list items must not be empty")
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", errors.New("string list encoding failed")
	}
	return string(encoded), nil
}

func topologyIntegerListValue(value any) (string, error) {
	items, ok := value.([]int64)
	if !ok {
		return "", errors.New("value must be a 64-bit integer list")
	}
	if items == nil {
		items = []int64{}
	}
	seen := make(map[int64]struct{}, len(items))
	normalized := make([]int64, 0, len(items))
	for _, item := range items {
		if item <= 0 {
			return "", errors.New("integer list items must be positive")
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", errors.New("integer list encoding failed")
	}
	return string(encoded), nil
}

func topologyIntegerValue(value any) (int64, error) {
	switch number := value.(type) {
	case int:
		return int64(number), nil
	case int8:
		return int64(number), nil
	case int16:
		return int64(number), nil
	case int32:
		return int64(number), nil
	case int64:
		return number, nil
	case uint:
		if uint64(number) <= math.MaxInt64 {
			return int64(number), nil
		}
	case uint8:
		return int64(number), nil
	case uint16:
		return int64(number), nil
	case uint32:
		return int64(number), nil
	case uint64:
		if number <= math.MaxInt64 {
			return int64(number), nil
		}
	case json.Number:
		parsed, err := number.Int64()
		if err == nil {
			return parsed, nil
		}
	}
	return 0, errors.New("value must be a 64-bit integer")
}

func topologyVIDLiteral(vid string) string {
	return `"` + vid + `"`
}

func topologyPropertyIdentifier(name string) string {
	return "`" + name + "`"
}

func topologyValueExpression(name string, propertyType topologyPropertyType, value any) string {
	if propertyType == topologyDateTime && value != nil {
		return "datetime($" + name + ")"
	}
	return "$" + name
}

func topologyTime(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format(topologyTimeFormat)
}

func withoutTopologyParameter(params map[string]interface{}, excluded ...string) map[string]interface{} {
	result := make(map[string]interface{}, len(params))
	exclusion := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		exclusion[name] = struct{}{}
	}
	for name, value := range params {
		if _, skip := exclusion[name]; !skip {
			result[name] = value
		}
	}
	return result
}

func topologyResultVIDs(result *nebulago.ResultSet) ([]string, error) {
	if result == nil || len(result.GetColNames()) != 1 || result.GetColNames()[0] != "stale_vid" {
		return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
	}
	vids := make([]string, 0, result.GetRowSize())
	for _, row := range result.GetRows() {
		if row == nil || len(row.Values) != 1 || row.Values[0] == nil || row.Values[0].SVal == nil {
			return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
		}
		vid := string(row.Values[0].SVal)
		if !topologyVIDPattern.MatchString(vid) {
			return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
		}
		vids = append(vids, vid)
	}
	return vids, nil
}

func topologySingleStringRow(result *nebulago.ResultSet, columns ...string) ([]string, error) {
	if result == nil || len(result.GetColNames()) != len(columns) || result.GetRowSize() != 1 {
		return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
	}
	for index, column := range result.GetColNames() {
		if column != columns[index] {
			return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
		}
	}
	row := result.GetRows()[0]
	if row == nil || len(row.Values) != len(columns) {
		return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
	}
	values := make([]string, len(columns))
	for index, value := range row.Values {
		if value == nil || value.SVal == nil {
			return nil, fmt.Errorf("%w: malformed topology graph result", ErrInventoryNotReady)
		}
		values[index] = string(value.SVal)
	}
	return values, nil
}
