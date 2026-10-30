package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
)

type uplinkGraph struct {
	repository.TopologyGraphRepository
	missing     string
	wrongOwner  bool
	readErr     error
	readFailAt  int
	writeErr    error
	writeFailAt int
	writes      []model.Relation
	checks      []model.EntityIdentity
}

func (g *uplinkGraph) VertexExists(_ context.Context, id model.EntityIdentity) (bool, error) {
	g.checks = append(g.checks, id)
	if g.readFailAt == 0 || len(g.checks) == g.readFailAt {
		return id.StableID != g.missing, g.readErr
	}
	return id.StableID != g.missing, nil
}
func (g *uplinkGraph) ServerUplinkEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error) {
	return !g.wrongOwner, nil
}

func TestServerUplinkWrongGraphOwnerDoesNotWrite(t *testing.T) {
	graph := &uplinkGraph{wrongOwner: true}
	worker := uplinkWorkerFixture(t, nil, graph)
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err != nil || result.Edges != 0 || result.MissingEndpoints != 1 || len(graph.writes) != 0 {
		t.Fatalf("unsafe ownership result %+v err %v writes %v", result, err, graph.writes)
	}
}

func (g *uplinkGraph) UpsertRelation(_ context.Context, r model.Relation) error {
	if g.writeErr != nil && (g.writeFailAt == 0 || len(g.writes) == g.writeFailAt) {
		return g.writeErr
	}
	g.writes = append(g.writes, r)
	return nil
}

func uplinkWorkerFixture(t *testing.T, mutate func(int, map[string]any), graph *uplinkGraph, serverRecords ...map[string]any) *ServerUplinkWorker {
	t.Helper()
	serverReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Page      struct{ Start, Limit int }
			Condition map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var items []map[string]any
		if r.URL.Path == "/pub/api/v3/find/instance/object/device_view" {
			if req.Condition["parent_type_id"] == float64(52) {
				serverReads++
				item := map[string]any{"inst_id": 1, "device_sn": "server-a", "parent_type_id": 52, "server_tor_ports": []any{map[string]any{"sn": "tor-a", "ports": []string{"Ethernet1", "Ethernet1"}}}}
				if mutate != nil {
					mutate(serverReads, item)
				}
				items = []map[string]any{item}
				if len(serverRecords) > 0 {
					items = serverRecords
				}
			} else {
				items = []map[string]any{{"inst_id": 2, "device_sn": "tor-a", "parent_type_id": 54}}
			}
		} else {
			items = []map[string]any{{"inst_id": 3, "port_uuid": "port-a", "port_name": "Ethernet1", "port_type": "physics"}, {"inst_id": 4, "port_uuid": "port-b", "port_name": "Ethernet2", "port_type": "physics"}}
		}
		end := min(req.Page.Start+req.Page.Limit, len(items))
		start := min(req.Page.Start, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.page_size", 1)
	conf.Set("inventory.cmdb.sources.test.device_condition", map[string]any{"idc_id": 7})
	return NewServerUplinkWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}

func TestServerUplinkWorkerDeterministicAndDeduplicated(t *testing.T) {
	graph := &uplinkGraph{}
	worker := uplinkWorkerFixture(t, nil, graph)
	snapshot := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result.Edges != 1 || result.Servers != 1 || result.Skipped != 0 || result.MissingEndpoints != 0 || len(graph.writes) != 1 {
		t.Fatalf("result %+v writes %v", result, graph.writes)
	}
	r := graph.writes[0]
	if !r.CreatedAt.Equal(snapshot) || !r.SyncedAt.Equal(snapshot) || len(r.Properties) != 0 || r.Identity.To.StableID != "port-a" {
		t.Fatalf("relation %+v", r)
	}
	id, _ := r.Identity.ID()
	rank, _ := r.Identity.Rank()
	if _, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, snapshot); err != nil {
		t.Fatal(err)
	}
	id2, _ := graph.writes[1].Identity.ID()
	rank2, _ := graph.writes[1].Identity.Rank()
	if id != id2 || rank != rank2 {
		t.Fatal("identity changed")
	}
}

func TestServerUplinkWorkerGlobalDeduplicationAcrossServerPages(t *testing.T) {
	graph := &uplinkGraph{}
	record := func(inst int, sn string) map[string]any {
		return map[string]any{"inst_id": inst, "device_sn": sn, "parent_type_id": 52, "server_tor_ports": []any{map[string]any{"sn": "tor-a", "ports": []string{"Ethernet1"}}}}
	}
	worker := uplinkWorkerFixture(t, nil, graph, record(1, "server-a"), record(2, "server-a"), record(3, "server-b"))
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err == nil || len(graph.writes) != 0 {
		t.Fatalf("duplicate source identity accepted: result %+v err %v writes %v", result, err, graph.writes)
	}
}

func TestServerUplinkWorkerRejectsCrossPageServerOrdering(t *testing.T) {
	graph := &uplinkGraph{}
	record := func(inst int, sn string) map[string]any {
		return map[string]any{"inst_id": inst, "device_sn": sn, "parent_type_id": 52, "server_tor_ports": []any{}}
	}
	worker := uplinkWorkerFixture(t, nil, graph, record(2, "server-a"), record(1, "server-b"))
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err == nil || len(graph.writes) != 0 || len(graph.checks) != 0 {
		t.Fatalf("err %v writes %v", err, graph.writes)
	}
}

