package repository

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

type graphInventoryRepository struct{ client graphclient.Client }
type unavailableGraphInventoryRepository struct{}

func NewGraphInventoryRepository(conf *viper.Viper) (GraphInventoryRepository, func(), error) {
	return newGraphInventoryRepository(conf, graphclient.New)
}

func newGraphInventoryRepository(conf *viper.Viper, open func(graphclient.Config) (graphclient.Client, error)) (GraphInventoryRepository, func(), error) {
	c, err := graphclient.ParseConfig(conf)
	if err != nil {
		return nil, func() {}, err
	}
	if len(c.Hosts) == 0 || c.Space == "" {
		return unavailableGraphInventoryRepository{}, func() {}, nil
	}
	client, err := open(c)
	if err != nil {
		return nil, func() {}, err
	}
	return &graphInventoryRepository{client: client}, client.Close, nil
}

func (unavailableGraphInventoryRepository) Device(context.Context, string, string) (*model.Device, error) {
	return nil, ErrInventoryNotReady
}
func (unavailableGraphInventoryRepository) List(context.Context, string, InventoryListQuery) (*InventoryListResult, error) {
	return nil, ErrInventoryNotReady
}

var graphEntityID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var graphScopeID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type graphResource struct {
	tag, kind string
	fields    []string
}

func graphResourceFor(resource string) (graphResource, bool) {
	switch resource {
	case "devices":
		return graphResource{"device", "d", []string{"name", "device_kind", "role", "lifecycle", "resolution_status", "serial_number"}}, true
	case "interfaces":
		return graphResource{"interface", "i", []string{"device_id", "namespace", "source_name", "normalized_name", "interface_kind", "admin_state", "oper_state", "lifecycle", "resolution_status", "speed_bps"}}, true
	case "addresses":
		return graphResource{"address", "a", []string{"device_id", "interface_id", "address_family", "address", "prefix_length", "address_scope_key", "scope_status", "purpose", "lifecycle", "resolution_status"}}, true
	default:
		return graphResource{}, false
	}
}

func (g graphResource) columns() []string {
	return append([]string{"vid", "scope_id", "entity_id"}, g.fields...)
}
func (g graphResource) yield(fetch bool) string {
	fields := []string{"id(vertex) AS vid"}
	prefix := "`" + g.tag + "`"
	if fetch {
		prefix = "properties(vertex)"
	}
	for _, field := range g.columns()[1:] {
		fields = append(fields, prefix+"."+field+" AS "+field)
	}
	return " YIELD " + strings.Join(fields, ", ")
}
func graphVID(scope, kind, id string) string { return scope + ":" + kind + ":" + id }

func (r *graphInventoryRepository) execute(ctx context.Context, query string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := r.client.ExecuteParameter(ctx, query, params)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || result == nil {
		return nil, fmt.Errorf("%w: graph query failed", ErrInventoryNotReady)
	}
	if !result.IsSucceed() {
		return nil, fmt.Errorf("%w: graph query rejected", ErrInventoryNotReady)
	}
	return result, nil
}

func (r *graphInventoryRepository) Device(ctx context.Context, scopeID, id string) (*model.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !graphScopeID.MatchString(scopeID) || !graphEntityID.MatchString(id) {
		return nil, fmt.Errorf("invalid graph inventory identity")
	}
	g, _ := graphResourceFor("devices")
	result, err := r.execute(ctx, "FETCH PROP ON `device` $vid"+g.yield(true)+";", map[string]interface{}{"vid": graphVID(scopeID, g.kind, id)})
	if err != nil {
		return nil, err
	}
	rows, err := graphRows(result, g)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrInventoryNotFound
	}
	if len(rows) != 1 {
		return nil, graphMalformed()
	}
	if err := rows[0].identity(scopeID, g.kind, id); err != nil {
		return nil, err
	}
	device, err := rows[0].device()
	if err != nil {
		return nil, err
	}
	return &device, nil
}

