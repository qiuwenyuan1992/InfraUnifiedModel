package main

import (
	"context"
	"database/sql"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

// 仅明确授权清空后切换开发来源，保留旧来源但停用，不触及用户和Schema。
func TestSwitchLocalSourceToIDC238(t *testing.T) {
	path := os.Getenv("IDC238_SWITCH_CONFIG")
	if path == "" {
		t.Skip("显式审批后切换本地来源")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	require.Equal(t, 238, conf.GetInt("inventory.cmdb.sources.idc238.device_condition.idc_id"))
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var runs int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs").Scan(&runs))
	require.Zero(t, runs, "仅允许清空后切换")
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "UPDATE sources SET enabled=0,updated_at=? WHERE id=?", serverUplinkNow(), "00000000000000000000000000001030")
	require.NoError(t, err)
	now := serverUplinkNow()
	_, err = tx.ExecContext(ctx, "INSERT INTO sources(id,name,adapter_kind,config_ref,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE config_ref=VALUES(config_ref),enabled=VALUES(enabled),updated_at=VALUES(updated_at)", "00000000000000000000000000000238", "idc238", "cmdb", "idc238", true, now, now)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	t.Log("enabled IDC238 source 00000000000000000000000000000238, old IDC1030 source disabled")
}
