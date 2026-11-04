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

func TestLiveLinksToControlStatusReadOnly(t *testing.T) {
	path := os.Getenv("LINKS_TO_CONTROL_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读查看互联任务与检查点状态")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT id,status,created_at,started_at,finished_at,error_code,lease_expires_at FROM sync_runs WHERE idempotency_key LIKE 'cli-links-to-%' ORDER BY created_at DESC LIMIT 5")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var created time.Time
		var started, finished, lease sql.NullTime
		var code sql.NullString
		require.NoError(t, rows.Scan(&id, &status, &created, &started, &finished, &code, &lease))
		t.Logf("run=%s status=%s created=%s started=%v finished=%v code=%s lease=%v", id, status, created, started, finished, code.String, lease)
	}
	require.NoError(t, rows.Err())
	var active int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE status IN ('queued','running','validating','publishing')").Scan(&active))
	t.Logf("active_tasks=%d", active)
}
