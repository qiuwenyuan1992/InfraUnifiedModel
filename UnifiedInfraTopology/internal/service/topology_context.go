package service

import (
	"context"
	"errors"
	"sort"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

const (
	topologyContextQueries = 40
	topologyContextNodes   = 200
	topologyContextEdges   = 400
	topologyContextStep    = 50
)

func topologyContextFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrInventoryNotReady
}

type TopologyDeviceContext struct {
	Root      *repository.TopologyNode  `json:"root"`
	Nodes     []repository.TopologyNode `json:"nodes"`
	Edges     []repository.TopologyEdge `json:"edges"`
	Truncated bool                      `json:"truncated"`
	Limit     int                       `json:"limit"`
	Warnings  []string                  `json:"warnings"`
}

type topologyContextRead struct {
	identity model.EntityIdentity
	query    repository.TopologyNeighborQuery
}

type topologyContextWalk struct {
	service  *topologyBrowseService
	ctx      context.Context
	result   *TopologyDeviceContext
	queries  int
	nodes    map[string]repository.TopologyNode
	edges    map[string]repository.TopologyEdge
	cache    map[topologyContextRead][]repository.TopologyNode
	warnings map[string]bool
}

func (s *topologyBrowseService) DeviceContext(ctx context.Context, user string, identity model.EntityIdentity) (*TopologyDeviceContext, error) {
	if err := s.auth.authorize(user, "sources"); err != nil {
		return nil, err
	}
	if err := ValidateTopologyIdentity(identity); err != nil {
		return nil, err
	}
	if identity.EntityType != model.EntityDevice {
		return nil, ErrInventoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.repo == nil {
		return nil, ErrInventoryNotReady
	}
	ctx = repository.WithTopologyNodeCache(repository.WithTopologyQueryBudget(ctx, topologyContextQueries))
	root, err := s.repo.Node(ctx, identity)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, repository.ErrInventoryNotFound) {
		return nil, ErrInventoryNotFound
	}
	if err != nil {
		return nil, topologyContextFailure(err)
	}
	if root == nil {
		return nil, ErrInventoryNotFound
	}
	w := &topologyContextWalk{service: s, ctx: ctx, result: &TopologyDeviceContext{Root: root, Limit: topologyContextNodes, Nodes: []repository.TopologyNode{}, Edges: []repository.TopologyEdge{}, Warnings: []string{}}, queries: 1, nodes: map[string]repository.TopologyNode{root.ID: *root}, edges: map[string]repository.TopologyEdge{}, cache: map[topologyContextRead][]repository.TopologyNode{}, warnings: map[string]bool{}}
	if err := w.walk(*root); err != nil {
		return nil, err
	}
	for _, n := range w.nodes {
		w.result.Nodes = append(w.result.Nodes, n)
	}
	for _, e := range w.edges {
		w.result.Edges = append(w.result.Edges, e)
	}
	sort.Slice(w.result.Nodes, func(i, j int) bool { return w.result.Nodes[i].ID < w.result.Nodes[j].ID })
	sort.Slice(w.result.Edges, func(i, j int) bool { return w.result.Edges[i].ID < w.result.Edges[j].ID })
	sort.Strings(w.result.Warnings)
	return w.result, nil
}

func (w *topologyContextWalk) warn(message string, truncated bool) {
	if !w.warnings[message] {
		w.warnings[message] = true
		w.result.Warnings = append(w.result.Warnings, message)
	}
	w.result.Truncated = w.result.Truncated || truncated
}

