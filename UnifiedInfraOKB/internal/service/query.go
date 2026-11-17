// query.go S1 查询原语：lookup / trace / evidence。
// trace 为 BFS 遍历 + 受控条件求值（passed/failed/unknown）；
// 所有响应钉住 revision_id（F5）。fragment 级权限本期不启用（后置 S3）。
package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
)

// LookupResult lookup 原语输出：告警 → 候选异常。
type LookupResult struct {
	AlertSignature string            `json:"alert_signature"`
	Candidates     []LookupCandidate `json:"candidates"`
}

// LookupCandidate 单个候选异常。
type LookupCandidate struct {
	AnomalyID      string              `json:"anomaly_id"`
	StableCode     string              `json:"stable_code"`
	Name           string              `json:"name"`
	Confidence     float64             `json:"confidence"`
	EvidenceLevel  model.EvidenceLevel `json:"evidence_level"`
	RevisionID     string              `json:"revision_id"`
	Status         model.KnowledgeStatus `json:"status"`
}

// TraceResult trace 原语输出：候选因果链集合。
type TraceResult struct {
	StartAnomalyID string        `json:"start_anomaly_id"`
	MaxHops        int           `json:"max_hops"`
	Chains         []TraceChain  `json:"chains"`
}

// TraceChain 一条候选根因链（多跳）。
type TraceChain struct {
	Hops                   []TraceHop `json:"hops"`
	FullyCurated           bool        `json:"fully_curated"` // 逐跳全部 curated
	MinConfidence          float64     `json:"min_confidence"`
	CombinedNote           string      `json:"combined_note,omitempty"` // 组合推导标注
}

// TraceHop 一跳：边 + 条件求值结果。
type TraceHop struct {
	EdgeID        string                 `json:"edge_id"`
	TargetID      string                 `json:"target_anomaly_id"`
	TargetCode    string                 `json:"target_stable_code"`
	TargetName    string                 `json:"target_name"`
	Conditions    []model.ConditionClause `json:"conditions,omitempty"`
	Evaluations   []ConditionEval         `json:"evaluations,omitempty"`
	TimeConstraints string                `json:"time_constraints,omitempty"`
	EvidenceLevel model.EvidenceLevel     `json:"evidence_level"`
	Confidence    float64                 `json:"confidence"`
	Status        model.KnowledgeStatus  `json:"status"`
	RevisionID    string                  `json:"revision_id"`
}

// ConditionEval 单条条件求值：passed/failed/unknown。
// unknown 的条件作为待验证检查项交给调用方取证（evidence-driven）。
type ConditionEval struct {
	FactKey string `json:"fact_key"`
	Op      string `json:"op"`
	Result  string `json:"result"` // passed / failed / unknown
}

// EvidenceView evidence 原语输出：单条知识的证据回链。
type EvidenceView struct {
	EntityType  model.EntityType `json:"entity_type"`
	EntityID    string           `json:"entity_id"`
	RevisionID  string           `json:"revision_id"`
	Items        []EvidenceItem  `json:"items"`
}

// EvidenceItem 单条证据。
type EvidenceItem struct {
	EvidenceID   string              `json:"evidence_id"`
	Kind         model.EvidenceKind `json:"kind"`
	Level        model.EvidenceLevel `json:"level"`
	FragmentID   string              `json:"fragment_id"`
	Summary      string              `json:"summary"` // 摘要：始终返回
	Content      string              `json:"content"` // 原文：权限分级后置 S3，本期直返
	SourceKind   model.SourceKind    `json:"source_kind"`
	CreatedBy    model.CreatorKind   `json:"created_by"`
}

// GraphView 整图视图：全部节点与边（图谱可视化消费）。
type GraphView struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode 可视化节点。
type GraphNode struct {
	ID           string             `json:"id"`
	StableCode   string             `json:"stable_code"`
	Name         string             `json:"name"`
	Category     string             `json:"category"`
	Status       model.KnowledgeStatus `json:"status"`
	RevisionID   string             `json:"revision_id"`
}

