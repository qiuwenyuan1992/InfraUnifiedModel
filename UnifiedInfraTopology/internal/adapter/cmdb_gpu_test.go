package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
)

func gpuRaw(t *testing.T, item map[string]any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGPUCollectionPagingDeduplicationAndConflicts(t *testing.T) {
	for _, scenario := range []string{"duplicate_cards", "duplicate_uplinks", "distinct_uplink_UUID", "conflicting_owner", "conflicting_properties", "conflicting_uplink", "card_order", "uplink_order", "server_order", "count_change", "missing_inst_id", "ambiguous_parts"} {
		t.Run(scenario, func(t *testing.T) {
			requests := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Page      struct{ Start, Limit int }
					Condition map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				object := r.URL.Path[len("/pub/api/v3/find/instance/object/"):]
				requests[object]++
				var items []map[string]any
				switch object {
				case "device_view":
					if request.Condition["parent_type_id"] == float64(52) {
						items = []map[string]any{{"inst_id": 1, "device_sn": "server-a", "parent_type_id": 52, "device_type_id": 58}}
						if scenario == "conflicting_owner" || scenario == "server_order" {
							items = append(items, map[string]any{"inst_id": 2, "device_sn": "server-b", "parent_type_id": 52, "device_type_id": 58})
							if scenario == "server_order" {
								items[0]["inst_id"] = 3
							}
						}
					} else {
						items = []map[string]any{{"inst_id": 1, "device_sn": "tor-a", "parent_type_id": 54}}
					}
				case "server_gpu":
					items = []map[string]any{{"inst_id": 1, "uuid": "card-a", "device_sn": request.Condition["device_sn"], "parts_sn": "parts-a"}}
					if scenario == "duplicate_cards" || scenario == "card_order" || scenario == "count_change" || scenario == "ambiguous_parts" {
						duplicate := map[string]any{"inst_id": 2, "uuid": "card-a", "device_sn": request.Condition["device_sn"], "parts_sn": "parts-a"}
						if scenario == "card_order" {
							duplicate["inst_id"] = 1
						}
						if scenario == "ambiguous_parts" {
							duplicate["uuid"] = "card-b"
						}
						items = append(items, duplicate)
					}
					if scenario == "missing_inst_id" {
						delete(items[0], "inst_id")
					}
					if scenario == "conflicting_properties" {
						items = append(items, map[string]any{"inst_id": 2, "device_sn": "server-a", "parts_sn": "parts-a", "slot": "different"}, map[string]any{"inst_id": 3, "device_sn": "server-a", "parts_sn": "parts-a"})
					}
					if (scenario == "conflicting_owner" || scenario == "conflicting_properties") && request.Condition["device_sn"] == "server-a" {
						items = append(items, map[string]any{"inst_id": 4, "device_sn": "server-a", "parts_sn": "safe-sn"})
					}
				case "gpu_uplink":
					expectedSN := "parts-a"
					if scenario == "conflicting_owner" || scenario == "conflicting_properties" {
						expectedSN = "safe-sn"
					}
					if len(request.Condition) != 1 || request.Condition["gpu_sn"] != expectedSN {
						t.Errorf("conflicting SN must not query uplinks: %v", request.Condition)
					}
					items = []map[string]any{{"inst_id": 1, "uuid": "link-a", "device_sn": "server-a", "gpu_sn": expectedSN, "tor_sn": "tor-a", "tor_port": "Ethernet1"}}
					if scenario == "duplicate_uplinks" || scenario == "conflicting_uplink" || scenario == "uplink_order" || scenario == "distinct_uplink_UUID" {
						duplicate := map[string]any{}
						for key, value := range items[0] {
							duplicate[key] = value
						}
						duplicate["inst_id"] = 2
						if scenario == "conflicting_uplink" {
							duplicate["gpu_port"] = "different"
						}
						if scenario == "uplink_order" {
							duplicate["inst_id"] = 1
						}
						if scenario == "distinct_uplink_UUID" {
							duplicate["uuid"] = "link-b"
						}
						items = append(items, duplicate)
					}
				case "port_view":
					items = []map[string]any{{"inst_id": 1, "port_uuid": "port-a", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": "tor-a"}}
				default:
					t.Errorf("unexpected object %s", object)
				}
				count := len(items)
				if scenario == "count_change" && object == "server_gpu" && request.Page.Start > 0 {
					count++
				}
				start := min(request.Page.Start, len(items))
				end := min(start+request.Page.Limit, len(items))
				_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": count, "info": items[start:end]}})
			}))
			defer server.Close()
			conf := viper.New()
			prefix := "inventory.cmdb.sources.test."
			conf.Set(prefix+"base_url", server.URL)
			conf.Set(prefix+"allow_insecure_http", true)
			conf.Set(prefix+"page_size", 1)
			conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7})
			result, err := NewCMDBWithClient(conf, server.Client()).CollectGPU(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			if scenario == "conflicting_owner" || scenario == "conflicting_properties" {
				count := 2
				if scenario == "conflicting_properties" {
					count = 3
				}
				if err != nil || len(result.Cards) != 1 || result.Cards[0].Identity.StableID != "safe-sn" || len(result.Uplinks) != 1 || result.Uplinks[0].Identity.From.StableID != "safe-sn" || result.Skipped != count || result.Reasons["gpu_conflicting_parts_sn"] != count || result.Reasons["gpu_conflicting_parts_sn_groups"] != 1 || requests["gpu_uplink"] != 1 {
					t.Fatalf("conflicting SN exclusion: %+v err %v requests %v", result, err, requests)
				}
			}
			valid := scenario == "conflicting_owner" || scenario == "conflicting_properties" || scenario == "duplicate_cards" || scenario == "duplicate_uplinks" || scenario == "distinct_uplink_UUID" || scenario == "ambiguous_parts"
			if valid && err != nil {
				t.Fatal(err)
			}
			if !valid && err == nil {
				t.Fatalf("unsafe source accepted %+v", result)
			}
			if scenario == "duplicate_cards" && (len(result.Cards) != 1 || requests["server_gpu"] != 2) {
				t.Fatalf("card pagination/dedup %+v requests %v", result, requests)
			}
			if scenario == "duplicate_uplinks" && (len(result.Uplinks) != 1 || requests["gpu_uplink"] != 2) {
				t.Fatalf("link pagination/dedup %+v requests %v", result, requests)
			}
			if scenario == "distinct_uplink_UUID" && len(result.Uplinks) != 2 {
				t.Fatalf("same pair lost distinct source UUID %+v", result)
			}
			if scenario == "ambiguous_parts" && (len(result.Cards) != 1 || len(result.Uplinks) != 1 || result.Cards[0].Identity.StableID != "parts-a") {
				t.Fatalf("CMDB UUID must not split one GPU SN %+v", result)
			}
		})
	}
}

