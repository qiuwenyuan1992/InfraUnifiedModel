package migration

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

func TestApplyCreatesSchemaAndChecksumsAndReruns(t *testing.T) {
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
	require.Len(t, versions, 2)
	for i, row := range versions {
		require.Equal(t, i, row.Version)
		require.Len(t, row.Checksum, 64)
		require.Equal(t, "applied", row.State)
		require.NotNil(t, row.AppliedAt)
	}
	for _, table := range []string{"inventory_state", "sources", "generations", "sync_runs", "sync_run_sources", "entities", "source_keys", "identity_bindings", "device_versions", "interface_versions", "address_versions"} {
		require.True(t, db.Migrator().HasTable(table), table)
	}
}

func TestApplyRejectsAlteredChecksumAndDirtyState(t *testing.T) {
	for _, version := range []int{0, 1} {
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
	require.False(t, db.Migrator().HasTable("inventory_state"), "SQLite DDL 应回滚但保留失败账本")
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
	require.EqualValues(t, 2, count)
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

func TestApplySeedsConstrainedInventoryState(t *testing.T) {
	db := testDB(t)
	require.NoError(t, Apply(context.Background(), db))

	var state struct {
		ID              uint
		ProjectionState string
		ProjectionEpoch int64
	}
	require.NoError(t, db.Table("inventory_state").First(&state).Error)
	require.EqualValues(t, 1, state.ID)
	require.Equal(t, "uninitialized", state.ProjectionState)
	require.Zero(t, state.ProjectionEpoch)
	require.Error(t, db.Exec("INSERT INTO inventory_state (id) VALUES (2)").Error)
	require.Error(t, db.Exec("UPDATE inventory_state SET projection_state = 'unknown' WHERE id = 1").Error)
	require.Error(t, db.Exec("UPDATE inventory_state SET projection_epoch = -1 WHERE id = 1").Error)
}
