package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLiveSyncManagementReadOnly(t *testing.T) {
	path := os.Getenv("SYNC_MANAGEMENT_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对本地管理API控制面，不创建任务或访问CMDB/图库")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	require.Equal(t, "mysql", conf.GetString("data.db.main.driver"))
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var state string
	require.NoError(t, db.WithContext(ctx).Table("topology_schema_migrations").Select("state").Where("version = ?", 3).Scan(&state).Error)
	require.Equal(t, "applied", state)
	content, err := os.ReadFile("../../internal/migration/mysql/0003_sync_rounds.sql")
	require.NoError(t, err)
	var checksum string
	require.NoError(t, db.WithContext(ctx).Table("topology_schema_migrations").Select("checksum").Where("version = ?", 3).Scan(&checksum).Error)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(content)), checksum, "已执行迁移不能与代码校验和漂移")
	for _, name := range []string{"sync_rounds", "sync_round_phases", "sync_diagnostics"} {
		require.True(t, db.Migrator().HasTable(name))
	}
	// 测试调用内部只读服务，覆盖权限仅存在于本测试配置，不修改线上用户或授权。
	conf.Set("inventory.cursor_key", strings.Repeat("read-only-test-key", 3))
	conf.Set("inventory.grants", []map[string]any{{"user_id": "schema_verification_reader", "permissions": []string{"sync:read"}}})
	management := service.NewInventoryRoundService(repository.NewInventoryRoundRepository(repository.NewRepository(nil, db)), conf)
	page, err := management.List(ctx, "schema_verification_reader", service.InventoryQuery{Limit: 5})
	require.NoError(t, err)
	rounds, ok := page.Items.([]model.SyncRound)
	require.True(t, ok)
	for _, item := range rounds {
		round, phases, err := management.Get(ctx, "schema_verification_reader", item.ID)
		require.NoError(t, err)
		dto := v1.InventoryRound(*round, phases)
		require.Equal(t, 15, dto.TotalPhases)
		require.Len(t, dto.Phases, 15)
	}
	require.True(t, db.Migrator().HasColumn(&model.SyncDiagnostic{}, "round_id"))
	t.Logf("migration_version=3 state=%s; round_list_items=%d; management read-only query PASS; no task enqueued", state, len(rounds))
}
