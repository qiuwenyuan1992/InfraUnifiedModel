package service

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestScenePublishesOnlyVerifiedComponentOwners(t *testing.T) {
	device := contextNode(model.EntityDevice, "device")
	port := contextNode(model.EntityInterface, "port")
	edges := scenePage(device, []repository.TopologyNode{port}, model.EdgeComposition, model.RelationOwnsInterface, false).Edges
	for _, status := range []string{"", "truncated", "ambiguous", "missing"} {
		result := &TopologyScene{}
		if status != "" {
			result.Unassigned = []SceneUnassigned{{Node: compactSceneNode(port), Status: status}}
		}
		walk := &sceneWalk{nodes: map[string]repository.TopologyNode{device.ID: device, port.ID: port}, edges: map[string]repository.TopologyEdge{edges[0].ID: edges[0]}, warnings: map[string]bool{}, result: result}
		walk.finish()
		if status == "" {
			require.Equal(t, []SceneComponentOwner{{ParentID: device.ID, ChildIDs: []string{port.ID}}}, result.ComponentOwners)
		} else {
			require.Empty(t, result.ComponentOwners)
		}
	}
}

func TestSceneComponentOwnerDoesNotChooseAmongConflictingParents(t *testing.T) {
	first := contextNode(model.EntityDevice, "first")
	second := contextNode(model.EntityDevice, "second")
	port := contextNode(model.EntityInterface, "port")
	edges := map[string]repository.TopologyEdge{}
	for _, parent := range []repository.TopologyNode{first, second} {
		edge := scenePage(parent, []repository.TopologyNode{port}, model.EdgeComposition, model.RelationOwnsInterface, false).Edges[0]
		edges[edge.ID] = edge
	}
	result := &TopologyScene{}
	walk := &sceneWalk{nodes: map[string]repository.TopologyNode{first.ID: first, second.ID: second, port.ID: port}, edges: edges, warnings: map[string]bool{}, result: result}
	walk.finish()
	require.Empty(t, result.ComponentOwners)
	require.Len(t, result.Edges, 2, "歧义边仍保留，不任意选择父节点")
}

func TestSceneBatchNetworkTruncationAndValidation(t *testing.T) {
	device := contextNode(model.EntityDevice, "switch")
	port := contextNode(model.EntityInterface, "port")
	remote := contextNode(model.EntityInterface, "remote")
	for _, name := range []string{"truncated", "budget", "source", "root", "unsupported relation", "link outside scope", "too many edges"} {
		t.Run(name, func(t *testing.T) {
			page := scenePage(device, []repository.TopologyNode{port}, model.EdgeComposition, model.RelationOwnsInterface, false)
			page.Root = nil
			page.Nodes = []repository.TopologyNode{device, port, remote}
			var readErr error
			switch name {
			case "truncated":
				page.Truncated = true
			case "budget":
				readErr = repository.ErrTopologyQueryBudget
			case "source":
				page.Edges[0].SourceID = "other"
			case "root":
				page.Root = &device
			case "unsupported relation":
				page.Edges[0].RelationKind = string(model.RelationServerUplink)
			case "link outside scope":
				page.Edges = scenePage(remote, []repository.TopologyNode{port}, model.EdgeNetwork, model.RelationLinksTo, false).Edges
			case "too many edges":
				for len(page.Edges) <= topologySceneEdges {
					page.Edges = append(page.Edges, page.Edges[0])
				}
			}
			stub := &sceneReadStub{network: func(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error) {
				return page, readErr
			}}
			walk := &sceneWalk{service: &topologyBrowseService{repo: stub}, ctx: context.Background(), source: device.SourceID, result: &TopologyScene{}, nodes: map[string]repository.TopologyNode{device.ID: device}, edges: map[string]repository.TopologyEdge{}, warnings: map[string]bool{}}
			err := walk.network([]repository.TopologyNode{device})
			if name == "truncated" {
				require.NoError(t, err)
				require.True(t, walk.result.Truncated)
				require.Len(t, walk.result.Unassigned, 1)
				require.Equal(t, "truncated", walk.result.Unassigned[0].Status)
			} else if name == "budget" {
				require.NoError(t, err)
				require.True(t, walk.exhausted.Load())
				require.True(t, walk.warnings["query_budget_exhausted"])
			} else {
				require.ErrorIs(t, err, ErrInventoryNotReady)
			}
		})
	}
}

func TestSceneBatchNetworkDeduplicatesVisibleSeeds(t *testing.T) {
	device := contextNode(model.EntityDevice, "switch")
	hidden := contextNode(model.EntityDevice, "not-loaded")
	calls := 0
	stub := &sceneReadStub{network: func(_ context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
		calls++
		require.Equal(t, []repository.TopologyNode{device}, seeds)
		require.Equal(t, topologySceneEdges, limit)
		return &repository.TopologyNeighborhood{Nodes: seeds, Edges: []repository.TopologyEdge{}}, nil
	}}
	walk := &sceneWalk{service: &topologyBrowseService{repo: stub}, ctx: context.Background(), source: device.SourceID, result: &TopologyScene{}, nodes: map[string]repository.TopologyNode{device.ID: device}, edges: map[string]repository.TopologyEdge{}, warnings: map[string]bool{}}
	require.NoError(t, walk.network([]repository.TopologyNode{device, device, hidden}))
	require.Equal(t, 1, calls)
}

