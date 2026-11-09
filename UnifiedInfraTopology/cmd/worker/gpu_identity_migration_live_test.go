package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveGPUIdentitySchema(t *testing.T) {
	path := os.Getenv("GPU_SCHEMA_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对GPU Schema；独立确认令牌才允许DDL")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	apply := os.Getenv("GPU_IDENTITY_MIGRATION_CONFIRM")
	if apply != "" {
		require.Equal(t, "GPU_PARTS_SN_EMPTY_GRAPH_ONLY", apply)
		require.Equal(t, "unified_infra_topology", cfg.Space)
		for _, host := range cfg.Hosts {
			require.Equal(t, "127.0.0.1", host.Host)
		}
	}
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	read := func(query string) [][]string {
		r, err := client.ExecuteParameter(ctx, query, nil)
		require.NoError(t, persistenceCheckGraphResult(r, err))
		rows := r.AsStringTable()
		t.Logf("%s %v", query, rows)
		return rows
	}
	if snapshot := os.Getenv("GPU_PROGRESS_SNAPSHOT"); snapshot != "" {
		require.Empty(t, apply)
		at, err := time.Parse(time.RFC3339Nano, snapshot)
		require.NoError(t, err)
		for _, query := range []string{
			"LOOKUP ON gpu WHERE gpu.source_id == $source AND gpu.synced_at == datetime($snapshot) YIELD id(vertex) AS vid | YIELD count(*) AS total;",
			"LOOKUP ON composition_relation WHERE composition_relation.relation_kind == \"contains_gpu\" AND composition_relation.synced_at == datetime($snapshot) YIELD src(edge) AS src | YIELD count(*) AS total;",
			"LOOKUP ON network_relation WHERE network_relation.relation_kind == \"gpu_uplink\" AND network_relation.synced_at == datetime($snapshot) YIELD src(edge) AS src | YIELD count(*) AS total;",
		} {
			result, err := client.ExecuteParameter(ctx, query, map[string]interface{}{"snapshot": at.UTC().Format("2006-01-02T15:04:05.000000+00:00"), "source": "00000000000000000000000000000238"})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.IsSucceed(), "%s", result.GetErrorMsg())
			t.Logf("current GPU snapshot: %s %v", query, result.AsStringTable())
		}
		return
	}
	before := read("SHOW CREATE TAG gpu;")
	read("SHOW TAG INDEXES;")
	read("SHOW CREATE EDGE network_relation;")
	read("LOOKUP ON gpu YIELD id(vertex) AS vid | YIELD count(*) AS total;")
	read("LOOKUP ON composition_relation WHERE composition_relation.relation_kind == \"contains_gpu\" YIELD src(edge) AS src | YIELD count(*) AS total;")
	read("LOOKUP ON network_relation WHERE network_relation.relation_kind == \"gpu_uplink\" YIELD src(edge) AS src | YIELD count(*) AS total;")
	if apply != "" {
		require.GreaterOrEqual(t, len(before), 2)
		require.Contains(t, before[1][1], "uuid")
		for _, query := range []string{
			"LOOKUP ON gpu YIELD id(vertex) AS vid | YIELD count(*) AS total;",
			"LOOKUP ON composition_relation WHERE composition_relation.relation_kind == \"contains_gpu\" YIELD src(edge) AS src | YIELD count(*) AS total;",
			"LOOKUP ON network_relation WHERE network_relation.relation_kind == \"gpu_uplink\" YIELD src(edge) AS src | YIELD count(*) AS total;",
		} {
			rows := read(query)
			count, err := standaloneGraphCount(rows)
			require.NoError(t, err)
			require.Zero(t, count, "不删除或迁移现有GPU节点/边")
		}
		content, err := os.ReadFile("../../internal/migration/nebula/0004_gpu_parts_sn_identity.ngql")
		require.NoError(t, err)
		for _, statement := range strings.Split(string(content), ";") {
			lines := []string{}
			for _, line := range strings.Split(statement, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "--") {
					lines = append(lines, line)
				}
			}
			query := strings.TrimSpace(strings.Join(lines, "\n"))
			if query != "" {
				read(query + ";")
			}
		}
	}
	after := read("SHOW CREATE TAG gpu;")
	index := read("SHOW CREATE TAG INDEX idx_gpu_identity;")
	if os.Getenv("GPU_SCHEMA_EXPECT_PARTS_SN") == "1" || apply != "" {
		require.GreaterOrEqual(t, len(after), 2)
		require.GreaterOrEqual(t, len(index), 2)
		require.NotContains(t, after[1][1], "uuid")
		require.Contains(t, strings.ReplaceAll(after[1][1], "`", ""), "parts_sn string NOT NULL")
		require.Contains(t, strings.ReplaceAll(index[1][1], "`", ""), "parts_sn(192)")
		require.NotContains(t, index[1][1], "uuid")
		for _, sn := range []string{"1654924122254", "1654724129203", "1655024054851"} {
			r, err := client.ExecuteParameter(ctx, "LOOKUP ON gpu WHERE gpu.parts_sn == $parts_sn YIELD id(vertex) AS vid | YIELD count(*) AS total;", map[string]interface{}{"parts_sn": sn})
			require.NoError(t, persistenceCheckGraphResult(r, err))
			rows, err := persistenceTable(r)
			require.NoError(t, err)
			count, err := standaloneGraphCount(rows)
			require.NoError(t, err)
			require.Zero(t, count, "冲突SN不可发布")
			t.Logf("excluded conflicting GPU SN=%s count=%d", sn, count)
		}
	}
}
