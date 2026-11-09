package main

import (
	"context"
	"os"
	"testing"
	"time"

	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveClearEntireGraphSpace(t *testing.T) {
	path := os.Getenv("GRAPH_CLEAR_SPACE_CONFIG")
	if path == "" {
		t.Skip("全图清空必须显式授权，默认不访问外部服务")
	}
	require.Equal(t, "CLEAR_ENTIRE_UNIFIED_INFRA_TOPOLOGY", os.Getenv("GRAPH_CLEAR_SPACE_CONFIRM"))
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	query := func(q string) [][]string {
		result, e := client.ExecuteParameter(ctx, q, nil)
		require.NoError(t, e)
		require.NotNil(t, result)
		require.True(t, result.IsSucceed(), "%s: %s", q, result.GetErrorMsg())
		return result.AsStringTable()
	}
	tags := query("SHOW TAGS;")
	edges := query("SHOW EDGES;")
	indexes := query("SHOW TAG INDEXES;")
	edgeIndexes := query("SHOW EDGE INDEXES;")
	query("CLEAR SPACE unified_infra_topology;")
	require.Equal(t, tags, query("SHOW TAGS;"))
	require.Equal(t, edges, query("SHOW EDGES;"))
	require.Equal(t, indexes, query("SHOW TAG INDEXES;"))
	require.Equal(t, edgeIndexes, query("SHOW EDGE INDEXES;"))
	for _, tag := range []string{"data_center", "cabinet", "device", "transformer", "ups_group", "ups", "rpp", "pod", "interface", "gpu"} {
		rows := query("LOOKUP ON " + tag + " YIELD id(vertex) AS vid | YIELD count(*) AS total;")
		count, e := standaloneGraphCount(rows)
		require.NoError(t, e)
		require.Zero(t, count, "全来源节点必须归零: %s", tag)
		t.Logf("tag=%s total=%d", tag, count)
	}
	for _, edge := range []string{"spatial_relation", "composition_relation", "network_relation", "power_relation"} {
		rows := query("LOOKUP ON " + edge + " YIELD src(edge) AS src | YIELD count(*) AS total;")
		count, e := standaloneGraphCount(rows)
		require.NoError(t, e)
		require.Zero(t, count, "全来源关系必须归零: %s", edge)
		t.Logf("edge=%s total=%d", edge, count)
	}
	t.Log("整个图空间数据已清空；所有来源数据删除，Schema/索引保留，未操作MySQL")
}
