package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
)

func linksTestCMDB(t *testing.T, change func(map[string]any), duplicate bool) *CMDB {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req cmdbEntityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Page.Sort != "inst_id" {
			t.Errorf("sort %s", req.Page.Sort)
		}
		var items []map[string]any
		if r.URL.Path == deviceViewEndpoint {
			if req.Condition["idc_id"] != nil {
				if req.Condition["idc_id"] != float64(7) || req.Condition["parent_type_id"] != float64(54) {
					t.Errorf("discovery %v", req.Condition)
				}
				items = []map[string]any{{"inst_id": 1, "device_sn": "a", "parent_type_id": 54}, {"inst_id": 2, "device_sn": "b", "parent_type_id": 54}}
			} else {
				sn := req.Condition["device_sn"]
				items = []map[string]any{{"inst_id": 3, "device_sn": sn, "parent_type_id": 54}}
				if duplicate {
					items = append(items, map[string]any{"inst_id": 4, "device_sn": sn, "parent_type_id": 54})
				}
			}
		} else {
			if req.Condition["port_type"] != "physics" || req.Condition["sn"] == nil || req.Condition["idc_id"] != nil {
				t.Errorf("port condition %v", req.Condition)
			}
			sn := req.Condition["sn"].(string)
			remote, localPort, remotePort := "b", "z-port", "a-port"
			if sn == "b" {
				remote, localPort, remotePort = "a", "a-port", "z-port"
			}
			item := map[string]any{"inst_id": 10, "port_uuid": localPort, "uuid": "view-only", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": sn, "remote_device_sn": remote, "remote_interface_uuid": remotePort, "remote_interface_type": "physics"}
			if change != nil {
				change(item)
			}
			items = []map[string]any{item}
		}
		start := min(req.Page.Start, len(items))
		end := min(start+req.Page.Limit, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.page_size", 1)
	conf.Set("inventory.cmdb.sources.test.interface_condition", map[string]any{"idc_id": 7})
	return NewCMDBWithClient(conf, server.Client())
}

func TestLinksToExactUUIDAndCanonicalOwnerSwap(t *testing.T) {
	c := linksTestCMDB(t, nil, false)
	source := model.Source{ID: "s", ConfigRef: "test"}
	first, err := c.CollectLinksToPage(context.Background(), source, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.CollectLinksToPage(context.Background(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.NextOffset != 1 || first.Total != 2 || first.Done || !second.Done || len(first.Links) != 1 || len(second.Links) != 1 {
		t.Fatalf("pages %+v %+v", first, second)
	}
	a, b := first.Links[0], second.Links[0]
	if a != b || a.Identity.From.StableID != "a-port" || a.FromDeviceSN != "b" || a.ToDeviceSN != "a" {
		t.Fatalf("canonical owners %+v %+v", a, b)
	}
}

func TestLinksToFailClosedResolution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(map[string]any)
		duplicate bool
		reason    string
	}{
		{"no remote", func(p map[string]any) { p["remote_device_sn"] = ""; p["remote_interface_uuid"] = "" }, false, "no_remote"},
		{"view UUID", func(p map[string]any) { p["remote_interface_uuid"] = "view-only" }, false, "unresolved_port"},
		{"virtual remote", func(p map[string]any) { p["remote_interface_type"] = "virtual" }, false, "nonphysical_remote"},
		{"ambiguous owner", nil, true, "nonnetwork_remote"},
		{"self loop", func(p map[string]any) {
			p["remote_interface_uuid"] = p["port_uuid"]
			p["remote_device_sn"] = p["local_device_sn"]
		}, false, "self_loop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := linksTestCMDB(t, tc.change, tc.duplicate)
			page, err := c.CollectLinksToPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
			if err != nil || len(page.Links) != 0 || page.Skipped != 1 || page.Reasons[tc.reason] != 1 {
				t.Fatalf("page %+v err %v", page, err)
			}
		})
	}
}

func TestLinksToRejectsDuplicateSourcePortUUID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req cmdbEntityRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		items := []map[string]any{{"inst_id": 1, "device_sn": "a", "parent_type_id": 54}}
		if r.URL.Path != deviceViewEndpoint {
			items = []map[string]any{{"inst_id": 10, "port_uuid": "same", "port_type": "physics", "local_device_sn": "a"}, {"inst_id": 11, "port_uuid": "same", "port_type": "physics", "local_device_sn": "a"}}
		}
		start := min(req.Page.Start, len(items))
		end := min(start+req.Page.Limit, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	defer server.Close()
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.interface_condition", map[string]any{"idc_id": 7})
	_, err := NewCMDB(conf).CollectLinksToPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
	if err == nil {
		t.Fatal("duplicate source UUID accepted")
	}
}

func TestLinksToRejectsPartitionOwnerMismatch(t *testing.T) {
	c := linksTestCMDB(t, func(p map[string]any) { p["local_device_sn"] = "wrong" }, false)
	if _, err := c.CollectLinksToPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0); err == nil {
		t.Fatal("accepted wrong partition owner")
	}
}
