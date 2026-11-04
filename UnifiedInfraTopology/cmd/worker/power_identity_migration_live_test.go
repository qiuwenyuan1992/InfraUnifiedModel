package main

import (
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyLocalPowerInstIDMigration(t *testing.T) {
	path := os.Getenv("POWER_IDENTITY_MIGRATION_CONFIG")
	if path == "" {
		t.Skip("仅显式审批后迁移本地四类电力身份Schema")
	}
	require.Equal(t, "POWER_INST_ID_ONLY", os.Getenv("POWER_IDENTITY_MIGRATION_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	require.Equal(t, "unified_infra_topology", cfg.Space)
	for _, host := range cfg.Hosts {
		require.Equal(t, "127.0.0.1", host.Host)
	}
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, tag := range []string{"transformer", "ups_group", "ups", "rpp"} {
		r, err := client.ExecuteParameter(ctx, "LOOKUP ON "+tag+" YIELD id(vertex) AS vid | YIELD count(*) AS total;", nil)
		require.NoError(t, persistenceCheckGraphResult(r, err))
		rows, err := persistenceTable(r)
		require.NoError(t, err)
		require.Equal(t, "0", rows[1][0], "仅在四类电力无数据时执行，不删现存资产")
	}
	sqlPath := os.Getenv("POWER_IDENTITY_MIGRATION_FILE")
	require.NotEmpty(t, sqlPath)
	content, err := os.ReadFile(sqlPath)
	require.NoError(t, err)
	for _, statement := range strings.Split(string(content), ";") {
		var lines []string
		for _, line := range strings.Split(statement, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		query := strings.TrimSpace(strings.Join(lines, "\n"))
		if query == "" {
			continue
		}
		r, err := client.ExecuteParameter(ctx, query+";", nil)
		if r != nil {
			require.True(t, r.IsSucceed(), "statement=%s error=%s", query, r.GetErrorMsg())
		}
		require.NoError(t, persistenceCheckGraphResult(r, err))
		t.Logf("applied: %s", query)
	}
	t.Log("limited power identity migration applied; no vertex/edge deletion")
}