func TestGPUCardAuthoritativeFields(t *testing.T) {
	card, err := cmdbGPUCard(gpuRaw(t, map[string]any{"uuid": "card-a", "device_sn": "server-a", "parts_sn": "parts-a", "index": "07", "VRAM": "80G", "maintenance_status": 1, "maintenance_end": "2029-07-29", "conn_ports": []any{" ib8 ", "ib8", "", nil}, "id": "ignored"}), "s", "server-a")
	if err != nil {
		t.Fatal(err)
	}
	p := card.Properties
	if p["card_index"] != "07" || p["vram"] != "80G" || p["maintenance_status"] != "1" || !p["maintenance_end"].(time.Time).Equal(time.Date(2029, 7, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("properties %+v", p)
	}
	if ports := p["conn_ports"].([]string); len(ports) != 1 || ports[0] != "ib8" {
		t.Fatalf("ports %v", ports)
	}
	for _, key := range []string{"parts_sn", "uuid", "id", "device_sn", "inst_id", "name"} {
		if _, ok := p[key]; ok {
			t.Fatalf("unexpected %s", key)
		}
	}
}

func TestGPUCardRejectsIdentityAndWrongSourceTypes(t *testing.T) {
	for _, patch := range []map[string]any{{"parts_sn": ""}, {"device_sn": "other"}, {"index": 7}, {"conn_ports": []any{8}}, {"maintenance_end": "2029-02-30"}, {"maintenance_status": "1"}} {
		item := map[string]any{"uuid": "card-a", "parts_sn": "parts-a", "device_sn": "server-a"}
		for k, v := range patch {
			item[k] = v
		}
		if _, err := cmdbGPUCard(gpuRaw(t, item), "s", "server-a"); err == nil {
			t.Fatalf("accepted %+v", patch)
		}
	}
}

func TestGPUCardUsesSNWithoutCMDBUUID(t *testing.T) {
	item := map[string]any{"device_sn": "server-a", "parts_sn": "parts-a"}
	card, err := cmdbGPUCard(gpuRaw(t, item), "s", "server-a")
	if err != nil {
		t.Fatal(err)
	}
	if card.Identity.StableID != "parts-a" {
		t.Fatalf("wrong GPU identity: %+v", card.Identity)
	}
	item["uuid"] = "changed-cmdb-uuid"
	again, err := cmdbGPUCard(gpuRaw(t, item), "s", "server-a")
	if err != nil || card.Identity != again.Identity {
		t.Fatalf("CMDB UUID changed GPU identity: %v", err)
	}
}

func TestGPUCollectionQueriesEachSNAndRejectsRepeatedWrongEndpoint(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			queried := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Condition map[string]any
					Page      struct{ Start, Limit int }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				object := strings.TrimPrefix(r.URL.Path, "/pub/api/v3/find/instance/object/")
				var items []map[string]any
				switch object {
				case "device_view":
					if req.Condition["parent_type_id"] == float64(52) {
						items = []map[string]any{{"inst_id": 1, "device_sn": "server-a", "parent_type_id": 52, "device_type_id": 58}}
					} else {
						items = []map[string]any{{"inst_id": 1, "device_sn": "tor-a", "parent_type_id": 54}}
					}
				case "server_gpu":
					items = []map[string]any{{"inst_id": 1, "device_sn": "server-a", "parts_sn": "parts-a"}, {"inst_id": 2, "device_sn": "server-a", "parts_sn": "parts-b"}, {"inst_id": 3, "device_sn": "server-a", "uuid": "ignored-no-sn"}}
				case "gpu_uplink":
					sn, _ := req.Condition["gpu_sn"].(string)
					queried[sn]++
					if sn != "parts-a" && sn != "parts-b" {
						t.Errorf("unexpected GPU SN condition %v", req.Condition)
					}
					returned := sn
					if wrong {
						returned = "parts-a"
					}
					items = []map[string]any{{"inst_id": 1, "uuid": "link-" + returned, "device_sn": "server-a", "gpu_sn": returned, "tor_sn": "tor-a", "tor_port": "Ethernet1"}}
				case "port_view":
					items = []map[string]any{{"inst_id": 1, "port_uuid": "port-a", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": "tor-a"}}
				default:
					t.Errorf("unexpected object %s", object)
				}
				start := min(req.Page.Start, len(items))
				end := min(start+req.Page.Limit, len(items))
				_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
			}))
			defer server.Close()
			conf := viper.New()
			prefix := "inventory.cmdb.sources.test."
			conf.Set(prefix+"base_url", server.URL)
			conf.Set(prefix+"allow_insecure_http", true)
			conf.Set(prefix+"page_size", 1)
			conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7})
			result, err := NewCMDBWithClient(conf, server.Client()).CollectGPU(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			if wrong {
				if err == nil {
					t.Fatal("repeated link bypassed queried SN validation")
				}
			} else {
				if err != nil || len(result.Cards) != 2 || len(result.Uplinks) != 2 || result.Skipped != 1 || result.Reasons["gpu_missing_parts_sn"] != 1 || result.Objects != 5 {
					t.Fatalf("result %+v err %v", result, err)
				}
			}
			if len(queried) != 2 || queried["parts-a"] != 1 || queried["parts-b"] != 1 {
				t.Fatalf("wrong per-card queries: %v", queried)
			}
		})
	}
}
