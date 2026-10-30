package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

func verificationFixture(t *testing.T, total int64) (persistenceReport, []persistenceSample) {
	t.Helper()
	at := time.Date(2026, 10, 29, 1, 2, 3, 123456000, time.UTC)
	finished := at.Add(time.Minute)
	r := persistenceReport{SourceID: "source-1", Entity: model.EntityDataCenter, Run: &persistenceRun{ID: "run-1", SourceID: "source-1", Status: "succeeded", StartedAt: &at, FinishedAt: &finished}, Checkpoint: &persistenceCheckpoint{Complete: true, CompletedAt: &finished, Cursor: persistenceCursor{Version: 1, RunID: "run-1", SnapshotAt: at, NextOffset: total, Total: total}}, GraphTotal: total, GraphCurrent: total}
	var samples []persistenceSample
	for i := int64(0); i < total; i++ {
		stable := strings.Repeat("x", int(i)+1)
		vid, err := (model.EntityIdentity{SourceID: r.SourceID, EntityType: r.Entity, StableID: stable}).VID()
		require.NoError(t, err)
		samples = append(samples, persistenceSample{VID: vid, StableID: stable, Code: "dc", CreatedAt: at.Add(-time.Hour), SyncedAt: at})
	}
	return r, samples
}

func TestValidatePersistenceReport(t *testing.T) {
	cases := []struct {
		name   string
		change func(*persistenceReport, []persistenceSample)
	}{
		{"wrong source", func(r *persistenceReport, _ []persistenceSample) { r.Run.SourceID = "other" }},
		{"unfinished run", func(r *persistenceReport, _ []persistenceSample) { r.Run.FinishedAt = nil }},
		{"unstarted run", func(r *persistenceReport, _ []persistenceSample) { r.Run.StartedAt = nil }},
		{"failed run", func(r *persistenceReport, _ []persistenceSample) { r.Run.Status = "failed" }},
		{"leased run", func(r *persistenceReport, _ []persistenceSample) { r.Run.LeaseExpiresAt = r.Run.StartedAt }},
		{"unfinished checkpoint", func(r *persistenceReport, _ []persistenceSample) { r.Checkpoint.Complete = false }},
		{"checkpoint without completion time", func(r *persistenceReport, _ []persistenceSample) { r.Checkpoint.CompletedAt = nil }},
		{"wrong cursor version", func(r *persistenceReport, _ []persistenceSample) { r.Checkpoint.Cursor.Version = 2 }},
		{"wrong checkpoint run", func(r *persistenceReport, _ []persistenceSample) { r.Checkpoint.Cursor.RunID = "old" }},
		{"wrong snapshot", func(r *persistenceReport, _ []persistenceSample) {
			r.Checkpoint.Cursor.SnapshotAt = r.Checkpoint.Cursor.SnapshotAt.Add(time.Microsecond)
		}},
		{"incomplete offset", func(r *persistenceReport, _ []persistenceSample) { r.Checkpoint.Cursor.NextOffset-- }},
		{"negative total", func(r *persistenceReport, _ []persistenceSample) { r.Checkpoint.Cursor.Total = -1 }},
		{"wrong current count", func(r *persistenceReport, _ []persistenceSample) { r.GraphCurrent++ }},
		{"wrong total count", func(r *persistenceReport, _ []persistenceSample) { r.GraphTotal = 0 }},
		{"blank stable identity", func(_ *persistenceReport, s []persistenceSample) { s[0].StableID = " " }},
		{"wrong VID", func(_ *persistenceReport, s []persistenceSample) { s[0].VID = "wrong" }},
		{"zero creation time", func(_ *persistenceReport, s []persistenceSample) { s[0].CreatedAt = time.Time{} }},
		{"future creation time", func(_ *persistenceReport, s []persistenceSample) { s[0].CreatedAt = s[0].SyncedAt.Add(time.Second) }},
		{"wrong vertex snapshot", func(_ *persistenceReport, s []persistenceSample) { s[0].SyncedAt = s[0].SyncedAt.Add(time.Second) }},
		{"duplicate vertex", func(_ *persistenceReport, s []persistenceSample) { s[1] = s[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, samples := verificationFixture(t, 2)
			tc.change(&r, samples)
			require.Error(t, validatePersistenceReport(&r, samples))
			require.NotEqual(t, "PASS", r.Status)
		})
	}
	t.Run("bounded proof", func(t *testing.T) {
		r, samples := verificationFixture(t, 7)
		require.NoError(t, validatePersistenceReport(&r, samples))
		require.Equal(t, "PASS", r.Status)
		require.Len(t, r.Samples, 5)
		require.True(t, r.ObjectsPresent)
	})
	t.Run("zero objects is explicit", func(t *testing.T) {
		r, samples := verificationFixture(t, 0)
		require.NoError(t, validatePersistenceReport(&r, samples))
		require.False(t, r.ObjectsPresent)
		require.Contains(t, r.Note, "no objects")
	})
	t.Run("missing vertex rows", func(t *testing.T) {
		r, _ := verificationFixture(t, 1)
		require.Error(t, validatePersistenceReport(&r, nil))
	})
}

func TestVerificationGraphQueriesAreReadOnlyAndParameterized(t *testing.T) {
	for _, entity := range []model.EntityType{model.EntityDevice, model.EntityDataCenter, model.EntityCabinet, model.EntityInterface, model.EntityTransformer} {
		for _, current := range []bool{false, true} {
			for _, count := range []bool{false, true} {
				query, err := persistenceGraphQuery(entity, current, count)
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(query, "LOOKUP ON "+string(entity)+" WHERE "))
				require.Contains(t, query, ".source_id == $source_id")
				require.Equal(t, current, strings.Contains(query, ".synced_at == datetime($snapshot)"))
				if count {
					require.Contains(t, query, "count(*) AS total")
				} else {
					require.Contains(t, query, "AS identity")
					require.Contains(t, query, "AS created_at")
					if entity == model.EntityDevice {
						require.Contains(t, query, "device.device_sn")
						require.NotContains(t, query, "device.code")
					} else if entity == model.EntityInterface {
						require.Contains(t, query, "interface.port_uuid")
						require.Contains(t, query, "interface.port_name AS code")
						require.NotContains(t, query, "interface.uuid")
					} else {
						require.Contains(t, query, string(entity)+".uuid")
						require.Contains(t, query, string(entity)+".code")
					}
				}
			}
		}
	}
	_, err := persistenceGraphQuery(model.EntityType("device; DELETE VERTEX"), true, true)
	require.Error(t, err)
	_, err = persistenceGraphQuery(model.EntityGPU, true, true)
	require.Error(t, err)
}