// GraphEdge 可视化边（源/目标用 stable_code，便于前端直连）。
type GraphEdge struct {
	ID            string               `json:"id"`
	Source        string               `json:"source"`
	Target        string               `json:"target"`
	EvidenceLevel model.EvidenceLevel  `json:"evidence_level"`
	Confidence    float64              `json:"confidence"`
	Status        model.KnowledgeStatus `json:"status"`
	ReviewFlagged bool                 `json:"review_flagged"`
	CaseSupport   int                  `json:"case_support_count"`
}

// Graph 返回整图（节点 + 边），供前端力导向可视化。
func (s *Service) Graph() (*GraphView, error) {
	nodes, err := s.store.ListAllAnomalies(0)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	rules, err := s.store.ListAllCausalRules(0)
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}
	codeByID := make(map[string]string, len(nodes))
	view := &GraphView{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	for _, n := range nodes {
		codeByID[n.ID] = n.StableCode
		gn := GraphNode{ID: n.ID, StableCode: n.StableCode, Name: n.Name, Category: n.Category, Status: n.Status}
		if rev, err := s.store.LatestRevision(model.EntityAnomalyType, n.ID); err == nil {
			gn.RevisionID = rev.ID
		}
		view.Nodes = append(view.Nodes, gn)
	}
	for _, r := range rules {
		src, ok1 := codeByID[r.SourceAnomalyID]
		dst, ok2 := codeByID[r.TargetAnomalyID]
		if !ok1 || !ok2 {
			continue // 孤儿边跳过
		}
		view.Edges = append(view.Edges, GraphEdge{
			ID: r.ID, Source: src, Target: dst,
			EvidenceLevel: r.EvidenceLevel, Confidence: r.Confidence,
			Status: r.Status, ReviewFlagged: r.ReviewFlagged, CaseSupport: r.CaseSupportCount,
		})
	}
	return view, nil
}

// KnowledgeRow 知识清单行（三类统一视图，字段对齐展示需要）。
type KnowledgeRow struct {
	ID            string               `json:"id"`
	Kind          string               `json:"kind"` // anomaly / rule / mapping
	// anomaly
	StableCode    string               `json:"stable_code,omitempty"`
	Name          string               `json:"name,omitempty"`
	Category      string               `json:"category,omitempty"`
	// rule
	SourceCode    string               `json:"source_code,omitempty"`
	TargetCode    string               `json:"target_code,omitempty"`
	// mapping
	AlertSignature string              `json:"alert_signature,omitempty"`
	// 通用
	EvidenceLevel model.EvidenceLevel  `json:"evidence_level,omitempty"`
	Confidence    float64              `json:"confidence,omitempty"`
	CaseSupport   int                  `json:"case_support_count,omitempty"`
	Status        model.KnowledgeStatus `json:"status"`
	ReviewFlagged bool                 `json:"review_flagged,omitempty"`
	RevisionID    string               `json:"revision_id"`
	UpdatedAt     string               `json:"updated_at"`
}

// KnowledgeList 知识清单查询结果。
type KnowledgeList struct {
	Kind  string         `json:"kind"`
	Total int64          `json:"total"`
	Rows  []KnowledgeRow `json:"rows"`
}

