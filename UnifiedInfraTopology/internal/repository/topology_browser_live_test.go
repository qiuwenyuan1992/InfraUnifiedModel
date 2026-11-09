package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

type topologyReadLiveTrace struct {
	graphclient.Client
	t *testing.T
}

func (c topologyReadLiveTrace) ExecuteParameter(ctx context.Context, query string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	result, err := c.Client.ExecuteParameter(ctx, query, params)
	if err != nil {
		c.t.Logf("图库调用失败: %v", err)
	} else if result != nil && !result.IsSucceed() {
		c.t.Logf("图库查询错误: %s query=%s", result.GetErrorMsg(), query)
	}
	return result, err
}

func TestLiveTopologyBrowserReadOnly(t *testing.T) {
	path := os.Getenv("TOPOLOGY_BROWSER_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读验证节点和邻居，不修改图库")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	repo, cleanup, err := newTopologyReadRepository(conf, func(cfg graphclient.Config) (graphclient.Client, error) {
		client, e := graphclient.New(cfg)
		if e != nil {
			return nil, e
		}
		return topologyReadLiveTrace{client, t}, nil
	})
	require.NoError(t, err)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	identity := model.EntityIdentity{SourceID: "00000000000000000000000000000238", EntityType: model.EntityDevice, StableID: "A514359X5413481"}
	node, err := repo.Node(ctx, identity)
	require.NoError(t, err)
	require.Equal(t, identity.StableID, node.StableID)
	graph, err := repo.Neighbors(ctx, identity, TopologyNeighborQuery{Limit: 100, Direction: "both"})
	require.NoError(t, err)
	require.NotEmpty(t, graph.Edges)
	require.LessOrEqual(t, len(graph.Edges), 100)
	counts := map[string]int{}
	nodes := map[string]TopologyNode{}
	for _, n := range graph.Nodes {
		require.Equal(t, identity.SourceID, n.SourceID)
		nodes[n.ID] = n
	}
	for _, e := range graph.Edges {
		require.Equal(t, identity.SourceID, e.SourceID)
		require.True(t, e.From == node.ID || e.To == node.ID)
		require.NotEmpty(t, e.Rank)
		require.Contains(t, nodes, e.From)
		require.Contains(t, nodes, e.To)
		counts[e.RelationKind]++
	}
	require.Positive(t, counts["contains_gpu"])
	t.Logf("真实设备=%s 节点=%d 边=%d 类型=%v 截断=%v", node.StableID, len(graph.Nodes), len(graph.Edges), counts, graph.Truncated)
	gpu := model.EntityIdentity{SourceID: identity.SourceID, EntityType: model.EntityGPU, StableID: "1750125024470"}
	gpuNode, err := repo.Node(ctx, gpu)
	require.NoError(t, err)
	require.Equal(t, gpu.StableID, gpuNode.StableID)
	gpuGraph, err := repo.Neighbors(ctx, gpu, TopologyNeighborQuery{Limit: 100, Direction: "both"})
	require.NoError(t, err)
	network := 0
	for _, e := range gpuGraph.Edges {
		if e.RelationKind == "gpu_uplink" {
			network++
		}
	}
	require.Positive(t, network)
	t.Logf("真实GPU=%s 节点=%d 边=%d 上联=%d", gpuNode.StableID, len(gpuGraph.Nodes), len(gpuGraph.Edges), network)
	small, err := repo.Neighbors(ctx, identity, TopologyNeighborQuery{Limit: 1, Direction: "both"})
	require.NoError(t, err)
	require.Len(t, small.Edges, 1)
	require.True(t, small.Truncated)
	_, err = repo.Node(ctx, model.EntityIdentity{SourceID: identity.SourceID, EntityType: model.EntityDevice, StableID: "missing-test-node"})
	require.ErrorIs(t, err, ErrInventoryNotFound)
}
