package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func TestCMDBCollectTransformerPageMapsOnlyGraphProperties(t *testing.T) {
	item := `{"inst_id":1588,"uuid":" TRANSFORMER-UUID ","code":" 1T203 ","idc":1030,"standby_transformer":1593,"transformer_brand":" 许继 ","transformer_type":" SCB11-2500KVA/10 ","transformer_capacity":2500,"create_time":"2024-08-13T13:53:25.913+08:00","last_time":"2024-10-31T16:32:33.631+08:00","idc_id":999,"brand":"wrong","created_at":"2025-01-01 00:00:00","updated_at":"2025-01-01 00:00:00","building":404,"module":100,"transformer_id_tag":"A1-1"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/pub/api/v3/find/instance/object/idc_transformer", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.Equal(t, "cmdb_all", r.URL.Query().Get("user"))
		var request cmdbEntityRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.ElementsMatch(t, []string{"inst_id", "uuid", "code", "idc", "standby_transformer", "transformer_brand", "transformer_type", "transformer_capacity", "create_time", "last_time"}, request.Fields)
		require.Equal(t, deviceViewPage{Start: 2, Limit: 2, Sort: "inst_id"}, request.Page)
		require.Equal(t, map[string]any{"idc": float64(1030)}, request.Condition)
		fmt.Fprint(w, entityResponse(item, 3))
	}))
	defer server.Close()
	conf := cmdbTestConfig(server.URL, map[string]string{"Authorization": "Bearer secret"})
	conf.Set("inventory.cmdb.sources.primary.entity_type", "transformer")
	conf.Set("inventory.cmdb.sources.primary.query", map[string]string{"user": "cmdb_all"})
	conf.Set("inventory.cmdb.sources.primary.transformer_condition", map[string]any{"idc": 1030})
	conf.Set("inventory.cmdb.sources.primary.device_condition", map[string]any{"inst_id": 999})
	conf.Set("inventory.cmdb.sources.primary.cabinet_condition", map[string]any{"idc_id": 999})
	collector := NewCMDBWithClient(conf, server.Client())
	page, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityTransformer, 2)
	require.NoError(t, err)
	require.Equal(t, EntityPage{Records: []EntityRecord{{SourceInstanceID: 1588, Identity: "TRANSFORMER-UUID", Properties: map[string]any{
		"inst_id": int64(1588), "code": "1T203", "idc_id": int64(1030), "standby_transformer_id": int64(1593),
		"brand": "许继", "model": "SCB11-2500KVA/10", "rated_capacity": int64(2500),
		"source_created_at": time.Date(2024, 8, 13, 5, 53, 25, 913000000, time.UTC),
		"source_updated_at": time.Date(2024, 10, 31, 8, 32, 33, 631000000, time.UTC),
	}}}, NextOffset: 3, Total: 3, Done: true}, page)
}

func TestCMDBTransformerRecordNormalizesOptionalValues(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		capacity     any
	}{
		{name: "missing", fields: `"unused":true`},
		{name: "null", fields: `"idc":null,"standby_transformer":null,"transformer_capacity":null`},
		{name: "zero references", fields: `"idc":0,"standby_transformer":0,"transformer_capacity":0`, capacity: int64(0)},
		{name: "negative references", fields: `"idc":-1030,"standby_transformer":-1,"transformer_capacity":2500`, capacity: int64(2500)},
		{name: "fractional references", fields: `"idc":1.5,"standby_transformer":2.5`},
		{name: "text references", fields: `"idc":"1030","standby_transformer":"invalid"`},
		{name: "other invalid references", fields: `"idc":{},"standby_transformer":true`},
		{name: "overflow references", fields: `"idc":9223372036854775808,"standby_transformer":[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{"inst_id":1,"uuid":" transformer ","code":" ","transformer_brand":" ","transformer_type":"","create_time":"2000-01-01 00:00:00","last_time":"invalid",`+tc.fields+`}`), &item))
			record, err := cmdbEntityRecord(item, model.EntityTransformer)
			require.NoError(t, err)
			require.Equal(t, EntityRecord{SourceInstanceID: 1, Identity: "transformer", Properties: map[string]any{
				"inst_id": int64(1), "code": nil, "idc_id": nil, "standby_transformer_id": nil,
				"brand": nil, "model": nil, "rated_capacity": tc.capacity, "source_created_at": nil, "source_updated_at": nil,
			}}, record)
		})
	}
}

func TestCMDBTransformerRecordRejectsInvalidIdentityAndSpecifications(t *testing.T) {
	for _, fields := range []string{
		`"inst_id":1`, `"inst_id":1,"uuid":" "`, `"inst_id":1,"uuid":null`, `"uuid":"transformer"`,
		`"inst_id":0,"uuid":"transformer"`, `"inst_id":-1,"uuid":"transformer"`,
		`"inst_id":1.5,"uuid":"transformer"`, `"inst_id":"1","uuid":"transformer"`, `"inst_id":1,"uuid":9`,
		`"inst_id":1,"uuid":"transformer","code":9`, `"inst_id":1,"uuid":"transformer","transformer_brand":true`,
		`"inst_id":1,"uuid":"transformer","transformer_type":{}`,
		`"inst_id":1,"uuid":"transformer","transformer_capacity":"2500"`,
		`"inst_id":1,"uuid":"transformer","transformer_capacity":true`,
		`"inst_id":1,"uuid":"transformer","transformer_capacity":{}`,
	} {
		t.Run(fields, func(t *testing.T) {
			var item map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{`+fields+`}`), &item))
			_, err := cmdbEntityRecord(item, model.EntityTransformer)
			require.ErrorIs(t, err, ErrCMDBRecord)
			require.NotContains(t, err.Error(), fields)
		})
	}
}

