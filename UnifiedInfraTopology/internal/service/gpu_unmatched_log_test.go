package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func gpuUnmatchedFixture(t *testing.T, graph *gpuGraph, links []map[string]any, collide bool, conflictingCards bool) *GPUSyncWorker {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Condition map[string]any }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		object := strings.TrimPrefix(r.URL.Path, "/pub/api/v3/find/instance/object/")
		var items []map[string]any
		switch object {
		case "device_view":
			if req.Condition["parent_type_id"] == float64(52) {
				items = []map[string]any{{"inst_id": 1, "device_sn": "server a", "parent_type_id": 52, "device_type_id": 58}}
			} else {
				items = []map[string]any{{"inst_id": 1, "device_sn": "tor a", "parent_type_id": 54}}
			}
		case "server_gpu":
			items = []map[string]any{{"inst_id": 1, "device_sn": "server a", "parts_sn": "parts a", "conn_ports": []string{"i\tb 8", "ib8"}, "slot": "slot a"}}
			if conflictingCards {
				items = append(items, map[string]any{"inst_id": 2, "device_sn": "server a", "parts_sn": "conflict a", "slot": "1"}, map[string]any{"inst_id": 3, "device_sn": "server a", "parts_sn": "conflict a", "slot": "2"})
			}
		case "gpu_uplink":
			items = links
		case "port_view":
			items = []map[string]any{{"inst_id": 1, "port_uuid": "port a", "port_name": "Ether\tnet\u20031", "port_type": "physics", "local_device_sn": "tor a"}}
			if collide {
				items = append(items, map[string]any{"inst_id": 2, "port_uuid": "port b", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": "tor a"})
			}
		default:
			t.Errorf("unexpected object %s", object)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items}})
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"base_url", server.URL)
	conf.Set(prefix+"allow_insecure_http", true)
	conf.Set(prefix+"page_size", 1000)
	conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7})
	return NewGPUSyncWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}

func gpuUnmatchedRow(id int, patch map[string]any) map[string]any {
	row := map[string]any{"inst_id": id, "uuid": "relation a", "device_sn": "server a", "gpu_sn": "parts a", "gpu_port": " i\tb\u00a08 ", "gpu_slot": "slot a", "tor_sn": "tor a", "tor_port": " \tEther\nnet\u20031\u00a0 ", "gpu_ip": "192.0.2.1", "bond_name": "bond a", "password": "private-secret", "token": "private-token"}
	for key, value := range patch {
		row[key] = value
	}
	return row
}

func TestGPUSyncNormalizesPortsBeforeMatchingAndSaving(t *testing.T) {
	graph := &gpuGraph{}
	worker := gpuUnmatchedFixture(t, graph, []map[string]any{gpuUnmatchedRow(1, nil)}, false, false)
	result, err := worker.Run(context.Background(), model.Source{ID: "source a", ConfigRef: "test"}, time.Now())
	if err != nil || result.Nodes != 1 || result.Edges != 2 {
		t.Fatalf("normalized match: %+v err=%v", result, err)
	}
	props := graph.edges[1].Properties
	if props["gpu_port"] != "ib8" || props["tor_port"] != "Ethernet1" || props["gpu_ip"] != "192.0.2.1" || props["bond_name"] != "bond a" {
		t.Fatalf("saved properties: %+v", props)
	}
	if graph.edges[1].Identity.SourceRelationID != "relation a" || graph.edges[1].Identity.From.StableID != "parts a" || graph.edges[1].Identity.To.StableID != "port a" {
		t.Fatalf("identity changed: %+v", graph.edges[1].Identity)
	}
	if !reflect.DeepEqual(graph.nodes[0].Properties["conn_ports"], []string{"ib8"}) {
		t.Fatalf("card ports: %+v", graph.nodes[0].Properties)
	}
}

