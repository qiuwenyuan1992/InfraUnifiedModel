package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestLinksToModes(t *testing.T) {
	for _, tc := range []struct {
		name                string
		sync, verify, other bool
		source, entity, run string
		valid               bool
	}{
		{"sync", true, false, false, "source", "", "", true},
		{"preflight", false, true, false, "source", "", "", true},
		{"completed", false, true, false, "source", "", "run", true},
		{"both", true, true, false, "source", "", "", false},
		{"other", true, false, true, "source", "", "", false},
		{"missing source", true, false, false, "", "", "", false},
		{"entity", true, false, false, "source", "interface", "", false},
		{"run sync", true, false, false, "source", "", "run", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.valid, validLinksToModes(tc.other, false, false, false, false, tc.sync, tc.verify, tc.source, tc.entity, tc.run))
		})
	}
}

type linksToTestClient struct {
	t       *testing.T
	at      time.Time
	failure string
	empty   bool
}

func (*linksToTestClient) Close() {}
func (c *linksToTestClient) ExecuteParameter(_ context.Context, q string, p map[string]interface{}) (*nebulago.ResultSet, error) {
	require.Equal(c.t, "source", p["source_id"])
	require.Equal(c.t, "links_to", p["relation_kind"])
	if c.failure == "query" {
		return nil, errors.New("query failed")
	}
	identity := model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "port-a"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "port-b"}}
	if c.failure == "escaped identity" {
		identity.From.StableID = "A"
		identity.To.StableID = "<"
	}
	id, _ := identity.ID()
	rank, _ := identity.Rank()
	src, _ := identity.From.VID()
	dst, _ := identity.To.VID()
	at := c.at.Format(time.RFC3339Nano)
	var rows [][]any
	columns := 1
	switch {
	case strings.Contains(q, "count(*)"):
		n := int64(1)
		if c.empty {
			n = 0
		}
		rows = [][]any{{n}}
	case strings.Contains(q, "LOOKUP"):
		columns = 6
		if !c.empty && c.failure != "count" {
			rows = [][]any{{src, dst, rank, id, at, at}}
			switch c.failure {
			case "raw rank":
				rows[0][2] = rank + 1
			case "raw vid":
				rows[0][0] = "wrong"
			case "raw timestamp":
				rows[0][5] = c.at.Add(time.Second).Format(time.RFC3339Nano)
			case "created":
				rows[0][4] = c.at.Add(time.Second).Format(time.RFC3339Nano)
			case "duplicate edge":
				rows = append(rows, rows[0])
			}
		}
	default:
		columns = 18
		require.Contains(c.t, q, "(d1:device)-[o1:composition_relation]->(p1:interface)-[l:network_relation]->(p2:interface)<-[o2:composition_relation]-(d2:device)")
		require.Contains(c.t, q, "o1.source_id == $source_id")
		require.Contains(c.t, q, "o2.source_id == $source_id")
		require.NotContains(c.t, q, "d1.device.source_id == $source_id")
		require.NotContains(c.t, q, "d2.device.source_id == $source_id")
		require.NotContains(c.t, q, "parent_type_id == 54")
		require.NotContains(c.t, q, `port_type == "physics"`)
		require.NotContains(c.t, q, "LIMIT")
		require.NotContains(c.t, q, "p1.interface.synced_at ==")
		if !c.empty && c.failure != "missing path" {
			d1 := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "SW-A"}
			d2 := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "SW-B"}
			v1, _ := d1.VID()
			v2, _ := d2.VID()
			rows = [][]any{{"SW-A", int64(54), "source", identity.From.StableID, "physics", "SW-B", int64(54), "source", identity.To.StableID, "physics", id, rank, at, at, src, dst, v1, v2}}
			switch c.failure {
			case "owner class":
				rows[0][1] = int64(52)
			case "owner source":
				rows[0][7] = "other"
			case "virtual":
				rows[0][9] = "logical"
			case "uuid whitespace":
				rows[0][3] = " port-a "
			case "direction":
				rows[0][3], rows[0][8] = "port-b", "port-a"
			case "self port":
				rows[0][8] = "port-a"
			case "path id":
				rows[0][10] = "wrong"
			case "owner vid":
				rows[0][16] = "wrong"
			case "port vid":
				rows[0][14] = "wrong"
			case "multiple paths":
				rows = append(rows, rows[0])
			case "same owner":
				rows[0][5] = "SW-A"
				rows[0][17] = v1
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
func TestLinksToGraphRejectsFalseSuccess(t *testing.T) {
	at := time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	for _, failure := range []string{"", "same owner", "query", "count", "raw rank", "raw vid", "raw timestamp", "created", "duplicate edge", "missing path", "owner class", "owner source", "virtual", "uuid whitespace", "direction", "self port", "path id", "owner vid", "port vid", "multiple paths"} {
		t.Run(failure, func(t *testing.T) {
			r, err := verifyLinksToGraph(context.Background(), &linksToTestClient{t: t, at: at, failure: failure}, "source", at, 1)
			if failure != "" && failure != "same owner" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(1), r.ValidEndpointCount)
			require.Len(t, r.Samples, 1)
			require.Equal(t, "port-a", r.Samples[0].FromPortUUID)
		})
	}
}
func TestLinksToVerificationUsesModelCanonicalOrdering(t *testing.T) {
	at := serverUplinkNow()
	r, err := verifyLinksToGraph(context.Background(), &linksToTestClient{t: t, at: at, failure: "escaped identity"}, "source", at, 1)
	require.NoError(t, err)
	require.Equal(t, "A", r.Samples[0].FromPortUUID)
	require.Equal(t, "<", r.Samples[0].ToPortUUID)
}

