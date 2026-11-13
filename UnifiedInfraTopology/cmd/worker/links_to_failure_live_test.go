package main

import (
	"UnifiedInfraTopology/internal/repository"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"os"
	"testing"
	"time"
)

type linksFailureTrace struct {
	graphclient.Client
	t *testing.T
}

func (c linksFailureTrace) ExecuteParameter(ctx context.Context, q string, p map[string]interface{}) (*nebulago.ResultSet, error) {
	started := time.Now()
	result, err := c.Client.ExecuteParameter(ctx, q, p)
	if err != nil {
		c.t.Logf("graph call error=%v elapsed=%s", err, time.Since(started))
	} else if result != nil && !result.IsSucceed() {
		c.t.Logf("graph query error=%s elapsed=%s", result.GetErrorMsg(), time.Since(started))
	} else if result != nil {
		c.t.Logf("graph rows=%d elapsed=%s", result.GetRowSize(), time.Since(started))
	}
	return result, err
}

func TestLiveLinksToFailureReadOnly(t *testing.T) {
	path := os.Getenv("LINKS_FAILURE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读复核失败任务，不同步或写图")
	}
	id := os.Getenv("LINKS_FAILURE_RUN_ID")
	require.True(t, inventoryID.MatchString(id))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	var raw string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT `cursor` FROM sync_checkpoints WHERE source_id=? AND resource=?", "00000000000000000000000000000238", "links_to").Scan(&raw))
	var cursor repository.DeviceSyncCursor
	require.NoError(t, json.Unmarshal([]byte(raw), &cursor))
	require.Equal(t, id, cursor.RunID)
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	report, err := verifyLinksToGraph(ctx, linksFailureTrace{client, t}, "00000000000000000000000000000238", cursor.SnapshotAt, int64(cursor.Total))
	t.Logf("checkpoint total=%d snapshot=%s graph_report=%+v verification_error=%v", cursor.Total, cursor.SnapshotAt.Format(time.RFC3339Nano), report, err)
	require.NoError(t, err)
}