func TestCMDBTransformerPageReusesStrictPaging(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       error
		done       bool
	}{
		{name: "next page", body: entityResponse(`{"inst_id":1,"uuid":"a"},{"inst_id":2,"uuid":"b"}`, 3)},
		{name: "complete", body: entityResponse(`{"inst_id":1,"uuid":"a"},{"inst_id":2,"uuid":"b"}`, 2), done: true},
		{name: "empty complete", body: entityResponse(``, 0), done: true},
		{name: "duplicate", body: entityResponse(`{"inst_id":1,"uuid":"a"},{"inst_id":1,"uuid":"b"}`, 2), kind: ErrCMDBResponse},
		{name: "decreasing", body: entityResponse(`{"inst_id":2,"uuid":"a"},{"inst_id":1,"uuid":"b"}`, 2), kind: ErrCMDBResponse},
		{name: "over limit", body: entityResponse(`{"inst_id":1,"uuid":"a"},{"inst_id":2,"uuid":"b"},{"inst_id":3,"uuid":"c"}`, 3), kind: ErrCMDBResponse},
		{name: "early empty", body: entityResponse(``, 2), kind: ErrCMDBResponse},
		{name: "missing envelope", body: `{}`, kind: ErrCMDBResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			collector := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client())
			page, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityTransformer, 0)
			if tc.kind != nil {
				require.ErrorIs(t, err, tc.kind)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.done, page.Done)
			require.Equal(t, len(page.Records), page.NextOffset)
		})
	}
}

