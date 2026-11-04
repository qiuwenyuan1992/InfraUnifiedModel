package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type spatialFixture struct {
	items  map[string][]map[string]any
	mutate func(string, int, map[string]any)
}

func spatialTestCollector(t *testing.T, f *spatialFixture) (*CMDB, *viper.Viper) {
	t.Helper()
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Fields    []string
			Page      struct{ Start, Limit int }
			Condition map[string]any
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		object := r.URL.Path[len("/pub/api/v3/find/instance/object/"):]
		switch object {
		case "device_view":
			require.Equal(t, map[string]any{"idc_id": float64(7), "parent_type_id": float64(52)}, req.Condition)
			require.Equal(t, append(append([]string{}, deviceViewFields...), "idc_id"), req.Fields)
		case "cabinet":
			require.Equal(t, map[string]any{"idc_id": float64(7)}, req.Condition)
			require.Equal(t, cabinetFields, req.Fields)
		case "idc":
			require.Equal(t, map[string]any{"inst_id": float64(7)}, req.Condition)
			require.Equal(t, dataCenterFields, req.Fields)
		case "pod":
			require.Empty(t, req.Condition)
			require.Equal(t, podFields, req.Fields)
		default:
			t.Errorf("unexpected object %s", object)
		}
		if req.Page.Start == 0 {
			reads[object]++
		}
		items := f.items[object]
		if items == nil {
			items = []map[string]any{}
		}
		end := min(req.Page.Start+req.Page.Limit, len(items))
		payload := map[string]any{"count": len(items), "info": items[req.Page.Start:end]}
		if f.mutate != nil {
			f.mutate(object, reads[object], payload)
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": payload}))
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"base_url", server.URL)
	conf.Set(prefix+"allow_insecure_http", true)
	conf.Set(prefix+"page_size", 1)
	conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7, "parent_type_id": 52})
	conf.Set(prefix+"cabinet_condition", map[string]any{"idc_id": 7})
	conf.Set(prefix+"data_center_condition", map[string]any{"inst_id": 7})
	return NewCMDBWithClient(conf, server.Client()), conf
}
func spatialBaseFixture() *spatialFixture {
	return &spatialFixture{items: map[string][]map[string]any{
		"device_view": {{"inst_id": 1, "device_sn": " D ", "cabinet_uuid": " C ", "idc_id": 7, "parent_type_id": 52}},
		"cabinet":     {{"inst_id": 10, "uuid": " C ", "idc_id": 7, "pod_ids": []any{20, 20, nil, 0}, "pod_id": 99, "phy_idc_id": 99}},
		"idc":         {{"inst_id": 7, "uuid": " IDC "}},
		"pod":         {{"inst_id": 20, "uuid": " P "}},
	}}
}
func TestCollectSpatialThreeCombinationsAndDeterministicDigest(t *testing.T) {
	f := spatialBaseFixture()
	c, _ := spatialTestCollector(t, f)
	s := model.Source{ID: "s", ConfigRef: "test"}
	got, err := c.CollectSpatial(context.Background(), s)
	require.NoError(t, err)
	require.Len(t, got.Relations, 3)
	require.Equal(t, 2, got.Objects)
	require.Zero(t, got.Skipped)
	combos := map[string]bool{}
	for _, r := range got.Relations {
		require.NoError(t, model.ValidateRelation(r))
		require.Equal(t, "s", r.Identity.From.SourceID)
		require.Equal(t, "s", r.Identity.To.SourceID)
		combos[string(r.Identity.From.EntityType)+"/"+string(r.Identity.Kind)+"/"+string(r.Identity.To.EntityType)] = true
	}
	require.Equal(t, map[string]bool{"device/located_in/cabinet": true, "cabinet/located_in/data_center": true, "cabinet/tagged_with/pod": true}, combos)
	f.items["cabinet"][0]["pod_ids"] = []any{nil, 20, 0, 20}
	again, err := c.CollectSpatial(context.Background(), s)
	require.NoError(t, err)
	require.Equal(t, got.SourceDigest, again.SourceDigest)
	f.items["pod"] = append(f.items["pod"], map[string]any{"inst_id": 21, "uuid": "unused"})
	changed, err := c.CollectSpatial(context.Background(), s)
	require.NoError(t, err)
	require.NotEqual(t, got.SourceDigest, changed.SourceDigest)
}
func TestCollectSpatialMissingAndAmbiguousReferences(t *testing.T) {
	tests := []struct {
		name   string
		change func(*spatialFixture)
		edges  int
		reason string
	}{
		{"empty cabinet reference forbids numeric fallback", func(f *spatialFixture) {
			delete(f.items["device_view"][0], "cabinet_uuid")
			f.items["device_view"][0]["cabinet_id"] = 10
		}, 2, "empty_cabinet_reference"},
		{"empty IDC forbids physical fallback", func(f *spatialFixture) {
			delete(f.items["cabinet"][0], "idc_id")
			f.items["cabinet"][0]["phy_idc_id"] = 7
		}, 2, "empty_data_center_reference"},
		{"ambiguous IDC ID", func(f *spatialFixture) {
			f.items["idc"] = append(f.items["idc"], map[string]any{"inst_id": 7, "uuid": "other"})
		}, 2, "ambiguous_data_center"},
		{"missing cabinet", func(f *spatialFixture) { f.items["device_view"][0]["cabinet_uuid"] = "unknown" }, 2, "missing_cabinet"},
		{"missing IDC", func(f *spatialFixture) { f.items["idc"] = nil }, 2, "missing_data_center"},
		{"missing POD", func(f *spatialFixture) { f.items["pod"] = nil }, 2, "missing_pod"},
		{"ambiguous POD UUID", func(f *spatialFixture) {
			f.items["pod"] = append(f.items["pod"], map[string]any{"inst_id": 21, "uuid": "P"})
		}, 2, "ambiguous_pod"},
		{"ambiguous POD ID", func(f *spatialFixture) {
			f.items["pod"] = append(f.items["pod"], map[string]any{"inst_id": 20, "uuid": "other"})
		}, 2, "ambiguous_pod"},
		{"ambiguous cabinet UUID", func(f *spatialFixture) {
			f.items["cabinet"] = append(f.items["cabinet"], map[string]any{"inst_id": 11, "uuid": "C", "idc_id": 7, "pod_ids": []int{20}})
		}, 0, "ambiguous_cabinet"},
		{"no single POD fallback", func(f *spatialFixture) {
			delete(f.items["cabinet"][0], "pod_ids")
			f.items["cabinet"][0]["pod_id"] = 20
		}, 2, "empty_pod_references"},
		{"empty device SN", func(f *spatialFixture) { delete(f.items["device_view"][0], "device_sn") }, 2, "missing_device_sn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := spatialBaseFixture()
			tt.change(f)
			c, _ := spatialTestCollector(t, f)
			got, err := c.CollectSpatial(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			require.NoError(t, err)
			require.Len(t, got.Relations, tt.edges)
			require.Positive(t, got.Reasons[tt.reason])
		})
	}
}
func TestCollectSpatialRejectsMalformedPaginationAndScope(t *testing.T) {
	tests := []struct {
		name   string
		change func(*spatialFixture, *viper.Viper)
	}{
		{"malformed pod array", func(f *spatialFixture, _ *viper.Viper) { f.items["cabinet"][0]["pod_ids"] = "20,21" }},
		{"malformed UUID", func(f *spatialFixture, _ *viper.Viper) { f.items["pod"][0]["uuid"] = 20 }},
		{"missing directory identity", func(f *spatialFixture, _ *viper.Viper) { delete(f.items["idc"][0], "uuid") }},
		{"descending IDs", func(f *spatialFixture, _ *viper.Viper) {
			f.items["pod"] = append(f.items["pod"], map[string]any{"inst_id": 19, "uuid": "other"})
		}},
		{"count drift", func(f *spatialFixture, _ *viper.Viper) {
			f.items["pod"] = append(f.items["pod"], map[string]any{"inst_id": 21, "uuid": "other"})
			f.mutate = func(o string, _ int, p map[string]any) {
				if o == "pod" && len(p["info"].([]map[string]any)) > 0 && p["info"].([]map[string]any)[0]["inst_id"] == 21 {
					p["count"] = 3
				}
			}
		}},
		{"scope mismatch", func(_ *spatialFixture, c *viper.Viper) {
			c.Set("inventory.cmdb.sources.test.cabinet_condition.idc_id", 8)
		}},
		{"unsupported narrowing", func(_ *spatialFixture, c *viper.Viper) {
			c.Set("inventory.cmdb.sources.test.device_condition.device_type_id", 58)
		}},
		{"cross IDC cabinet", func(f *spatialFixture, _ *viper.Viper) { f.items["cabinet"][0]["idc_id"] = 8 }},
		{"cross IDC device", func(f *spatialFixture, _ *viper.Viper) { f.items["device_view"][0]["idc_id"] = 8 }},
		{"missing device IDC", func(f *spatialFixture, _ *viper.Viper) { delete(f.items["device_view"][0], "idc_id") }},
		{"wrong filtered device category", func(f *spatialFixture, _ *viper.Viper) { f.items["device_view"][0]["parent_type_id"] = 54 }},
		{"missing filtered device category", func(f *spatialFixture, _ *viper.Viper) { delete(f.items["device_view"][0], "parent_type_id") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := spatialBaseFixture()
			c, conf := spatialTestCollector(t, f)
			tt.change(f, conf)
			_, err := c.CollectSpatial(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			require.Error(t, err)
		})
	}
}
func TestCollectSpatialDuplicateRecordsDeduplicateAndMissingArrayChangesDigest(t *testing.T) {
	f := spatialBaseFixture()
	f.items["device_view"] = append(f.items["device_view"], map[string]any{"inst_id": 2, "device_sn": "D", "cabinet_uuid": "C", "idc_id": 7, "parent_type_id": 52})
	c, _ := spatialTestCollector(t, f)
	s := model.Source{ID: "s", ConfigRef: "test"}
	got, err := c.CollectSpatial(context.Background(), s)
	require.NoError(t, err)
	require.Len(t, got.Relations, 3)
	delete(f.items["cabinet"][0], "pod_ids")
	missing, err := c.CollectSpatial(context.Background(), s)
	require.NoError(t, err)
	f.items["cabinet"][0]["pod_ids"] = []int{}
	empty, err := c.CollectSpatial(context.Background(), s)
	require.NoError(t, err)
	require.NotEqual(t, missing.SourceDigest, empty.SourceDigest)
}
