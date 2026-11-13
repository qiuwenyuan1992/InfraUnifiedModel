package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

const TopologySceneTimeout = 15 * time.Second
const topologySceneQueries = 128
const topologySceneNodes = 400
const topologySceneEdges = 600

type SceneNode struct {
	ID           string  `json:"id"`
	SourceID     string  `json:"source_id"`
	EntityType   string  `json:"entity_type"`
	StableID     string  `json:"stable_id"`
	Label        string  `json:"label"`
	Role         string  `json:"role"`
	ParentTypeID *int64  `json:"parent_type_id"`
	SyncedAt     *string `json:"synced_at"`
}
type SceneEdge struct {
	ID           string `json:"id"`
	SourceID     string `json:"source_id"`
	EdgeType     string `json:"edge_type"`
	RelationKind string `json:"relation_kind"`
	From         string `json:"from"`
	To           string `json:"to"`
	Rank         string `json:"rank"`
	PowerPath    string `json:"power_path,omitempty"`
	Plane        *int64 `json:"plane,omitempty"`
}
type SceneCabinet struct {
	Node      SceneNode   `json:"node"`
	Devices   []SceneNode `json:"devices"`
	NextAfter string      `json:"next_after"`
}
type SceneUnassigned struct {
	Node   SceneNode `json:"node"`
	Status string    `json:"status" enums:"missing,ambiguous,truncated"`
}
type SceneNetworkLevel struct {
	Role  string      `json:"role"`
	Level *int        `json:"level"`
	Nodes []SceneNode `json:"nodes"`
}
type SceneComponentOwner struct {
	ParentID string   `json:"parent_id"`
	ChildIDs []string `json:"child_ids"`
}

type TopologyScene struct {
	Root            SceneNode             `json:"root"`
	Cabinets        []SceneCabinet        `json:"cabinets"`
	Unassigned      []SceneUnassigned     `json:"unassigned"`
	NetworkLevels   []SceneNetworkLevel   `json:"network_levels"`
	ComponentOwners []SceneComponentOwner `json:"component_owners"`
	Nodes           []SceneNode           `json:"nodes"`
	Edges           []SceneEdge           `json:"edges"`
	NextAfter       string                `json:"next_after"`
	Truncated       bool                  `json:"truncated"`
	Warnings        []string              `json:"warnings"`
}

func NormalizeTopologySceneQuery(q repository.TopologyDirectoryQuery) (repository.TopologyDirectoryQuery, error) {
	if q.Limit == 0 {
		q.Limit = 5
	}
	if q.Limit < 1 || q.Limit > 10 || len(q.After) > 4096 {
		return q, ErrInventoryInvalid
	}
	return q, nil
}
func sceneInteger(v any) *int64 {
	var n int64
	switch value := v.(type) {
	case int64:
		n = value
	case int:
		n = int64(value)
	case float64:
		if value < -9223372036854775808 || value >= 9223372036854775808 || float64(int64(value)) != value {
			return nil
		}
		n = int64(value)
	default:
		return nil
	}
	return &n
}
func compactSceneNode(n repository.TopologyNode) SceneNode {
	role, _ := n.Properties["role"].(string)
	result := SceneNode{ID: n.ID, SourceID: n.SourceID, EntityType: n.EntityType, StableID: n.StableID, Label: n.Label, Role: role, ParentTypeID: sceneInteger(n.Properties["parent_type_id"])}
	if n.EntityType == string(model.EntityDataCenter) {
		cnName, _ := n.Properties["cn_name"].(string)
		if cnName = strings.TrimSpace(cnName); cnName != "" {
			result.Label = cnName
			if n.Label != "" && n.Label != cnName {
				result.Label += "（" + n.Label + "）"
			}
		}
	}
	if n.SyncedAt != nil {
		value := n.SyncedAt.Format(time.RFC3339Nano)
		result.SyncedAt = &value
	}
	return result
}
func compactSceneEdge(e repository.TopologyEdge) SceneEdge {
	path, _ := e.Properties["power_path"].(string)
	return SceneEdge{ID: e.ID, SourceID: e.SourceID, EdgeType: e.EdgeType, RelationKind: e.RelationKind, From: e.From, To: e.To, Rank: e.Rank, PowerPath: path, Plane: sceneInteger(e.Properties["plane"])}
}
func validSceneNode(n repository.TopologyNode, source string) bool {
	i := sceneIdentityOf(n)
	id, err := i.VID()
	return err == nil && n.SourceID == source && n.ID == id
}
func sceneIdentityOf(n repository.TopologyNode) model.EntityIdentity {
	return model.EntityIdentity{SourceID: n.SourceID, EntityType: model.EntityType(n.EntityType), StableID: n.StableID}
}