func TestCMDBLiveFilteredInterfaceReadOnly(t *testing.T) {
	path := os.Getenv("CMDB_INTERFACE_READ_CONFIG")
	if path == "" {
		t.Skip("显式提供配置后只读探测 IDC1030 接口采集，不写数据库")
	}
	conf := cmdbTestConfig("", nil)
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 200)
	conf.Set("inventory.cmdb.sources.idc1030.timeout_seconds", 60)
	conf.Set("inventory.cmdb.sources.idc1030.interface_condition", map[string]any{"sn": "210235A53L5264L100G9", "idc_id": 1030, "port_type": "physics"})
	collector := NewCMDB(conf)
	if os.Getenv("CMDB_NETWORK_DEVICE_PROBE") == "1" {
		conf.Set("inventory.cmdb.sources.idc1030.device_condition", map[string]any{"idc_id": 1030, "parent_type_id": 54})
		devices, err := collector.CollectDevicePage(context.Background(), model.Source{ConfigRef: "idc1030"}, 0)
		require.NoError(t, err)
		require.NotEmpty(t, devices.Records)
		for _, device := range devices.Records {
			require.Equal(t, int64(54), device.ParentTypeID)
		}
		t.Logf("network devices total=%d first_page=%d", devices.Total, len(devices.Records))
	}
	_, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "idc1030"}, model.EntityDataCenter, 0)
	require.NoError(t, err, "对照检查数据中心端点是否可访问")
	if os.Getenv("CMDB_INTERFACE_DEVICE_PROBE") == "1" {
		devices, err := collector.CollectDevicePage(context.Background(), model.Source{ConfigRef: "idc1030"}, 0)
		require.NoError(t, err)
		require.NotEmpty(t, devices.Records)
		conf.Set("inventory.cmdb.sources.idc1030.interface_condition", map[string]any{"idc_id": 1030, "local_device_sn": devices.Records[0].DeviceSN})
		t.Log("保留 IDC1030 条件并额外限定首个已采集设备进行只读诊断")
	}
	page, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "idc1030"}, model.EntityInterface, 0)
	require.NoError(t, err)
	t.Logf("total=%d page=%d next=%d done=%t", page.Total, len(page.Records), page.NextOffset, page.Done)
	if len(page.Records) > 0 {
		t.Logf("first inst_id=%d port_uuid=%s speed=%v status=%v", page.Records[0].SourceInstanceID, page.Records[0].Identity, page.Records[0].Properties["port_speed"], page.Records[0].Properties["port_operation_status"])
	}
}

