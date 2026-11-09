package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
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
	for _, scenario := range []string{"duplicate_cards", "duplicate_uplinks", "distinct_uplink_UUID", "conflicting_owner", "conflicting_properties", "only_conflicting_cards", "conflicting_uplink", "conflicting_filtered_uplink", "card_order", "uplink_order", "server_order", "count_change", "missing_inst_id", "ambiguous_parts"} {
		t.Run(scenario, func(t *testing.T) {
			requests := map[string]int{}
			var requestMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestMu.Lock()
				defer requestMu.Unlock()
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
					if scenario == "conflicting_properties" || scenario == "only_conflicting_cards" {
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
					if len(request.Condition) != 1 || request.Condition["device_sn"] != "server-a" {
						t.Errorf("uplinks require a server with valid cards: %v", request.Condition)
					}
					items = []map[string]any{{"inst_id": 1, "uuid": "link-a", "device_sn": "server-a", "gpu_sn": expectedSN, "tor_sn": "tor-a", "tor_port": "Ethernet1"}}
					if scenario == "conflicting_owner" || scenario == "conflicting_properties" {
						items = append(items, map[string]any{"inst_id": 2, "uuid": "excluded-link", "device_sn": "server-a", "gpu_sn": "parts-a", "tor_sn": "tor-a", "tor_port": "Ethernet1"})
					}
					if scenario == "duplicate_uplinks" || scenario == "conflicting_uplink" || scenario == "conflicting_filtered_uplink" || scenario == "uplink_order" || scenario == "distinct_uplink_UUID" {
						duplicate := map[string]any{}
						for key, value := range items[0] {
							duplicate[key] = value
						}
						duplicate["inst_id"] = 2
						if scenario == "conflicting_uplink" {
							duplicate["gpu_port"] = "different"
						}
						if scenario == "conflicting_filtered_uplink" {
							duplicate["gpu_sn"] = "unknown"
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
				// 分页场景提供真实的 1001 条记录，不依赖发现页大小触发设备分页。
				if (object == "server_gpu" && (scenario == "duplicate_cards" || scenario == "count_change")) || (object == "gpu_uplink" && scenario == "duplicate_uplinks") {
					for len(items) < 1001 {
						duplicate := map[string]any{}
						for field, value := range items[0] {
							duplicate[field] = value
						}
						duplicate["inst_id"] = len(items) + 1
						items = append(items, duplicate)
					}
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
			requestMu.Lock()
			defer requestMu.Unlock()
			if scenario == "conflicting_owner" || scenario == "conflicting_properties" {
				count := 2
				if scenario == "conflicting_properties" {
					count = 3
				}
				if err != nil || len(result.Cards) != 1 || result.Cards[0].Identity.StableID != "safe-sn" || len(result.Uplinks) != 1 || result.Uplinks[0].Identity.From.StableID != "safe-sn" || result.Skipped != count+1 || result.Reasons["gpu_conflicting_parts_sn"] != count || result.Reasons["gpu_conflicting_parts_sn_groups"] != 1 || result.Reasons["gpu_uplink_conflicting_gpu_sn"] != 1 || requests["gpu_uplink"] != 1 {
					t.Fatalf("conflicting SN exclusion: %+v err %v requests %v", result, err, requests)
				}
			}
			if scenario == "only_conflicting_cards" && (err != nil || len(result.Cards) != 0 || len(result.Uplinks) != 0 || result.Skipped != 3 || result.Reasons["gpu_conflicting_parts_sn"] != 3 || requests["gpu_uplink"] != 0) {
				t.Fatalf("excluded-only server queried or published: %+v err %v requests %v", result, err, requests)
			}
			valid := scenario == "only_conflicting_cards" || scenario == "conflicting_owner" || scenario == "conflicting_properties" || scenario == "duplicate_cards" || scenario == "duplicate_uplinks" || scenario == "distinct_uplink_UUID" || scenario == "ambiguous_parts"
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

func TestGPUCollectionBoundedParallelPagesAndDeterministicMerge(t *testing.T) {
	for _, pageSize := range []int{20, 200} {
		t.Run(fmt.Sprint(pageSize), func(t *testing.T) {
			const concurrency, serverCount = 3, 7
			var mu sync.Mutex
			active, peak, finishedCards := map[string]int{}, map[string]int{}, 0
			queries := map[string]int{}
			starts := map[string][]int{}
			gates := map[string]chan struct{}{"server_gpu": make(chan struct{}), "gpu_uplink": make(chan struct{})}
			opened := map[string]bool{}
			metadata := "original"
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
				sn, _ := req.Condition["device_sn"].(string)
				key := object + ":" + sn
				mu.Lock()
				queries[key]++
				starts[key] = append(starts[key], req.Page.Start)
				rawMetadata := metadata
				if object == "server_gpu" || object == "gpu_uplink" {
					if len(req.Condition) != 1 || req.Page.Limit != 1000 {
						t.Errorf("wrong per-server request: %+v", req)
					}
					if object == "gpu_uplink" && finishedCards != serverCount {
						t.Errorf("uplinks started before all cards: %d", finishedCards)
					}
					active[object]++
					peak[object] = max(peak[object], active[object])
					if active[object] == concurrency && !opened[object] {
						close(gates[object])
						opened[object] = true
					}
				}
				mu.Unlock()
				if gate := gates[object]; gate != nil {
					select {
					case <-gate:
					case <-time.After(200 * time.Millisecond):
					}
				}
				items := []map[string]any{}
				switch object {
				case "device_view":
					if req.Condition["parent_type_id"] == float64(52) {
						if req.Page.Limit != pageSize {
							t.Errorf("discovery page changed: %d", req.Page.Limit)
						}
						for i := 0; i < serverCount; i++ {
							items = append(items, map[string]any{"inst_id": i + 1, "device_sn": fmt.Sprintf("server-%d", i), "parent_type_id": 52, "device_type_id": 58})
						}
					} else {
						items = append(items, map[string]any{"inst_id": 1, "device_sn": "tor-a", "parent_type_id": 54})
					}
				case "server_gpu", "gpu_uplink":
					count := 2
					if sn == "server-0" {
						count = 1001
					}
					for i := 0; i < count; i++ {
						partsSN := fmt.Sprintf("%s-card-%d", sn, i)
						item := map[string]any{"inst_id": i + 1, "device_sn": sn, "parts_sn": partsSN, "raw_metadata": rawMetadata}
						if object == "gpu_uplink" {
							item = map[string]any{"inst_id": i + 1, "device_sn": sn, "gpu_sn": partsSN, "uuid": fmt.Sprintf("%s-link-%d", sn, i), "tor_sn": "tor-a", "tor_port": "Ethernet1", "raw_metadata": rawMetadata}
						}
						items = append(items, item)
					}
				case "port_view":
					items = append(items, map[string]any{"inst_id": 1, "port_uuid": "port-a", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": "tor-a"})
				default:
					t.Errorf("unexpected object %s", object)
				}
				start, end := min(req.Page.Start, len(items)), min(req.Page.Start+req.Page.Limit, len(items))
				mu.Lock()
				if object == "server_gpu" || object == "gpu_uplink" {
					active[object]--
					if object == "server_gpu" && end == len(items) {
						finishedCards++
					}
				}
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
			}))
			defer server.Close()
			conf := viper.New()
			prefix := "inventory.cmdb.sources.test."
			conf.Set(prefix+"base_url", server.URL)
			conf.Set(prefix+"allow_insecure_http", true)
			conf.Set(prefix+"page_size", pageSize)
			conf.Set(prefix+"concurrency", concurrency)
			conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7})
			client := NewCMDBWithClient(conf, server.Client())
			result, err := client.CollectGPU(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Cards) != 1013 || len(result.Uplinks) != 1013 || result.Objects != 2026 {
				t.Fatalf("incomplete collection: cards=%d links=%d objects=%d", len(result.Cards), len(result.Uplinks), result.Objects)
			}
			mu.Lock()
			for _, object := range []string{"server_gpu", "gpu_uplink"} {
				if peak[object] <= 1 || peak[object] > concurrency {
					t.Errorf("%s concurrency %d", object, peak[object])
				}
				for i := 0; i < serverCount; i++ {
					want := []int{0}
					if i == 0 {
						want = []int{0, 1000}
					}
					if got := starts[fmt.Sprintf("%s:server-%d", object, i)]; !reflect.DeepEqual(got, want) {
						t.Errorf("%s server-%d pages %v", object, i, got)
					}
				}
			}
			if queries["port_view:"] != 1 || queries["device_view:tor-a"] != 1 {
				t.Errorf("ToR cache not reused: %v", queries)
			}
			finishedCards = 0
			mu.Unlock()
			conf.Set(prefix+"concurrency", 1)
			again, err := client.CollectGPU(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			if err != nil || !reflect.DeepEqual(result, again) {
				t.Fatalf("scheduler changed collection/digest: %v", err)
			}
			mu.Lock()
			metadata = "changed"
			finishedCards = 0
			mu.Unlock()
			changed, err := client.CollectGPU(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
			if err != nil || changed.SourceDigest == result.SourceDigest {
				t.Fatalf("raw source metadata missing from digest: %v", err)
			}
		})
	}
}

type gpuTestTransport func(*http.Request) (*http.Response, error)

func (transport gpuTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestGPUCollectionWorkersCancelAndJoinWithoutPerServerGoroutines(t *testing.T) {
	for _, scenario := range []string{"request_error", "context_cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const concurrency = 3
			var mu sync.Mutex
			calls, active := 0, 0
			baseline := runtime.NumGoroutine()
			gate := make(chan struct{})
			transport := gpuTestTransport(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				calls++
				index := calls
				active++
				if calls == concurrency {
					if delta := runtime.NumGoroutine() - baseline; delta > 64 {
						t.Errorf("goroutines grow with 10000 servers: %d", delta)
					}
					close(gate)
					if scenario == "context_cancel" {
						cancel()
					}
				}
				mu.Unlock()
				defer func() { mu.Lock(); active--; mu.Unlock() }()
				select {
				case <-gate:
				case <-time.After(time.Second):
					t.Error("workers did not start concurrently")
					cancel()
				}
				if scenario == "request_error" && index == 1 {
					return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
				}
				<-req.Context().Done()
				return nil, req.Context().Err()
			})
			client := NewCMDBWithClient(viper.New(), &http.Client{Transport: transport})
			servers := make([]string, 10000)
			for index := range servers {
				servers[index] = fmt.Sprintf("server-%d", index)
			}
			result, err := client.collectGPUByServer(ctx, cmdbConfig{BaseURL: "https://cmdb.invalid", Concurrency: concurrency}, "server_gpu", servers)
			wantErr := ErrCMDBHTTP
			if scenario == "context_cancel" {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) || result != nil {
				t.Fatalf("partial success or lost first error: result=%v err=%v", result, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if active != 0 || calls != concurrency {
				t.Fatalf("workers not joined/bounded: active=%d calls=%d", active, calls)
			}
		})
	}
}

func TestGPUCollectionRejectsUnsafeSecondDevicePage(t *testing.T) {
	for _, object := range []string{"server_gpu", "gpu_uplink"} {
		for _, scenario := range []string{"count_change", "incomplete", "order", "integer_type"} {
			t.Run(object+"/"+scenario, func(t *testing.T) {
				transport := gpuTestTransport(func(req *http.Request) (*http.Response, error) {
					var request struct{ Page struct{ Start, Limit int } }
					if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
						t.Error(err)
						return nil, err
					}
					if request.Page.Limit != 1000 {
						t.Errorf("wrong device limit: %d", request.Page.Limit)
					}
					items := []map[string]any{}
					count := 1001
					if request.Page.Start == 0 {
						for i := 1; i <= 1000; i++ {
							items = append(items, map[string]any{"inst_id": i})
						}
					} else {
						items = append(items, map[string]any{"inst_id": 1001})
						switch scenario {
						case "count_change":
							count = 1002
							items = append(items, map[string]any{"inst_id": 1002})
						case "incomplete":
							items = []map[string]any{}
						case "order":
							items[0]["inst_id"] = 1000
						case "integer_type":
							items[0]["inst_id"] = "1001"
						}
					}
					encoded, _ := json.Marshal(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": count, "info": items}})
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encoded))), Header: http.Header{}}, nil
				})
				client := NewCMDBWithClient(viper.New(), &http.Client{Transport: transport})
				result, err := client.collectGPUByServer(context.Background(), cmdbConfig{BaseURL: "https://cmdb.invalid", Concurrency: 2, MaxResponseBytes: 1 << 20}, object, []string{"server-a"})
				if err == nil || result != nil {
					t.Fatalf("unsafe page accepted: %v %v", result, err)
				}
			})
		}
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

func TestGPUCollectionQueriesEachServerAndResolvesCardSNs(t *testing.T) {
	for _, scenario := range []string{"valid", "unknown_sn", "missing_sn", "numeric_reference", "wrong_owner", "conflicting_uuid"} {
		t.Run(scenario, func(t *testing.T) {
			queried := map[string]int{}
			starts := []int{}
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
					sn, _ := req.Condition["device_sn"].(string)
					queried[sn]++
					starts = append(starts, req.Page.Start)
					if len(req.Condition) != 1 || sn != "server-a" {
						t.Errorf("uplinks require one server SN condition: %v", req.Condition)
					}
					items = []map[string]any{{"inst_id": 1, "uuid": "link-a", "device_sn": "server-a", "gpu_sn": "parts-a", "tor_sn": "tor-a", "tor_port": "Ethernet1"}, {"inst_id": 2, "uuid": "link-b", "device_sn": "server-a", "gpu_sn": "parts-b", "tor_sn": "tor-a", "tor_port": "Ethernet1"}}
					if scenario == "wrong_owner" {
						items[1]["device_sn"] = "server-b"
					}
					if scenario == "conflicting_uuid" {
						items[1]["uuid"] = "link-a"
					}
					if scenario == "unknown_sn" || scenario == "missing_sn" || scenario == "numeric_reference" {
						unknownSN := "not-collected"
						if scenario == "missing_sn" {
							unknownSN = " "
						}
						if scenario == "numeric_reference" {
							unknownSN = "1"
						}
						items = append(items, map[string]any{"inst_id": 3, "uuid": "link-skipped", "device_sn": "server-a", "gpu_sn": unknownSN, "gpu_id": 1, "tor_sn": "tor-a", "tor_port": "Ethernet1"})
					}
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
			pages := 1
			if scenario == "wrong_owner" || scenario == "conflicting_uuid" {
				if err == nil {
					t.Fatal("unsafe uplink accepted")
				}
			} else {
				objects, skipped := 5, 1
				if scenario != "valid" {
					objects, skipped = 6, 2
					reason := "gpu_uplink_unknown_gpu_sn"
					if scenario == "missing_sn" {
						reason = "gpu_uplink_missing_gpu_sn"
					}
					if result.Reasons[reason] != 1 {
						t.Fatalf("missing SN diagnostic: %+v", result.Reasons)
					}
				}
				if err != nil || len(result.Cards) != 2 || len(result.Uplinks) != 2 || result.Uplinks[0].Identity.From.StableID != "parts-a" || result.Uplinks[1].Identity.From.StableID != "parts-b" || result.Skipped != skipped || result.Reasons["gpu_missing_parts_sn"] != 1 || result.Objects != objects {
					t.Fatalf("result %+v err %v", result, err)
				}
				again, err := NewCMDBWithClient(conf, server.Client()).CollectGPU(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
				if err != nil || again.SourceDigest != result.SourceDigest {
					t.Fatalf("non-deterministic reread digest: %v", err)
				}
				pages *= 2
			}
			if len(queried) != 1 || queried["server-a"] != pages || len(starts) != pages {
				t.Fatalf("wrong per-server pagination: queries %v starts %v", queried, starts)
			}
			for _, start := range starts {
				if start != 0 {
					t.Fatalf("repeated or missing server page: %v", starts)
				}
			}
		})
	}
}
