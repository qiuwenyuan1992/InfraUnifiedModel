package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/stretchr/testify/require"
)

type sceneReadStub struct {
	repository.TopologyReadRepository
	root      repository.TopologyNode
	children  func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error)
	neighbors func(model.EntityIdentity, repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error)
}

func (r *sceneReadStub) Node(context.Context, model.EntityIdentity) (*repository.TopologyNode, error) {
	return &r.root, nil
}
func (r *sceneReadStub) Children(_ context.Context, i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	return r.children(i, q)
}
func (r *sceneReadStub) Neighbors(_ context.Context, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
	return r.neighbors(i, q)
}
func sceneIdentity(n repository.TopologyNode) model.EntityIdentity {
	return model.EntityIdentity{SourceID: n.SourceID, EntityType: model.EntityType(n.EntityType), StableID: n.StableID}
}
func scenePage(root repository.TopologyNode, peers []repository.TopologyNode, edgeType model.EdgeType, kind model.RelationKind, in bool) *repository.TopologyNeighborhood {
	p := &repository.TopologyNeighborhood{Root: &root, Nodes: peers, Edges: []repository.TopologyEdge{}}
	for _, n := range peers {
		from, to := root.ID, n.ID
		if in {
			from, to = to, from
		}
		p.Edges = append(p.Edges, repository.TopologyEdge{ID: from + "/" + to, SourceID: root.SourceID, From: from, To: to, EdgeType: string(edgeType), RelationKind: string(kind), Rank: "0"})
	}
	return p
}

func TestScenePodMissingAmbiguousAndActualRoles(t *testing.T) {
	root := contextNode(model.EntityPod, "pod")
	devices := []repository.TopologyNode{contextNode(model.EntityDevice, "missing"), contextNode(model.EntityDevice, "ambiguous"), contextNode(model.EntityDevice, "unique")}
	devices[0].Properties = map[string]any{"role": "T0", "parent_type_id": int64(54), "secret": "not-in-json"}
	devices[1].Properties = map[string]any{"role": "T3"}
	devices[2].Properties = map[string]any{"role": "T2", "parent_type_id": int64(54)}
	c1, c2 := contextNode(model.EntityCabinet, "cab1"), contextNode(model.EntityCabinet, "cab2")
	r := &sceneReadStub{root: root}
	r.children = func(i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		require.Equal(t, 5, q.Limit)
		return &repository.TopologyDirectoryPage{Items: devices, NextAfter: strings.Repeat("b", 64)}, nil
	}
	r.neighbors = func(i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		n := contextNode(i.EntityType, i.StableID)
		if q.EdgeType == model.EdgeNetwork || q.EdgeType == model.EdgeComposition {
			require.Equal(t, "out", q.Direction)
			return scenePage(n, nil, q.EdgeType, q.RelationKind, false), nil
		}
		require.Equal(t, model.RelationLocatedIn, q.RelationKind)
		require.Equal(t, model.EntityCabinet, q.PeerEntityType)
		peers := []repository.TopologyNode{}
		if i.StableID == "ambiguous" {
			peers = []repository.TopologyNode{c1, c2}
		}
		if i.StableID == "unique" {
			peers = []repository.TopologyNode{c1}
		}
		return scenePage(n, peers, q.EdgeType, q.RelationKind, false), nil
	}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	s := NewTopologyBrowseService(r, conf).(*topologyBrowseService)
	p, err := s.Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Len(t, p.Cabinets, 1)
	require.Len(t, p.Cabinets[0].Devices, 1)
	require.Equal(t, devices[2].ID, p.Cabinets[0].Devices[0].ID)
	require.Len(t, p.Unassigned, 2)
	require.Equal(t, "missing", p.Unassigned[0].Status)
	require.Equal(t, "ambiguous", p.Unassigned[1].Status)
	require.NotEmpty(t, p.NextAfter)
	require.NotEqual(t, strings.Repeat("b", 64), p.NextAfter)
	_, err = s.decodeDirectoryCursor(p.NextAfter, topologyDirectoryCursor{Version: 1, User: "reader", Source: root.SourceID, EntityType: model.EntityPod, Parent: root.StableID})
	require.NoError(t, err)
	levels := map[string]*int{}
	for _, l := range p.NetworkLevels {
		levels[l.Role] = l.Level
	}
	require.Equal(t, 0, *levels["T0"])
	require.Equal(t, 2, *levels["T2"])
	require.Nil(t, levels["T3"])
	data, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(data), "properties")
	require.NotContains(t, string(data), "created_at")
	require.NotContains(t, string(data), "secret")
	require.Contains(t, string(data), `"synced_at":null`)
	require.Contains(t, string(data), `"parent_type_id":54`)
	require.Len(t, p.Edges, 3, "only stored spatial edges, never POD membership guesses")
}