func TestCMDBHTTPCollectionPreservesContextCancellation(t *testing.T) {
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityDataCenter} {
		t.Run(string(entity), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel() }))
			defer server.Close()
			collector := NewCMDB(cmdbTestConfig(server.URL, nil))
			var err error
			if entity == model.EntityDevice {
				_, err = collector.CollectDevicePage(ctx, model.Source{ConfigRef: "primary"}, 0)
			} else {
				_, err = collector.CollectEntityPage(ctx, model.Source{ConfigRef: "primary"}, entity, 0)
			}
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestCMDBSelectedEntityValidatesConfiguredWhitelist(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		want    model.EntityType
		invalid bool
	}{
		{name: "default", want: model.EntityDevice},
		{name: "device", value: "device", want: model.EntityDevice},
		{name: "data center", value: "data_center", want: model.EntityDataCenter},
		{name: "cabinet", value: "cabinet", want: model.EntityCabinet},
		{name: "interface", value: "interface", want: model.EntityInterface},
		{name: "transformer", value: "transformer", want: model.EntityTransformer},
		{name: "unsupported", value: "pod", invalid: true},
		{name: "empty configured", value: "", invalid: true},
		{name: "wrong type", value: []string{"cabinet"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := cmdbTestConfig("https://cmdb.example.test", nil)
			if tc.value != nil {
				conf.Set("inventory.cmdb.sources.primary.entity_type", tc.value)
			}
			selector, ok := any(NewCMDB(conf)).(interface {
				SelectedEntity(model.Source) (model.EntityType, error)
			})
			require.True(t, ok, "CMDB must select the configured entity")
			got, err := selector.SelectedEntity(model.Source{ConfigRef: "primary"})
			if tc.invalid {
				require.ErrorIs(t, err, ErrCMDBConfig)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestCMDBCoversFullSourceUsesOnlyEntityCondition(t *testing.T) {
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityDataCenter, model.EntityCabinet, model.EntityInterface, model.EntityTransformer} {
		t.Run(string(entity), func(t *testing.T) {
			conf := cmdbTestConfig("https://cmdb.example.test", nil)
			prefix := "inventory.cmdb.sources.primary."
			for _, other := range []model.EntityType{model.EntityDevice, model.EntityDataCenter, model.EntityCabinet, model.EntityInterface, model.EntityTransformer} {
				if other != entity {
					conf.Set(prefix+string(other)+"_condition", map[string]any{"idc_id": 451})
				}
			}
			collector, ok := any(NewCMDB(conf)).(interface {
				CoversFullSource(model.Source, model.EntityType) (bool, error)
			})
			require.True(t, ok, "CMDB must determine entity-specific source coverage")
			full, err := collector.CoversFullSource(model.Source{ConfigRef: "primary"}, entity)
			require.NoError(t, err)
			require.True(t, full)
			conf.Set(prefix+string(entity)+"_condition", map[string]any{"idc_id": 451})
			full, err = collector.CoversFullSource(model.Source{ConfigRef: "primary"}, entity)
			require.NoError(t, err)
			require.False(t, full)
			conf.Set(prefix+string(entity)+"_condition", "invalid-secret")
			_, err = collector.CoversFullSource(model.Source{ConfigRef: "primary"}, entity)
			require.ErrorIs(t, err, ErrCMDBConfig)
			require.NotContains(t, err.Error(), "invalid-secret")
		})
	}
}

func TestCMDBCollectInterfacePageMapsOnlyGraphProperties(t *testing.T) {
	item := `{"inst_id":7,"port_uuid":" PORT-UUID ","uuid":"VIEW-UUID","local_interface_uuid":"ALIAS-UUID","port_name":" 100GE1/0/5 ","port_type":"physics","port_speed":100000,"port_operation_status":1,"group_uuid":" GROUP-UUID ","port_group_uuid":" MEMBER-UUID ","port_group_name":" Group name ","group_name":"wrong name","group_type":3,"port_group_type":6,"group_subtype":17,"device_uuid":"device","local_device_sn":"SN","remote_interface_uuid":"remote","port_id":9,"idc_id":1030,"created_at":"2026-09-15 23:01:33"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/pub/api/v3/find/instance/object/port_view", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.Equal(t, "cmdb_all", r.URL.Query().Get("user"))
		var request cmdbEntityRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.ElementsMatch(t, []string{"inst_id", "port_uuid", "port_name", "port_type", "port_speed", "port_operation_status", "group_uuid", "port_group_uuid", "port_group_name", "group_type", "port_group_type", "group_subtype"}, request.Fields)
		require.Equal(t, deviceViewPage{Start: 2, Limit: 2, Sort: "inst_id"}, request.Page)
		require.Equal(t, map[string]any{"idc_id": float64(1030)}, request.Condition)
		fmt.Fprint(w, entityResponse(item, 3))
	}))
	defer server.Close()
	conf := cmdbTestConfig(server.URL, map[string]string{"Authorization": "Bearer secret"})
	conf.Set("inventory.cmdb.sources.primary.entity_type", "interface")
	conf.Set("inventory.cmdb.sources.primary.query", map[string]string{"user": "cmdb_all"})
	conf.Set("inventory.cmdb.sources.primary.interface_condition", map[string]any{"idc_id": 1030})
	conf.Set("inventory.cmdb.sources.primary.device_condition", map[string]any{"inst_id": 999})
	collector := NewCMDBWithClient(conf, server.Client())
	page, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface, 2)
	require.NoError(t, err)
	require.Equal(t, EntityPage{Records: []EntityRecord{{SourceInstanceID: 7, Identity: "PORT-UUID", Properties: map[string]any{
		"port_name": "100GE1/0/5", "port_type": "physics", "port_speed": "100000", "port_operation_status": "1",
		"group_uuid": "GROUP-UUID", "port_group_uuid": "MEMBER-UUID", "group_name": "Group name",
		"group_type": int64(3), "port_group_type": int64(6), "group_subtype": int64(17),
	}}}, NextOffset: 3, Total: 3, Done: true}, page)
}

func TestCMDBInterfaceRecordNormalizesGroupsAndPreservesRawValues(t *testing.T) {
	for _, tc := range []struct {
		name, fields  string
		speed, status any
	}{
		{name: "numeric", fields: `"port_speed":100000,"port_operation_status":1`, speed: "100000", status: "1"},
		{name: "zero", fields: `"port_speed":0,"port_operation_status":0`, speed: "0", status: "0"},
		{name: "text", fields: `"port_speed":"100000","port_operation_status":"unknown"`, speed: "100000", status: "unknown"},
		{name: "missing", fields: `"unused":true`},
		{name: "null", fields: `"port_speed":null,"port_operation_status":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{"inst_id":1,"port_uuid":"port","group_uuid":" ","port_group_uuid":"","port_group_name":" ","group_type":0,"port_group_type":0,"group_subtype":0,`+tc.fields+`}`), &item))
			record, err := cmdbEntityRecord(item, model.EntityInterface)
			require.NoError(t, err)
			require.Equal(t, "port", record.Identity)
			require.Equal(t, map[string]any{"port_name": nil, "port_type": nil, "port_speed": tc.speed, "port_operation_status": tc.status, "group_uuid": nil, "port_group_uuid": nil, "group_name": nil, "group_type": nil, "port_group_type": nil, "group_subtype": nil}, record.Properties)
		})
	}
}

func TestCMDBInterfaceRecordRejectsInvalidIdentityAndTypes(t *testing.T) {
	for _, fields := range []string{
		`"inst_id":1,"uuid":"view","local_interface_uuid":"alias"`,
		`"inst_id":1,"port_uuid":" ","uuid":"view","local_interface_uuid":"alias"`,
		`"inst_id":1,"port_uuid":null,"uuid":"view"`,
		`"port_uuid":"port"`,
		`"inst_id":0,"port_uuid":"port"`,
		`"inst_id":1.5,"port_uuid":"port"`,
		`"inst_id":1,"port_uuid":9`,
		`"inst_id":1,"port_uuid":"port","port_name":9`,
		`"inst_id":1,"port_uuid":"port","port_speed":true`,
		`"inst_id":1,"port_uuid":"port","port_operation_status":{}`,
		`"inst_id":1,"port_uuid":"port","group_type":1.5`,
		`"inst_id":1,"port_uuid":"port","port_group_type":"6"`,
		`"inst_id":1,"port_uuid":"port","group_subtype":true`,
	} {
		t.Run(fields, func(t *testing.T) {
			var item map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(`{`+fields+`}`), &item))
			_, err := cmdbEntityRecord(item, model.EntityInterface)
			require.ErrorIs(t, err, ErrCMDBRecord)
			require.NotContains(t, err.Error(), fields)
		})
	}
}

func TestCMDBInterfacePageReusesStrictPaging(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       error
		done       bool
	}{
		{name: "next page", body: entityResponse(`{"inst_id":1,"port_uuid":"a"},{"inst_id":2,"port_uuid":"b"}`, 3)},
		{name: "complete", body: entityResponse(`{"inst_id":1,"port_uuid":"a"},{"inst_id":2,"port_uuid":"b"}`, 2), done: true},
		{name: "empty complete", body: entityResponse(``, 0), done: true},
		{name: "duplicate", body: entityResponse(`{"inst_id":1,"port_uuid":"a"},{"inst_id":1,"port_uuid":"b"}`, 2), kind: ErrCMDBResponse},
		{name: "decreasing", body: entityResponse(`{"inst_id":2,"port_uuid":"a"},{"inst_id":1,"port_uuid":"b"}`, 2), kind: ErrCMDBResponse},
		{name: "over limit", body: entityResponse(`{"inst_id":1,"port_uuid":"a"},{"inst_id":2,"port_uuid":"b"},{"inst_id":3,"port_uuid":"c"}`, 3), kind: ErrCMDBResponse},
		{name: "early empty", body: entityResponse(``, 2), kind: ErrCMDBResponse},
		{name: "missing envelope", body: `{}`, kind: ErrCMDBResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			collector := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client())
			page, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface, 0)
			if tc.kind != nil {
				require.ErrorIs(t, err, tc.kind)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.done, page.Done)
			require.Equal(t, len(page.Records), page.NextOffset)
		})
	}
}

func TestCMDBCollectEntityPageMapsAuthoritativeFields(t *testing.T) {
	for _, entity := range []model.EntityType{model.EntityDataCenter, model.EntityCabinet} {
		t.Run(string(entity), func(t *testing.T) {
			item := `{"inst_id":451,"uuid":" IDC-UUID ","cn_name":" 中文名称 ","code":" RH ","address":" 地址 ","location":" ","created_at":"2021-03-15 18:02:11","updated_at":"2000-01-01 00:00:00","last_time":"2026-09-15T23:01:33.343123456+08:00","alias":"discard","city_id":761,"in_monitor_scope_id":99,"status":1}`
			want := map[string]any{"inst_id": int64(451), "uuid": "IDC-UUID", "cn_name": "中文名称", "code": "RH", "address": "地址", "location": nil, "source_created_at": time.Date(2021, 3, 15, 10, 2, 11, 0, time.UTC), "source_updated_at": time.Date(2026, 9, 15, 15, 1, 33, 343123000, time.UTC)}
			path := "/pub/api/v3/find/instance/object/idc"
			if entity == model.EntityCabinet {
				path = "/pub/api/v3/find/instance/object/cabinet"
				item = `{"inst_id":451,"uuid":" CABINET-UUID ","code":" C-1 ","dc_colo_rack":" DC ","isp_rack_code":" ISP ","rack":" R08 ","rack_column_code":" COL ","rack_row":" ","idc_id":451,"phy_building_id":404,"phy_room_id":889,"idc_module_id":100,"idc_building_structure_id":1728,"idc_rows_code":15756,"pod_ids":[914,null,0,914,915],"row_switch_id_A":6204,"row_switch_id_B":6114,"created_at":"2000-01-01 00:00:00","updated_at":"2026-09-15T23:01:33+08:00","last_time":"2026-09-16T00:00:00+08:00","phy_idc_id":999,"pod_id":999,"row_switch_id_a":999,"device_num":6,"in_monitor_scope_id":99}`
				want = map[string]any{"inst_id": int64(451), "uuid": "CABINET-UUID", "code": "C-1", "dc_colo_rack": "DC", "isp_rack_code": "ISP", "rack": "R08", "rack_column_code": "COL", "rack_row": nil, "idc_id": int64(451), "phy_building_id": int64(404), "phy_room_id": int64(889), "idc_module_id": int64(100), "idc_building_structure_id": int64(1728), "idc_rows_code": int64(15756), "pod_ids": []int64{914, 915}, "row_switch_id_a": int64(6204), "row_switch_id_b": int64(6114), "source_created_at": nil, "source_updated_at": time.Date(2026, 9, 15, 15, 1, 33, 0, time.UTC)}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, path, r.URL.Path)
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				require.Equal(t, "cmdb_all", r.URL.Query().Get("user"))
				var request struct {
					Fields    []string       `json:"fields"`
					Page      deviceViewPage `json:"page"`
					Condition map[string]any `json:"condition"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Contains(t, request.Fields, "uuid")
				require.NotContains(t, request.Fields, "in_monitor_scope_id")
				if entity == model.EntityCabinet {
					require.Contains(t, request.Fields, "row_switch_id_A")
					require.Contains(t, request.Fields, "row_switch_id_B")
				}
				require.Equal(t, deviceViewPage{Start: 2, Limit: 2, Sort: "inst_id"}, request.Page)
				require.Equal(t, map[string]any{"idc_id": float64(451)}, request.Condition)
				fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":3,"info":[%s]}}`, item)
			}))
			defer server.Close()
			conf := cmdbTestConfig(server.URL, map[string]string{"Authorization": "Bearer secret"})
			conf.Set("inventory.cmdb.sources.primary.entity_type", string(entity))
			conf.Set("inventory.cmdb.sources.primary.query", map[string]string{"user": "cmdb_all"})
			conf.Set("inventory.cmdb.sources.primary.device_condition", map[string]any{"inst_id": 999})
			conf.Set("inventory.cmdb.sources.primary."+string(entity)+"_condition", map[string]any{"idc_id": 451})
			collector, ok := any(NewCMDBWithClient(conf, server.Client())).(EntityPageCollector)
			require.True(t, ok, "CMDB must implement entity page collection")
			page, err := collector.CollectEntityPage(context.Background(), model.Source{ID: "source", ConfigRef: "primary"}, entity, 2)
			require.NoError(t, err)
			identity := want["uuid"].(string)
			delete(want, "uuid")
			require.Equal(t, EntityPage{Records: []EntityRecord{{SourceInstanceID: 451, Identity: identity, Properties: want}}, NextOffset: 3, Total: 3, Done: true}, page)
			require.NotContains(t, page.Records[0].Properties, "uuid")
		})
	}
}

