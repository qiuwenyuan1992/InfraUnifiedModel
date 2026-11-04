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
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func podMembershipFixture(t *testing.T, graph *uplinkGraph, mutate func(int, []map[string]any)) *PODMembershipWorker {
	t.Helper()
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Page      struct{ Start, Limit int }
			Condition map[string]any
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var items []map[string]any
		switch r.URL.Path {
		case "/pub/api/v3/find/instance/object/device_view":
			require.Equal(t, map[string]any{"idc_id": float64(7)}, req.Condition)
			if req.Page.Start == 0 {
				reads++
			}
			items = []map[string]any{{"inst_id": 1, "device_sn": "device-a", "pod_id": 10, "compute_plane": []any{map[string]any{"pod_id": 20}, map[string]any{"pod_id": 20}}}, {"inst_id": 2, "device_sn": "device-b", "pod_uuid": "main"}, {"inst_id": 3, "device_sn": "device-c"}}
			if mutate != nil {
				mutate(reads, items)
			}
		case "/pub/api/v3/find/instance/object/pod":
			items = []map[string]any{{"inst_id": 10, "uuid": "main", "plane": 1}, {"inst_id": 20, "uuid": "compute", "plane": 2}}
		default:
			t.Fatalf("unexpected membership request: %s", r.URL.Path)
		}
		end := min(req.Page.Start+req.Page.Limit, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[req.Page.Start:end]}})
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.page_size", 1)
	conf.Set("inventory.cmdb.sources.test.device_condition", map[string]any{"idc_id": 7, "parent_type_id": 52})
	return NewPODMembershipWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}

func TestPODMembershipWorkerWritesOnlyNormalizedRelations(t *testing.T) {
	graph := &uplinkGraph{}
	worker := podMembershipFixture(t, graph, nil)
	snapshot := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, snapshot)
	require.NoError(t, err)
	require.Equal(t, 3, result.Edges)
	require.Equal(t, 3, result.Objects)
	require.Zero(t, result.Skipped)
	require.Equal(t, 1, result.Reasons["no_pod_references"])
	for _, relation := range graph.writes {
		require.NoError(t, model.ValidateRelation(relation))
		require.Equal(t, snapshot, relation.CreatedAt)
		require.Equal(t, snapshot, relation.SyncedAt)
		require.Equal(t, model.EntityDevice, relation.Identity.From.EntityType)
		require.Equal(t, model.EntityPod, relation.Identity.To.EntityType)
		require.Equal(t, "s", relation.Identity.To.SourceID)
	}
}

func TestPODMembershipWorkerMissingEndpointCountsUniqueVertices(t *testing.T) {
	graph := &uplinkGraph{missing: "main"}
	result, err := podMembershipFixture(t, graph, nil).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, result.Edges)
	require.Equal(t, 2, result.Skipped)
	require.Equal(t, 1, result.MissingEndpoints)
}

func TestPODMembershipWorkerRejectsDuplicatesBeforeAnyGraphAccess(t *testing.T) {
	graph := &uplinkGraph{}
	worker := podMembershipFixture(t, graph, func(_ int, items []map[string]any) { items[1]["device_sn"] = "device-a" })
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.Error(t, err)
	require.Empty(t, graph.writes)
	require.Empty(t, graph.checks)
}

func TestPODMembershipWorkerDetectsSecondPassSourceChanges(t *testing.T) {
	graph := &uplinkGraph{}
	worker := podMembershipFixture(t, graph, func(pass int, items []map[string]any) {
		if pass > 1 {
			items[0]["pod_id"] = 20
		}
	})
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.ErrorContains(t, err, "source changed")
	require.Equal(t, 3, result.Edges)
}

func TestPODMembershipWorkerReadsAllEndpointsBeforeWrites(t *testing.T) {
	failure := errors.New("endpoint unavailable")
	graph := &uplinkGraph{readErr: failure, readFailAt: 4}
	_, err := podMembershipFixture(t, graph, nil).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.ErrorIs(t, err, failure)
	require.Empty(t, graph.writes)
}
