package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

type uplinkTestClient struct {
	t       *testing.T
	at      time.Time
	failure string
	empty   bool
}

type uplinkControlTest struct {
	repository.InventoryRepository
	mu                     sync.Mutex
	status                 string
	transitions            []string
	renewed                chan string
	renewFailure           bool
	canceled               bool
	awaitPublishingRenewal bool
	completed              bool
	cursor                 repository.DeviceSyncCursor
	beginSnapshot          time.Time
}

func TestServerUplinkHeartbeatObservesCancellationDuringCollection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	control := &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 2048)}
	runner := uplinkRunnerTest(func(runCtx context.Context, _ model.Source, _ time.Time) (service.ServerUplinkResult, error) {
		control.mu.Lock()
		control.canceled = true
		control.mu.Unlock()
		<-runCtx.Done()
		return service.ServerUplinkResult{}, runCtx.Err()
	})
	_, err := executeServerUplink(ctx, control, runner, run, model.Source{ID: "source"}, time.Millisecond, func(context.Context, time.Time, int64) error { return nil })
	if err == nil || ctx.Err() != nil {
		t.Fatalf("heartbeat did not stop canceled collection: err=%v parent=%v", err, ctx.Err())
	}
	if control.status != model.SyncRunStatusCanceled {
		t.Fatalf("status %s", control.status)
	}
}

func (c *uplinkControlTest) RenewRunLease(context.Context, string, time.Time) error {
	c.mu.Lock()
	status, failed := c.status, c.renewFailure
	c.mu.Unlock()
	if failed {
		return errors.New("lease expired; private upstream details")
	}
	c.renewed <- status
	return nil
}
func (c *uplinkControlTest) RunCancellationRequested(context.Context, string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canceled, nil
}
func (c *uplinkControlTest) TransitionRun(ctx context.Context, id, from, to string, at time.Time, code string) (*model.SyncRun, error) {
	c.mu.Lock()
	if c.status != from {
		c.mu.Unlock()
		return nil, errors.New("wrong status")
	}
	c.status = to
	c.transitions = append(c.transitions, to)
	c.mu.Unlock()
	if to == model.SyncRunStatusPublishing && c.awaitPublishingRenewal {
		select {
		case <-c.renewed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &model.SyncRun{ID: id, Status: to}, nil
}
func (c *uplinkControlTest) BeginCheckpoint(_ context.Context, _, resource, run string, snapshot, _ time.Time) (*repository.DeviceSyncCheckpoint, error) {
	if resource != serverUplinkResource {
		return nil, errors.New("wrong resource")
	}
	c.beginSnapshot = snapshot
	return &repository.DeviceSyncCheckpoint{Cursor: repository.DeviceSyncCursor{Version: 1, RunID: run, SnapshotAt: snapshot}}, nil
}
func (c *uplinkControlTest) AdvanceCheckpoint(_ context.Context, _, resource, _ string, cursor repository.DeviceSyncCursor, _ time.Time) (*repository.DeviceSyncCheckpoint, error) {
	if resource != serverUplinkResource {
		return nil, errors.New("wrong resource")
	}
	c.cursor = cursor
	return &repository.DeviceSyncCheckpoint{Cursor: cursor}, nil
}
func (c *uplinkControlTest) CompleteCheckpointAndRun(_ context.Context, _, _, run, expected string, _ time.Time) (*model.SyncRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status != model.SyncRunStatusPublishing || expected != c.status || c.canceled {
		return nil, errors.New("completion invalid")
	}
	c.completed = true
	c.status = model.SyncRunStatusSucceeded
	return &model.SyncRun{ID: run, Status: c.status}, nil
}

type uplinkRunnerTest func(context.Context, model.Source, time.Time) (service.ServerUplinkResult, error)

func (f uplinkRunnerTest) Run(ctx context.Context, source model.Source, at time.Time) (service.ServerUplinkResult, error) {
	return f(ctx, source, at)
}

func TestServerUplinkExecutionKeepsLeaseThroughPublishing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	at := time.Now().UTC().Truncate(time.Microsecond)
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	control := &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16), awaitPublishingRenewal: true}
	worker := uplinkRunnerTest(func(ctx context.Context, source model.Source, snapshot time.Time) (service.ServerUplinkResult, error) {
		require.Equal(t, at, snapshot)
		select {
		case status := <-control.renewed:
			require.Equal(t, model.SyncRunStatusRunning, status)
		case <-ctx.Done():
			return service.ServerUplinkResult{}, ctx.Err()
		}
		return service.ServerUplinkResult{Edges: 3, Servers: 2, Skipped: 4, MissingEndpoints: 1}, nil
	})
	result, err := executeServerUplink(ctx, control, worker, run, model.Source{ID: "source"}, time.Millisecond, func(ctx context.Context, snapshot time.Time, expected int64) error {
		require.Equal(t, at, snapshot)
		require.Equal(t, int64(3), expected)
		select {
		case status := <-control.renewed:
			require.Equal(t, model.SyncRunStatusValidating, status)
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, result.Edges)
	require.Equal(t, 4, result.Skipped)
	require.Equal(t, 1, result.MissingEndpoints)
	require.True(t, control.completed)
	require.Equal(t, []string{model.SyncRunStatusValidating, model.SyncRunStatusPublishing}, control.transitions)
	require.Equal(t, at, control.beginSnapshot)
	require.Equal(t, at, control.cursor.SnapshotAt)
	require.Equal(t, "run", control.cursor.RunID)
	require.Equal(t, 3, control.cursor.NextOffset)
	require.Equal(t, 3, control.cursor.Total)
}

func TestServerUplinkExecutionNeverSucceedsAfterLeaseLossOrCancellation(t *testing.T) {
	for _, failure := range []string{"lease", "cancel", "write", "validation"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			at := time.Now().UTC().Truncate(time.Microsecond)
			lease := at.Add(time.Minute)
			run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
			control := &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16), renewFailure: failure == "lease", canceled: failure == "cancel"}
			worker := uplinkRunnerTest(func(ctx context.Context, _ model.Source, _ time.Time) (service.ServerUplinkResult, error) {
				if failure == "lease" {
					<-ctx.Done()
					return service.ServerUplinkResult{}, ctx.Err()
				}
				if failure == "write" {
					return service.ServerUplinkResult{}, errors.New("credential=secret")
				}
				return service.ServerUplinkResult{}, nil
			})
			_, err := executeServerUplink(ctx, control, worker, run, model.Source{ID: "source"}, time.Millisecond, func(context.Context, time.Time, int64) error {
				if failure == "validation" {
					return errors.New("bad path")
				}
				return nil
			})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.False(t, control.completed)
			if failure == "cancel" {
				require.Equal(t, model.SyncRunStatusCanceled, control.status)
			} else {
				require.Equal(t, model.SyncRunStatusFailed, control.status)
			}
		})
	}
}

