package repository

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

const graphTestScope = "11111111111111111111111111111111"
const graphTestParent = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeGraphClient struct {
	result        *nebulago.ResultSet
	err           error
	statement     string
	params        map[string]interface{}
	calls, closes int
	after         func()
}

func (f *fakeGraphClient) ExecuteParameter(_ context.Context, statement string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	f.calls++
	f.statement = statement
	f.params = params
	if f.after != nil {
		f.after()
	}
	return f.result, f.err
}
func (f *fakeGraphClient) Close() { f.closes++ }

func graphValue(v interface{}) *nebula.Value {
	switch v := v.(type) {
	case string:
		return &nebula.Value{SVal: []byte(v)}
	case int64:
		return &nebula.Value{IVal: &v}
	case bool:
		return &nebula.Value{BVal: &v}
	case nil:
		n := nebula.NullType___NULL__
		return &nebula.Value{NVal: &n}
	default:
		panic("unsupported fixture")
	}
}

func graphFixture(t *testing.T, resource string, entries ...map[string]interface{}) *nebulago.ResultSet {
	t.Helper()
	g, _ := graphResourceFor(resource)
	ds := &nebula.DataSet{}
	for _, column := range g.columns() {
		ds.ColumnNames = append(ds.ColumnNames, []byte(column))
	}
	for _, entry := range entries {
		row := &nebula.Row{}
		for _, column := range g.columns() {
			row.Values = append(row.Values, graphValue(entry[column]))
		}
		ds.Rows = append(ds.Rows, row)
	}
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: ds})
	require.NoError(t, err)
	return result
}

func graphEntry(resource string, n int) map[string]interface{} {
	g, _ := graphResourceFor(resource)
	id := fmt.Sprintf("%032x", n)
	entry := map[string]interface{}{"scope_id": graphTestScope, "entity_id": id, "vid": graphVID(graphTestScope, g.kind, id)}
	for _, field := range g.fields {
		entry[field] = "value"
	}
	entry["lifecycle"] = "active"
	switch resource {
	case "devices":
		entry["serial_number"] = nil
	case "interfaces":
		entry["device_id"] = graphTestParent
		entry["speed_bps"] = nil
	case "addresses":
		entry["device_id"] = graphTestParent
		entry["interface_id"] = nil
		entry["address_family"] = int64(4)
		entry["address"] = "192.0.2.1"
		entry["prefix_length"] = nil
	}
	return entry
}

func TestGraphDeviceDetailAndMissing(t *testing.T) {
	entry := graphEntry("devices", 1)
	entry["serial_number"] = "SN-123"
	fake := &fakeGraphClient{result: graphFixture(t, "devices", entry)}
	r := &graphInventoryRepository{client: fake}
	device, err := r.Device(context.Background(), graphTestScope, entry["entity_id"].(string))
	require.NoError(t, err)
	require.Equal(t, "SN-123", *device.SerialNumber)
	require.Equal(t, "value", device.Name)
	require.Equal(t, "value", device.DeviceKind)
	require.Equal(t, "value", device.Role)
	require.Equal(t, "active", device.Lifecycle)
	require.Equal(t, "value", device.ResolutionStatus)
	require.Empty(t, device.GenerationID)
	require.Contains(t, fake.statement, "FETCH PROP ON `device` $vid")
	require.Contains(t, fake.statement, "properties(vertex).name AS name")
	require.NotContains(t, fake.statement, "`device`.name")
	require.NotContains(t, fake.statement, graphTestScope)
	require.Equal(t, entry["vid"], fake.params["vid"])
	fake.result = graphFixture(t, "devices")
	device, err = r.Device(context.Background(), graphTestScope, entry["entity_id"].(string))
	require.ErrorIs(t, err, ErrInventoryNotFound)
	require.Nil(t, device)
}

