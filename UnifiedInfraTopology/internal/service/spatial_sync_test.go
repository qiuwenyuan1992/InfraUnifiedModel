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

func spatialWorkerFixture(t *testing.T, graph *uplinkGraph, mutate func(string, int, []map[string]any)) *SpatialSyncWorker {
	t.Helper()
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Page      struct{ Start, Limit int }
			Condition map[string]any
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		object := r.URL.Path[len("/pub/api/v3/find/instance/object/"):]
		var rows []map[string]any
		switch object {
		case "device_view":
			rows = []map[string]any{{"inst_id": 1, "device_sn": "D1", "cabinet_uuid": "C", "idc_id": 7, "parent_type_id": 52}, {"inst_id": 2, "device_sn": "D2", "cabinet_uuid": "C", "idc_id": 7, "parent_type_id": 52}}
		case "cabinet":
			rows = []map[string]any{{"inst_id": 10, "uuid": "C", "idc_id": 7, "pod_ids": []int{20, 20}}}
		case "idc":
			rows = []map[string]any{{"inst_id": 7, "uuid": "IDC"}}
		case "pod":
			rows = []map[string]any{{"inst_id": 20, "uuid": "P"}, {"inst_id": 21, "uuid": "unused"}}
		default:
			t.Errorf("unexpected spatial endpoint %s", object)
		}
		if req.Page.Start == 0 {
			reads[object]++
		}
		if mutate != nil {
			mutate(object, reads[object], rows)
		}
		end := min(req.Page.Start+req.Page.Limit, len(rows))
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(rows), "info": rows[req.Page.Start:end]}}))
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
	return NewSpatialSyncWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}
func TestSpatialSyncWorkerWritesOnlyNormalizedEdgesWithSharedTime(t *testing.T) {
	graph := &uplinkGraph{}
	worker := spatialWorkerFixture(t, graph, nil)
	snapshot := time.Date(2026, 11, 3, 13, 2, 1, 123456789, time.FixedZone("offset", 8*3600))
	got, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, snapshot)
	require.NoError(t, err)
	require.Equal(t, 4, got.Edges)
	require.Equal(t, 3, got.Objects)
	require.Zero(t, got.Skipped)
	require.Len(t, graph.checks, 5)
	previous := ""
	for _, r := range graph.writes {
		require.NoError(t, model.ValidateRelation(r))
		require.Equal(t, snapshot.UTC().Truncate(time.Microsecond), r.SyncedAt)
		require.Equal(t, r.SyncedAt, r.CreatedAt)
		require.Equal(t, time.UTC, r.SyncedAt.Location())
		require.Equal(t, "s", r.Identity.SourceID)
		require.Equal(t, "s", r.Identity.From.SourceID)
		require.Equal(t, "s", r.Identity.To.SourceID)
		require.Empty(t, r.Properties)
		id, e := r.Identity.ID()
		require.NoError(t, e)
		require.Greater(t, id, previous)
		previous = id
	}
}
func TestSpatialSyncWorkerPrechecksAllEndpointsBeforeWriting(t *testing.T) {
	failure := errors.New("read unavailable")
	graph := &uplinkGraph{readErr: failure, readFailAt: 5}
	_, err := spatialWorkerFixture(t, graph, nil).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.ErrorIs(t, err, failure)
	require.Empty(t, graph.writes)
}
func TestSpatialSyncWorkerCountsUniqueMissingEndpoints(t *testing.T) {
	graph := &uplinkGraph{missing: "C"}
	got, err := spatialWorkerFixture(t, graph, nil).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.NoError(t, err)
	require.Zero(t, got.Edges)
	require.Equal(t, 4, got.Skipped)
	require.Equal(t, 1, got.MissingEndpoints)
	require.Equal(t, 4, got.Reasons["missing_endpoint"])
	require.Len(t, graph.checks, 5)
}
func TestSpatialSyncWorkerWriteFailurePreservesEarlierEdges(t *testing.T) {
	failure := errors.New("write unavailable")
	graph := &uplinkGraph{writeErr: failure, writeFailAt: 1}
	got, err := spatialWorkerFixture(t, graph, nil).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.ErrorIs(t, err, failure)
	require.ErrorContains(t, err, "earlier edges may persist")
	require.Equal(t, 1, got.Edges)
	require.Len(t, graph.writes, 1)
}
func TestSpatialSyncWorkerIndependentRereadDetectsDirectoryAndReferenceChanges(t *testing.T) {
	for _, object := range []string{"device_view", "cabinet", "idc", "pod"} {
		t.Run(object, func(t *testing.T) {
			graph := &uplinkGraph{}
			worker := spatialWorkerFixture(t, graph, func(o string, pass int, rows []map[string]any) {
				if o != object || pass < 2 {
					return
				}
				switch o {
				case "device_view":
					rows[0]["cabinet_uuid"] = "unknown"
				case "cabinet":
					rows[0]["pod_ids"] = []int{21}
				case "idc":
					rows[0]["uuid"] = "new-idc"
				case "pod":
					rows[1]["uuid"] = "changed-unused"
				}
			})
			got, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
			require.ErrorContains(t, err, "source changed")
			require.Equal(t, 4, got.Edges)
			require.Len(t, graph.writes, 4)
		})
	}
}
func TestSpatialSyncWorkerMalformedCollectionPreventsGraphAccess(t *testing.T) {
	graph := &uplinkGraph{}
	worker := spatialWorkerFixture(t, graph, func(o string, _ int, rows []map[string]any) {
		if o == "cabinet" {
			rows[0]["pod_ids"] = "20"
		}
	})
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.Error(t, err)
	require.Empty(t, graph.checks)
	require.Empty(t, graph.writes)
}
func TestSpatialSyncWorkerValidationAndCancellation(t *testing.T) {
	graph := &uplinkGraph{}
	worker := spatialWorkerFixture(t, graph, nil)
	_, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Time{})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = worker.Run(ctx, model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, graph.checks)
}
