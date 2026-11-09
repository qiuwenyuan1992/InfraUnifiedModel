package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"
	"context"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"testing"
	"time"
)

func TestSyncInterfaceLogsPassAndGraphWriteDurations(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	at := time.Now().UTC()
	lease := at.Add(time.Minute)
	runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "run-timing", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}, source: &model.Source{ID: "source", AdapterKind: "cmdb"}}
	page := adapter.EntityPage{Records: []adapter.EntityRecord{{SourceInstanceID: 1, Identity: "private-port", OwnerDeviceSN: "private-owner", Partition: 1}}, NextOffset: 1, Total: 1, Done: true}
	source := &fakeDeviceSource{selected: model.EntityInterface, filtered: true, entityPages: []adapter.EntityPage{page}, entityReplay: []adapter.EntityPage{page}}
	worker := NewSyncWorker(nil, runs, &fakeDeviceGraph{}, source, &log.Logger{Logger: zap.New(core)})
	if err := worker.ProcessClaimed(context.Background(), runs.run, *runs.source); err != nil {
		t.Fatal(err)
	}
	stages := map[string]bool{}
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		stage, ok := fields["stage"].(string)
		if !ok {
			continue
		}
		stages[stage] = true
		if fields["run_id"] != "run-timing" || fields["resource"] != "interfaces" || fields["duration_ms"] == nil {
			t.Fatalf("missing safe timing context: %+v", fields)
		}
		if stage == "page_write" {
			if fields["cumulative_graph_records"] != int64(2) || fields["write_concurrency"] != int64(8) {
				t.Fatalf("wrong graph metrics: %+v", fields)
			}
		}
		if fields["identity"] != nil || fields["properties"] != nil {
			t.Fatal("payload leaked")
		}
	}
	if stages["source_reread"] {
		t.Fatal("source reread must not be logged")
	}
	if source.passOpenCalls != 1 || len(source.offsets) != 1 || source.offsets[0] != 0 {
		t.Fatalf("source must be read once: opens=%d offsets=%v", source.passOpenCalls, source.offsets)
	}
	for _, stage := range []string{"source_collect", "page_collect", "page_write", "complete"} {
		if !stages[stage] {
			t.Errorf("missing timing stage %s", stage)
		}
	}
}

func TestSyncCancellationTimingIsNotMarkedSucceeded(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	at := time.Now().UTC()
	lease := at.Add(time.Minute)
	runs := &fakeWorkerRuns{run: &model.SyncRun{ID: "cancel-timing", SourceID: "s", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}, cancelChecks: []bool{true}}
	worker := NewSyncWorker(nil, runs, &fakeDeviceGraph{}, &fakeDeviceSource{}, &log.Logger{Logger: zap.New(core)})
	if err := worker.ProcessClaimed(context.Background(), runs.run, model.Source{ID: "s", AdapterKind: "cmdb"}); err != nil {
		t.Fatal(err)
	}
	if runs.run.Status != model.SyncRunStatusCanceled {
		t.Fatalf("status %s", runs.run.Status)
	}
	for _, entry := range observed.All() {
		if entry.ContextMap()["stage"] == "complete" {
			if entry.ContextMap()["succeeded"] != false {
				t.Fatal("取消任务不能记为成功")
			}
			return
		}
	}
	t.Fatal("缺少完成状态日志")
}
