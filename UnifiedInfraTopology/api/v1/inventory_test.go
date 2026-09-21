package v1

import (
	"encoding/json"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func TestInventoryItemsMapsCurrentPublicFields(t *testing.T) {
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.FixedZone("local", 3600))
	tests := []struct {
		name  string
		items interface{}
		want  string
	}{
		{
			name:  "sources omit config reference",
			items: []model.Source{{ID: "source", Name: "lab", AdapterKind: "cmdb", ConfigRef: "secret-config", Enabled: true}},
			want:  `[{"id":"source","name":"lab","adapter_kind":"cmdb","enabled":true}]`,
		},
		{
			name: "runs omit internal request fields",
			items: []model.SyncRun{{
				ID: "run", SourceID: "source", Status: "failed", Mode: "full",
				RequestHash: "private-hash", IdempotencyKey: "private-key", RequestedBy: "private-user",
				CancelRequestedAt: &now, CreatedAt: now, StartedAt: &now, FinishedAt: &now, ErrorCode: "upstream_failure",
			}},
			want: `[{"id":"run","source_id":"source","status":"failed","mode":"full","cancel_requested_at":"2026-09-21T08:00:00Z","created_at":"2026-09-21T08:00:00Z","started_at":"2026-09-21T08:00:00Z","finished_at":"2026-09-21T08:00:00Z","error_code":"upstream_failure"}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := InventoryItems(test.items)
			require.NoError(t, err)
			data, err := json.Marshal(got)
			require.NoError(t, err)
			require.JSONEq(t, test.want, string(data))
			require.NotContains(t, string(data), "private-")
			require.NotContains(t, string(data), "secret-config")
		})
	}
}

func TestInventoryItemsEmptySlicesAreArrays(t *testing.T) {
	for _, items := range []interface{}{
		[]model.Source(nil), []model.SyncRun(nil), []model.Source{}, []model.SyncRun{},
	} {
		got, err := InventoryItems(items)
		require.NoError(t, err)
		data, err := json.Marshal(got)
		require.NoError(t, err)
		require.Equal(t, "[]", string(data), "%T", items)
	}
}

func TestInventoryItemsRejectsUnsupportedTypes(t *testing.T) {
	for _, items := range []interface{}{nil, []string{"secret"}, model.Source{ConfigRef: "secret"}} {
		got, err := InventoryItems(items)
		require.Error(t, err)
		require.Nil(t, got)
	}
}

func TestInventoryRunMatchesListMapping(t *testing.T) {
	run := model.SyncRun{ID: "run", SourceID: "source"}
	list, err := InventoryItems([]model.SyncRun{run})
	require.NoError(t, err)
	listJSON, err := json.Marshal(list)
	require.NoError(t, err)
	detailJSON, err := json.Marshal(InventoryRun(run))
	require.NoError(t, err)
	require.JSONEq(t, "["+string(detailJSON)+"]", string(listJSON))
}
