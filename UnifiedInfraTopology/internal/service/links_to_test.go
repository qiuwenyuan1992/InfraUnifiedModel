package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelLinksCandidatesBoundAndCancellation(t *testing.T) {
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var active, maxActive, calls atomic.Int32
	go func() {
		for index := 0; index < 8; index++ {
			<-started
		}
		close(release)
	}()
	err := parallelLinksCandidates(context.Background(), 32, func(ctx context.Context, _ int) error {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if old >= n || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		if calls.Add(1) <= 8 {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int32(32), calls.Load())
	require.Equal(t, int32(8), maxActive.Load())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = parallelLinksCandidates(ctx, 2, func(context.Context, int) error { t.Error("canceled candidate executed"); return nil })
	require.ErrorIs(t, err, context.Canceled)
}

type linksGraph struct {
	uplinkGraph
	eligible    bool
	endpointErr error
}

func (g *linksGraph) LinksToEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error) {
	return g.eligible, g.endpointErr
}
func linksWorkerFixture(t *testing.T, graph *linksGraph, changed bool, onPass ...func(int)) *LinksToWorker {
	pass := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Condition map[string]any             `json:"condition"`
			Page      struct{ Start, Limit int } `json:"page"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		items := []map[string]any{}
		if r.URL.Path == "/pub/api/v3/find/instance/object/device_view" {
			if req.Condition["idc_id"] != nil {
				require.Equal(t, float64(54), req.Condition["parent_type_id"])
				if req.Page.Start == 0 {
					pass++
					for _, notify := range onPass {
						notify(pass)
					}
				}
				items = []map[string]any{{"inst_id": 1, "device_sn": "a", "parent_type_id": 54}, {"inst_id": 2, "device_sn": "b", "parent_type_id": 54}}
			} else {
				items = []map[string]any{{"inst_id": 3, "device_sn": req.Condition["device_sn"], "parent_type_id": 54}}
			}
		} else {
			require.NotNil(t, req.Condition["sn"])
			require.Nil(t, req.Condition["idc_id"])
			sn := req.Condition["sn"].(string)
			local, remote, owner := "z-port", "a-port", "b"
			if sn == "b" {
				local, remote, owner = "a-port", "z-port", "a"
			}
			if changed && pass > 1 {
				remote = "wrong-uuid"
			}
			items = []map[string]any{{"inst_id": 10, "port_uuid": local, "port_type": "physics", "local_device_sn": sn, "remote_device_sn": owner, "remote_interface_uuid": remote, "remote_interface_type": "physics"}}
		}
		count := len(items)
		start := min(req.Page.Start, count)
		end := min(start+req.Page.Limit, count)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": count, "info": items[start:end]}}))
	}))
	t.Cleanup(server.Close)
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.test.base_url", server.URL)
	conf.Set("inventory.cmdb.sources.test.allow_insecure_http", true)
	conf.Set("inventory.cmdb.sources.test.page_size", 1)
	conf.Set("inventory.cmdb.sources.test.interface_condition", map[string]any{"idc_id": 1030})
	return NewLinksToWorker(adapter.NewCMDBWithClient(conf, server.Client()), graph)
}
func TestLinksToWorkerUsesSingleCompleteCollection(t *testing.T) {
	graph := &linksGraph{eligible: true}
	passes := 0
	worker := linksWorkerFixture(t, graph, true, func(pass int) { passes = pass })
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, passes)
	require.Equal(t, 2, result.Devices)
	require.Equal(t, 1, result.Edges)
	require.Len(t, graph.writes, 1)
}

func TestLinksToWorkerCanonicalDedupAndNoVertexWrites(t *testing.T) {
	graph := &linksGraph{eligible: true}
	at := time.Now().UTC()
	result, err := linksWorkerFixture(t, graph, false).Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, at)
	require.NoError(t, err)
	require.Equal(t, 1, result.Edges)
	require.Equal(t, 2, result.Devices)
	require.Len(t, graph.writes, 1)
	require.Equal(t, "a-port", graph.writes[0].Identity.From.StableID)
	require.Equal(t, at, graph.writes[0].SyncedAt)
}
func TestLinksToWorkerFailsClosed(t *testing.T) {
	for _, failure := range []string{"missing endpoint", "read error", "write error", "canceled", "no snapshot"} {
		t.Run(failure, func(t *testing.T) {
			graph := &linksGraph{eligible: true}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			at := time.Now()
			switch failure {
			case "missing endpoint":
				graph.eligible = false
			case "read error":
				graph.endpointErr = errors.New("read")
			case "write error":
				graph.writeErr = errors.New("write")
			case "canceled":
				cancel()
			case "no snapshot":
				at = time.Time{}
			}
			result, err := linksWorkerFixture(t, graph, false).Run(ctx, model.Source{ID: "s", ConfigRef: "test"}, at)
			if failure == "missing endpoint" {
				require.NoError(t, err)
				require.Zero(t, result.Edges)
				require.Equal(t, 1, result.MissingEndpoints)
			} else {
				require.Error(t, err)
			}
			if failure != "write error" {
				require.Empty(t, graph.writes)
			}
		})
	}
}
