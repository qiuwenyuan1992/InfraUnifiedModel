package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"testing"
)

func TestTopologyBudgetCountsActualGraphExecutions(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	gpu := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
	edge := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: root, To: gpu}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}), readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, readEdge(t, edge, nil))}}
	ctx := WithTopologyQueryBudget(context.Background(), 2)
	_, err := (&topologyReadRepository{client: fake}).Neighbors(ctx, root, TopologyNeighborQuery{Limit: 10, Direction: "out", EdgeType: model.EdgeComposition})
	require.ErrorIs(t, err, ErrTopologyQueryBudget)
	require.Len(t, fake.calls, 2, "读取对端前必须因真实请求预算停止")
	_, err = (&topologyReadRepository{client: fake}).Node(ctx, root)
	require.ErrorIs(t, err, ErrTopologyQueryBudget)
	require.Len(t, fake.calls, 2)
}

func TestTopologyContextNodeCacheIsRequestScoped(t *testing.T) {
	identity := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(identity))})}}
	repo := &topologyReadRepository{client: fake}
	ctx := WithTopologyNodeCache(WithTopologyQueryBudget(context.Background(), 1))
	first, err := repo.Node(ctx, identity)
	require.NoError(t, err)
	second, err := repo.Node(ctx, identity)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Len(t, fake.calls, 1)
	_, err = repo.Node(WithTopologyQueryBudget(context.Background(), 0), identity)
	require.ErrorIs(t, err, ErrTopologyQueryBudget, "不能跨请求复用缓存")
}

func TestTopologyContextReusesValidatedNeighborNodes(t *testing.T) {
	root := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	gpu := model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "g"}
	edge := model.RelationIdentity{SourceID: "s", EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: root, To: gpu}
	fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(root))}), readResult(t, []string{"src_vid", "dst_vid", "edge_rank", "props"}, readEdge(t, edge, nil)), readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, gpu)})}}
	ctx := WithTopologyNodeCache(WithTopologyQueryBudget(context.Background(), 3))
	repo := &topologyReadRepository{client: fake}
	_, err := repo.Neighbors(ctx, root, TopologyNeighborQuery{Limit: 10, Direction: "out", EdgeType: model.EdgeComposition})
	require.NoError(t, err)
	for _, identity := range []model.EntityIdentity{root, gpu} {
		node, err := repo.Node(ctx, identity)
		require.NoError(t, err)
		require.Equal(t, identity.StableID, node.StableID)
	}
	require.Len(t, fake.calls, 3)
}
