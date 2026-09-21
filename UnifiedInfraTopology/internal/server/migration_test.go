package server

import (
	"context"
	"path/filepath"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func migrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	return db
}

func TestMigrateServerStartReturnsAndCreatesSchema(t *testing.T) {
	db := migrationTestDB(t)
	srv := NewMigrateServer(db, &log.Logger{Logger: zap.NewNop()})
	require.NoError(t, srv.Start(context.Background()))
	require.True(t, db.Migrator().HasTable(&model.User{}))
	for _, table := range []string{"topology_schema_migrations", "sources", "sync_runs", "sync_checkpoints", "sync_diagnostics", "publications"} {
		require.True(t, db.Migrator().HasTable(table), table)
	}
	for _, table := range []string{"inventory_state", "generations", "sync_run_sources", "entities", "source_keys", "identity_bindings", "device_versions", "interface_versions", "address_versions"} {
		require.False(t, db.Migrator().HasTable(table), table)
	}
	require.NoError(t, srv.Start(context.Background()))
	var count int64
	require.NoError(t, db.Table("topology_schema_migrations").Where("state = ?", "applied").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.True(t, db.Migrator().HasColumn(&model.Publication{}, "schema_version"))
}

func TestMigrateServerStartReturnsInventoryMigrationError(t *testing.T) {
	db := migrationTestDB(t)
	require.NoError(t, db.Exec("CREATE TABLE sources (existing_column TEXT)").Error)
	srv := NewMigrateServer(db, &log.Logger{Logger: zap.NewNop()})
	require.ErrorContains(t, srv.Start(context.Background()), "manual recovery")
}

func TestMigrateServerStartHonorsCanceledContext(t *testing.T) {
	db := migrationTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := NewMigrateServer(db, &log.Logger{Logger: zap.NewNop()})
	require.ErrorIs(t, srv.Start(ctx), context.Canceled)
	require.False(t, db.Migrator().HasTable(&model.User{}))
}
