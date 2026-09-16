package v1

import (
	"encoding/json"
	"math"
	"net/netip"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"

	"github.com/stretchr/testify/require"
)

func TestInventoryItemsMapsPublicFields(t *testing.T) {
	serial, active, iface := "serial", "generation", "interface"
	prefix, speed := 24, int64(math.MaxInt64)
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.FixedZone("local", 3600))
	tests := []struct {
		name  string
		items interface{}
		want  string
	}{
		{
			name:  "scopes",
			items: []model.TopologyScope{{ID: "scope", Name: "lab", ActiveGenerationID: &active}},
			want:  `[{"id":"scope","name":"lab","active_generation_id":"generation"}]`,
		},
		{
			name:  "sources omit config reference",
			items: []model.Source{{ID: "source", ScopeID: "scope", Name: "lab", AdapterKind: "fixture", ConfigRef: "secret-config", Enabled: true}},
			want:  `[{"id":"source","scope_id":"scope","name":"lab","adapter_kind":"fixture","enabled":true}]`,
		},
		{
			name:  "generations",
			items: []model.Generation{{ID: "generation", ScopeID: "scope", RunID: "run", State: "published", InventoryReady: true, GraphReady: false, RoutingReady: false, CreatedAt: now, PublishedAt: &now}},
			want:  `[{"id":"generation","scope_id":"scope","run_id":"run","state":"published","inventory_ready":true,"graph_ready":false,"routing_ready":false,"created_at":"2026-09-15T08:00:00Z","published_at":"2026-09-15T08:00:00Z"}]`,
		},
		{
			name:  "devices stable entity ID",
			items: []model.Device{{GenerationID: "generation", EntityID: "device", Name: "router", DeviceKind: "router", Role: "core", Lifecycle: "active", ResolutionStatus: "resolved", SerialNumber: &serial}},
			want:  `[{"entity_id":"device","generation_id":"generation","name":"router","device_kind":"router","role":"core","lifecycle":"active","resolution_status":"resolved","serial_number":"serial"}]`,
		},
		{
			name:  "interfaces decimal speed",
			items: []model.Interface{{GenerationID: "generation", EntityID: "interface", DeviceID: "device", Namespace: "default", SourceName: "Ethernet1", NormalizedName: "eth1", InterfaceKind: "physical", AdminState: "up", OperState: "down", Lifecycle: "active", ResolutionStatus: "resolved", SpeedBPS: &speed}},
			want:  `[{"entity_id":"interface","generation_id":"generation","device_id":"device","namespace":"default","source_name":"Ethernet1","normalized_name":"eth1","interface_kind":"physical","admin_state":"up","oper_state":"down","lifecycle":"active","resolution_status":"resolved","speed_bps":"9223372036854775807"}]`,
		},
		{
			name:  "addresses textual IP",
			items: []model.Address{{GenerationID: "generation", EntityID: "address", DeviceID: "device", InterfaceID: &iface, AddressFamily: 4, Address: []byte{192, 0, 2, 1}, PrefixLength: &prefix, AddressScopeKey: "lab:vrf", ScopeStatus: "known", Purpose: "management", Lifecycle: "active", ResolutionStatus: "resolved"}},
			want:  `[{"entity_id":"address","generation_id":"generation","device_id":"device","interface_id":"interface","address_family":4,"address":"192.0.2.1","prefix_length":24,"address_scope_key":"lab:vrf","scope_status":"known","purpose":"management","lifecycle":"active","resolution_status":"resolved"}]`,
		},
		{
			name:  "runs omit internal request fields",
			items: []model.SyncRun{{ID: "run", ScopeID: "scope", Status: "failed", Mode: "full", BaseGenerationID: &active, GenerationID: &active, RequestHash: "private-hash", IdempotencyKey: "private-key", RequestedBy: "private-user", CancelRequestedAt: &now, CreatedAt: now, StartedAt: &now, FinishedAt: &now, ErrorCode: "upstream_failure"}},
			want:  `[{"id":"run","scope_id":"scope","status":"failed","mode":"full","base_generation_id":"generation","generation_id":"generation","cancel_requested_at":"2026-09-15T08:00:00Z","created_at":"2026-09-15T08:00:00Z","started_at":"2026-09-15T08:00:00Z","finished_at":"2026-09-15T08:00:00Z","error_code":"upstream_failure"}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InventoryItems(tt.items)
			require.NoError(t, err)
			data, err := json.Marshal(got)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(data))
		})
	}
}

func TestInventoryItemsEmptySlicesAreArrays(t *testing.T) {
	for _, items := range []interface{}{
		[]model.TopologyScope(nil), []model.Source(nil), []model.Generation(nil),
		[]model.Device(nil), []model.Interface(nil),
		[]model.Address(nil), []model.SyncRun(nil),
		[]model.TopologyScope{}, []model.Source{}, []model.Generation{},
		[]model.Device{}, []model.Interface{},
		[]model.Address{}, []model.SyncRun{},
	} {
		got, err := InventoryItems(items)
		require.NoError(t, err)
		data, err := json.Marshal(got)
		require.NoError(t, err)
		require.Equal(t, "[]", string(data), "%T", items)
	}
}

func TestInventoryItemsPreservesUnknownValues(t *testing.T) {
	for _, tt := range []struct {
		items interface{}
		keys  []string
	}{
		{[]model.TopologyScope{{}}, []string{"active_generation_id"}},
		{[]model.Device{{}}, []string{"serial_number"}},
		{[]model.Interface{{}}, []string{"speed_bps"}},
		{[]model.Address{{AddressFamily: 4, Address: []byte{192, 0, 2, 1}}}, []string{"interface_id", "prefix_length"}},
		{[]model.Generation{{}}, []string{"published_at"}},
		{[]model.SyncRun{{}}, []string{"base_generation_id", "generation_id", "cancel_requested_at", "started_at", "finished_at"}},
	} {
		got, err := InventoryItems(tt.items)
		require.NoError(t, err)
		data, err := json.Marshal(got)
		require.NoError(t, err)
		var objects []map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &objects))
		for _, key := range tt.keys {
			require.Contains(t, objects[0], key)
			require.Nil(t, objects[0][key], key)
		}
	}
}

func TestInventoryItemsAddressesRoundTrip(t *testing.T) {
	for _, text := range []string{"192.0.2.1", "2001:db8::1", "::ffff:192.0.2.1", "::"} {
		t.Run(text, func(t *testing.T) {
			address := netip.MustParseAddr(text)
			family := 6
			if address.Is4() {
				family = 4
			}
			got, err := InventoryItems([]model.Address{{AddressFamily: family, Address: address.AsSlice()}})
			require.NoError(t, err)
			data, err := json.Marshal(got)
			require.NoError(t, err)
			var objects []struct {
				Address string `json:"address"`
			}
			require.NoError(t, json.Unmarshal(data, &objects))
			parsed, err := netip.ParseAddr(objects[0].Address)
			require.NoError(t, err)
			require.Equal(t, address, parsed)
		})
	}
}

func TestInventoryItemsRejectsInvalidAddressBytes(t *testing.T) {
	for _, bytes := range [][]byte{nil, {}, {192, 0, 2}, make([]byte, 5), make([]byte, 15), make([]byte, 17)} {
		got, err := InventoryItems([]model.Address{{AddressFamily: 4, Address: bytes}})
		require.Error(t, err)
		require.Nil(t, got)
	}
}

func TestInventoryStoredIPv4UsesMappedSixteenBytes(t *testing.T) {
	stored := netip.MustParseAddr("192.0.2.1").As16()
	got, err := InventoryItems([]model.Address{{AddressFamily: 4, Address: stored[:]}})
	require.NoError(t, err)
	require.Equal(t, "192.0.2.1", got.([]InventoryAddressDTO)[0].Address)
	for _, item := range []model.Address{
		{AddressFamily: 4, Address: netip.MustParseAddr("2001:db8::1").AsSlice()},
		{AddressFamily: 6, Address: []byte{192, 0, 2, 1}},
		{AddressFamily: 0, Address: stored[:]},
	} {
		_, err := InventoryItems([]model.Address{item})
		require.Error(t, err)
	}
}

func TestInventoryItemsRejectsUnsupportedTypes(t *testing.T) {
	for _, items := range []interface{}{nil, []string{"secret"}, model.Source{ConfigRef: "secret"}} {
		got, err := InventoryItems(items)
		require.Error(t, err)
		require.Nil(t, got)
	}
}

func TestInventoryDetailMappingsMatchListMappings(t *testing.T) {
	device := model.Device{EntityID: "device", GenerationID: "generation"}
	generation := model.Generation{ID: "generation"}
	run := model.SyncRun{ID: "run"}
	for _, tt := range []struct{ items, detail interface{} }{
		{[]model.Device{device}, InventoryDevice(device)},
		{[]model.Generation{generation}, InventoryGeneration(generation)},
		{[]model.SyncRun{run}, InventoryRun(run)},
	} {
		list, err := InventoryItems(tt.items)
		require.NoError(t, err)
		listJSON, err := json.Marshal(list)
		require.NoError(t, err)
		detailJSON, err := json.Marshal(tt.detail)
		require.NoError(t, err)
		require.JSONEq(t, "["+string(detailJSON)+"]", string(listJSON))
	}
}
