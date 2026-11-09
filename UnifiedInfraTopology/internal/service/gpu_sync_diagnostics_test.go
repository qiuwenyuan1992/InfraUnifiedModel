package service

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/pkg/log"
	"context"
	"errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"strings"
	"testing"
	"time"
)

type diagnosticGPUGraph struct {
	*gpuGraph
	stage string
	cause error
}

func (g *diagnosticGPUGraph) VertexExists(c context.Context, id model.EntityIdentity) (bool, error) {
	if g.stage == "endpoint_check" {
		return false, g.cause
	}
	return g.gpuGraph.VertexExists(c, id)
}
func (g *diagnosticGPUGraph) UpsertVertex(c context.Context, v repository.TopologyVertex) error {
	if g.stage == "node_write" {
		return g.cause
	}
	return g.gpuGraph.UpsertVertex(c, v)
}
func (g *diagnosticGPUGraph) UpsertRelation(c context.Context, r model.Relation) error {
	if (g.stage == "containment_write" && r.Identity.Kind == model.RelationContainsGPU) || (g.stage == "uplink_write" && r.Identity.Kind == model.RelationGPUUplink) {
		return g.cause
	}
	return g.gpuGraph.UpsertRelation(c, r)
}

func TestGPUSyncStageFailuresPreserveCauseWithoutSecrets(t *testing.T) {
	for _, stage := range []string{"endpoint_check", "node_write", "containment_write", "uplink_write"} {
		t.Run(stage, func(t *testing.T) {
			cause := errors.New("token=private-secret https://user:password@example.test/private")
			graph := &diagnosticGPUGraph{gpuGraph: &gpuGraph{}, stage: stage, cause: cause}
			worker := gpuWorkerFixture(t, graph.gpuGraph, nil)
			worker.graph = graph
			result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
			var failure *GPUSyncError
			if !errors.As(err, &failure) || failure.Stage != stage || failure.Code != "gpu_"+stage+"_failed" || !errors.Is(err, cause) {
				t.Fatalf("stage error not preserved: %v", err)
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "password") {
				t.Fatal("public error leaked credentials")
			}
			if stage == "containment_write" && result.Nodes != 1 {
				t.Fatalf("partial count lost: %+v", result)
			}
			if stage == "uplink_write" && (result.Nodes != 1 || result.Edges != 1) {
				t.Fatalf("partial count lost: %+v", result)
			}
			if failure.Details["nodes"] != result.Nodes || failure.Details["edges"] != result.Edges {
				t.Fatalf("diagnostic partial counts lost: %+v", failure.Details)
			}
		})
	}
}

func TestGPUSyncLogsSinglePassStageTiming(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	reads := 0
	worker := gpuWorkerFixture(t, &gpuGraph{}, func(object string, read int, item map[string]any) {
		if object == "gpu_uplink" {
			reads = read
			if read > 1 {
				item["pod_id"] = 99
			}
		}
	}).WithLogger(&log.Logger{Logger: zap.New(core)})
	result, err := worker.Run(context.Background(), model.Source{ID: "s", ConfigRef: "test"}, time.Now())
	if err != nil || result.Nodes != 1 || result.Edges != 2 || reads != 1 {
		t.Fatalf("single-pass sync failed: result=%+v reads=%d err=%v", result, reads, err)
	}
	stages := map[string]bool{}
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if stage, ok := fields["stage"].(string); ok {
			stages[stage] = true
			if _, ok := fields["duration_ms"]; !ok {
				t.Fatalf("duration absent: %+v", fields)
			}
		}
		if strings.Contains(entry.Message, "parts-a") {
			t.Fatal("sensitive payload logged")
		}
	}
	if stages["source_reread"] || stages["source_changed"] {
		t.Fatal("single-pass sync logged a source reread or change stage")
	}
	for _, stage := range []string{"source_collect", "endpoint_check", "node_write", "containment_write", "uplink_write", "complete"} {
		if !stages[stage] {
			t.Errorf("missing stage %s", stage)
		}
	}
}
