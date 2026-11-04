package main

import (
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestLivePowerValidationDetails(t *testing.T) {
	path := os.Getenv("POWER_VALIDATION_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验证最新成功电力任务")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	var at time.Time
	var id string
	require.NoError(t, db.QueryRow("SELECT id,started_at FROM sync_runs WHERE source_id = ? AND status = 'succeeded' AND idempotency_key LIKE 'cli-power-%' ORDER BY created_at DESC LIMIT 1", "00000000000000000000000000001030").Scan(&id, &at))
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	var expected int64
	var cursor string
	require.NoError(t, db.QueryRow("SELECT `cursor` FROM sync_checkpoints WHERE source_id = ? AND resource = 'power_relations'", "00000000000000000000000000001030").Scan(&cursor))
	var cp persistenceCursor
	require.NoError(t, json.Unmarshal([]byte(cursor), &cp))
	require.Equal(t, id, cp.RunID)
	expected = cp.Total
	r, err := verifyPowerGraph(context.Background(), client, "00000000000000000000000000001030", at, expected)
	require.NoError(t, err)
	t.Logf("run=%s edges=%d by_kind=%v", id, r.Current, r.ByKind)
}