type sceneRead struct {
	page   *repository.TopologyNeighborhood
	err    error
	budget bool
}
type sceneWalk struct {
	service   *topologyBrowseService
	ctx       context.Context
	user      string
	source    string
	result    *TopologyScene
	nodes     map[string]repository.TopologyNode
	edges     map[string]repository.TopologyEdge
	warnings  map[string]bool
	exhausted atomic.Bool
}

func (w *sceneWalk) warn(message string) { w.warnings[message] = true; w.result.Truncated = true }
func (w *sceneWalk) read(n repository.TopologyNode, q repository.TopologyNeighborQuery) sceneRead {
	if err := w.ctx.Err(); err != nil {
		return sceneRead{err: err}
	}
	if w.exhausted.Load() {
		return sceneRead{budget: true}
	}
	p, err := w.service.repo.Neighbors(w.ctx, sceneIdentityOf(n), q)
	if w.ctx.Err() != nil {
		return sceneRead{err: w.ctx.Err()}
	}
	if errors.Is(err, repository.ErrTopologyQueryBudget) {
		w.exhausted.Store(true)
		return sceneRead{budget: true}
	}
	if err != nil {
		return sceneRead{err: topologyReadFailure(err)}
	}
	if p == nil {
		return sceneRead{err: ErrInventoryNotReady}
	}
	return sceneRead{page: p}
}
func spatialSceneQuery(peer model.EntityType) repository.TopologyNeighborQuery {
	return repository.TopologyNeighborQuery{Limit: 20, Direction: "out", EdgeType: model.EdgeSpatial, RelationKind: model.RelationLocatedIn, PeerEntityType: peer}
}