func TestGPUSyncLogsEveryCollectedUnmatchedWithoutSecrets(t *testing.T) {
	cases := []struct {
		uuid, reason string
		patch        map[string]any
	}{
		{"missing a", "gpu_uplink_missing_gpu_sn", map[string]any{"gpu_sn": ""}},
		{"unknown a", "gpu_uplink_unknown_gpu_sn", map[string]any{"gpu_sn": "unknown a"}},
		{"conflict a", "gpu_uplink_conflicting_gpu_sn", map[string]any{"gpu_sn": "conflict a"}},
		{"port a", "gpu_port_conflict", map[string]any{"gpu_port": "ib9"}},
		{"slot a", "gpu_slot_conflict", map[string]any{"gpu_slot": "other"}},
		{"both a", "gpu_port_conflict,gpu_slot_conflict", map[string]any{"gpu_port": "ib9", "gpu_slot": "other"}},
		{"tor a", "tor_reference_missing", map[string]any{"tor_sn": ""}},
		{"ambiguous a", "tor_port_not_unique", nil},
	}
	links := []map[string]any{}
	for i, tc := range cases {
		row := gpuUnmatchedRow(i+1, tc.patch)
		row["uuid"] = tc.uuid
		links = append(links, row)
	}
	duplicate := gpuUnmatchedRow(len(links)+1, cases[0].patch)
	duplicate["uuid"] = cases[0].uuid
	links = append(links, duplicate)
	core, observed := observer.New(zap.WarnLevel)
	worker := gpuUnmatchedFixture(t, &gpuGraph{}, links, true, true).WithLogger(&log.Logger{Logger: zap.New(core)})
	result, err := worker.Run(WithSyncRunID(context.Background(), "run a"), model.Source{ID: "source a", ConfigRef: "test"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	entries := observed.FilterMessage("GPU uplink unmatched").All()
	if len(entries) != len(cases) {
		t.Fatalf("all unmatched required: got=%d want=%d result=%+v", len(entries), len(cases), result)
	}
	for i, entry := range entries {
		fields := entry.ContextMap()
		if entry.Level != zap.WarnLevel || fields["run_id"] != "run a" || fields["source_id"] != "source a" || fields["server_sn"] != "server a" || fields["source_relation_id"] != cases[i].uuid || fields["reason"] != cases[i].reason || fields["raw_tor_port"] != " \tEther\nnet\u20031\u00a0 " || fields["tor_port"] != "Ethernet1" {
			t.Fatalf("unmatched identifiers: %+v", fields)
		}
		if len(fields) != 10 {
			t.Fatalf("unexpected non-whitelist fields: %+v", fields)
		}
		encoded, _ := json.Marshal(fields)
		if strings.Contains(string(encoded), "private-") {
			t.Fatalf("credentials logged: %s", encoded)
		}
	}
}

func TestGPUSyncLogsCollectedUnmatchedBeforeLaterCollectionError(t *testing.T) {
	core, observed := observer.New(zap.WarnLevel)
	links := []map[string]any{gpuUnmatchedRow(1, map[string]any{"gpu_sn": "", "uuid": "missing a"}), gpuUnmatchedRow(2, map[string]any{"uuid": "invalid a", "gpu_port": 7})}
	graph := &gpuGraph{}
	_, err := gpuUnmatchedFixture(t, graph, links, false, false).WithLogger(&log.Logger{Logger: zap.New(core)}).Run(context.Background(), model.Source{ID: "source a", ConfigRef: "test"}, time.Now())
	if err == nil || len(graph.nodes) != 0 || len(graph.edges) != 0 {
		t.Fatalf("fatal parsing must remain fatal without writes: %v", err)
	}
	entries := observed.FilterMessage("GPU uplink unmatched").All()
	if len(entries) != 1 || entries[0].ContextMap()["source_relation_id"] != "missing a" {
		t.Fatalf("partial collection logs lost: %+v", entries)
	}
}

func TestGPUSyncLogsGraphRejectedCandidates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		graph   *gpuGraph
		reasons []string
	}{
		{"parent", &gpuGraph{invalidParent: true}, []string{"gpu_parent_missing_or_invalid", "gpu_uplink_endpoint_missing_or_invalid"}},
		{"endpoint", &gpuGraph{wrongOwner: true}, []string{"gpu_uplink_endpoint_missing_or_invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, observed := observer.New(zap.WarnLevel)
			worker := gpuUnmatchedFixture(t, tc.graph, []map[string]any{gpuUnmatchedRow(1, nil)}, false, false).WithLogger(&log.Logger{Logger: zap.New(core)})
			_, err := worker.Run(context.Background(), model.Source{ID: "source a", ConfigRef: "test"}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			entries := observed.FilterMessage("GPU uplink unmatched").All()
			if len(entries) != len(tc.reasons) {
				t.Fatalf("graph rejection logs: %+v", entries)
			}
			for i, entry := range entries {
				fields := entry.ContextMap()
				if fields["reason"] != tc.reasons[i] || fields["server_sn"] != "server a" || fields["gpu_sn"] != "parts a" || len(fields) != 10 {
					t.Fatalf("graph log identifiers: %+v", fields)
				}
				if tc.reasons[i] == "gpu_uplink_endpoint_missing_or_invalid" && (fields["tor_port"] != "Ethernet1" || fields["source_relation_id"] != "relation a") {
					t.Fatalf("uplink identity: %+v", fields)
				}
			}
		})
	}
}
