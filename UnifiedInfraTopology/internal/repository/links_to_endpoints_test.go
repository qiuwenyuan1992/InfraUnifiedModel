package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

func linksToTestIdentities() (model.EntityIdentity, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) {
	return model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "FROM"},
		model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "TO"},
		model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "FROM_PORT"},
		model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "TO_PORT"}
}

func linksToTestValues(device, port model.EntityIdentity) []any {
	return []any{port.SourceID, port.StableID, "physics", device.StableID, int64(54), device.SourceID, device.StableID, int64(54), port.StableID, "physics"}
}

func linksToTestResult(t *testing.T, columns int, rows ...[]any) *nebulago.ResultSet {
	t.Helper()
	data := &nebula.DataSet{}
	for i := 0; i < columns; i++ {
		data.ColumnNames = append(data.ColumnNames, []byte(fmt.Sprint(i)))
	}
	for _, values := range rows {
		row := &nebula.Row{}
		for _, value := range values {
			switch v := value.(type) {
			case string:
				row.Values = append(row.Values, &nebula.Value{SVal: []byte(v)})
			case int64:
				x := v
				row.Values = append(row.Values, &nebula.Value{IVal: &x})
			case nil:
				x := nebula.NullType___NULL__
				row.Values = append(row.Values, &nebula.Value{NVal: &x})
			default:
				t.Fatalf("unsupported test value %T", value)
			}
		}
		data.Rows = append(data.Rows, row)
	}
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
	require.NoError(t, err)
	return result
}

func TestLinksToEndpointsRequireActualUniqueTypedOwners(t *testing.T) {
	from, to, fromPort, toPort := linksToTestIdentities()
	for _, endpoint := range []string{"from", "to"} {
		for _, failure := range []string{"valid", "missing owner", "multiple owners", "port source", "port UUID", "port type", "device SN", "device classification", "owner source", "owner SN", "owner classification", "owner port UUID", "owner port type", "NULL", "string classification", "malformed columns"} {
			t.Run(endpoint+"/"+failure, func(t *testing.T) {
				first, second := linksToTestValues(from, fromPort), linksToTestValues(to, toPort)
				values := first
				if endpoint == "to" {
					values = second
				}
				index := map[string]int{"port source": 0, "port UUID": 1, "port type": 2, "device SN": 3, "device classification": 4, "owner source": 5, "owner SN": 6, "owner classification": 7, "owner port UUID": 8, "owner port type": 9}[failure]
				switch failure {
				case "device classification", "owner classification":
					values[index] = int64(52)
				case "NULL":
					values[6] = nil
				case "string classification":
					values[7] = "54"
				case "port source", "port UUID", "port type", "device SN", "owner source", "owner SN", "owner port UUID", "owner port type":
					values[index] = "wrong"
				}
				rows := [][]any{values}
				if failure == "missing owner" {
					rows = nil
				}
				if failure == "multiple owners" {
					other := append([]any(nil), values...)
					other[6] = "OTHER_OWNER"
					rows = append(rows, other)
				}
				columns := 10
				if failure == "malformed columns" {
					columns = 9
					rows[0] = rows[0][:9]
				}
				bad := linksToTestResult(t, columns, rows...)
				results := []*nebulago.ResultSet{bad, linksToTestResult(t, 10, second)}
				if endpoint == "to" {
					results = []*nebulago.ResultSet{linksToTestResult(t, 10, first), bad}
				}
				fake := &topologyGraphFake{results: results}
				var reader LinksToEndpointReader = &topologyGraphRepository{client: fake}
				ok, err := reader.LinksToEndpointsValid(context.Background(), from, to, fromPort, toPort)
				if failure == "malformed columns" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, failure == "valid", ok)
				if failure == "valid" {
					require.Len(t, fake.calls, 2)
					for i, call := range fake.calls {
						require.Contains(t, call.statement, "id(p) == $port_vid")
						require.Contains(t, call.statement, "owns_interface")
						require.Contains(t, call.statement, "parent_type_id")
						require.NotContains(t, call.statement, "t.device.device_sn ==")
						require.NotContains(t, call.statement, "id(t) ==")
						require.Equal(t, "source", call.params["source_id"])
						port := fromPort
						if i == 1 {
							port = toPort
						}
						vid, err := port.VID()
						require.NoError(t, err)
						require.Equal(t, vid, call.params["port_vid"])
					}
				}
			})
		}
	}
}