func TestSceneRejectsCrossSourceAndWrongRoot(t *testing.T) {
	root := contextNode(model.EntityPod, "pod")
	device := contextNode(model.EntityDevice, "device")
	device.SourceID = strings.Repeat("b", 32)
	r := &sceneReadStub{root: root, children: func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{device}}, nil
	}}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	s := NewTopologyBrowseService(r, conf).(*topologyBrowseService)
	_, err := s.Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryNotReady)
	_, err = s.Scene(context.Background(), "reader", sceneIdentity(contextNode(model.EntityPod, "other")), repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryNotReady)
	_, err = s.Scene(context.Background(), "writer", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryForbidden)
	_, err = s.Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{Limit: 11})
	require.ErrorIs(t, err, ErrInventoryInvalid)
}

func TestSceneDataCenterActualEdgesAndCabinetPagination(t *testing.T) {
	root, cabinet, device := contextNode(model.EntityDataCenter, "dc"), contextNode(model.EntityCabinet, "cab"), contextNode(model.EntityDevice, "server")
	r := &sceneReadStub{root: root}
	r.children = func(i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		if i.EntityType == model.EntityDataCenter {
			require.Equal(t, 5, q.Limit)
			return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{cabinet}}, nil
		}
		require.Equal(t, 20, q.Limit)
		return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{device}, NextAfter: strings.Repeat("c", 64)}, nil
	}
	r.neighbors = func(i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		n := contextNode(i.EntityType, i.StableID)
		if q.EdgeType == model.EdgeNetwork || q.EdgeType == model.EdgeComposition {
			return scenePage(n, nil, q.EdgeType, q.RelationKind, false), nil
		}
		parent := cabinet
		if i.EntityType == model.EntityCabinet {
			parent = root
			require.Equal(t, model.EntityDataCenter, q.PeerEntityType)
		}
		return scenePage(n, []repository.TopologyNode{parent}, q.EdgeType, q.RelationKind, false), nil
	}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	s := NewTopologyBrowseService(r, conf).(*topologyBrowseService)
	p, err := s.Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Len(t, p.Cabinets, 1)
	require.Len(t, p.Cabinets[0].Devices, 1)
	require.Len(t, p.Edges, 2)
	require.Empty(t, p.NextAfter)
	require.True(t, p.Truncated)
	after, err := s.decodeDirectoryCursor(p.Cabinets[0].NextAfter, topologyDirectoryCursor{Version: 1, User: "reader", Source: root.SourceID, EntityType: model.EntityCabinet, Parent: cabinet.StableID})
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("c", 64), after)
}

