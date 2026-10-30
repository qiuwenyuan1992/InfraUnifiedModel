package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
)

func uplinkTestCMDB(t *testing.T, servers []map[string]any, ports []map[string]any, mutate func(string, *cmdbEntityRequest), networkRecords ...map[string]any) *CMDB {
	t.Helper()
	handler := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req cmdbEntityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if mutate != nil {
			mutate(r.URL.Path, &req)
		}
		var items []map[string]any
		if r.URL.Path == deviceViewEndpoint {
			if req.Condition["parent_type_id"] == float64(52) {
				if !reflect.DeepEqual(req.Fields, []string{"inst_id", "device_sn", "parent_type_id", "server_tor_ports"}) {
					t.Errorf("server fields: %v", req.Fields)
				}
				if req.Condition["idc_id"] != float64(7) {
					t.Errorf("server discovery lost IDC filter: %v", req.Condition)
				}
				items = servers
			} else {
				if req.Condition["device_sn"] != "tor-a" || req.Condition["parent_type_id"] != float64(54) {
					t.Errorf("network condition: %v", req.Condition)
				}
				if _, restricted := req.Condition["idc_id"]; restricted {
					t.Errorf("SN-based ToR lookup must not restrict IDC: %v", req.Condition)
				}
				items = []map[string]any{{"inst_id": 10, "device_sn": "tor-a", "parent_type_id": 54}}
				if len(networkRecords) > 0 {
					items = networkRecords
				}
			}
		} else {
			if r.URL.Path != "/pub/api/v3/find/instance/object/port_view" || req.Condition["sn"] != "tor-a" || req.Condition["port_type"] != "physics" {
				t.Errorf("unindexed port request: %s %v", r.URL.Path, req.Condition)
			}
			if _, restricted := req.Condition["idc_id"]; restricted {
				t.Errorf("SN-based port lookup must not restrict IDC: %v", req.Condition)
			}
			if req.Page.Sort != "inst_id" {
				t.Errorf("sort: %s", req.Page.Sort)
			}
			items = ports
		}
		end := min(req.Page.Start+req.Page.Limit, len(items))
		start := min(req.Page.Start, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	t.Cleanup(handler.Close)
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", handler.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.page_size", 1)
	conf.Set("inventory.cmdb.sources.test.device_condition", map[string]any{"idc_id": 7})
	return NewCMDBWithClient(conf, handler.Client())
}

func uplinkServer(raw any) map[string]any {
	return map[string]any{"inst_id": 1, "device_sn": "server-a", "parent_type_id": 52, "server_tor_ports": raw}
}
func uplinkManifest(ports ...string) any { return []map[string]any{{"sn": "tor-a", "ports": ports}} }

func TestCollectServerUplinkPageRequiresActualNetworkToR(t *testing.T) {
	for _, device := range []map[string]any{
		{"inst_id": 10, "device_sn": "tor-a", "parent_type_id": 52},
		{"inst_id": 10, "device_sn": "wrong-tor", "parent_type_id": 54},
		{"inst_id": 0, "device_sn": "tor-a", "parent_type_id": 54},
	} {
		t.Run("unresolved-tor", func(t *testing.T) {
			portRequests := 0
			cmdb := uplinkTestCMDB(t, []map[string]any{uplinkServer(uplinkManifest("Ethernet1"))}, nil, func(path string, _ *cmdbEntityRequest) {
				if path != deviceViewEndpoint {
					portRequests++
				}
			}, device)
			page, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
			if err != nil || len(page.Links) != 0 || page.Skipped != 1 || portRequests != 0 {
				t.Fatalf("page %+v requests %d err %v", page, portRequests, err)
			}
		})
	}
}

func TestCollectServerUplinkPageRejectsGloballyAmbiguousToRSN(t *testing.T) {
	portRequests := 0
	cmdb := uplinkTestCMDB(t, []map[string]any{uplinkServer(uplinkManifest("Ethernet1"))}, nil, func(path string, _ *cmdbEntityRequest) {
		if path != deviceViewEndpoint {
			portRequests++
		}
	},
		map[string]any{"inst_id": 10, "device_sn": "tor-a", "parent_type_id": 54, "idc_id": 7},
		map[string]any{"inst_id": 11, "device_sn": "tor-a", "parent_type_id": 54, "idc_id": 8})
	page, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
	if err != nil || len(page.Links) != 0 || page.Skipped != 1 || portRequests != 0 {
		t.Fatalf("ambiguous global SN accepted: page=%+v ports=%d err=%v", page, portRequests, err)
	}
}