func TestSceneUsesBatchNetworkAndKeepsAmbiguousOwners(t *testing.T) {
	root := contextNode(model.EntityPod, "pod")
	device := contextNode(model.EntityDevice, "switch")
	port := contextNode(model.EntityInterface, "port")
	other := contextNode(model.EntityDevice, "other")
	calls := 0
	stub := &sceneReadStub{root: root}
	stub.children = func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{device}}, nil
	}
	stub.neighbors = func(i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		if q.EdgeType != model.EdgeSpatial {
			return nil, errors.New("unexpected per-node network traversal")
		}
		return scenePage(device, nil, q.EdgeType, q.RelationKind, false), nil
	}
	stub.network = func(ctx context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
		calls++
		require.Equal(t, []repository.TopologyNode{device}, seeds)
		require.Equal(t, topologySceneEdges, limit)
		page := scenePage(device, []repository.TopologyNode{port}, model.EdgeComposition, model.RelationOwnsInterface, false)
		page.Root = nil
		page.Nodes = []repository.TopologyNode{device, port, other}
		page.Edges = append(page.Edges, scenePage(other, []repository.TopologyNode{port}, model.EdgeComposition, model.RelationOwnsInterface, false).Edges...)
		return page, nil
	}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	scene, err := NewTopologyBrowseService(stub, conf).Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Len(t, scene.Edges, 2)
	found := false
	for _, entry := range scene.Unassigned {
		if entry.Node.ID == port.ID {
			found = true
			require.Equal(t, "ambiguous", entry.Status)
		}
	}
	require.True(t, found)
}

func TestSceneReadsSwitchPortsAndIncomingPhysicalLink(t *testing.T) {
	root := contextNode(model.EntityPod, "pod")
	sw := contextNode(model.EntityDevice, "switch")
	sw.Properties = map[string]any{"parent_type_id": int64(54), "role": "T1"}
	local, remote := contextNode(model.EntityInterface, "local"), contextNode(model.EntityInterface, "remote")
	owner := contextNode(model.EntityDevice, "upstream")
	owner.Properties = map[string]any{"parent_type_id": int64(54), "role": "T2"}
	stub := &sceneReadStub{root: root, children: func(model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
		return &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{sw}}, nil
	}}
	stub.neighbors = func(i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
		require.Equal(t, model.EdgeSpatial, q.EdgeType)
		n := contextNode(i.EntityType, i.StableID)
		return scenePage(n, nil, q.EdgeType, q.RelationKind, false), nil
	}
	calls := 0
	stub.network = func(_ context.Context, seeds []repository.TopologyNode, limit int) (*repository.TopologyNeighborhood, error) {
		calls++
		require.Equal(t, []repository.TopologyNode{sw}, seeds)
		require.Equal(t, topologySceneEdges, limit)
		page := scenePage(sw, []repository.TopologyNode{local}, model.EdgeComposition, model.RelationOwnsInterface, false)
		page.Root = nil
		page.Nodes = []repository.TopologyNode{sw, local, remote, owner}
		page.Edges = append(page.Edges, scenePage(local, []repository.TopologyNode{remote}, model.EdgeNetwork, model.RelationLinksTo, true).Edges...)
		page.Edges = append(page.Edges, scenePage(remote, []repository.TopologyNode{owner}, model.EdgeComposition, model.RelationOwnsInterface, true).Edges...)
		return page, nil
	}
	conf := topologyConf()
	conf.Set("inventory.cursor_key", strings.Repeat("k", 32))
	scene, err := NewTopologyBrowseService(stub, conf).Scene(context.Background(), "reader", sceneIdentity(root), repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.ElementsMatch(t, []SceneEdge{
		compactSceneEdge(scenePage(sw, []repository.TopologyNode{local}, model.EdgeComposition, model.RelationOwnsInterface, false).Edges[0]),
		compactSceneEdge(scenePage(local, []repository.TopologyNode{remote}, model.EdgeNetwork, model.RelationLinksTo, true).Edges[0]),
		compactSceneEdge(scenePage(remote, []repository.TopologyNode{owner}, model.EdgeComposition, model.RelationOwnsInterface, true).Edges[0]),
	}, scene.Edges)
	for _, entry := range scene.Unassigned {
		require.Equal(t, sw.ID, entry.Node.ID, "both interface owners are known")
	}
	links := 0
	for _, e := range scene.Edges {
		if e.RelationKind == "links_to" {
			links++
			require.Equal(t, remote.ID, e.From)
			require.Equal(t, local.ID, e.To)
		}
	}
	require.Equal(t, 1, links)
	require.Len(t, scene.NetworkLevels, 2)
}