func (w *topologyContextWalk) neighbors(root repository.TopologyNode, edgeType model.EdgeType, kind model.RelationKind, peerType model.EntityType, direction, missing string) ([]repository.TopologyNode, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	read := topologyContextRead{identity: model.EntityIdentity{SourceID: root.SourceID, EntityType: model.EntityType(root.EntityType), StableID: root.StableID}, query: repository.TopologyNeighborQuery{Limit: topologyContextStep, Direction: direction, EdgeType: edgeType, RelationKind: kind, PeerEntityType: peerType}}
	if peers, ok := w.cache[read]; ok {
		return peers, nil
	}
	if w.queries >= topologyContextQueries {
		w.warn("query_budget_exhausted", true)
		return nil, nil
	}
	w.queries++
	page, err := w.service.repo.Neighbors(w.ctx, read.identity, read.query)
	if w.ctx.Err() != nil {
		return nil, w.ctx.Err()
	}
	if errors.Is(err, repository.ErrTopologyQueryBudget) {
		w.queries = topologyContextQueries
		w.warn("query_budget_exhausted", true)
		return nil, nil
	}
	if err != nil {
		return nil, topologyContextFailure(err)
	}
	if page == nil {
		return nil, ErrInventoryNotReady
	}
	if page.Truncated {
		w.warn("step_truncated", true)
	}
	nodes := map[string]repository.TopologyNode{root.ID: root}
	if page.Root != nil {
		nodes[page.Root.ID] = *page.Root
	}
	for _, n := range page.Nodes {
		nodes[n.ID] = n
	}
	// 每条边和缺失端点一起提交，避免预算截断留下悬空边。
	edges := append([]repository.TopologyEdge(nil), page.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	peers := map[string]repository.TopologyNode{}
	for index, e := range edges {
		if index >= topologyContextStep {
			w.warn("step_truncated", true)
			break
		}
		from, fromOK := nodes[e.From]
		to, toOK := nodes[e.To]
		if !fromOK || !toOK {
			return nil, ErrInventoryNotReady
		}
		peer := to
		if direction == "in" {
			peer = from
		}
		if peer.EntityType != string(peerType) || peer.SourceID != root.SourceID || (direction == "out" && e.From != root.ID) || (direction == "in" && e.To != root.ID) {
			return nil, ErrInventoryNotReady
		}
		newNodes := 0
		if _, ok := w.nodes[from.ID]; !ok {
			newNodes++
		}
		if _, ok := w.nodes[to.ID]; !ok && to.ID != from.ID {
			newNodes++
		}
		_, exists := w.edges[e.ID]
		if len(w.nodes)+newNodes > topologyContextNodes {
			w.warn("node_budget_exhausted", true)
			continue
		}
		if !exists && len(w.edges) >= topologyContextEdges {
			w.warn("edge_budget_exhausted", true)
			continue
		}
		w.nodes[from.ID], w.nodes[to.ID] = from, to
		w.edges[e.ID] = e
		peers[peer.ID] = peer
	}
	result := make([]repository.TopologyNode, 0, len(peers))
	for _, n := range peers {
		result = append(result, n)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(page.Edges) == 0 && !page.Truncated && missing != "" {
		w.warn(missing, false)
	}
	w.cache[read] = result
	return result, nil
}

func (w *topologyContextWalk) walk(device repository.TopologyNode) error {
	cabinets, err := w.neighbors(device, model.EdgeSpatial, model.RelationLocatedIn, model.EntityCabinet, "out", "missing_cabinet")
	if err != nil {
		return err
	}
	for _, cabinet := range cabinets {
		if _, err := w.neighbors(cabinet, model.EdgeSpatial, model.RelationLocatedIn, model.EntityDataCenter, "out", ""); err != nil {
			return err
		}
		rpps, err := w.neighbors(cabinet, model.EdgePower, model.RelationPowerUpstream, model.EntityRPP, "out", "missing_rpp")
		if err != nil {
			return err
		}
		for _, rpp := range rpps {
			groups, err := w.neighbors(rpp, model.EdgePower, model.RelationPowerUpstream, model.EntityUPSGroup, "out", "missing_ups_group")
			if err != nil {
				return err
			}
			for _, group := range groups {
				if _, err := w.neighbors(group, model.EdgePower, model.RelationPowerUpstream, model.EntityTransformer, "out", "missing_transformer"); err != nil {
					return err
				}
				if _, err := w.neighbors(group, model.EdgePower, model.RelationMemberOf, model.EntityUPS, "in", "missing_ups"); err != nil {
					return err
				}
			}
		}
	}
	if _, err := w.neighbors(device, model.EdgeSpatial, model.RelationMemberOf, model.EntityPod, "out", ""); err != nil {
		return err
	}
	gpus, err := w.neighbors(device, model.EdgeComposition, model.RelationContainsGPU, model.EntityGPU, "out", "")
	if err != nil {
		return err
	}
	for _, gpu := range gpus {
		ports, err := w.neighbors(gpu, model.EdgeNetwork, model.RelationGPUUplink, model.EntityInterface, "out", "")
		if err != nil {
			return err
		}
		if err := w.owners(ports); err != nil {
			return err
		}
	}
	ports, err := w.neighbors(device, model.EdgeNetwork, model.RelationServerUplink, model.EntityInterface, "out", "")
	if err != nil {
		return err
	}
	return w.owners(ports)
}

func (w *topologyContextWalk) owners(ports []repository.TopologyNode) error {
	for _, port := range ports {
		if _, err := w.neighbors(port, model.EdgeComposition, model.RelationOwnsInterface, model.EntityDevice, "in", ""); err != nil {
			return err
		}
	}
	return nil
}