func TestLinksToExplicitEmptyAndPreflight(t *testing.T) {
	at := serverUplinkNow()
	r, err := verifyLinksToGraph(context.Background(), &linksToTestClient{t: t, at: at, empty: true}, "source", at, 0)
	require.NoError(t, err)
	require.Zero(t, r.Current)
	require.Empty(t, r.Samples)
	r, err = verifyLinksToGraph(context.Background(), &linksToTestClient{t: t, at: at}, "source", time.Time{}, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), r.Total)
	require.Zero(t, r.Current)
}

type linksToRunnerTest func(context.Context, model.Source, time.Time) (service.LinksToResult, error)

func (f linksToRunnerTest) Run(c context.Context, s model.Source, at time.Time) (service.LinksToResult, error) {
	return f(c, s, at)
}

type linksToControlTest struct {
	*uplinkControlTest
	resources []string
}

func (c *linksToControlTest) BeginCheckpoint(ctx context.Context, s, resource, run string, snapshot, at time.Time) (*repository.DeviceSyncCheckpoint, error) {
	c.resources = append(c.resources, resource)
	requireResource := resource == linksToResource
	if !requireResource {
		return nil, errors.New("wrong resource")
	}
	return c.uplinkControlTest.BeginCheckpoint(ctx, s, serverUplinkResource, run, snapshot, at)
}
func (c *linksToControlTest) AdvanceCheckpoint(ctx context.Context, s, resource, run string, cursor repository.DeviceSyncCursor, at time.Time) (*repository.DeviceSyncCheckpoint, error) {
	c.resources = append(c.resources, resource)
	if resource != linksToResource {
		return nil, errors.New("wrong resource")
	}
	return c.uplinkControlTest.AdvanceCheckpoint(ctx, s, serverUplinkResource, run, cursor, at)
}
func (c *linksToControlTest) CompleteCheckpointAndRun(ctx context.Context, s, resource, run, status string, at time.Time) (*model.SyncRun, error) {
	c.resources = append(c.resources, resource)
	if resource != linksToResource {
		return nil, errors.New("wrong resource")
	}
	return c.uplinkControlTest.CompleteCheckpointAndRun(ctx, s, resource, run, status, at)
}
func TestLinksToExecutionMapsOnlyResourceAndPreservesResult(t *testing.T) {
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	c := &linksToControlTest{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16)}}
	want := service.LinksToResult{Edges: 3, Devices: 2, Skipped: 4, MissingEndpoints: 1}
	got, err := executeLinksTo(context.Background(), c, linksToRunnerTest(func(_ context.Context, s model.Source, snapshot time.Time) (service.LinksToResult, error) {
		require.Equal(t, "source", s.ID)
		require.Equal(t, at, snapshot)
		return want, nil
	}), run, model.Source{ID: "source"}, time.Minute, func(_ context.Context, snapshot time.Time, n int64) error {
		require.Equal(t, at, snapshot)
		require.Equal(t, int64(3), n)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, []string{linksToResource, linksToResource, linksToResource}, c.resources)
	require.Zero(t, c.cursor.LastInstanceID)
	require.Zero(t, c.cursor.LastPartition)
	require.True(t, c.completed)
}
func TestLinksToPersistenceRejectsMismatchAndEmptyClaim(t *testing.T) {
	at := serverUplinkNow()
	finished := at.Add(time.Second)
	for _, failure := range []string{"", "lease", "run", "offset", "snapshot", "count", "incomplete"} {
		t.Run(failure, func(t *testing.T) {
			r := &linksToPersistenceReport{Status: "preflight", SourceID: "source", Run: &persistenceRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusSucceeded, StartedAt: &at, FinishedAt: &finished}, Checkpoint: &persistenceCheckpoint{Complete: true, CompletedAt: &finished, Cursor: persistenceCursor{Version: 1, RunID: "run", SnapshotAt: at}}, Graph: &linksToGraphReport{Samples: []linksToSample{}}}
			switch failure {
			case "lease":
				r.Run.LeaseExpiresAt = &finished
			case "run":
				r.Checkpoint.Cursor.RunID = "other"
			case "offset":
				r.Checkpoint.Cursor.NextOffset = 1
			case "snapshot":
				r.Checkpoint.Cursor.SnapshotAt = finished
			case "count":
				r.Graph.Current = 1
			case "incomplete":
				r.Checkpoint.Complete = false
			}
			err := validateLinksToPersistenceReport(r)
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