func TestGraphListParametersAndPagination(t *testing.T) {
	injection := `x"; DROP SPACE production; --`
	entries := []map[string]interface{}{graphEntry("devices", 2), graphEntry("devices", 3), graphEntry("devices", 4)}
	for _, entry := range entries {
		entry["name"] = injection
		entry["device_kind"] = "router"
	}
	fake := &fakeGraphClient{result: graphFixture(t, "devices", entries...)}
	r := &graphInventoryRepository{client: fake}
	page, err := r.List(context.Background(), "devices", InventoryListQuery{ScopeID: graphTestScope, Limit: 2, LastID: fmt.Sprintf("%032x", 1), Name: injection, DeviceKind: "router", Lifecycle: "active", GenerationID: "ignored-generation"})
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Equal(t, fmt.Sprintf("%032x", 3), page.LastID)
	require.Len(t, page.Items.([]model.Device), 2)
	require.Empty(t, page.Items.([]model.Device)[0].GenerationID)
	require.NotContains(t, fake.statement, injection)
	require.NotContains(t, fake.statement, "generation")
	require.Contains(t, fake.statement, "`device`.scope_id == $scope_id")
	require.Contains(t, fake.statement, "`device`.entity_id > $last_id")
	require.Contains(t, fake.statement, "ORDER BY $-.entity_id ASC | LIMIT 3")
	require.Contains(t, fake.statement, "`device`.name AS name")
	require.Equal(t, injection, fake.params["name"])
	require.Equal(t, "router", fake.params["device_kind"])
	require.Equal(t, graphTestScope, fake.params["scope_id"])
}

func TestGraphChildLists(t *testing.T) {
	for _, resource := range []string{"interfaces", "addresses"} {
		t.Run(resource, func(t *testing.T) {
			entry := graphEntry(resource, 1)
			q := InventoryListQuery{ScopeID: graphTestScope, ParentID: graphTestParent, Limit: 200, Lifecycle: "active"}
			if resource == "interfaces" {
				q.InterfaceKind = "value"
			} else {
				q.AddressFamily = "4"
			}
			fake := &fakeGraphClient{result: graphFixture(t, resource, entry)}
			page, err := (&graphInventoryRepository{client: fake}).List(context.Background(), resource, q)
			require.NoError(t, err)
			require.False(t, page.HasMore)
			require.Contains(t, fake.statement, ".device_id == $device_id")
			require.Contains(t, fake.statement, "LIMIT 201")
			require.Equal(t, graphTestParent, fake.params["device_id"])
			if resource == "addresses" {
				v := page.Items.([]model.Address)[0]
				require.Len(t, v.Address, 16)
				require.Equal(t, []byte(net.ParseIP("192.0.2.1").To16()), v.Address)
				require.Nil(t, v.InterfaceID)
				require.Nil(t, v.PrefixLength)
				require.Empty(t, v.GenerationID)
				require.Equal(t, int64(4), fake.params["address_family"])
			} else {
				v := page.Items.([]model.Interface)[0]
				require.Nil(t, v.SpeedBPS)
				require.Empty(t, v.GenerationID)
			}
		})
	}
}

func TestGraphRejectsInvalidRows(t *testing.T) {
	tests := []struct {
		name, resource, field string
		value                 interface{}
	}{
		{"scope leak", "devices", "scope_id", "other"}, {"vid leak", "devices", "vid", "other:d:id"},
		{"bad entity", "devices", "entity_id", "not-an-id"}, {"wrong string type", "devices", "name", int64(1)},
		{"null required", "devices", "role", nil}, {"wrong nullable type", "devices", "serial_number", false},
		{"bad owner", "interfaces", "device_id", "invalid"}, {"negative speed", "interfaces", "speed_bps", int64(-1)},
		{"wrong speed type", "interfaces", "speed_bps", "1000"}, {"bad interface", "addresses", "interface_id", "invalid"},
		{"wrong family type", "addresses", "address_family", "4"}, {"bad family", "addresses", "address_family", int64(5)},
		{"family mismatch", "addresses", "address", "2001:db8::1"}, {"invalid ip", "addresses", "address", "garbage"},
		{"bad prefix", "addresses", "prefix_length", int64(33)}, {"negative prefix", "addresses", "prefix_length", int64(-1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := graphEntry(test.resource, 1)
			entry[test.field] = test.value
			fake := &fakeGraphClient{result: graphFixture(t, test.resource, entry)}
			page, err := (&graphInventoryRepository{client: fake}).List(context.Background(), test.resource, InventoryListQuery{ScopeID: graphTestScope, Limit: 2})
			require.ErrorIs(t, err, ErrInventoryNotReady)
			require.Nil(t, page)
		})
	}
}

