package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
)

type gpuGraph struct {
	repository.TopologyGraphRepository
	missing                   string
	invalidParent, wrongOwner bool
	nodes                     []repository.TopologyVertex
	edges                     []model.Relation
}

func (g *gpuGraph) VertexExists(_ context.Context, id model.EntityIdentity) (bool, error) {
	return id.StableID != g.missing, nil
}
func (g *gpuGraph) ParentGPUDeviceValid(context.Context, model.EntityIdentity) (bool, error) {
	return !g.invalidParent, nil
}
func (g *gpuGraph) ServerUplinkEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error) {
	return !g.wrongOwner, nil
}
func (g *gpuGraph) UpsertVertex(_ context.Context, v repository.TopologyVertex) error {
	g.nodes = append(g.nodes, v)
	return nil
}
func (g *gpuGraph) UpsertRelation(_ context.Context, r model.Relation) error {
	g.edges = append(g.edges, r)
	return nil
}
func (g *gpuGraph) DeleteRelationsNotSeen(context.Context, string, model.EdgeType, time.Time) error {
	panic("GPU must not clean scoped sources")
}
func (g *gpuGraph) DeleteOrphanVerticesNotSeen(context.Context, string, model.EntityType, time.Time) error {
	panic("GPU must not clean scoped sources")
}

func gpuWorkerFixture(t *testing.T, graph *gpuGraph, mutate func(string, int, map[string]any)) *GPUSyncWorker {
	t.Helper()
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Page      struct{ Start, Limit int }
			Condition map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		object := r.URL.Path[len("/pub/api/v3/find/instance/object/"):]
		reads[object]++
		item := map[string]any{}
		switch object {
		case "device_view":
			if req.Condition["parent_type_id"] == float64(52) {
				if req.Condition["device_type_id"] != float64(58) || req.Condition["idc_id"] != float64(7) {
					t.Errorf("wrong discovery %+v", req.Condition)
				}
				item = map[string]any{"inst_id": 1, "device_sn": "server-a", "parent_type_id": 52, "device_type_id": 58}
			} else {
				if _, ok := req.Condition["idc_id"]; ok {
					t.Error("target ToR incorrectly scoped to IDC")
				}
				item = map[string]any{"inst_id": 2, "device_sn": "tor-a", "parent_type_id": 54}
			}
		case "server_gpu":
			item = map[string]any{"inst_id": 3, "uuid": "card-a", "device_sn": "server-a", "parts_sn": "parts-a", "slot": "204", "model": "S5000", "VRAM": "80G", "conn_ports": []string{"ib8"}}
		case "gpu_uplink":
			item = map[string]any{"inst_id": 4, "uuid": "uplink-a", "device_sn": "server-a", "gpu_sn": "parts-a", "gpu_port": "ib8", "gpu_slot": "204", "gpu_model": "S5000", "gpu_vram": "80GB", "tor_sn": "tor-a", "tor_port": "Ethernet1", "gpu_port_speed": "400G", "tor_port_speed": "400000"}
		case "port_view":
			item = map[string]any{"inst_id": 5, "port_uuid": "port-a", "port_name": "Ethernet1", "port_type": "physics", "local_device_sn": "tor-a"}
		default:
			t.Errorf("unexpected %s", object)
		}
		key, value := "device_sn", "server-a"
		if object == "gpu_uplink" {
			key, value = "gpu_sn", "parts-a"
		}
		if (object == "server_gpu" || object == "gpu_uplink") && (len(req.Condition) != 1 || req.Condition[key] != value) {
			t.Errorf("wrong card condition %+v", req.Condition)
		}
		if mutate != nil {
			mutate(object, reads[object], item)
		}
		items := []map[string]any{item}
		if item["empty"] == true {
			items = []map[string]any{}
		}
		start := min(req.Page.Start, len(items))
		end := min(start+req.Page.Limit, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"base_url", server.URL)
	conf.Set(prefix+"allow_insecure_http", true)
	conf.Set(prefix+"page_size", 1)
	conf.Set(prefix+"entity_type", "gpu")
	conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7})
	return NewGPUSyncWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}

