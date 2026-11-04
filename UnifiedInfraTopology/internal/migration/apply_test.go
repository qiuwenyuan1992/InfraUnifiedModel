package migration

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "inventory.db") + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func TestApplyCreatesOnlyControlPlaneSchemaAndReruns(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	require.NoError(t, Apply(context.Background(), db))
	var versions []struct {
		Version   int
		Checksum  string
		State     string
		AppliedAt *string
	}
	require.NoError(t, db.Table("topology_schema_migrations").Order("version").Find(&versions).Error)
	require.Len(t, versions, 4)
	for i, row := range versions {
		require.Equal(t, i, row.Version)
		require.Len(t, row.Checksum, 64)
		require.Equal(t, "applied", row.State)
		require.NotNil(t, row.AppliedAt)
	}
	for _, table := range []string{"sources", "sync_runs", "sync_rounds", "sync_round_phases", "sync_checkpoints", "sync_diagnostics", "publications", "topology_schema_migrations"} {
		require.True(t, db.Migrator().HasTable(table), table)
	}
	for _, table := range []string{"inventory_state", "generations", "sync_run_sources", "entities", "source_keys", "identity_bindings", "device_versions", "interface_versions", "address_versions"} {
		require.False(t, db.Migrator().HasTable(table), table)
	}
}

func TestApplyRejectsAlteredChecksumAndDirtyState(t *testing.T) {
	for _, version := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("checksum/%d", version), func(t *testing.T) {
			db := testDB(t)
			require.NoError(t, Apply(context.Background(), db))
			require.NoError(t, db.Exec("UPDATE topology_schema_migrations SET checksum = ? WHERE version = ?", strings.Repeat("0", 64), version).Error)
			require.ErrorContains(t, Apply(context.Background(), db), "checksum")
		})
	}
	for _, state := range []string{"dirty", "failed"} {
		t.Run(state, func(t *testing.T) {
			db := testDB(t)
			require.NoError(t, Apply(context.Background(), db))
			require.NoError(t, db.Exec("UPDATE topology_schema_migrations SET state = ? WHERE version = 1", state).Error)
			err := Apply(context.Background(), db)
			require.ErrorContains(t, err, state)
			require.ErrorContains(t, err, "manual recovery")
		})
	}
}

func TestApplyFailureIsRecordedAndCannotRetry(t *testing.T) {
	db := testDB(t)
	// 制造中途 DDL 冲突，不使用修改过的测试专用迁移脚本。
	require.NoError(t, db.Exec("CREATE TABLE sources (existing_column TEXT)").Error)
	err := Apply(context.Background(), db)
	require.ErrorContains(t, err, "manual recovery")
	var state string
	require.NoError(t, db.Raw("SELECT state FROM topology_schema_migrations WHERE version = 1").Scan(&state).Error)
	require.Equal(t, "failed", state)
	require.False(t, db.Migrator().HasTable("sync_runs"), "SQLite DDL 应回滚但保留失败账本")
	require.ErrorContains(t, Apply(context.Background(), db), "failed")
}

func TestApplySerializesConcurrentCallers(t *testing.T) {
	db := testDB(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Apply(context.Background(), db)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, db.Table("topology_schema_migrations").Count(&count).Error)
	require.EqualValues(t, 4, count)
}

type otherDialect struct{ gorm.Dialector }

func (otherDialect) Name() string { return "postgres" }

func TestApplyRejectsUnsupportedDialect(t *testing.T) {
	db := testDB(t)
	db.Dialector = otherDialect{db.Dialector}
	require.ErrorContains(t, Apply(context.Background(), db), "unsupported dialect")
	require.False(t, db.Migrator().HasTable("topology_schema_migrations"))
}

func TestApplyRejectsUnknownLedgerVersion(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	require.NoError(t, db.Exec("INSERT INTO topology_schema_migrations (version, name, checksum, state, started_at) VALUES (99, 'future', ?, 'applied', CURRENT_TIMESTAMP)", strings.Repeat("a", 64)).Error)
	require.ErrorContains(t, Apply(context.Background(), db), "unknown migration")
}

func TestApplyDoesNotCreateLegacyProjectionState(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	require.False(t, db.Migrator().HasTable("inventory_state"))
}