func TestLinksToEndpointsRejectIdentitiesBeforeQuery(t *testing.T) {
	for _, field := range []int{0, 1, 2, 3} {
		for _, failure := range []string{"empty SN", "empty source", "wrong source", "wrong entity"} {
			t.Run(fmt.Sprintf("%d/%s", field, failure), func(t *testing.T) {
				from, to, fp, tp := linksToTestIdentities()
				ids := []model.EntityIdentity{from, to, fp, tp}
				switch failure {
				case "empty SN":
					ids[field].StableID = ""
				case "empty source":
					ids[field].SourceID = ""
				case "wrong source":
					ids[field].SourceID = "other"
				case "wrong entity":
					ids[field].EntityType = model.EntityGPU
				}
				fake := &topologyGraphFake{}
				ok, err := (&topologyGraphRepository{client: fake}).LinksToEndpointsValid(context.Background(), ids[0], ids[1], ids[2], ids[3])
				require.Error(t, err)
				require.False(t, ok)
				require.Empty(t, fake.calls)
			})
		}
	}
	from, to, fp, _ := linksToTestIdentities()
	fake := &topologyGraphFake{}
	ok, err := (&topologyGraphRepository{client: fake}).LinksToEndpointsValid(context.Background(), from, to, fp, fp)
	require.Error(t, err)
	require.False(t, ok)
	require.Empty(t, fake.calls)
}

func TestLinksToEndpointsFailuresAndCancellation(t *testing.T) {
	from, to, fp, tp := linksToTestIdentities()
	for _, failure := range []string{"error", "nil", "rejected", "cancel before", "cancel during"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &topologyGraphFake{}
			switch failure {
			case "error":
				fake.err = errors.New("query failed")
			case "rejected":
				fake.results = []*nebulago.ResultSet{topologyGraphRejectedResult(t, "rejected")}
			case "cancel before":
				cancel()
			case "cancel during":
				fake.onExecute = func(context.Context) { cancel() }
			}
			ok, err := (&topologyGraphRepository{client: fake}).LinksToEndpointsValid(ctx, from, to, fp, tp)
			require.False(t, ok)
			if strings.HasPrefix(failure, "cancel") {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, ErrInventoryNotReady)
			}
			if failure == "cancel before" {
				require.Empty(t, fake.calls)
			}
		})
	}
	var unavailable LinksToEndpointReader = unavailableTopologyGraphRepository{}
	ok, err := unavailable.LinksToEndpointsValid(context.Background(), from, to, fp, tp)
	require.False(t, ok)
	require.ErrorIs(t, err, ErrInventoryNotReady)
}

func TestLinksToCheckpointSQLiteLifecycleAndProgress(t *testing.T) {
	for _, total := range []int{0, 4} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			repo, _, db := inventoryRepositoryFixture(t)
			at := time.Now().UTC().Truncate(time.Microsecond)
			lease := at.Add(time.Minute)
			source, run := strings.Repeat("a", 32), strings.Repeat("1", 32)
			require.NoError(t, db.Create(&model.SyncRun{ID: run, SourceID: source, Status: model.SyncRunStatusRunning, CreatedAt: at, StartedAt: &at, LeaseExpiresAt: &lease}).Error)
			checkpoint, err := repo.BeginCheckpoint(context.Background(), source, "links_to", run, at, at)
			require.NoError(t, err)
			cursor := checkpoint.Cursor
			cursor.Total = total
			if total == 0 {
				_, err = repo.AdvanceCheckpoint(context.Background(), source, "links_to", run, cursor, at.Add(time.Second))
				require.ErrorIs(t, err, ErrInventoryConflict)
			} else {
				cursor.NextOffset = 2
				checkpoint, err = repo.AdvanceCheckpoint(context.Background(), source, "links_to", run, cursor, at.Add(time.Second))
				require.NoError(t, err)
				require.Equal(t, cursor, checkpoint.Cursor)
				require.Error(t, validateDeviceSyncCursor(cursor))
				for _, bad := range []DeviceSyncCursor{cursor, {Version: 1, RunID: run, SnapshotAt: at, NextOffset: 3, Total: 5}, {Version: 1, RunID: run, SnapshotAt: at, NextOffset: 3, Total: 4, LastInstanceID: 1}, {Version: 1, RunID: run, SnapshotAt: at, NextOffset: 3, Total: 4, LastPartition: 1}} {
					_, err = repo.AdvanceCheckpoint(context.Background(), source, "links_to", run, bad, at.Add(2*time.Second))
					require.ErrorIs(t, err, ErrInventoryConflict)
				}
				cursor.NextOffset = total
				_, err = repo.AdvanceCheckpoint(context.Background(), source, "links_to", run, cursor, at.Add(3*time.Second))
				require.NoError(t, err)
			}
			require.NoError(t, db.Model(&model.SyncRun{}).Where("id = ?", run).Update("status", model.SyncRunStatusPublishing).Error)
			completed, err := repo.CompleteCheckpointAndRun(context.Background(), source, "links_to", run, model.SyncRunStatusPublishing, at.Add(4*time.Second))
			require.NoError(t, err)
			require.Equal(t, model.SyncRunStatusSucceeded, completed.Status)
			checkpoint, err = repo.LoadCheckpoint(context.Background(), source, "links_to")
			require.NoError(t, err)
			require.True(t, checkpoint.Complete)
			require.Equal(t, total, checkpoint.Cursor.NextOffset)
			require.Zero(t, checkpoint.Cursor.LastInstanceID)
		})
	}
}