func (s *topologyBrowseService) Scene(ctx context.Context, user string, identity model.EntityIdentity, query repository.TopologyDirectoryQuery) (*TopologyScene, error) {
	if err := s.auth.authorize(user, "sources"); err != nil {
		return nil, err
	}
	if ValidateTopologyIdentity(identity) != nil || (identity.EntityType != model.EntityDataCenter && identity.EntityType != model.EntityRoom && identity.EntityType != model.EntityPod) {
		return nil, ErrInventoryInvalid
	}
	query, err := NormalizeTopologySceneQuery(query)
	if err != nil {
		return nil, err
	}
	if s.repo == nil || len(s.auth.cursorKey) < 32 {
		return nil, ErrInventoryNotReady
	}
	ctx, cancel := context.WithTimeout(ctx, TopologySceneTimeout)
	defer cancel()
	ctx = repository.WithTopologyNodeCache(repository.WithTopologyQueryBudget(ctx, topologySceneQueries))
	root, err := s.repo.Node(ctx, identity)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, topologyReadFailure(err)
	}
	if root == nil {
		return nil, ErrInventoryNotFound
	}
	if !validSceneNode(*root, identity.SourceID) || sceneIdentityOf(*root) != identity {
		return nil, ErrInventoryNotReady
	}
	w := &sceneWalk{service: s, ctx: ctx, user: user, source: identity.SourceID, result: &TopologyScene{Root: compactSceneNode(*root), Cabinets: []SceneCabinet{}, Unassigned: []SceneUnassigned{}, NetworkLevels: []SceneNetworkLevel{}, Nodes: []SceneNode{}, Edges: []SceneEdge{}, Warnings: []string{}}, nodes: map[string]repository.TopologyNode{root.ID: *root}, edges: map[string]repository.TopologyEdge{}, warnings: map[string]bool{}}
	page, err := w.children(*root, query)
	if err != nil {
		return nil, err
	}
	if page != nil {
		w.result.NextAfter = page.NextAfter
		var devices []repository.TopologyNode
		if identity.EntityType == model.EntityDataCenter {
			devices, err = w.dataCenterRooms(*root, page.Items, query.Limit)
		} else if identity.EntityType == model.EntityRoom {
			devices, err = w.dataCenter(*root, page.Items)
		} else {
			devices = page.Items
		}
		if err != nil {
			return nil, err
		}
		if err = w.devices(devices, identity.EntityType != model.EntityPod); err != nil {
			return nil, err
		}
		if err = w.network(devices); err != nil {
			return nil, err
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	w.finish()
	return w.result, nil
}

func (w *sceneWalk) children(n repository.TopologyNode, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	if w.exhausted.Load() {
		w.warn("query_budget_exhausted")
		return nil, nil
	}
	p, err := w.service.Children(w.ctx, w.user, sceneIdentityOf(n), q)
	// directory 将仓储预算错误映射为 503，因此直接检查请求预算错误由调用包装保留。
	if errors.Is(err, repository.ErrTopologyQueryBudget) {
		w.exhausted.Store(true)
		w.warn("query_budget_exhausted")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p == nil || len(p.Items) > q.Limit {
		return nil, ErrInventoryNotReady
	}
	expected := model.EntityDevice
	if n.EntityType == string(model.EntityDataCenter) {
		expected = model.EntityRoom
	} else if n.EntityType == string(model.EntityRoom) {
		expected = model.EntityCabinet
	}
	for _, child := range p.Items {
		if !validSceneNode(child, w.source) || child.EntityType != string(expected) {
			return nil, ErrInventoryNotReady
		}
	}
	if p.NextAfter != "" {
		w.warn("directory_truncated")
	}
	return p, nil
}

func (w *sceneWalk) addNode(n repository.TopologyNode) bool {
	if _, ok := w.nodes[n.ID]; ok {
		return true
	}
	if len(w.nodes) >= topologySceneNodes {
		w.warn("node_budget_exhausted")
		return false
	}
	w.nodes[n.ID] = n
	return true
}

// 只接受查询约束内的真实边；节点与边一起纳入预算，避免悬空端点。
func (w *sceneWalk) merge(root repository.TopologyNode, q repository.TopologyNeighborQuery, r sceneRead) ([]repository.TopologyNode, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	if r.budget {
		w.warn("query_budget_exhausted")
		return nil, true, nil
	}
	p := r.page
	if p.Root != nil && (!validSceneNode(*p.Root, w.source) || p.Root.ID != root.ID) {
		return nil, false, ErrInventoryNotReady
	}
	nodes := map[string]repository.TopologyNode{root.ID: root}
	for _, n := range p.Nodes {
		if !validSceneNode(n, w.source) {
			return nil, false, ErrInventoryNotReady
		}
		nodes[n.ID] = n
	}
	incomplete := p.Truncated
	if p.Truncated {
		w.warn("step_truncated")
	}
	peers := map[string]repository.TopologyNode{}
	if len(p.Edges) > q.Limit {
		return nil, false, ErrInventoryNotReady
	}
	for _, e := range p.Edges {
		from, fok := nodes[e.From]
		to, tok := nodes[e.To]
		if !fok || !tok || e.ID == "" || e.SourceID != w.source || e.EdgeType != string(q.EdgeType) || (q.RelationKind != "" && e.RelationKind != string(q.RelationKind)) {
			return nil, false, ErrInventoryNotReady
		}
		peer := to
		if q.Direction == "both" {
			if e.From == root.ID {
				peer = to
			} else if e.To == root.ID {
				peer = from
			} else {
				return nil, false, ErrInventoryNotReady
			}
		} else if q.Direction == "in" {
			peer = from
			if e.To != root.ID {
				return nil, false, ErrInventoryNotReady
			}
		} else if e.From != root.ID {
			return nil, false, ErrInventoryNotReady
		}
		if q.PeerEntityType != "" && peer.EntityType != string(q.PeerEntityType) {
			return nil, false, ErrInventoryNotReady
		}
		if !(model.RelationSpec{EdgeType: model.EdgeType(e.EdgeType), Kind: model.RelationKind(e.RelationKind), From: model.EntityType(from.EntityType), To: model.EntityType(to.EntityType)}).Valid() {
			return nil, false, ErrInventoryNotReady
		}
		newNodes := 0
		if _, ok := w.nodes[from.ID]; !ok {
			newNodes++
		}
		if _, ok := w.nodes[to.ID]; !ok && to.ID != from.ID {
			newNodes++
		}
		_, exists := w.edges[e.ID]
		if len(w.nodes)+newNodes > topologySceneNodes {
			w.warn("node_budget_exhausted")
			incomplete = true
			continue
		}
		if !exists && len(w.edges) >= topologySceneEdges {
			w.warn("edge_budget_exhausted")
			incomplete = true
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
	return result, incomplete, nil
}

func (w *sceneWalk) unassigned(n repository.TopologyNode, status string) {
	if !w.addNode(n) {
		return
	}
	for i, entry := range w.result.Unassigned {
		if entry.Node.ID == n.ID {
			if status == "truncated" {
				w.result.Unassigned[i].Status = status
			}
			return
		}
	}
	w.result.Unassigned = append(w.result.Unassigned, SceneUnassigned{Node: compactSceneNode(n), Status: status})
}
func sceneParentStatus(peers []repository.TopologyNode, incomplete bool) string {
	if incomplete {
		return "truncated"
	}
	if len(peers) == 0 {
		return "missing"
	}
	if len(peers) > 1 {
		return "ambiguous"
	}
	return ""
}
func (w *sceneWalk) cabinet(n repository.TopologyNode) int {
	for i, c := range w.result.Cabinets {
		if c.Node.ID == n.ID {
			return i
		}
	}
	if !w.addNode(n) {
		return -1
	}
	w.result.Cabinets = append(w.result.Cabinets, SceneCabinet{Node: compactSceneNode(n), Devices: []SceneNode{}})
	return len(w.result.Cabinets) - 1
}
func (w *sceneWalk) dataCenterRooms(root repository.TopologyNode, rooms []repository.TopologyNode, cabinetLimit int) ([]repository.TopologyNode, error) {
	devices := []repository.TopologyNode{}
	remaining := cabinetLimit
	for _, room := range rooms {
		q := spatialSceneQuery(model.EntityDataCenter)
		parents, incomplete, err := w.merge(room, q, w.read(room, q))
		if err != nil {
			return nil, err
		}
		status := sceneParentStatus(parents, incomplete)
		if status == "" && parents[0].ID != root.ID {
			status = "missing"
			w.warnings["parent_outside_scope"] = true
		}
		if status != "" {
			w.unassigned(room, status)
			continue
		}
		if remaining == 0 {
			w.warn("cabinet_budget_exhausted")
			continue
		}
		page, err := w.children(room, repository.TopologyDirectoryQuery{Limit: min(5, remaining)})
		if err != nil {
			return nil, err
		}
		if page == nil {
			continue
		}
		remaining -= len(page.Items)
		members, err := w.dataCenter(room, page.Items)
		if err != nil {
			return nil, err
		}
		devices = append(devices, members...)
	}
	return devices, nil
}

func (w *sceneWalk) dataCenter(root repository.TopologyNode, cabinets []repository.TopologyNode) ([]repository.TopologyNode, error) {
	devices := []repository.TopologyNode{}
	for _, cabinet := range cabinets {
		q := spatialSceneQuery(model.EntityType(root.EntityType))
		parents, incomplete, err := w.merge(cabinet, q, w.read(cabinet, q))
		if err != nil {
			return nil, err
		}
		status := sceneParentStatus(parents, incomplete)
		if status == "" && parents[0].ID != root.ID {
			status = "missing"
			w.warnings["parent_outside_scope"] = true
		}
		if status != "" {
			w.unassigned(cabinet, status)
			continue
		}
		index := w.cabinet(cabinet)
		if index < 0 {
			continue
		}
		page, err := w.children(cabinet, repository.TopologyDirectoryQuery{Limit: 20})
		if err != nil {
			return nil, err
		}
		if page == nil {
			continue
		}
		w.result.Cabinets[index].NextAfter = page.NextAfter
		devices = append(devices, page.Items...)
	}
	return devices, nil
}

func (w *sceneWalk) batch(nodes []repository.TopologyNode, q repository.TopologyNeighborQuery) []sceneRead {
	reads := make([]sceneRead, len(nodes))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				reads[index] = w.read(nodes[index], q)
			}
		}()
	}
	for index := range nodes {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return reads
}

func (w *sceneWalk) devices(devices []repository.TopologyNode, dc bool) error {
	reads := w.batch(devices, spatialSceneQuery(model.EntityCabinet))
	seen := map[string]bool{}
	for index, device := range devices {
		if seen[device.ID] {
			continue
		}
		seen[device.ID] = true
		if !w.addNode(device) {
			continue
		}
		parents, incomplete, err := w.merge(device, spatialSceneQuery(model.EntityCabinet), reads[index])
		if err != nil {
			return err
		}
		status := sceneParentStatus(parents, incomplete)
		if status != "" {
			w.unassigned(device, status)
			continue
		}
		cabinetIndex := -1
		for i, c := range w.result.Cabinets {
			if c.Node.ID == parents[0].ID {
				cabinetIndex = i
				break
			}
		}
		if cabinetIndex < 0 && !dc {
			cabinetIndex = w.cabinet(parents[0])
		}
		if cabinetIndex < 0 {
			w.unassigned(device, "missing")
			w.warnings["parent_outside_scope"] = true
			continue
		}
		w.result.Cabinets[cabinetIndex].Devices = append(w.result.Cabinets[cabinetIndex].Devices, compactSceneNode(device))
	}
	return nil
}

func (w *sceneWalk) network(devices []repository.TopologyNode) error {
	seen := map[string]bool{}
	visible := []repository.TopologyNode{}
	for _, device := range devices {
		if !seen[device.ID] {
			seen[device.ID] = true
			if _, shown := w.nodes[device.ID]; shown {
				visible = append(visible, device)
			}
		}
	}
	return w.networkBatch(visible)
}

func (w *sceneWalk) networkBatch(seeds []repository.TopologyNode) error {
	if len(seeds) == 0 {
		return nil
	}
	if w.exhausted.Load() {
		w.warn("query_budget_exhausted")
		return nil
	}
	page, err := w.service.repo.SceneNetwork(w.ctx, seeds, topologySceneEdges)
	if w.ctx.Err() != nil {
		return w.ctx.Err()
	}
	if errors.Is(err, repository.ErrTopologyQueryBudget) {
		w.exhausted.Store(true)
		w.warn("query_budget_exhausted")
		return nil
	}
	if err != nil {
		return topologyReadFailure(err)
	}
	if page == nil || page.Root != nil || len(page.Edges) > topologySceneEdges || len(page.Nodes) > len(seeds)+2*topologySceneEdges {
		return ErrInventoryNotReady
	}
	nodes := map[string]repository.TopologyNode{}
	selected := map[string]bool{}
	for _, seed := range seeds {
		selected[seed.ID] = true
		nodes[seed.ID] = seed
	}
	for _, node := range page.Nodes {
		if !validSceneNode(node, w.source) {
			return ErrInventoryNotReady
		}
		nodes[node.ID] = node
	}
	initial := map[string]bool{}
	ports := map[string]bool{}
	stages := [3][]repository.TopologyEdge{}
	seen := map[string]bool{}
	for _, edge := range page.Edges {
		from, fok := nodes[edge.From]
		to, tok := nodes[edge.To]
		if !fok || !tok || edge.ID == "" || seen[edge.ID] || edge.SourceID != w.source || !(model.RelationSpec{EdgeType: model.EdgeType(edge.EdgeType), Kind: model.RelationKind(edge.RelationKind), From: model.EntityType(from.EntityType), To: model.EntityType(to.EntityType)}).Valid() {
			return ErrInventoryNotReady
		}
		seen[edge.ID] = true
		if selected[edge.From] && (edge.RelationKind == string(model.RelationOwnsInterface) || edge.RelationKind == string(model.RelationServerUplink)) {
			initial[edge.To], ports[edge.To] = true, true
			stages[0] = append(stages[0], edge)
		}
	}
	for _, edge := range page.Edges {
		if edge.RelationKind == string(model.RelationLinksTo) && (initial[edge.From] || initial[edge.To]) {
			ports[edge.From], ports[edge.To] = true, true
			stages[1] = append(stages[1], edge)
		} else if edge.RelationKind == string(model.RelationLinksTo) {
			return ErrInventoryNotReady
		}
	}
	for _, edge := range page.Edges {
		if edge.RelationKind == string(model.RelationOwnsInterface) && ports[edge.To] {
			if !selected[edge.From] {
				stages[2] = append(stages[2], edge)
			}
		} else if edge.RelationKind != string(model.RelationLinksTo) && !(edge.RelationKind == string(model.RelationServerUplink) && selected[edge.From]) {
			return ErrInventoryNotReady
		}
	}
	incomplete := page.Truncated
	if incomplete {
		w.warn("step_truncated")
	}
	for stage, edges := range stages {
		for _, edge := range edges {
			root := nodes[edge.From]
			query := repository.TopologyNeighborQuery{Limit: topologySceneEdges, Direction: "out", EdgeType: model.EdgeType(edge.EdgeType), RelationKind: model.RelationKind(edge.RelationKind), PeerEntityType: model.EntityInterface}
			if stage == 1 {
				query.Direction = "both"
				if !initial[root.ID] {
					root = nodes[edge.To]
				}
			}
			if stage == 2 {
				root = nodes[edge.To]
				query.Direction, query.PeerEntityType = "in", model.EntityDevice
			}
			_, truncated, err := w.merge(root, query, sceneRead{page: &repository.TopologyNeighborhood{Nodes: []repository.TopologyNode{nodes[edge.From], nodes[edge.To]}, Edges: []repository.TopologyEdge{edge}}})
			if err != nil {
				return err
			}
			incomplete = incomplete || truncated
		}
	}
	owners := map[string]map[string]bool{}
	for _, edge := range w.edges {
		if edge.EdgeType == string(model.EdgeComposition) && edge.RelationKind == string(model.RelationOwnsInterface) && ports[edge.To] {
			if owners[edge.To] == nil {
				owners[edge.To] = map[string]bool{}
			}
			owners[edge.To][edge.From] = true
		}
	}
	ids := make([]string, 0, len(ports))
	for id := range ports {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if port, shown := w.nodes[id]; shown {
			status := ""
			switch {
			case incomplete:
				status = "truncated"
			case len(owners[id]) == 0:
				status = "missing"
			case len(owners[id]) > 1:
				status = "ambiguous"
			}
			if status != "" {
				w.unassigned(port, status)
			}
		}
	}
	return nil
}

func (w *sceneWalk) finish() {
	// 组件归属在后端统一解析；不完整或多父关系只保留诊断和原始边。
	w.result.ComponentOwners = []SceneComponentOwner{}
	unresolved := map[string]bool{}
	for _, item := range w.result.Unassigned {
		unresolved[item.Node.ID] = true
	}
	parents := map[string]map[string]bool{}
	for _, edge := range w.edges {
		if edge.EdgeType != string(model.EdgeComposition) || (edge.RelationKind != string(model.RelationOwnsInterface) && edge.RelationKind != string(model.RelationContainsGPU)) {
			continue
		}
		if parents[edge.To] == nil {
			parents[edge.To] = map[string]bool{}
		}
		parents[edge.To][edge.From] = true
	}
	children := map[string][]string{}
	for child, owners := range parents {
		if unresolved[child] || len(owners) != 1 {
			continue
		}
		for parent := range owners {
			children[parent] = append(children[parent], child)
		}
	}
	for parent, ids := range children {
		sort.Strings(ids)
		w.result.ComponentOwners = append(w.result.ComponentOwners, SceneComponentOwner{ParentID: parent, ChildIDs: ids})
	}
	sort.Slice(w.result.ComponentOwners, func(i, j int) bool {
		return w.result.ComponentOwners[i].ParentID < w.result.ComponentOwners[j].ParentID
	})
	for _, n := range w.nodes {
		w.result.Nodes = append(w.result.Nodes, compactSceneNode(n))
	}
	for _, e := range w.edges {
		w.result.Edges = append(w.result.Edges, compactSceneEdge(e))
	}
	sort.Slice(w.result.Nodes, func(i, j int) bool { return w.result.Nodes[i].ID < w.result.Nodes[j].ID })
	sort.Slice(w.result.Edges, func(i, j int) bool { return w.result.Edges[i].ID < w.result.Edges[j].ID })
	levels := map[string][]SceneNode{}
	for _, n := range w.result.Nodes {
		if n.EntityType == string(model.EntityDevice) && n.ParentTypeID != nil && *n.ParentTypeID == 54 {
			levels[n.Role] = append(levels[n.Role], n)
		}
	}
	roles := make([]string, 0, len(levels))
	for role := range levels {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		var level *int
		switch role {
		case "T0":
			v := 0
			level = &v
		case "T1":
			v := 1
			level = &v
		case "T2":
			v := 2
			level = &v
		}
		w.result.NetworkLevels = append(w.result.NetworkLevels, SceneNetworkLevel{Role: role, Level: level, Nodes: levels[role]})
	}
	for message := range w.warnings {
		w.result.Warnings = append(w.result.Warnings, message)
	}
	sort.Strings(w.result.Warnings)
}