func (r *graphInventoryRepository) List(ctx context.Context, resource string, q InventoryListQuery) (*InventoryListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, ok := graphResourceFor(resource)
	if !ok || q.Limit < 1 || q.Limit > 200 || !graphScopeID.MatchString(q.ScopeID) || (q.LastID != "" && !graphEntityID.MatchString(q.LastID)) || (q.ParentID != "" && (!graphEntityID.MatchString(q.ParentID) || resource == "devices")) {
		return nil, fmt.Errorf("invalid graph inventory list query")
	}
	params := map[string]interface{}{"scope_id": q.ScopeID}
	where := []string{"`" + g.tag + "`.scope_id == $scope_id"}
	filters := map[string]interface{}{}
	if q.ParentID != "" {
		filters["device_id"] = q.ParentID
	}
	if q.Lifecycle != "" {
		filters["lifecycle"] = q.Lifecycle
	}
	if resource == "devices" {
		if q.DeviceKind != "" {
			filters["device_kind"] = q.DeviceKind
		}
		if q.Name != "" {
			filters["name"] = q.Name
		}
	}
	if resource == "interfaces" && q.InterfaceKind != "" {
		filters["interface_kind"] = q.InterfaceKind
	}
	if resource == "addresses" && q.AddressFamily != "" {
		family, err := strconv.Atoi(q.AddressFamily)
		if err != nil || (family != 4 && family != 6) {
			return nil, fmt.Errorf("invalid graph address family")
		}
		filters["address_family"] = int64(family)
	}
	// 固定字段顺序保证查询稳定；所有外部值只通过参数传入。
	for _, key := range []string{"device_id", "lifecycle", "device_kind", "name", "interface_kind", "address_family"} {
		if value, ok := filters[key]; ok {
			where = append(where, "`"+g.tag+"`."+key+" == $"+key)
			params[key] = value
		}
	}
	if q.LastID != "" {
		where = append(where, "`"+g.tag+"`.entity_id > $last_id")
		params["last_id"] = q.LastID
	}
	// 原生 LIMIT 接受整数计数；这里只输出校验后的 2..201，不拼接外部字符串。
	query := "LOOKUP ON `" + g.tag + "` WHERE " + strings.Join(where, " AND ") + g.yield(false) + " | ORDER BY $-.entity_id ASC | LIMIT " + strconv.Itoa(q.Limit+1) + ";"
	result, err := r.execute(ctx, query, params)
	if err != nil {
		return nil, err
	}
	rows, err := graphRows(result, g)
	if err != nil {
		return nil, err
	}
	if len(rows) > q.Limit+1 {
		return nil, graphMalformed()
	}
	devices := make([]model.Device, 0, len(rows))
	interfaces := make([]model.Interface, 0, len(rows))
	addresses := make([]model.Address, 0, len(rows))
	lastID := q.LastID
	for _, row := range rows {
		id, err := row.text("entity_id")
		if err != nil || id <= lastID {
			return nil, graphMalformed()
		}
		if err := row.identity(q.ScopeID, g.kind, id); err != nil {
			return nil, err
		}
		for key, value := range filters {
			switch expected := value.(type) {
			case string:
				actual, err := row.text(key)
				if err != nil || actual != expected {
					return nil, graphMalformed()
				}
			case int64:
				actual, err := row.integer(key)
				if err != nil || actual != expected {
					return nil, graphMalformed()
				}
			}
		}
		switch resource {
		case "devices":
			v, err := row.device()
			if err != nil {
				return nil, err
			}
			devices = append(devices, v)
		case "interfaces":
			v, err := row.iface()
			if err != nil {
				return nil, err
			}
			interfaces = append(interfaces, v)
		case "addresses":
			v, err := row.address()
			if err != nil {
				return nil, err
			}
			addresses = append(addresses, v)
		}
		lastID = id
	}
	page := &InventoryListResult{HasMore: len(rows) > q.Limit}
	count := len(rows)
	if page.HasMore {
		count = q.Limit
	}
	if count != 0 {
		page.LastID, _ = rows[count-1].text("entity_id")
	}
	switch resource {
	case "devices":
		page.Items = devices[:count]
	case "interfaces":
		page.Items = interfaces[:count]
	case "addresses":
		page.Items = addresses[:count]
	}
	return page, nil
}

type graphRow map[string]*nebula.Value

func graphMalformed() error {
	return fmt.Errorf("%w: malformed graph inventory result", ErrInventoryNotReady)
}

func graphRows(result *nebulago.ResultSet, g graphResource) ([]graphRow, error) {
	columns := result.GetColNames()
	expected := g.columns()
	if len(columns) != len(expected) {
		return nil, graphMalformed()
	}
	seen := map[string]bool{}
	for _, column := range columns {
		if seen[column] {
			return nil, graphMalformed()
		}
		seen[column] = true
	}
	for _, column := range expected {
		if !seen[column] {
			return nil, graphMalformed()
		}
	}
	rows := make([]graphRow, 0, result.GetRowSize())
	for _, raw := range result.GetRows() {
		if raw == nil || len(raw.Values) != len(columns) {
			return nil, graphMalformed()
		}
		row := graphRow{}
		for i, value := range raw.Values {
			if value == nil || value.CountSetFieldsValue() != 1 {
				return nil, graphMalformed()
			}
			row[columns[i]] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
