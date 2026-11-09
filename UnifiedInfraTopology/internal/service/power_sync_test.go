package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
)

type powerGraph struct {
	repository.TopologyGraphRepository
	missing           string
	readErr, writeErr error
	failAfter         int
	writes            []model.Relation
	checks            map[model.EntityIdentity]int
	cancel            context.CancelFunc
}

func (g *powerGraph) UpsertVertex(context.Context, repository.TopologyVertex) error {
	panic("power sync must not fabricate vertices")
}
func (g *powerGraph) VertexExists(_ context.Context, id model.EntityIdentity) (bool, error) {
	if g.checks == nil {
		g.checks = map[model.EntityIdentity]int{}
	}
	g.checks[id]++
	return id.StableID != g.missing, g.readErr
}
func (g *powerGraph) UpsertRelation(_ context.Context, r model.Relation) error {
	if g.writeErr != nil && len(g.writes) >= g.failAfter {
		return g.writeErr
	}
	g.writes = append(g.writes, r)
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}
func powerRows() map[string][]map[string]any {
	return map[string][]map[string]any{
		"cabinet":         {{"inst_id": 1, "uuid": "cab", "row_switch_id_A": 2, "row_switch_id_B": 2}},
		"idc_RPP":         {{"inst_id": 2, "uuid": "rpp", "ups_group": 3, "transformer_group_a": 4}},
		"idc_ups":         {{"inst_id": 5, "uuid": "ups", "ups_group": 3, "transformer_id_up": 4}},
		"idc_ups_group":   {{"inst_id": 3, "uuid": "group", "transformer_id_up": 4}},
		"idc_transformer": {{"inst_id": 4, "uuid": "transformer", "standby_transformer": 6}, {"inst_id": 6, "uuid": "standby"}},
	}
}
func powerFixture(t *testing.T, rows map[string][]map[string]any, graph *powerGraph, mutate func(string, int, []map[string]any), pageSize int) *PowerSyncWorker {
	t.Helper()
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimPrefix(r.URL.Path, "/pub/api/v3/find/instance/object/")
		var req struct {
			Page      struct{ Start, Limit int }
			Condition map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if len(req.Condition) == 0 {
			t.Errorf("unscoped %s request", kind)
		}
		if req.Page.Start == 0 {
			reads[kind]++
		}
		encoded, _ := json.Marshal(rows[kind])
		var items []map[string]any
		_ = json.Unmarshal(encoded, &items)
		if mutate != nil {
			mutate(kind, reads[kind], items)
		}
		start, end := min(req.Page.Start, len(items)), min(req.Page.Start+req.Page.Limit, len(items))
		if items == nil {
			items = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"base_url", server.URL)
	conf.Set(prefix+"allow_insecure_http", true)
	conf.Set(prefix+"page_size", pageSize)
	for _, kind := range []string{"cabinet", "rpp", "ups", "ups_group", "transformer"} {
		field := "idc"
		if kind == "cabinet" {
			field = "idc_id"
		}
		conf.Set(prefix+kind+"_condition", map[string]any{field: 1030})
	}
	return NewPowerSyncWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}
func runPower(worker *PowerSyncWorker) (PowerSyncResult, error) {
	return worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC))
}
func TestPowerSyncAllKindsAndPaths(t *testing.T) {
	graph := &powerGraph{}
	worker := powerFixture(t, powerRows(), graph, nil, 1)
	result, err := runPower(worker)
	if err != nil {
		t.Fatal(err)
	}
	if result.Edges != 6 || result.Objects != 6 || result.Skipped != 0 || result.ByKind["power_upstream"] != 4 || result.ByKind["member_of"] != 1 || result.ByKind["has_standby"] != 1 {
		t.Fatalf("result %+v", result)
	}
	ranks := map[int64]bool{}
	paths := map[string]bool{}
	for _, r := range graph.writes {
		if _, err := model.NormalizeRelation(r); err != nil {
			t.Fatal(err)
		}
		if r.Identity.From.EntityType == model.EntityCabinet {
			rank, _ := r.Identity.Rank()
			ranks[rank] = true
			paths[r.Properties["power_path"].(string)] = true
		}
		if (r.Identity.From.EntityType == model.EntityUPS || r.Identity.From.EntityType == model.EntityRPP) && r.Identity.To.EntityType == model.EntityTransformer {
			t.Fatal("cross-layer shortcut")
		}
	}
	if len(ranks) != 2 || !paths["A"] || !paths["B"] {
		t.Fatalf("ranks %v paths %v", ranks, paths)
	}
	for id, count := range graph.checks {
		if count != 1 {
			t.Fatalf("uncached %v %d", id, count)
		}
	}
	first := append([]model.Relation(nil), graph.writes...)
	graph.writes = nil
	if _, err := runPower(worker); err != nil || !reflect.DeepEqual(first, graph.writes) {
		t.Fatalf("not deterministic: %v", err)
	}
}
func TestPowerSyncMissingPathIndependent(t *testing.T) {
	rows := powerRows()
	rows["cabinet"][0]["row_switch_id_B"] = 999
	graph := &powerGraph{}
	result, err := runPower(powerFixture(t, rows, graph, nil, 1))
	if err != nil || result.Edges != 5 || result.Skipped != 1 || result.Reasons["missing_reference"] != 1 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncConsistencyPreservesFacts(t *testing.T) {
	rows := powerRows()
	rows["idc_RPP"][0]["transformer_group_a"] = 999
	rows["idc_ups"][0]["transformer_id_up"] = 998
	result, err := runPower(powerFixture(t, rows, &powerGraph{}, nil, 2))
	if err != nil || result.Edges != 6 || result.Skipped != 0 || result.Reasons["conflict"] != 2 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncDuplicateUUIDDoesNotAliasDistinctInstanceIDs(t *testing.T) {
	rows := powerRows()
	rows["idc_RPP"] = append(rows["idc_RPP"], map[string]any{"inst_id": 7, "uuid": "rpp", "ups_group": 3})
	graph := &powerGraph{}
	result, err := runPower(powerFixture(t, rows, graph, nil, 1))
	if err != nil || result.Edges != 7 || result.Skipped != 0 || result.Reasons["ambiguous_reference"] != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, stableID := range []string{"2", "7"} {
		if graph.checks[model.EntityIdentity{SourceID: "s", EntityType: model.EntityRPP, StableID: stableID}] != 1 {
			t.Fatalf("missing distinct inst_id %s", stableID)
		}
	}
}

func TestPowerSyncAmbiguousInstanceID(t *testing.T) {
	rows := powerRows()
	rows["idc_RPP"] = append(rows["idc_RPP"], map[string]any{"inst_id": 2, "uuid": "other-rpp", "ups_group": 3})
	result, err := runPower(powerFixture(t, rows, &powerGraph{}, nil, 1))
	if err != nil || result.Edges != 3 || result.Reasons["ambiguous_reference"] != 2 || result.Skipped != 4 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncStandbySelfRejected(t *testing.T) {
	rows := powerRows()
	rows["idc_transformer"][0]["standby_transformer"] = 4
	graph := &powerGraph{}
	result, err := runPower(powerFixture(t, rows, graph, nil, 1))
	if err != nil || result.Edges != 5 || result.Reasons["standby_self_reference"] != 1 || result.Skipped != 1 || result.ByKind["has_standby"] != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, relation := range graph.writes {
		if relation.Identity.Kind == model.RelationHasStandby {
			t.Fatal("self standby must not be written")
		}
	}
}
func TestPowerSyncThreeNodeStandbyCycleAccepted(t *testing.T) {
	rows := powerRows()
	rows["idc_transformer"][1]["standby_transformer"] = 8
	rows["idc_transformer"] = append(rows["idc_transformer"], map[string]any{"inst_id": 8, "standby_transformer": 4})
	graph := &powerGraph{}
	result, err := runPower(powerFixture(t, rows, graph, nil, 1))
	if err != nil || result.Edges != 8 || result.Skipped != 0 || result.ByKind["has_standby"] != 3 || result.Reasons["standby_cycle"] != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	directions := map[string]bool{}
	for _, relation := range graph.writes {
		if relation.Identity.Kind == model.RelationHasStandby {
			directions[relation.Identity.From.StableID+"->"+relation.Identity.To.StableID] = true
		}
	}
	if len(directions) != 3 || !directions["4->6"] || !directions["6->8"] || !directions["8->4"] {
		t.Fatalf("standby directions=%v", directions)
	}
}
func TestPowerSyncCycleAndIncomingEdgeAccepted(t *testing.T) {
	rows := powerRows()
	rows["idc_transformer"][1]["standby_transformer"] = 8
	rows["idc_transformer"] = append(rows["idc_transformer"], map[string]any{"inst_id": 8, "standby_transformer": 4}, map[string]any{"inst_id": 9, "uuid": "external", "standby_transformer": 4})
	result, err := runPower(powerFixture(t, rows, &powerGraph{}, nil, 1))
	if err != nil || result.Edges != 9 || result.Skipped != 0 || result.ByKind["has_standby"] != 4 || result.Reasons["standby_cycle"] != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncMutualStandbyPreservesBothDirectionsAndIDs(t *testing.T) {
	rows := powerRows()
	rows["idc_transformer"][1]["standby_transformer"] = 4
	graph := &powerGraph{}
	worker := powerFixture(t, rows, graph, nil, 1)
	result, err := runPower(worker)
	if err != nil || result.Edges != 7 || result.Skipped != 0 || result.ByKind["has_standby"] != 2 || result.Reasons["standby_cycle"] != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	ids := map[string]bool{}
	directions := map[string]bool{}
	for _, relation := range graph.writes {
		if relation.Identity.Kind != model.RelationHasStandby {
			continue
		}
		id, err := relation.Identity.ID()
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = true
		directions[relation.Identity.From.StableID+"->"+relation.Identity.To.StableID] = true
	}
	if len(ids) != 2 || !directions["4->6"] || !directions["6->4"] {
		t.Fatalf("ids=%v directions=%v", ids, directions)
	}
	first := append([]model.Relation(nil), graph.writes...)
	graph.writes = nil
	if _, err := runPower(worker); err != nil || !reflect.DeepEqual(first, graph.writes) {
		t.Fatalf("mutual standby IDs or ordering changed: %v", err)
	}
}

func TestPowerSyncUsesSingleCompleteCollection(t *testing.T) {
	reads := map[string]int{}
	graph := &powerGraph{}
	rows := powerRows()
	result, err := runPower(powerFixture(t, rows, graph, func(kind string, pass int, items []map[string]any) {
		reads[kind] = pass
		if kind == "idc_ups" && pass > 1 {
			items[0]["transformer_id_up"] = 999
		}
	}, 1))
	if err != nil || result.Edges != 6 || len(graph.writes) != 6 {
		t.Fatalf("%+v %v", result, err)
	}
	for kind := range rows {
		if reads[kind] != 1 {
			t.Errorf("%s collected %d times, want 1", kind, reads[kind])
		}
	}
}
func TestPowerSyncDigestIgnoresPagination(t *testing.T) {
	var expected []model.Relation
	for _, size := range []int{1, 2, 100} {
		graph := &powerGraph{}
		result, err := runPower(powerFixture(t, powerRows(), graph, nil, size))
		if err != nil || result.Edges != 6 {
			t.Fatalf("%+v %v", result, err)
		}
		if expected == nil {
			expected = graph.writes
		} else if !reflect.DeepEqual(expected, graph.writes) {
			t.Fatal("page size changed output order")
		}
	}
}
func TestPowerSyncMissingGraphEndpoint(t *testing.T) {
	graph := &powerGraph{missing: "2"}
	result, err := runPower(powerFixture(t, powerRows(), graph, nil, 2))
	if err != nil || result.Edges != 3 || result.MissingEndpoints != 3 || result.Reasons["missing_endpoint"] != 3 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncReadWriteErrorsAndCancel(t *testing.T) {
	failure := errors.New("failed")
	graph := &powerGraph{readErr: failure}
	result, err := runPower(powerFixture(t, powerRows(), graph, nil, 1))
	if !errors.Is(err, failure) || result.Edges != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	graph = &powerGraph{writeErr: failure, failAfter: 1}
	result, err = runPower(powerFixture(t, powerRows(), graph, nil, 1))
	if !errors.Is(err, failure) || result.Edges != 1 || !strings.Contains(err.Error(), "persist") {
		t.Fatalf("%+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	graph = &powerGraph{cancel: cancel}
	result, err = powerFixture(t, powerRows(), graph, nil, 1).Run(ctx, model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if !errors.Is(err, context.Canceled) || result.Edges != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	result, err = NewPowerSyncWorker(nil, nil).Run(ctx, model.Source{}, time.Time{})
	if !errors.Is(err, context.Canceled) || result.Edges != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncEmptyReferencesAreNotSkipped(t *testing.T) {
	rows := powerRows()
	rows["cabinet"][0]["row_switch_id_B"] = nil
	result, err := runPower(powerFixture(t, rows, &powerGraph{}, nil, 1))
	if err != nil || result.Edges != 5 || result.Skipped != 0 || result.Reasons["empty_reference"] != 2 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestPowerSyncDigestSortedAcrossPages(t *testing.T) {
	graph := &powerGraph{}
	worker := powerFixture(t, powerRows(), graph, func(kind string, pass int, items []map[string]any) {
		if kind == "idc_transformer" && pass%2 == 0 {
			items[0], items[1] = items[1], items[0]
		}
	}, 1)
	result, err := runPower(worker)
	if err != nil || result.Edges != 6 {
		t.Fatalf("reordered source %+v %v", result, err)
	}
	first, _, _, err := worker.collect(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := worker.collect(context.Background(), model.Source{ID: "s", ConfigRef: "test"})
	if err != nil || first != second {
		t.Fatalf("digest %q %q %v", first, second, err)
	}
}
func TestPowerSyncRejectsUnscopedSourceAndUnavailableGraph(t *testing.T) {
	if _, err := runPower(NewPowerSyncWorker(nil, nil)); err == nil {
		t.Fatal("nil dependencies accepted")
	}
	graph, cleanup, err := repository.NewTopologyGraphRepository(viper.New())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	worker := powerFixture(t, powerRows(), &powerGraph{}, nil, 1)
	worker.graph = graph
	unavailable, err := runPower(worker)
	if !errors.Is(err, repository.ErrInventoryNotReady) || unavailable.Edges != 0 {
		t.Fatalf("unavailable graph %+v %v", unavailable, err)
	}
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", "https://example.invalid")
	result, err := runPower(NewPowerSyncWorker(adapter.NewCMDB(conf), &powerGraph{}))
	if err == nil || result.Edges != 0 {
		t.Fatalf("unscoped result %+v %v", result, err)
	}
}