func TestServerUplinkWorkerMissingEndpointSkips(t *testing.T) {
	for _, missing := range []string{"server-a", "tor-a", "port-a"} {
		t.Run(missing, func(t *testing.T) {
			graph := &uplinkGraph{missing: missing}
			worker := uplinkWorkerFixture(t, nil, graph)
			result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
			if err != nil || result.Edges != 0 || result.Skipped != 1 || result.MissingEndpoints != 1 || len(graph.writes) != 0 {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
}

func TestServerUplinkWorkerReadFailureBeforeWrites(t *testing.T) {
	failure := errors.New("read failure")
	graph := &uplinkGraph{readErr: failure}
	worker := uplinkWorkerFixture(t, nil, graph)
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if !errors.Is(err, failure) || len(graph.writes) != 0 {
		t.Fatalf("err %v writes %v", err, graph.writes)
	}
}

func TestServerUplinkWorkerReadsAllEndpointsBeforeWriting(t *testing.T) {
	failure := errors.New("late endpoint read failure")
	graph := &uplinkGraph{readErr: failure, readFailAt: 4}
	worker := uplinkWorkerFixture(t, func(_ int, item map[string]any) {
		item["server_tor_ports"] = []any{map[string]any{"sn": "tor-a", "ports": []string{"Ethernet1", "Ethernet2"}}}
	}, graph)
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if !errors.Is(err, failure) || len(graph.writes) != 0 || len(graph.checks) != 4 {
		t.Fatalf("err %v checks %v writes %v", err, graph.checks, graph.writes)
	}
}

func TestServerUplinkWorkerPartialWriteFailureReportsPersistedCount(t *testing.T) {
	failure := errors.New("second write failed")
	graph := &uplinkGraph{writeErr: failure, writeFailAt: 1}
	worker := uplinkWorkerFixture(t, func(_ int, item map[string]any) {
		item["server_tor_ports"] = []any{map[string]any{"sn": "tor-a", "ports": []string{"Ethernet1", "Ethernet2"}}}
	}, graph)
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if !errors.Is(err, failure) || result.Edges != 1 || len(graph.writes) != 1 {
		t.Fatalf("result %+v err %v writes %v", result, err, graph.writes)
	}
}

func TestServerUplinkWorkerWriteFailure(t *testing.T) {
	failure := errors.New("write failure")
	graph := &uplinkGraph{writeErr: failure}
	worker := uplinkWorkerFixture(t, nil, graph)
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if !errors.Is(err, failure) {
		t.Fatalf("err %v", err)
	}
}

func TestServerUplinkWorkerSecondPassChangeFails(t *testing.T) {
	graph := &uplinkGraph{}
	worker := uplinkWorkerFixture(t, func(pass int, item map[string]any) {
		if pass > 1 {
			item["server_tor_ports"] = []any{map[string]any{"sn": "tor-a", "ports": []string{"Ethernet2"}}}
		}
	}, graph)
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err == nil || len(graph.writes) != 1 {
		t.Fatalf("err %v writes %v", err, graph.writes)
	}
}

func TestServerUplinkWorkerEmptyServersStillVerified(t *testing.T) {
	reads := 0
	graph := &uplinkGraph{}
	worker := uplinkWorkerFixture(t, func(_ int, item map[string]any) { reads++; item["server_tor_ports"] = []any{} }, graph)
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err != nil || result.Servers != 1 || result.Edges != 0 || reads != 2 || len(graph.checks) != 0 {
		t.Fatalf("result %+v reads %d err %v", result, reads, err)
	}
}

func TestServerUplinkWorkerSkippedOnlySourceChangeFails(t *testing.T) {
	graph := &uplinkGraph{}
	worker := uplinkWorkerFixture(t, func(pass int, item map[string]any) {
		name := "missing-one"
		if pass > 1 {
			name = "missing-two"
		}
		item["server_tor_ports"] = []any{map[string]any{"sn": "tor-a", "ports": []string{name}}}
	}, graph)
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err == nil || result.Skipped != 1 || len(graph.writes) != 0 {
		t.Fatalf("result %+v err %v", result, err)
	}
}

func TestServerUplinkWorkerSourceValidationBeforeGraph(t *testing.T) {
	graph := &uplinkGraph{}
	worker := uplinkWorkerFixture(t, func(_ int, item map[string]any) { item["parent_type_id"] = 54 }, graph)
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err == nil || len(graph.checks) != 0 || len(graph.writes) != 0 {
		t.Fatalf("err %v checks %v", err, graph.checks)
	}
}

func TestServerUplinkWorkerInvalidInputs(t *testing.T) {
	graph := &uplinkGraph{}
	worker := uplinkWorkerFixture(t, nil, graph)
	if _, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Time{}); err == nil {
		t.Fatal("zero snapshot accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := worker.Run(ctx, model.Source{ID: "s", ConfigRef: "test"}, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}