func TestSyncRoundMigrationDiagnosticOwnership(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))
	now := time.Now().UTC().Truncate(time.Microsecond)
	sourceID, otherSourceID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	roundID, otherRoundID := strings.Repeat("c", 32), strings.Repeat("d", 32)
	ownedRunID, standaloneRunID := strings.Repeat("e", 32), strings.Repeat("f", 32)
	for _, id := range []string{sourceID, otherSourceID} {
		require.NoError(t, db.Create(&model.Source{ID: id, Name: id, Enabled: true}).Error)
	}
	for _, id := range []string{roundID, otherRoundID} {
		require.NoError(t, db.Create(&model.SyncRound{ID: id, SourceID: sourceID, Status: "running", Mode: "full", RequestHash: strings.Repeat("a", 64), IdempotencyKey: id, CreatedAt: now}).Error)
	}
	for _, id := range []string{ownedRunID, standaloneRunID} {
		require.NoError(t, db.Create(&model.SyncRun{ID: id, SourceID: sourceID, Status: "succeeded", Mode: "full", RequestHash: strings.Repeat("a", 64), IdempotencyKey: id, CreatedAt: now}).Error)
	}
	require.NoError(t, db.Table("sync_round_phases").Create(map[string]interface{}{"round_id": roundID, "name": "device", "position": 1, "status": "succeeded", "run_id": ownedRunID}).Error)
	require.NoError(t, db.Table("sync_round_phases").Create(map[string]interface{}{"round_id": roundID, "name": "cabinet", "position": 2, "status": "queued", "run_id": nil}).Error)
	for _, tc := range []struct {
		name           string
		roundID, runID interface{}
		sourceID       string
		valid          bool
	}{
		{"owned_child", roundID, ownedRunID, sourceID, true},
		{"round_only", roundID, nil, sourceID, true},
		{"standalone", nil, standaloneRunID, sourceID, true},
		{"wrong_round_source", roundID, nil, otherSourceID, false},
		{"standalone_not_child", roundID, standaloneRunID, sourceID, false},
		{"child_of_other_round", otherRoundID, ownedRunID, sourceID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Table("sync_diagnostics").Create(map[string]interface{}{"id": tc.name, "round_id": tc.roundID, "run_id": tc.runID, "source_id": tc.sourceID, "resource": "device", "severity": "warning", "code": "missing_endpoint", "detail": "1", "created_at": now}).Error
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "FOREIGN KEY constraint failed")
			}
		})
	}
}

func TestSyncRoundMigrationPreservesExistingDiagnostics(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	steps, err := loadSteps("sqlite")
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	conn, err := pool.Conn(ctx)
	require.NoError(t, err)
	err = applySQLite(ctx, conn, steps[:3])
	require.NoError(t, conn.Close())
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	sourceID := strings.Repeat("a", 32)
	runID := strings.Repeat("b", 32)
	diagID := strings.Repeat("c", 32)
	require.NoError(t, db.Create(&model.Source{ID: sourceID, Name: "existing", Enabled: true, CreatedAt: now, UpdatedAt: now}).Error)
	require.NoError(t, db.Create(&model.SyncRun{ID: runID, SourceID: sourceID, Status: "succeeded", Mode: "full", RequestHash: strings.Repeat("d", 64), IdempotencyKey: "existing", RequestedBy: "test", CreatedAt: now}).Error)
	before := model.SyncDiagnostic{ID: diagID, RunID: runID, SourceID: sourceID, Resource: "device", Severity: "warning", Code: "missing_endpoint", Detail: "1", CreatedAt: now}
	require.NoError(t, db.Create(&before).Error)
	require.NoError(t, Apply(ctx, db))
	var after model.SyncDiagnostic
	require.NoError(t, db.Where("id = ?", diagID).First(&after).Error)
	require.Equal(t, before, after)
	roundID := strings.Repeat("e", 32)
	require.NoError(t, db.Create(&model.SyncRound{ID: roundID, SourceID: sourceID, Status: "succeeded", Mode: "full", RequestHash: strings.Repeat("f", 64), IdempotencyKey: "round", RequestedBy: "test", CreatedAt: now}).Error)
	require.NoError(t, db.Table("sync_diagnostics").Create(map[string]interface{}{"id": strings.Repeat("f", 32), "run_id": nil, "round_id": roundID, "source_id": sourceID, "resource": "gpu_sync", "severity": "warning", "code": "missing_endpoint", "detail": "2", "created_at": now}).Error)
	require.NoError(t, Apply(ctx, db))
	var count int64
	require.NoError(t, db.Table("sync_diagnostics").Count(&count).Error)
	require.EqualValues(t, 2, count)
}