func TestCMDBCollectEntityPageRejectsInvalidResponsesAndRecords(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       error
	}{
		{name: "missing envelope", body: `{}`, kind: ErrCMDBResponse},
		{name: "business failure", body: `{"result":false,"error_code":42,"error_msg":"secret"}`, kind: ErrCMDBResponse},
		{name: "missing info", body: `{"result":true,"error_code":0,"data":{"count":0}}`, kind: ErrCMDBResponse},
		{name: "null info", body: `{"result":true,"error_code":0,"data":{"count":0,"info":null}}`, kind: ErrCMDBResponse},
		{name: "negative count", body: `{"result":true,"error_code":0,"data":{"count":-1,"info":[]}}`, kind: ErrCMDBResponse},
		{name: "early empty", body: `{"result":true,"error_code":0,"data":{"count":2,"info":[]}}`, kind: ErrCMDBResponse},
		{name: "over limit", body: entityResponse(`{"inst_id":1,"uuid":"a"},{"inst_id":2,"uuid":"b"},{"inst_id":3,"uuid":"c"}`, 3), kind: ErrCMDBResponse},
		{name: "duplicate inst id", body: entityResponse(`{"inst_id":1,"uuid":"a"},{"inst_id":1,"uuid":"b"}`, 2), kind: ErrCMDBResponse},
		{name: "decreasing inst id", body: entityResponse(`{"inst_id":2,"uuid":"a"},{"inst_id":1,"uuid":"b"}`, 2), kind: ErrCMDBResponse},
		{name: "missing uuid", body: entityResponse(`{"inst_id":1,"uuid":" "}`, 1), kind: ErrCMDBRecord},
		{name: "missing inst id", body: entityResponse(`{"uuid":"uuid"}`, 1), kind: ErrCMDBRecord},
		{name: "fractional inst id", body: entityResponse(`{"inst_id":1.5,"uuid":"uuid"}`, 1), kind: ErrCMDBRecord},
		{name: "invalid optional integer", body: entityResponse(`{"inst_id":1,"uuid":"uuid","idc_id":"invalid"}`, 1), kind: ErrCMDBRecord},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			collector, ok := any(NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client())).(EntityPageCollector)
			require.True(t, ok)
			_, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityCabinet, 0)
			require.ErrorIs(t, err, tc.kind)
			require.NotContains(t, err.Error(), tc.body)
		})
	}
}