func TestGraphRejectsOrderingAndFilterLeaks(t *testing.T) {
	for _, ids := range [][]int{{2, 1}, {1, 1}, {1, 2, 3, 4}} {
		entries := make([]map[string]interface{}, 0, len(ids))
		for _, id := range ids {
			entries = append(entries, graphEntry("devices", id))
		}
		fake := &fakeGraphClient{result: graphFixture(t, "devices", entries...)}
		_, err := (&graphInventoryRepository{client: fake}).List(context.Background(), "devices", InventoryListQuery{ScopeID: graphTestScope, Limit: 2})
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
	for _, resource := range []string{"devices", "interfaces", "addresses"} {
		q := InventoryListQuery{ScopeID: graphTestScope, Limit: 1}
		if resource == "devices" {
			q.Name = "different"
		} else {
			q.ParentID = strings.Repeat("b", 32)
		}
		fake := &fakeGraphClient{result: graphFixture(t, resource, graphEntry(resource, 1))}
		_, err := (&graphInventoryRepository{client: fake}).List(context.Background(), resource, q)
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
}

func TestGraphCancellationAndErrors(t *testing.T) {
	fake := &fakeGraphClient{result: graphFixture(t, "devices", graphEntry("devices", 1))}
	r := &graphInventoryRepository{client: fake}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.List(ctx, "devices", InventoryListQuery{ScopeID: graphTestScope, Limit: 1})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, fake.calls)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	fake.after = cancel
	_, err = r.Device(ctx, graphTestScope, fmt.Sprintf("%032x", 1))
	require.ErrorIs(t, err, context.Canceled)
	fake.after = nil
	fake.err = errors.New("server error with secret")
	_, err = r.Device(context.Background(), graphTestScope, fmt.Sprintf("%032x", 1))
	require.ErrorIs(t, err, ErrInventoryNotReady)
	require.NotContains(t, err.Error(), "secret")
	fake.err = nil
	fake.result, err = nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_E_EXECUTION_ERROR, ErrorMsg: []byte("secret")})
	require.NoError(t, err)
	_, err = r.Device(context.Background(), graphTestScope, fmt.Sprintf("%032x", 1))
	require.ErrorIs(t, err, ErrInventoryNotReady)
	require.NotContains(t, err.Error(), "secret")
	fake.result = nil
	_, err = r.List(context.Background(), "devices", InventoryListQuery{ScopeID: graphTestScope, Limit: 1})
	require.ErrorIs(t, err, ErrInventoryNotReady)
}