func (*uplinkTestClient) Close() {}
func (c *uplinkTestClient) ExecuteParameter(_ context.Context, query string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	require.Equal(c.t, "source", params["source_id"])
	require.Equal(c.t, "server_uplink", params["relation_kind"])
	if c.failure == "query" {
		return nil, errors.New("unavailable")
	}
	identity := model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "SN"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "PORT"}}
	id, _ := identity.ID()
	rank, _ := identity.Rank()
	at := c.at.Format(time.RFC3339Nano)
	count := int64(1)
	if c.empty {
		count = 0
	}
	var rows [][]any
	columns := 1
	switch {
	case strings.Contains(query, "LOOKUP") && strings.Contains(query, "count(*)"):
		rows = [][]any{{count}}
	case strings.Contains(query, "LOOKUP"):
		columns = 6
		if c.failure == "timestamp" {
			at = c.at.Add(time.Second).Format(time.RFC3339Nano)
		}
		if c.failure == "raw rank" {
			rank = 0
		}
		if c.failure == "raw mismatched rank" {
			rank++
		}
		src, _ := identity.From.VID()
		dst, _ := identity.To.VID()
		if c.failure == "raw endpoint" {
			dst = "wrong-vertex"
		}
		if !c.empty && c.failure != "count" {
			rows = [][]any{{src, dst, rank, id, c.at.Format(time.RFC3339Nano), at}}
		}
	case strings.Contains(query, "count(u)"):
		require.Contains(c.t, query, "s.device.parent_type_id == 52")
		require.Contains(c.t, query, "t.device.parent_type_id == 54")
		require.Contains(c.t, query, `p.interface.port_type == "physics"`)
		require.Contains(c.t, query, `o.relation_kind == "owns_interface"`)
		require.NotContains(c.t, query, "p.interface.synced_at ==")
		if c.failure == "endpoint" {
			count = 0
		}
		rows = [][]any{{count}}
	default:
		columns = 10
		require.NotContains(c.t, query, "t.device.parent_type_id == 54", "不能先过滤错误 owner，否则多归属被隐藏")
		require.NotContains(c.t, query, "LIMIT 5", "必须逐边校验全部路径")
		if c.failure == "rank" {
			rank++
		}
		if c.failure == "identity" {
			id = "wrong"
		}
		owner := int64(54)
		if c.failure == "owner" {
			owner = 52
		}
		if !c.empty && c.failure != "endpoint" {
			rows = [][]any{{"SN", "PORT", "TOR", owner, id, rank, at, int64(52), "physics", "source"}}
			if c.failure == "multiple owners" {
				rows = append(rows, []any{"SN", "PORT", "WRONG", int64(52), id, rank, at, int64(52), "physics", "source"})
			}
		}
	}
	data := &nebula.DataSet{}
	for i := 0; i < columns; i++ {
		data.ColumnNames = append(data.ColumnNames, []byte(fmt.Sprint(i)))
	}
	for _, row := range rows {
		r := &nebula.Row{}
		for _, value := range row {
			switch v := value.(type) {
			case string:
				r.Values = append(r.Values, &nebula.Value{SVal: []byte(v)})
			case int64:
				x := v
				r.Values = append(r.Values, &nebula.Value{IVal: &x})
			}
		}
		data.Rows = append(data.Rows, r)
	}
	return nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
}