func TestCMDBCollectEntityPagePreservesTransportSafeguards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		max    int64
		kind   error
	}{
		{name: "status", status: 502, body: "secret", kind: ErrCMDBHTTP},
		{name: "redirect", status: 307, kind: ErrCMDBHTTP},
		{name: "payload limit", status: 200, body: strings.Repeat("x", 256), max: 128, kind: ErrCMDBResponse},
		{name: "invalid json", status: 200, body: "{", kind: ErrCMDBResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			conf := cmdbTestConfig(server.URL, map[string]string{"Authorization": "Bearer secret"})
			if tc.max > 0 {
				conf.Set("inventory.cmdb.sources.primary.max_response_bytes", tc.max)
			}
			collector, ok := any(NewCMDBWithClient(conf, server.Client())).(EntityPageCollector)
			require.True(t, ok)
			_, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityDataCenter, 0)
			require.ErrorIs(t, err, tc.kind)
			require.NotContains(t, err.Error(), "secret")
			require.False(t, called)
		})
	}
}

func TestCMDBCollectEntityPageRejectsUnsupportedEntityAndOffset(t *testing.T) {
	collector, ok := any(NewCMDB(cmdbTestConfig("https://cmdb.example.test", nil))).(EntityPageCollector)
	require.True(t, ok)
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityPod, "unknown"} {
		_, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, entity, 0)
		require.ErrorIs(t, err, ErrCMDBConfig)
	}
	_, err := collector.CollectEntityPage(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityDataCenter, -1)
	require.ErrorIs(t, err, ErrCMDBConfig)
	_, err = collector.CoversFullSource(model.Source{ConfigRef: "primary"}, model.EntityPod)
	require.ErrorIs(t, err, ErrCMDBConfig)
}