func TestVerifyPersistenceRejectsNonMySQLBeforeConnecting(t *testing.T) {
	conf := viper.New()
	conf.Set("data.db.main.driver", "sqlite")
	var output bytes.Buffer
	require.ErrorContains(t, verifyPersistence(context.Background(), conf, "source-1", model.EntityDevice, "", &output), "mysql")
	require.Empty(t, output.String())
}

func TestPersistenceGraphResultRequiresSuccess(t *testing.T) {
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_E_SYNTAX_ERROR})
	require.NoError(t, err)
	require.Error(t, persistenceCheckGraphResult(result, nil))
	require.Error(t, persistenceCheckGraphResult(nil, nil))
}

func TestPersistenceTableUsesTypedEscapedStrings(t *testing.T) {
	text := "port\"name\\new\\t"
	integer := int64(400000)
	null := nebula.NullType___NULL__
	result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: &nebula.DataSet{ColumnNames: [][]byte{[]byte("text"), []byte("number"), []byte("null")}, Rows: []*nebula.Row{{Values: []*nebula.Value{{SVal: []byte(text)}, {IVal: &integer}, {NVal: &null}}}}}})
	require.NoError(t, err)
	table, err := persistenceTable(result)
	require.NoError(t, err)
	value, err := persistenceNullableText(table[1][0])
	require.NoError(t, err)
	require.Equal(t, text, *value)
	require.Equal(t, "400000", table[1][1])
	value, err = persistenceNullableText(table[1][2])
	require.NoError(t, err)
	require.Nil(t, value)
}

func TestPersistedInterfaceTextPreservesTypeAndNull(t *testing.T) {
	for _, encoded := range []string{`"400000"`, `"up"`, `""`} {
		value, err := persistenceNullableText(encoded)
		require.NoError(t, err)
		require.NotNil(t, value)
	}
	for _, encoded := range []string{"NULL", "null", "__NULL__"} {
		value, err := persistenceNullableText(encoded)
		require.NoError(t, err)
		require.Nil(t, value)
	}
	for _, encoded := range []string{"400000", "true", "BAD_TYPE"} {
		_, err := persistenceNullableText(encoded)
		require.Error(t, err)
	}
	query, err := persistenceGraphQuery(model.EntityInterface, true, false)
	require.NoError(t, err)
	require.Contains(t, query, "interface.port_speed AS port_speed")
	require.Contains(t, query, "interface.port_operation_status AS port_operation_status")
	require.Contains(t, query, "interface.port_type AS port_type")
}

func TestPersistencePreflightReportDoesNotClaimPass(t *testing.T) {
	r := persistenceReport{Status: "preflight", SourceID: "s", Entity: model.EntityCabinet, ConfigRef: "local", Samples: []persistenceSample{}}
	encoded, err := json.Marshal(r)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PASS")
	require.Contains(t, string(encoded), `"status":"preflight"`)
}
