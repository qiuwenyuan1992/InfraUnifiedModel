package migration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testID(n int) string { return fmt.Sprintf("%032x", n) }

func insertSource(t *testing.T, db *gorm.DB, id, name string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.Exec(`
		INSERT INTO sources (id, name, adapter_kind, config_ref, enabled, created_at, updated_at)
		VALUES (?, ?, 'fixture', 'source/cmdb', TRUE, ?, ?)
	`, id, name, now, now).Error)
}

func insertRun(t *testing.T, db *gorm.DB, id, sourceID, idempotencyKey string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.Exec(`
		INSERT INTO sync_runs (
			id, source_id, status, mode, request_hash, idempotency_key,
			requested_by, created_at
		) VALUES (?, ?, 'queued', 'full', 'request-hash', ?, 'operator', ?)
	`, id, sourceID, idempotencyKey, now).Error)
}

func TestSchemaRoundTripsControlPlaneRecords(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))

	sourceID := testID(1)
	runID := testID(2)
	insertSource(t, db, sourceID, "cmdb-primary")
	insertRun(t, db, runID, sourceID, "request-1")

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.Exec(`
		INSERT INTO sync_checkpoints (source_id, resource, cursor, complete, updated_at)
		VALUES (?, 'device', 'cursor-1', FALSE, ?)
	`, sourceID, now).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO sync_diagnostics (
			id, run_id, source_id, resource, severity, code,
			object_ref, field_path, detail, created_at
		) VALUES (?, ?, ?, 'device', 'warning', 'invalid_time', 'SN1', 'created_at', 'invalid source time', ?)
	`, testID(3), runID, sourceID, now).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO publications (
			id, run_id, source_id, version, schema_version, status, created_at
		) VALUES (?, ?, ?, 1, 1, 'pending', ?)
	`, testID(4), runID, sourceID, now).Error)

	var source struct {
		CreatedAt time.Time
		UpdatedAt time.Time
	}
	require.NoError(t, db.Table("sources").Take(&source).Error)
	require.False(t, source.CreatedAt.IsZero())
	require.False(t, source.UpdatedAt.IsZero())

	var run struct {
		SourceID          string
		CancelRequestedAt *time.Time
		StartedAt         *time.Time
		FinishedAt        *time.Time
		ErrorCode         string
	}
	require.NoError(t, db.Table("sync_runs").Take(&run).Error)
	require.Equal(t, sourceID, run.SourceID)
	require.Nil(t, run.CancelRequestedAt)
	require.Nil(t, run.StartedAt)
	require.Nil(t, run.FinishedAt)
	require.Empty(t, run.ErrorCode)

	var checkpoint struct {
		Cursor      string
		Complete    bool
		CompletedAt *time.Time
	}
	require.NoError(t, db.Table("sync_checkpoints").Take(&checkpoint).Error)
	require.Equal(t, "cursor-1", checkpoint.Cursor)
	require.False(t, checkpoint.Complete)
	require.Nil(t, checkpoint.CompletedAt)

	var publication struct {
		Version       int64
		SchemaVersion int
		PublishedAt   *time.Time
	}
	require.NoError(t, db.Table("publications").Take(&publication).Error)
	require.EqualValues(t, 1, publication.Version)
	require.Equal(t, 1, publication.SchemaVersion)
	require.Nil(t, publication.PublishedAt)
}

func TestSchemaRejectsDuplicateSourceName(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	insertSource(t, db, testID(1), "cmdb-primary")

	now := time.Now().UTC().Truncate(time.Second)
	err := db.Exec(`
		INSERT INTO sources (id, name, adapter_kind, config_ref, enabled, created_at, updated_at)
		VALUES (?, 'cmdb-primary', 'fixture', 'source/other', TRUE, ?, ?)
	`, testID(2), now, now).Error
	require.Error(t, err)
}

func TestSchemaScopesRunIdempotencyBySource(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	insertSource(t, db, testID(1), "cmdb-primary")
	insertSource(t, db, testID(2), "cmdb-secondary")
	insertRun(t, db, testID(3), testID(1), "request-1")
	insertRun(t, db, testID(4), testID(2), "request-1")

	now := time.Now().UTC().Truncate(time.Second)
	err := db.Exec(`
		INSERT INTO sync_runs (
			id, source_id, status, mode, request_hash, idempotency_key,
			requested_by, created_at
		) VALUES (?, ?, 'queued', 'full', 'other-hash', 'request-1', 'operator', ?)
	`, testID(5), testID(1), now).Error
	require.Error(t, err)
}

func TestSchemaConstrainsCheckpointsAndDiagnostics(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	insertSource(t, db, testID(1), "cmdb-primary")
	insertSource(t, db, testID(2), "cmdb-secondary")
	insertRun(t, db, testID(3), testID(1), "request-1")

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.Exec(`
		INSERT INTO sync_checkpoints (source_id, resource, cursor, complete, updated_at)
		VALUES (?, 'device', '', FALSE, ?)
	`, testID(1), now).Error)
	require.Error(t, db.Exec(`
		INSERT INTO sync_checkpoints (source_id, resource, cursor, complete, updated_at)
		VALUES (?, 'device', '', FALSE, ?)
	`, testID(1), now).Error)
	require.Error(t, db.Exec(`
		INSERT INTO sync_checkpoints (source_id, resource, cursor, complete, updated_at)
		VALUES (?, 'device', '', FALSE, ?)
	`, testID(99), now).Error)

	insertDiagnostic := func(id, runID, sourceID string) error {
		return db.Exec(`
			INSERT INTO sync_diagnostics (
				id, run_id, source_id, resource, severity, code,
				object_ref, field_path, detail, created_at
			) VALUES (?, ?, ?, 'device', 'warning', 'invalid_time', 'SN1', 'created_at', 'invalid source time', ?)
		`, id, runID, sourceID, now).Error
	}
	require.NoError(t, insertDiagnostic(testID(4), testID(3), testID(1)))
	require.Error(t, insertDiagnostic(testID(5), testID(99), testID(1)))
	require.Error(t, insertDiagnostic(testID(6), testID(3), testID(2)))
}

func TestSchemaConstrainsPublications(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	insertSource(t, db, testID(1), "cmdb-primary")
	insertSource(t, db, testID(2), "cmdb-secondary")
	insertRun(t, db, testID(3), testID(1), "request-1")
	insertRun(t, db, testID(4), testID(1), "request-2")
	insertRun(t, db, testID(5), testID(2), "request-1")

	now := time.Now().UTC().Truncate(time.Second)
	insertPublication := func(id, runID, sourceID string, version int64) error {
		return db.Exec(`
			INSERT INTO publications (
				id, run_id, source_id, version, schema_version, status, created_at
			) VALUES (?, ?, ?, ?, 1, 'pending', ?)
		`, id, runID, sourceID, version, now).Error
	}
	require.NoError(t, insertPublication(testID(6), testID(3), testID(1), 1))
	require.Error(t, insertPublication(testID(7), testID(4), testID(1), 1))
	require.Error(t, insertPublication(testID(8), testID(3), testID(1), 2))
	require.NoError(t, insertPublication(testID(9), testID(5), testID(2), 1))
	require.Error(t, insertPublication(testID(10), testID(99), testID(1), 2))
	require.Error(t, insertPublication(testID(11), testID(4), testID(2), 2))
}
