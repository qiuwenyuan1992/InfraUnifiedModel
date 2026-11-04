package main

import (
	"context"
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

type relationCLIClient struct {
	t     *testing.T
	query func(string, map[string]interface{}) (int, [][]any)
}

func (*relationCLIClient) Close() {}
func (c *relationCLIClient) ExecuteParameter(_ context.Context, q string, p map[string]interface{}) (*nebulago.ResultSet, error) {
	n, rows := c.query(q, p)
	data := &nebula.DataSet{}
	for i := 0; i < n; i++ {
		data.ColumnNames = append(data.ColumnNames, []byte(fmt.Sprint(i)))
	}
	for _, row := range rows {
		r := &nebula.Row{}
		for _, v := range row {
			switch x := v.(type) {
			case string:
				r.Values = append(r.Values, &nebula.Value{SVal: []byte(x)})
			case int64:
				value := x
				r.Values = append(r.Values, &nebula.Value{IVal: &value})
			}
		}
		data.Rows = append(data.Rows, r)
	}
	return nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
}

func TestPODMembershipGraphChecksUnknownPositivePlaneAndAllPaths(t *testing.T) {
	at := serverUplinkNow()
	stamp := at.Format(time.RFC3339Nano)
	identity := model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeSpatial, Kind: model.RelationMemberOf, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "device"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityPod, StableID: "pod"}}
	identity.Discriminator = "9"
	id, _ := identity.ID()
	rank, _ := identity.Rank()
	from, _ := identity.From.VID()
	to, _ := identity.To.VID()
	for _, failure := range []string{"", "plane", "pod plane", "source", "duplicate", "missing", "rank", "vid"} {
		t.Run(failure, func(t *testing.T) {
			c := &relationCLIClient{t: t, query: func(q string, p map[string]interface{}) (int, [][]any) {
				require.Equal(t, "source", p["source_id"])
				require.NotContains(t, q, "LIMIT")
				if strings.Contains(q, "count(*)") {
					return 1, [][]any{{int64(1)}}
				}
				if strings.Contains(q, "LOOKUP") {
					row := []any{from, to, rank, id, int64(9), stamp, stamp, "source"}
					if failure == "plane" {
						row[4] = int64(0)
					}
					if failure == "rank" {
						row[2] = rank + 1
					}
					if failure == "vid" {
						row[0] = "bad"
					}
					return 8, [][]any{row}
				}
				require.NotContains(t, q, "plane ==")
				require.NotContains(t, q, "parent_type_id ==")
				row := []any{id, rank, from, to, "device", "source", "pod", "source", int64(9)}
				if failure == "pod plane" {
					row[8] = int64(2)
				}
				if failure == "source" {
					row[7] = "other"
				}
				if failure == "missing" {
					return 9, nil
				}
				if failure == "duplicate" {
					return 9, [][]any{row, row}
				}
				return 9, [][]any{row}
			}}
			r, err := verifyPODMembershipGraph(context.Background(), c, "source", at, 1)
			if failure == "" {
				require.NoError(t, err)
				require.Equal(t, int64(1), r.ValidEndpointCount)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type membershipRunnerTest func(context.Context, model.Source, time.Time) (service.PODMembershipResult, error)

func (f membershipRunnerTest) Run(c context.Context, s model.Source, t time.Time) (service.PODMembershipResult, error) {
	return f(c, s, t)
}

type relationResourceControlTest struct {
	*uplinkControlTest
	resource string
	seen     []string
}

func (c *relationResourceControlTest) BeginCheckpoint(ctx context.Context, s, res, r string, t, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	c.seen = append(c.seen, res)
	return c.uplinkControlTest.BeginCheckpoint(ctx, s, serverUplinkResource, r, t, n)
}
func (c *relationResourceControlTest) AdvanceCheckpoint(ctx context.Context, s, res, r string, cur repository.DeviceSyncCursor, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	c.seen = append(c.seen, res)
	return c.uplinkControlTest.AdvanceCheckpoint(ctx, s, serverUplinkResource, r, cur, n)
}
func (c *relationResourceControlTest) CompleteCheckpointAndRun(ctx context.Context, s, res, r, status string, n time.Time) (*model.SyncRun, error) {
	c.seen = append(c.seen, res)
	return c.uplinkControlTest.CompleteCheckpointAndRun(ctx, s, res, r, status, n)
}
func TestPODMembershipExecutionUsesEdgeCheckpoint(t *testing.T) {
	at := serverUplinkNow()
	lease := at.Add(time.Minute)
	run := &model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, StartedAt: &at, LeaseExpiresAt: &lease}
	c := &relationResourceControlTest{uplinkControlTest: &uplinkControlTest{status: model.SyncRunStatusRunning, renewed: make(chan string, 16)}}
	result, err := executePODMembership(context.Background(), c, membershipRunnerTest(func(context.Context, model.Source, time.Time) (service.PODMembershipResult, error) {
		return service.PODMembershipResult{Edges: 3, Objects: 7}, nil
	}), run, model.Source{ID: "source"}, time.Hour, func(_ context.Context, _ time.Time, n int64) error { require.Equal(t, int64(3), n); return nil })
	require.NoError(t, err)
	require.Equal(t, 3, result.Edges)
	require.Equal(t, 3, c.cursor.Total)
	require.Equal(t, []string{podMembershipResource, podMembershipResource, podMembershipResource}, c.seen)
}
