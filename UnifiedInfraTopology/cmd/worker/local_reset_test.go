package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/go-sql-driver/mysql"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// 显式授权的本地清理：只删项目资产点边和同步任务数据，保留来源、用户与 Schema。
func TestResetLocalInventoryData(t *testing.T) {
	path := os.Getenv("LOCAL_INVENTORY_RESET_CONFIG")
	if path == "" {
		t.Skip("仅在明确批准清空本地项目测试数据时开启")
	}
	require.Equal(t, "DELETE_LOCAL_INVENTORY", os.Getenv("LOCAL_INVENTORY_RESET_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	require.Equal(t, "mysql", conf.GetString("data.db.main.driver"))
	dbConfig, err := mysql.ParseDSN(conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:3306", dbConfig.Addr)
	require.Equal(t, "unified", dbConfig.DBName)
	graphConfig, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	require.Equal(t, "unified_infra_topology", graphConfig.Space)
	for _, host := range graphConfig.Hosts {
		require.Equal(t, "127.0.0.1", host.Host)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	var active int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE status IN ('queued','running','validating','publishing')").Scan(&active))
	require.Zero(t, active, "有活动同步任务时禁止清空")
	client, err := graphclient.New(graphConfig)
	require.NoError(t, err)
	defer client.Close()
	read := func(statement string) [][]string {
		result, err := client.ExecuteParameter(ctx, statement, nil)
		require.NoError(t, persistenceCheckGraphResult(result, err))
		rows, err := persistenceTable(result)
		require.NoError(t, err)
		return rows
	}
	// 显式限定现有模型的全部关系类型，包含可能没有实体端点的残留边。
	for _, edge := range []string{"composition_relation", "power_relation", "network_relation", "spatial_relation"} {
		for {
			result, err := client.ExecuteParameter(ctx, "LOOKUP ON "+edge+" YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank | LIMIT 500;", nil)
			require.NoError(t, persistenceCheckGraphResult(result, err))
			rows, err := persistenceTable(result)
			require.NoError(t, err)
			if len(rows) == 1 {
				break
			}
			statement := "DELETE EDGE " + edge + " "
			for index, row := range rows[1:] {
				if index > 0 {
					statement += ","
				}
				statement += fmt.Sprintf("%q->%q@%s", persistenceString(row[0]), persistenceString(row[1]), row[2])
			}
			read(statement + ";")
		}
		t.Logf("cleared edge=%s", edge)
	}
	for _, tag := range []string{"device", "data_center", "cabinet", "interface", "transformer", "pod", "gpu", "ups_group", "ups", "rpp"} {
		deleted := 0
		for {
			rows := read("LOOKUP ON " + tag + " YIELD id(vertex) AS vid | LIMIT 500;")
			if len(rows) == 1 {
				break
			}
			statement := "DELETE VERTEX "
			for index, row := range rows[1:] {
				if index > 0 {
					statement += ","
				}
				statement += fmt.Sprintf("%q", persistenceString(row[0]))
			}
			read(statement + " WITH EDGE;")
			deleted += len(rows) - 1
		}
		t.Logf("cleared tag=%s deleted=%d remaining=0", tag, deleted)
	}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	for _, table := range []string{"sync_diagnostics", "publications", "sync_checkpoints", "sync_runs"} {
		result, err := tx.ExecContext(ctx, "DELETE FROM "+table)
		require.NoError(t, err)
		affected, err := result.RowsAffected()
		require.NoError(t, err)
		t.Logf("cleared table=%s deleted=%d", table, affected)
	}
	require.NoError(t, tx.Commit())
	for _, table := range []string{"sync_diagnostics", "publications", "sync_checkpoints", "sync_runs"} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
		require.Zero(t, count)
	}
	var sources int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM sources").Scan(&sources))
	require.Positive(t, sources)
	t.Logf("PASS: asset graph and sync tables empty; sources retained=%d; schema/users untouched", sources)
}