func TestGraphConfigUnavailableAndCleanup(t *testing.T) {
	for _, conf := range []*viper.Viper{nil, viper.New()} {
		r, cleanup, err := NewGraphInventoryRepository(conf)
		require.NoError(t, err)
		cleanup()
		_, err = r.Device(context.Background(), graphTestScope, "id")
		require.ErrorIs(t, err, ErrInventoryNotReady)
		_, err = r.List(context.Background(), "devices", InventoryListQuery{})
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
	conf := viper.New()
	conf.Set("inventory.graph.hosts", []string{"localhost:9669"})
	conf.Set("inventory.graph.space", "current_inventory")
	conf.Set("inventory.graph.username", "reader")
	conf.Set("inventory.graph.password", "test-password")
	fake := &fakeGraphClient{}
	r, cleanup, err := newGraphInventoryRepository(conf, func(c graphclient.Config) (graphclient.Client, error) {
		require.Equal(t, "current_inventory", c.Space)
		return fake, nil
	})
	require.NoError(t, err)
	require.NotNil(t, r)
	cleanup()
	require.Equal(t, 1, fake.closes)
	_, cleanup, err = newGraphInventoryRepository(conf, func(graphclient.Config) (graphclient.Client, error) {
		return nil, errors.New("connection failed")
	})
	require.Error(t, err)
	cleanup()
	conf.Set("inventory.graph.space", "bad;space")
	_, _, err = NewGraphInventoryRepository(conf)
	require.Error(t, err)
}

func TestGraphScopeAndQueryValidation(t *testing.T) {
	fake := &fakeGraphClient{}
	r := &graphInventoryRepository{client: fake}
	for _, scope := range []string{"", "scope-1", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("A", 32), strings.Repeat("g", 32)} {
		_, err := r.Device(context.Background(), scope, graphTestParent)
		require.Error(t, err)
		_, err = r.List(context.Background(), "devices", InventoryListQuery{ScopeID: scope, Limit: 1})
		require.Error(t, err)
	}
	for _, q := range []InventoryListQuery{
		{ScopeID: graphTestScope, Limit: 0},
		{ScopeID: graphTestScope, Limit: 201},
		{ScopeID: graphTestScope, Limit: 1, LastID: "invalid"},
		{ScopeID: graphTestScope, Limit: 1, ParentID: "invalid"},
		{ScopeID: graphTestScope, Limit: 1, AddressFamily: "4; DROP SPACE x"},
	} {
		_, err := r.List(context.Background(), "addresses", q)
		require.Error(t, err)
	}
	require.Zero(t, fake.calls)
	require.Len(t, graphVID(graphTestScope, "d", graphTestParent), 67)
}

func TestGraphTypedOptionalFields(t *testing.T) {
	iface := graphEntry("interfaces", 1)
	iface["speed_bps"] = int64(100000000000)
	fake := &fakeGraphClient{result: graphFixture(t, "interfaces", iface)}
	r := &graphInventoryRepository{client: fake}
	page, err := r.List(context.Background(), "interfaces", InventoryListQuery{ScopeID: graphTestScope, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, int64(100000000000), *page.Items.([]model.Interface)[0].SpeedBPS)
	address := graphEntry("addresses", 1)
	address["interface_id"] = graphTestParent
	address["address_family"] = int64(6)
	address["address"] = "2001:db8::1"
	address["prefix_length"] = int64(128)
	fake.result = graphFixture(t, "addresses", address)
	page, err = r.List(context.Background(), "addresses", InventoryListQuery{ScopeID: graphTestScope, Limit: 1, AddressFamily: "6"})
	require.NoError(t, err)
	v := page.Items.([]model.Address)[0]
	require.Equal(t, graphTestParent, *v.InterfaceID)
	require.Equal(t, 128, *v.PrefixLength)
	require.Equal(t, []byte(net.ParseIP("2001:db8::1").To16()), v.Address)
	fake.result = graphFixture(t, "devices", graphEntry("devices", 1))
	device, err := r.Device(context.Background(), graphTestScope, fmt.Sprintf("%032x", 1))
	require.NoError(t, err)
	require.Nil(t, device.SerialNumber)
}

func TestGraphMalformedValueUnions(t *testing.T) {
	for _, malformed := range []*nebula.Value{nil, {}, {SVal: []byte("value"), IVal: new(int64)}} {
		result := graphFixture(t, "devices", graphEntry("devices", 1))
		result.GetRows()[0].Values[3] = malformed
		_, err := (&graphInventoryRepository{client: &fakeGraphClient{result: result}}).List(context.Background(), "devices", InventoryListQuery{ScopeID: graphTestScope, Limit: 1})
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
}

func TestGraphRejectsLifecycleAndKindLeaks(t *testing.T) {
	for _, tc := range []struct {
		resource string
		query    InventoryListQuery
	}{
		{"devices", InventoryListQuery{Lifecycle: "retired"}},
		{"devices", InventoryListQuery{DeviceKind: "router"}},
		{"interfaces", InventoryListQuery{InterfaceKind: "physical"}},
		{"addresses", InventoryListQuery{AddressFamily: "6"}},
	} {
		tc.query.ScopeID = graphTestScope
		tc.query.Limit = 1
		fake := &fakeGraphClient{result: graphFixture(t, tc.resource, graphEntry(tc.resource, 1))}
		_, err := (&graphInventoryRepository{client: fake}).List(context.Background(), tc.resource, tc.query)
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
}