func TestCMDBSourceTimeNormalizesOnlyKnownPlaceholders(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  any
	}{
		{value: "", want: nil},
		{value: "invalid", want: nil},
		{value: "0000-00-00 00:00:00", want: nil},
		{value: "1970-01-01T00:00:00Z", want: nil},
		{value: "2000-01-01 00:00:00", want: nil},
		{value: "2000-01-01T00:00:00+08:00", want: nil},
		{value: "2000-01-01 12:00:00", want: time.Date(2000, 1, 1, 4, 0, 0, 0, time.UTC)},
		{value: "2026-09-15 23:01:33", want: time.Date(2026, 9, 15, 15, 1, 33, 0, time.UTC)},
		{value: "2026-09-15T23:01:33.343123456+08:00", want: time.Date(2026, 9, 15, 15, 1, 33, 343123000, time.UTC)},
	} {
		t.Run(tc.value, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			require.NoError(t, err)
			require.Equal(t, tc.want, cmdbSourceTime(encoded))
		})
	}
}

func TestCMDBDevelopmentExampleKeepsEntityIDCFilters(t *testing.T) {
	conf := cmdbTestConfig("https://cmdb.example.test", nil)
	conf.SetConfigFile("../../config/local.example.yml")
	require.NoError(t, conf.ReadInConfig())
	collector := NewCMDB(conf)
	source := model.Source{ConfigRef: "example"}
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityDataCenter, model.EntityCabinet} {
		conf.Set("inventory.cmdb.sources.example.entity_type", string(entity))
		selected, err := collector.SelectedEntity(source)
		require.NoError(t, err)
		require.Equal(t, entity, selected)
		full, err := collector.CoversFullSource(source, entity)
		require.NoError(t, err)
		require.False(t, full)
		condition, err := collector.conditionFor(source, entity)
		require.NoError(t, err)
		key := "idc_id"
		if entity == model.EntityDataCenter {
			key = "inst_id"
		}
		require.EqualValues(t, 1030, condition[key])
		require.Len(t, condition, 1)
	}
}

func entityResponse(items string, total int) string {
	return fmt.Sprintf(`{"result":true,"error_code":0,"data":{"count":%d,"info":[%s]}}`, total, items)
}
