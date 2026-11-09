package repository

import (
	"context"
	"errors"
	"sync"
)

var ErrTopologyQueryBudget = errors.New("topology query budget exhausted")

type topologyQueryBudgetKey struct{}
type topologyQueryBudget struct {
	mu        sync.Mutex
	remaining int
}

// 预算属于单次HTTP设备上下文，不影响其他读取或同步任务。
func WithTopologyQueryBudget(ctx context.Context, limit int) context.Context {
	return context.WithValue(ctx, topologyQueryBudgetKey{}, &topologyQueryBudget{remaining: limit})
}

type topologyNodeCacheKey struct{}
type topologyNodeCache struct {
	mu    sync.Mutex
	nodes map[string]*TopologyNode
}

// 仅复用一次设备上下文内验证过的节点，不形成跨请求的资产缓存。
func WithTopologyNodeCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, topologyNodeCacheKey{}, &topologyNodeCache{nodes: map[string]*TopologyNode{}})
}

func cachedTopologyNode(ctx context.Context, vid string) *TopologyNode {
	cache, _ := ctx.Value(topologyNodeCacheKey{}).(*topologyNodeCache)
	if cache == nil {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.nodes[vid]
}

func cacheTopologyNode(ctx context.Context, node *TopologyNode) {
	cache, _ := ctx.Value(topologyNodeCacheKey{}).(*topologyNodeCache)
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.nodes[node.ID] = node
}

func consumeTopologyQuery(ctx context.Context) error {
	budget, _ := ctx.Value(topologyQueryBudgetKey{}).(*topologyQueryBudget)
	if budget == nil {
		return nil
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.remaining <= 0 {
		return ErrTopologyQueryBudget
	}
	budget.remaining--
	return nil
}