func TestSceneNetworkRealPortsOwnersAndParallelEdges(t *testing.T) {
	root, device := contextNode(model.EntityPod, "pod"), contextNode(model.EntityDevice, "server")
	port, remote, owner := contextNode(model.EntityInterface, "local"), contextNode(model.EntityInterface, "remote"), contextNode(model.EntityDevice, "switch")
	owner.Properties = map[string]any{"role": "T1"}
	r := &sceneReadStub{root: root, children: func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{device}}, nil
	}}
	r.neighbors = func(i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		n := contextNode(i.EntityType, i.StableID)
		if q.EdgeType == model.EdgeSpatial {
			return scenePage(n, nil, q.EdgeType, q.RelationKind, false), nil
		}
		if i.EntityType == model.EntityDevice {
			if q.EdgeType == model.EdgeComposition {
				return scenePage(n, nil, q.EdgeType, q.RelationKind, false), nil
			}
			p := scenePage(n, []repository.TopologyNode{port}, model.EdgeNetwork, model.RelationServerUplink, false)
			extra := p.Edges[0]
			extra.ID += "/parallel"
			extra.Rank = "2"
			extra.Properties = map[string]any{"plane": int64(2), "private": "not-sent"}
			p.Edges = append(p.Edges, extra)
			return p, nil
		}
		if q.EdgeType == model.EdgeNetwork {
			return scenePage(n, []repository.TopologyNode{remote}, model.EdgeNetwork, model.RelationLinksTo, false), nil
		}
		require.Equal(t, model.RelationOwnsInterface, q.RelationKind)
		require.Equal(t, "in", q.Direction)
		if i.StableID == "remote" {
			return scenePage(n, []repository.TopologyNode{owner}, model.EdgeComposition, model.RelationOwnsInterface, true), nil
		}
		return scenePage(n, nil, q.EdgeType, q.RelationKind, true), nil
	}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	p, err := NewTopologyBrowseService(r, conf).Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Len(t, p.Edges, 4)
	require.Len(t, p.Nodes, 5)
	require.Len(t, p.Unassigned, 2)
	require.Equal(t, port.ID, p.Unassigned[1].Node.ID)
	require.Equal(t, "missing", p.Unassigned[1].Status)
	for _, e := range p.Edges {
		require.NotEqual(t, device.ID+"/"+owner.ID, e.ID)
		if e.Rank == "2" {
			require.Equal(t, int64(2), *e.Plane)
		}
	}
	data, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private")
	require.NotContains(t, string(data), "properties")
}

func TestSceneQueryBudgetAndDeadline(t *testing.T) {
	root, device := contextNode(model.EntityPod, "pod"), contextNode(model.EntityDevice, "server")
	var calls atomic.Int32
	r := &sceneReadStub{root: root, children: func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{device}}, nil
	}, neighbors: func(model.EntityIdentity, repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		calls.Add(1)
		return nil, repository.ErrTopologyQueryBudget
	}}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	s := NewTopologyBrowseService(r, conf)
	p, err := s.Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.True(t, p.Truncated)
	require.Contains(t, p.Warnings, "query_budget_exhausted")
	require.Equal(t, "truncated", p.Unassigned[0].Status)
	require.Equal(t, int32(1), calls.Load())
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = s.Scene(ctx, "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	r.children = func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		return nil, repository.ErrTopologyQueryBudget
	}
	p, err = s.Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Contains(t, p.Warnings, "query_budget_exhausted")
	require.Empty(t, p.Cabinets)
}

func TestSceneMaximumTwoHundredDevicesAndBoundedSpatialConcurrency(t *testing.T) {
	root := contextNode(model.EntityDataCenter, "dc")
	var active, max atomic.Int32
	r := &sceneReadStub{root: root}
	r.children = func(i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		items := []repository.TopologyNode{}
		if i.EntityType == model.EntityDataCenter {
			require.Equal(t, 10, q.Limit)
			for j := 0; j < 10; j++ {
				items = append(items, contextNode(model.EntityCabinet, fmt.Sprint(j)))
			}
		} else {
			require.Equal(t, 20, q.Limit)
			for j := 0; j < 20; j++ {
				items = append(items, contextNode(model.EntityDevice, fmt.Sprintf("%s/%d", i.StableID, j)))
			}
		}
		return &repository.TopologyDirectoryPage{Items: items}, nil
	}
	r.neighbors = func(i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		n := contextNode(i.EntityType, i.StableID)
		if q.EdgeType == model.EdgeNetwork || q.EdgeType == model.EdgeComposition {
			return scenePage(n, nil, q.EdgeType, q.RelationKind, false), nil
		}
		parent := root
		if i.EntityType == model.EntityDevice {
			count := active.Add(1)
			for {
				old := max.Load()
				if count <= old || max.CompareAndSwap(old, count) {
					break
				}
			}
			defer active.Add(-1)
			parent = contextNode(model.EntityCabinet, strings.Split(i.StableID, "/")[0])
		}
		return scenePage(n, []repository.TopologyNode{parent}, q.EdgeType, q.RelationKind, false), nil
	}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	p, err := NewTopologyBrowseService(r, conf).Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, p.Cabinets, 10)
	require.Len(t, p.Nodes, 211)
	require.Len(t, p.Edges, 210)
	require.LessOrEqual(t, max.Load(), int32(4))
	for _, c := range p.Cabinets {
		require.Len(t, c.Devices, 20)
	}
}