func TestGPUSyncPublishesCardContainsAndUplink(t *testing.T) {
	g := &gpuGraph{}
	w := gpuWorkerFixture(t, g, nil)
	snapshot := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	result, err := w.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes != 1 || result.Edges != 2 || result.ByKind["contains_gpu"] != 1 || result.ByKind["gpu_uplink"] != 1 || len(g.nodes) != 1 || len(g.edges) != 2 {
		t.Fatalf("result %+v", result)
	}
	if g.edges[0].Identity.Kind != model.RelationContainsGPU || len(g.edges[0].Properties) != 0 || g.edges[1].Identity.SourceRelationID != "uplink-a" || g.edges[1].Properties["tor_port_speed"] != "400000" {
		t.Fatalf("edges %+v", g.edges)
	}
	if g.nodes[0].Identity.StableID != "parts-a" || g.edges[0].Identity.To.StableID != "parts-a" || g.edges[1].Identity.From.StableID != "parts-a" {
		t.Fatal("GPU endpoints must use parts_sn")
	}
	if !g.nodes[0].SyncedAt.Equal(snapshot) || !g.edges[1].SyncedAt.Equal(snapshot) {
		t.Fatal("wrong snapshot")
	}
	if result.Reasons["gpu_vram_difference"] != 1 {
		t.Fatalf("VRAM difference should be diagnostic %+v", result)
	}
}

func TestGPUSyncNeverCreatesOrphanCard(t *testing.T) {
	for _, g := range []*gpuGraph{{missing: "server-a"}, {invalidParent: true}} {
		result, err := gpuWorkerFixture(t, g, nil).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
		if err != nil || result.Nodes != 0 || len(g.nodes) != 0 || len(g.edges) != 0 {
			t.Fatalf("result %+v err %v", result, err)
		}
	}
}

func TestGPUSyncUnresolvedAndConflictingUplinkPreservesCards(t *testing.T) {
	for _, scenario := range []string{"port", "owner", "slot", "gpu_port"} {
		t.Run(scenario, func(t *testing.T) {
			g := &gpuGraph{}
			if scenario == "port" {
				g.missing = "port-a"
			}
			if scenario == "owner" {
				g.wrongOwner = true
			}
			mutate := func(object string, _ int, item map[string]any) {
				if object == "gpu_uplink" {
					if scenario == "slot" {
						item["gpu_slot"] = "other"
					}
					if scenario == "gpu_port" {
						item["gpu_port"] = "ib9"
					}
				}
			}
			result, err := gpuWorkerFixture(t, g, mutate).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
			if err != nil || result.Nodes != 1 || result.Edges != 1 || result.Skipped != 1 {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
}

func TestGPUSyncFirstPassErrorsBeforeWrites(t *testing.T) {
	for _, field := range []string{"uuid", "device_sn", "inst_id"} {
		t.Run(field, func(t *testing.T) {
			g := &gpuGraph{}
			mutate := func(object string, _ int, item map[string]any) {
				if object == "gpu_uplink" {
					if field == "inst_id" {
						item[field] = 0
					} else {
						item[field] = ""
					}
				}
			}
			_, err := gpuWorkerFixture(t, g, mutate).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
			if err == nil || len(g.nodes) != 0 || len(g.edges) != 0 {
				t.Fatalf("err %v writes %v %v", err, g.nodes, g.edges)
			}
		})
	}
}

func TestGPUSyncIndependentVerificationDetectsMetadataChange(t *testing.T) {
	g := &gpuGraph{}
	mutate := func(object string, read int, item map[string]any) {
		if object == "gpu_uplink" && read > 1 {
			item["pod_id"] = 99
		}
	}
	result, err := gpuWorkerFixture(t, g, mutate).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err == nil || result.Nodes != 1 || len(g.nodes) != 1 {
		t.Fatalf("result %+v err %v nodes %v", result, err, g.nodes)
	}
}

func TestGPUSyncEmptyScopeReportedWithoutCleanup(t *testing.T) {
	g := &gpuGraph{}
	mutate := func(object string, _ int, item map[string]any) {
		if object == "device_view" {
			item["empty"] = true
		}
	}
	result, err := gpuWorkerFixture(t, g, mutate).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err != nil || result.Nodes != 0 || result.Reasons["empty_gpu_scope"] != 1 || len(g.nodes) != 0 {
		t.Fatalf("result %+v err %v", result, err)
	}
}

func TestGPUSyncSkipsCardsWithoutSN(t *testing.T) {
	for _, value := range []any{nil, "", "   "} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			g := &gpuGraph{}
			mutate := func(object string, _ int, item map[string]any) {
				if object == "server_gpu" {
					item["parts_sn"] = value
				}
				if object == "gpu_uplink" {
					t.Error("missing-SN card must not query uplinks")
				}
			}
			result, err := gpuWorkerFixture(t, g, mutate).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
			if err != nil || result.Nodes != 0 || result.Edges != 0 || result.Skipped != 1 || result.Reasons["gpu_missing_parts_sn"] != 1 || result.Objects != 1 || len(g.nodes) != 0 || len(g.edges) != 0 {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
}
