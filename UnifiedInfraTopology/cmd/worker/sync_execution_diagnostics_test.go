package main

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type diagnosticUplinkControl struct {
	*uplinkControlTest
	code        string
	diagnostics []*model.SyncDiagnostic
}

func (c *diagnosticUplinkControl) TransitionRun(ctx context.Context, id, from, to string, at time.Time, code string) (*model.SyncRun, error) {
	c.code = code
	return c.uplinkControlTest.TransitionRun(ctx, id, from, to, at, code)
}
func (c *diagnosticUplinkControl) BeginCheckpoint(_ context.Context, _, _, run string, at, _ time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return &repository.DeviceSyncCheckpoint{Cursor: repository.DeviceSyncCursor{Version: 1, RunID: run, SnapshotAt: at}}, nil
}
func (c *diagnosticUplinkControl) RecordSyncExecutionDiagnostic(_ context.Context, d *model.SyncDiagnostic) error {
	c.diagnostics = append(c.diagnostics, d)
	return nil
}
type diagnosticCheckpointFailure struct { *diagnosticUplinkControl; cause error }
func(c *diagnosticCheckpointFailure) BeginCheckpoint(context.Context,string,string,string,time.Time,time.Time)(*repository.DeviceSyncCheckpoint,error){return nil,c.cause}
func TestSharedExecutorCheckpointFailureIsResourceAware(t *testing.T){
 at:=serverUplinkNow();lease:=at.Add(time.Minute)
 run:=&model.SyncRun{ID:"run",SourceID:"source",Status:model.SyncRunStatusRunning,StartedAt:&at,LeaseExpiresAt:&lease}
 base:=&diagnosticUplinkControl{uplinkControlTest:&uplinkControlTest{status:model.SyncRunStatusRunning,renewed:make(chan string,8)}}
 cause:=errors.New("mysql password=private-secret")
 control:=&diagnosticCheckpointFailure{diagnosticUplinkControl:base,cause:cause}
 _,err:=executeServerUplink(context.Background(),standaloneRelationControl{control,gpuSyncResource},nil,run,model.Source{ID:"source"},time.Minute,nil)
 if !errors.Is(err,cause)||err.Error()!="gpu_sync_checkpoint_begin_failed"||base.code!="gpu_sync_checkpoint_begin_failed"{t.Fatalf("checkpoint cause lost: %v code=%s",err,base.code)}
}

func TestSharedExecutorPreservesGPUFailureAndPersistsDiagnostic(t *testing.T) {
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	control := &diagnosticUplinkControl{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 8)}}
	failure := &service.GPUSyncError{Stage: "source_changed", Code: "gpu_source_changed", Details: map[string]any{"original_digest": strings.Repeat("a", 64), "reread_digest": strings.Repeat("b", 64), "original_objects": 5, "reread_objects": 6, "unexpected_secret": "private-token"}}
	runner := uplinkRunnerTest(func(context.Context, model.Source, time.Time) (service.ServerUplinkResult, error) {
		return service.ServerUplinkResult{Edges: 7}, failure
	})
	result, err := executeServerUplink(context.Background(), standaloneRelationControl{control, gpuSyncResource}, runner, run, model.Source{ID: "source"}, time.Minute, func(context.Context, time.Time, int64) error { return nil })
	if !errors.Is(err, failure) || control.code != "gpu_source_changed" || result.Edges != 7 {
		t.Fatalf("failure lost: %v code=%s result=%+v", err, control.code, result)
	}
	if len(control.diagnostics) != 1 {
		t.Fatalf("diagnostics=%+v", control.diagnostics)
	}
	diagnostic := control.diagnostics[0]
	if diagnostic.RunID != "run" || diagnostic.SourceID != "source" || diagnostic.Resource != gpuSyncResource || diagnostic.Code != "gpu_source_changed" || !strings.Contains(diagnostic.Detail, "original_digest") || strings.Contains(diagnostic.Detail, "private-token") {
		t.Fatalf("unsafe or incomplete diagnostic: %+v", diagnostic)
	}
}
func TestSharedExecutorSanitizesGenericResourceFailureAndPreservesCause(t *testing.T) {
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	control := &diagnosticUplinkControl{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 8)}}
	cause := errors.New("Authorization: Bearer private-secret")
	runner := uplinkRunnerTest(func(context.Context, model.Source, time.Time) (service.ServerUplinkResult, error) {
		return service.ServerUplinkResult{}, cause
	})
	_, err := executeServerUplink(context.Background(), standaloneRelationControl{control, podMembershipResource}, runner, run, model.Source{ID: "source"}, time.Minute, func(context.Context, time.Time, int64) error { return nil })
	if !errors.Is(err, cause) || strings.Contains(err.Error(), "private-secret") || control.code != "pod_membership_failed" {
		t.Fatalf("unsafe or misclassified failure: %v code=%s", err, control.code)
	}
}