// Knowledge 按类型分页列出知识（知识清单入口）。
// kind: anomaly / rule / mapping；status、q 可选；一次查一类，前端分区各取一次。
func (s *Service) Knowledge(kind string, status model.KnowledgeStatus, q string, limit, offset int) (*KnowledgeList, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	out := &KnowledgeList{Kind: kind, Rows: []KnowledgeRow{}}

	switch kind {
	case "anomaly":
		list, total, err := s.store.ListAnomaliesPage(status, q, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("list anomalies: %w", err)
		}
		out.Total = total
		for _, n := range list {
			row := KnowledgeRow{ID: n.ID, Kind: kind, StableCode: n.StableCode, Name: n.Name,
				Category: n.Category, Status: n.Status, UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339)}
			if rev, err := s.store.LatestRevision(model.EntityAnomalyType, n.ID); err == nil {
				row.RevisionID = rev.ID
			}
			out.Rows = append(out.Rows, row)
		}
	case "rule":
		list, total, err := s.store.ListRulesPage(status, q, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("list rules: %w", err)
		}
		out.Total = total
		rows, err := s.ruleRows(list)
		if err != nil {
			return nil, err
		}
		out.Rows = rows
	case "mapping":
		list, total, err := s.store.ListMappingsPage(status, q, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("list mappings: %w", err)
		}
		out.Total = total
		nodes, err := s.store.ListAllAnomalies(0)
		if err != nil {
			return nil, fmt.Errorf("list nodes: %w", err)
		}
		byID := make(map[string]model.AnomalyType, len(nodes))
		for _, n := range nodes {
			byID[n.ID] = n
		}
		for _, m := range list {
			row := KnowledgeRow{ID: m.ID, Kind: kind, AlertSignature: m.AlertSignature,
				Confidence: m.Confidence, EvidenceLevel: m.EvidenceLevel, Status: m.Status,
				UpdatedAt: m.UpdatedAt.UTC().Format(time.RFC3339)}
			if n, ok := byID[m.AnomalyID]; ok {
				row.StableCode = n.StableCode
				row.Name = n.Name
			}
			if rev, err := s.store.LatestRevision(model.EntityAlertMapping, m.ID); err == nil {
				row.RevisionID = rev.ID
			}
			out.Rows = append(out.Rows, row)
		}
	default:
		return nil, fmt.Errorf("unknown kind %q (expect anomaly/rule/mapping)", kind)
	}
	return out, nil
}