func TestServerUplinkVerificationRejectsFalseSuccess(t *testing.T) {
	at := time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	for _, failure := range []string{"", "query", "count", "timestamp", "raw rank", "raw mismatched rank", "raw endpoint", "endpoint", "rank", "identity", "owner", "multiple owners"} {
		t.Run(failure, func(t *testing.T) {
			r, err := verifyServerUplinkGraph(context.Background(), &uplinkTestClient{t: t, at: at, failure: failure}, "source", at, 1)
			if failure != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(1), r.ValidEndpointCount)
			require.Len(t, r.Samples, 1)
		})
	}
}

func TestServerUplinkVerificationAllowsExplicitEmptySnapshot(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	r, err := verifyServerUplinkGraph(context.Background(), &uplinkTestClient{t: t, at: at, empty: true}, "source", at, 0)
	require.NoError(t, err)
	require.Zero(t, r.Current)
	require.Empty(t, r.Samples)
}

func TestServerUplinkModeSelections(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		verify, filtered, once, sync, uplinkVerify bool
		source, entity, run                        string
		valid                                      bool
	}{
		{"daemon", false, false, false, false, false, "", "", "", true},
		{"sync", false, false, false, true, false, "source", "", "", true},
		{"preflight", false, false, false, false, true, "source", "", "", true},
		{"completed", false, false, false, false, true, "source", "", "run", true},
		{"two modes", true, false, false, true, false, "source", "", "", false},
		{"filtered", false, true, false, true, false, "source", "", "", false},
		{"once", false, false, true, true, false, "source", "", "", false},
		{"entity", false, false, false, true, false, "source", "device", "", false},
		{"run on sync", false, false, false, true, false, "source", "", "run", false},
		{"missing source", false, false, false, true, false, "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.valid, validWorkerModes(tc.verify, tc.filtered, tc.once, tc.sync, tc.uplinkVerify, tc.source, tc.entity, tc.run))
		})
	}
}

func TestServerUplinkPersistenceChecksCompletedRunAndEmptyClaim(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	finished := at.Add(time.Second)
	for _, failure := range []string{"", "lease", "run", "snapshot", "offset", "count", "incomplete"} {
		t.Run(failure, func(t *testing.T) {
			r := &serverUplinkPersistenceReport{Status: "preflight", SourceID: "source", Run: &persistenceRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusSucceeded, StartedAt: &at, FinishedAt: &finished}, Checkpoint: &persistenceCheckpoint{Complete: true, CompletedAt: &finished, Cursor: persistenceCursor{Version: 1, RunID: "run", SnapshotAt: at}}, Graph: &serverUplinkGraphReport{Samples: []serverUplinkSample{}}}
			switch failure {
			case "lease":
				r.Run.LeaseExpiresAt = &finished
			case "run":
				r.Checkpoint.Cursor.RunID = "other"
			case "snapshot":
				r.Checkpoint.Cursor.SnapshotAt = finished
			case "offset":
				r.Checkpoint.Cursor.NextOffset = 1
			case "count":
				r.Graph.Current = 1
			case "incomplete":
				r.Checkpoint.Complete = false
			}
			err := validateServerUplinkPersistenceReport(r)
			if failure != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "PASS", r.Status)
			require.False(t, r.TopologyPresent)
			require.Contains(t, r.Note, "empty")
		})
	}
}