func TestCollectServerUplinkPageCachesToRWithinServerPage(t *testing.T) {
	requests := 0
	first := uplinkServer(uplinkManifest("Ethernet1"))
	second := uplinkServer(uplinkManifest("Ethernet1"))
	second["inst_id"], second["device_sn"] = 2, "server-b"
	cmdb := uplinkTestCMDB(t, []map[string]any{first, second}, []map[string]any{{"inst_id": 10, "port_uuid": "port-a", "port_name": "Ethernet1", "port_type": "physics"}}, func(path string, _ *cmdbEntityRequest) {
		if path == "/pub/api/v3/find/instance/object/port_view" {
			requests++
		}
	})
	cmdb.conf.Set("inventory.cmdb.sources.test.page_size", 20000)
	page, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
	if err != nil || requests != 1 || page.Total != 2 || page.NextOffset != 2 || len(page.Links) != 2 || page.FirstInstanceID != 1 || page.LastInstanceID != 2 {
		t.Fatalf("page %+v requests %d err %v", page, requests, err)
	}
}

func TestCollectServerUplinkPageRejectsHTTPBoundary(t *testing.T) {
	for _, response := range []string{
		`{"result":true,"error_code":0,"data":{"count":1,"info":[]}}`,
		`{"result":true,"error_code":0,"data":{"count":0,"info":null}}`,
		`{"result":false,"error_code":1,"data":{"count":0,"info":[]}}`,
		`invalid`,
	} {
		t.Run("boundary", func(t *testing.T) {
			cmdb := uplinkTestCMDB(t, nil, nil, nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
			t.Cleanup(server.Close)
			cmdb.conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
			_, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
			if !errors.Is(err, ErrCMDBResponse) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestCollectServerUplinkPageIndexedResolution(t *testing.T) {
	cmdb := uplinkTestCMDB(t, []map[string]any{uplinkServer(uplinkManifest("Ethernet1", "Ethernet2"))}, []map[string]any{
		{"inst_id": 11, "port_uuid": "physical-1", "uuid": "view-uuid", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": "tor-a", "remote_device_sn": "server-z"},
		{"inst_id": 12, "port_uuid": "physical-2", "port_name": "Ethernet2", "port_type": "physics"},
	}, nil)
	page, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.NextOffset != 1 || !page.Done || len(page.Links) != 2 || page.Skipped != 0 {
		t.Fatalf("page: %+v", page)
	}
	for i, link := range page.Links {
		if link.From.StableID != "server-a" || link.To.StableID != []string{"physical-1", "physical-2"}[i] || page.Targets[link.To.StableID] != "tor-a" || link.Kind != model.RelationServerUplink {
			t.Fatalf("link: %+v targets %v", link, page.Targets)
		}
		if _, err := link.Rank(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCollectServerUplinkPageAmbiguousAndEmpty(t *testing.T) {
	cmdb := uplinkTestCMDB(t, []map[string]any{uplinkServer(uplinkManifest("dup", "blank", "missing"))}, []map[string]any{
		{"inst_id": 11, "port_uuid": "a", "port_name": "dup", "port_type": "physics"},
		{"inst_id": 12, "port_uuid": "b", "port_name": "dup", "port_type": "physics"},
		{"inst_id": 13, "port_uuid": "", "port_name": "blank", "port_type": "physics"},
	}, nil)
	page, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
	if err != nil || len(page.Links) != 0 || page.Skipped != 3 {
		t.Fatalf("page %+v err %v", page, err)
	}
	cmdb = uplinkTestCMDB(t, []map[string]any{uplinkServer([]any{nil, map[string]any{}, map[string]any{"sn": "", "ports": []string{""}}})}, nil, nil)
	page, err = cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
	if err != nil || len(page.Links) != 0 || page.Skipped != 3 {
		t.Fatalf("empty %+v err %v", page, err)
	}
}

func TestCollectServerUplinkPageRejectsMalformedAndClassification(t *testing.T) {
	for _, raw := range []any{"bad", []any{1}, []any{map[string]any{"sn": 1}}, []any{map[string]any{"sn": "tor-a", "ports": "bad"}}, []any{map[string]any{"sn": "tor-a", "ports": []any{1}}}} {
		t.Run("malformed", func(t *testing.T) {
			cmdb := uplinkTestCMDB(t, []map[string]any{uplinkServer(raw)}, nil, nil)
			_, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0)
			if !errors.Is(err, ErrCMDBRecord) {
				t.Fatalf("err %v", err)
			}
		})
	}
	server := uplinkServer(nil)
	server["parent_type_id"] = 54
	cmdb := uplinkTestCMDB(t, []map[string]any{server}, nil, nil)
	if _, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0); !errors.Is(err, ErrCMDBRecord) {
		t.Fatalf("classification: %v", err)
	}
	cmdb.conf.Set("inventory.cmdb.sources.test.device_condition", map[string]any{"idc_id": 0})
	if _, err := cmdb.CollectServerUplinkPage(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, 0); !errors.Is(err, ErrCMDBConfig) {
		t.Fatalf("idc: %v", err)
	}
}