// ruleRows 因果边 → 清单行；知识清单与复核队列共用（同构输出，字段 snake_case）。
func (s *Service) ruleRows(rules []model.CausalRule) ([]KnowledgeRow, error) {
	// id→code 映射（节点规模小，全量取一次）
	nodes, err := s.store.ListAllAnomalies(0)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	codeByID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		codeByID[n.ID] = n.StableCode
	}
	rows := make([]KnowledgeRow, 0, len(rules))
	for _, r := range rules {
		row := KnowledgeRow{ID: r.ID, Kind: "rule",
			SourceCode: codeByID[r.SourceAnomalyID], TargetCode: codeByID[r.TargetAnomalyID],
			EvidenceLevel: r.EvidenceLevel, Confidence: r.Confidence, CaseSupport: r.CaseSupportCount,
			Status: r.Status, ReviewFlagged: r.ReviewFlagged,
			UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339)}
		if rev, err := s.store.LatestRevision(model.EntityCausalRule, r.ID); err == nil {
			row.RevisionID = rev.ID
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Lookup 告警入口：signature → 候选异常。draft 默认排除，includeDraft 须审核权限（由 handler 校验）。
func (s *Service) Lookup(alertSignature string, includeDraft bool) (*LookupResult, error) {
	status := model.StatusCurated
	if includeDraft {
		status = ""
	}
	mappings, err := s.store.ListMappingsByAlert(alertSignature, status)
	if err != nil {
		return nil, fmt.Errorf("list mappings: %w", err)
	}
	res := &LookupResult{AlertSignature: alertSignature, Candidates: []LookupCandidate{}}
	for _, m := range mappings {
		node, err := s.store.GetAnomalyByID(m.AnomalyID)
		if err != nil {
			continue // 孤儿映射跳过，不中断查询
		}
		revID := ""
		if rev, err := s.store.LatestRevision(model.EntityAlertMapping, m.ID); err == nil {
			revID = rev.ID
		}
		res.Candidates = append(res.Candidates, LookupCandidate{
			AnomalyID: node.ID, StableCode: node.StableCode, Name: node.Name,
			Confidence: m.Confidence, EvidenceLevel: m.EvidenceLevel,
			RevisionID: revID, Status: m.Status,
		})
	}
	// 置信度降序，输出有界
	sort.Slice(res.Candidates, func(i, j int) bool {
		return res.Candidates[i].Confidence > res.Candidates[j].Confidence
	})
	return res, nil
}

// Trace 候选根因链遍历：从起始异常沿出边 BFS，条件求值，输出 top-k 链。
// facts 为调用方已知观测；求值失败的边不可走（防止可能性被放大成必然性）。
func (s *Service) Trace(startCode string, maxHops, topK int, facts map[string]interface{}, includeDraft bool) (*TraceResult, error) {
	if maxHops <= 0 || maxHops > s.cfg.MaxHops {
		maxHops = s.cfg.MaxHops
	}
	if topK <= 0 || topK > 20 {
		topK = s.cfg.TopK
	}
	start, err := s.store.GetAnomalyByCode(startCode)
	if err != nil {
		return nil, fmt.Errorf("start anomaly: %w", err)
	}
	status := model.StatusCurated
	if includeDraft {
		status = ""
	}

	res := &TraceResult{StartAnomalyID: start.ID, MaxHops: maxHops, Chains: []TraceChain{}}
	// DFS 收集所有 ≤maxHops 的链（环检测：同链不重复访节点）
	var walk func(nodeID string, hops []TraceHop, visited map[string]bool)
	walk = func(nodeID string, hops []TraceHop, visited map[string]bool) {
		if len(hops) >= maxHops {
			return
		}
		edges, err := s.store.ListEdgesFrom(nodeID, status)
		if err != nil {
			return
		}
		for _, e := range edges {
			if visited[e.TargetAnomalyID] {
				continue // 环：跳过
			}
			hop, ok := s.buildHop(e, facts)
			if !ok {
				continue // 条件 failed 的边不可走
			}
			next := append(hops, hop)
			chain := TraceChain{Hops: append([]TraceHop(nil), next...)}
			chain.FullyCurated = true
			chain.MinConfidence = 1.0
			for _, h := range chain.Hops {
				if h.Status != model.StatusCurated {
					chain.FullyCurated = false
				}
				if h.Confidence < chain.MinConfidence {
					chain.MinConfidence = h.Confidence
				}
			}
			if !chain.FullyCurated {
				chain.CombinedNote = "组合推导，未逐链审核"
			}
			res.Chains = append(res.Chains, chain)
			nv := make(map[string]bool, len(visited)+1)
			for k := range visited {
				nv[k] = true
			}
			nv[e.TargetAnomalyID] = true
			walk(e.TargetAnomalyID, next, nv)
		}
	}
	seed := map[string]bool{start.ID: true}
	walk(start.ID, []TraceHop{}, seed)

	// 排序：全 curated 优先，再按最小置信度降序；截断 top-k
	sort.SliceStable(res.Chains, func(i, j int) bool {
		if res.Chains[i].FullyCurated != res.Chains[j].FullyCurated {
			return res.Chains[i].FullyCurated
		}
		if len(res.Chains[i].Hops) != len(res.Chains[j].Hops) {
			return len(res.Chains[i].Hops) < len(res.Chains[j].Hops) // 短链（更直接根因）优先
		}
		return res.Chains[i].MinConfidence > res.Chains[j].MinConfidence
	})
	if len(res.Chains) > topK {
		res.Chains = res.Chains[:topK]
	}
	return res, nil
}

// buildHop 构造一跳并求值条件；返回 ok=false 表示边不可走（条件 failed）。
func (s *Service) buildHop(e model.CausalRule, facts map[string]interface{}) (TraceHop, bool) {
	hop := TraceHop{
		EdgeID: e.ID, TargetID: e.TargetAnomalyID,
		TimeConstraints: e.TimeConstraints,
		EvidenceLevel: e.EvidenceLevel, Confidence: e.Confidence,
		Status: e.Status,
	}
	if node, err := s.store.GetAnomalyByID(e.TargetAnomalyID); err == nil {
		hop.TargetCode, hop.TargetName = node.StableCode, node.Name
	}
	if rev, err := s.store.LatestRevision(model.EntityCausalRule, e.ID); err == nil {
		hop.RevisionID = rev.ID
	}
	if e.Conditions != "" {
		var clauses []model.ConditionClause
		if err := json.Unmarshal([]byte(e.Conditions), &clauses); err == nil {
			hop.Conditions = clauses
			hop.Evaluations = make([]ConditionEval, len(clauses))
			for i, c := range clauses {
				hop.Evaluations[i] = ConditionEval{FactKey: c.FactKey, Op: c.Op}
				switch evalCondition(c, facts) {
				case evalFailed:
					return hop, false // 条件不满足：边不可走
				case evalPassed:
					hop.Evaluations[i].Result = "passed"
				default:
					hop.Evaluations[i].Result = "unknown" // 待验证检查项
				}
			}
		}
	}
	return hop, true
}

const (
	evalUnknown = iota
	evalPassed
	evalFailed
)

// evalCondition 受控条件求值：只支持受控算符，不执行任意脚本。
func evalCondition(c model.ConditionClause, facts map[string]interface{}) int {
	v, ok := facts[c.FactKey]
	if !ok || v == nil {
		return evalUnknown
	}
	switch c.Op {
	case "exists":
		return evalPassed
	case "eq":
		if fmt.Sprintf("%v", v) == fmt.Sprintf("%v", c.Value) {
			return evalPassed
		}
		return evalFailed
	case "ne":
		if fmt.Sprintf("%v", v) != fmt.Sprintf("%v", c.Value) {
			return evalPassed
		}
		return evalFailed
	case "gt", "lt", "gte", "lte":
		f, ok1 := toFloat(v)
		cv, ok2 := toFloat(c.Value)
		if !ok1 || !ok2 {
			return evalUnknown
		}
		switch c.Op {
		case "gt":
			return boolToEval(f > cv)
		case "lt":
			return boolToEval(f < cv)
		case "gte":
			return boolToEval(f >= cv)
		case "lte":
			return boolToEval(f <= cv)
		}
	}
	return evalUnknown
}

func boolToEval(b bool) int {
	if b {
		return evalPassed
	}
	return evalFailed
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// Evidence 出处回链：实体的全部证据。权限分级本期不启用（2026-11-16 决定后置 S3），原文直返。
// entityRef 接受异常 stable_code 或边 ID。
func (s *Service) Evidence(entityRef string) (*EvidenceView, error) {
	// 先按边 ID，再按异常 stable_code 解析
	et := model.EntityCausalRule
	var entityID string
	if rule, err := s.store.GetCausalRule(entityRef); err == nil {
		entityID = rule.ID
	} else if node, err := s.store.GetAnomalyByCode(entityRef); err == nil {
		et, entityID = model.EntityAnomalyType, node.ID
	} else if !strings.Contains(entityRef, "/") && strings.HasPrefix(entityRef, "map_") {
		et = model.EntityAlertMapping
		entityID = strings.TrimPrefix(entityRef, "map_")
	} else {
		return nil, repository.ErrNotFound
	}
	evList, err := s.store.ListEvidenceByEntity(et, entityID)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	view := &EvidenceView{EntityType: et, EntityID: entityID, Items: []EvidenceItem{}}
	if rev, err := s.store.LatestRevision(et, entityID); err == nil {
		view.RevisionID = rev.ID
	}
	for _, ev := range evList {
		item := EvidenceItem{
			EvidenceID: ev.ID, Kind: ev.Kind, FragmentID: ev.FragmentID,
			CreatedBy: ev.CreatedBy,
		}
		switch ev.Kind {
		case model.EvidenceKindCase:
			item.Level = model.EvidenceCaseVerified
		case model.EvidenceKindDoc:
			item.Level = model.EvidenceDocExplicit
		case model.EvidenceKindExpert:
			item.Level = model.EvidenceExpertOnly
		default:
			item.Level = model.EvidenceInferred
		}
		if src, err := s.store.GetSource(ev.SourceID); err == nil {
			item.SourceKind = src.Kind
		}
		if frag, err := s.store.GetFragment(ev.FragmentID); err == nil {
			item.Summary = frag.Summary
			item.Content = frag.Content // 权限分级后置：原文直返
		}
		view.Items = append(view.Items, item)
	}
	return view, nil
}
