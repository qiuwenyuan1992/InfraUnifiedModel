package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGPUSyncProgressEveryThousandGraphRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Page struct{ Start, Limit int } }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		object := strings.TrimPrefix(r.URL.Path, "/pub/api/v3/find/instance/object/")
		items := []map[string]any{}
		switch object {
		case "device_view":
			items = append(items, map[string]any{"inst_id": 1, "device_sn": "server-a", "parent_type_id": 52, "device_type_id": 58})
		case "server_gpu":
			for i := 1; i <= 501; i++ {
				items = append(items, map[string]any{"inst_id": i, "uuid": fmt.Sprintf("uuid-%d", i), "parts_sn": fmt.Sprintf("parts-%d", i), "device_sn": "server-a", "slot": fmt.Sprint(i), "model": "GPU", "VRAM": "80G"})
			}
		case "gpu_uplink":
		default:
			t.Errorf("unexpected object %s", object)
		}
		start := min(request.Page.Start, len(items))
		end := min(start+request.Page.Limit, len(items))
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": len(items), "info": items[start:end]}})
	}))
	defer server.Close()
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	conf.Set(prefix+"base_url", server.URL)
	conf.Set(prefix+"allow_insecure_http", true)
	conf.Set(prefix+"entity_type", "gpu")
	conf.Set(prefix+"device_condition", map[string]any{"idc_id": 7})
	conf.Set(prefix+"page_size", 200)
	core, observed := observer.New(zap.InfoLevel)
	worker := NewGPUSyncWorker(adapter.NewCMDBWithClient(conf, server.Client()), &gpuGraph{}).WithLogger(&log.Logger{Logger: zap.New(core)})
	result, err := worker.Run(WithSyncRunID(context.Background(), "run-progress"), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err != nil || result.Nodes != 501 || result.Edges != 501 {
		t.Fatalf("bulk result=%+v error=%v", result, err)
	}
	entries := observed.FilterMessage("GPU graph write progress").All()
	if len(entries) != 1 || entries[0].ContextMap()["graph_records"] != int64(1000) || entries[0].ContextMap()["run_id"] != "run-progress" {
		t.Fatalf("incorrect progress: %+v", entries)
	}
}

func TestSyncFailureCategoryUsesSafeTypedCause(t *testing.T) {
	for _, test := range []struct {
		cause    error
		category string
	}{{adapter.ErrCMDBConfig, "cmdb_configuration"}, {adapter.ErrCMDBResponse, "cmdb_response"}, {adapter.ErrCMDBRecord, "cmdb_record"}, {errSyncSnapshotChanged, "source_changed"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"}} {
		if got := SyncFailureCategory(fmt.Errorf("token=private-secret: %w", test.cause)); got != test.category {
			t.Errorf("category=%s want=%s", got, test.category)
		}
	}
}

func TestGPUSyncCollectionFailureStages(t *testing.T) {
	graph := &gpuGraph{}
	reads := 0
	worker := gpuWorkerFixture(t, graph, func(object string, read int, item map[string]any) {
		if object == "gpu_uplink" {
			reads = read
			item["inst_id"] = 0
		}
	})
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	var failure *GPUSyncError
	if !errors.As(err, &failure) || failure.Stage != "source_collect" || failure.Code != "gpu_source_collect_failed" {
		t.Fatalf("stage lost: %v", err)
	}
	if reads != 1 || result.Nodes != 0 || result.Edges != 0 || len(graph.nodes) != 0 || len(graph.edges) != 0 {
		t.Fatalf("first-pass failure must precede writes: reads=%d result=%+v", reads, result)
	}
}
