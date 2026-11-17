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

// MitigationRow 处置建议行。
type MitigationRow struct {
	MitigationCode string              `json:"mitigation_code"` // 处置手段（边源，如"更换光模块"）
	MitigationName string              `json:"mitigation_name"`
	EdgeID         string              `json:"edge_id"`
	EvidenceLevel  model.EvidenceLevel `json:"evidence_level"`
	Confidence     float64             `json:"confidence"`
	Status         model.KnowledgeStatus `json:"status"`
	CaseSupport    int                 `json:"case_support_count"`
	FragmentRef    string              `json:"fragment_ref"` // 证据回链 ref
}

// Mitigations 处置查询：对某异常，返回缓解它的全部手段（沿 mitigate 边的入边）。
// 语义方向：手段 → 被缓解异常；查询"谁指向我"而非"我指向谁"。
func (s *Service) Mitigations(anomalyCode string, includeDraft bool) ([]MitigationRow, error) {
	node, err := s.store.GetAnomalyByCode(anomalyCode)
	if err != nil {
		return nil, fmt.Errorf("anomaly: %w", err)
	}
	status := model.StatusCurated
	if includeDraft {
		status = ""
	}
	rules, err := s.store.MitigationsFor(node.ID, status)
	if err != nil {
		return nil, fmt.Errorf("list mitigations: %w", err)
	}
	rows := make([]MitigationRow, 0, len(rules))
	for _, r := range rules {
		src, err := s.store.GetAnomalyByID(r.SourceAnomalyID)
		if err != nil {
			continue
		}
		row := MitigationRow{
			MitigationCode: src.StableCode, MitigationName: src.Name,
			EdgeID: r.ID, EvidenceLevel: r.EvidenceLevel, Confidence: r.Confidence,
			Status: r.Status, CaseSupport: r.CaseSupportCount, FragmentRef: r.ID,
		}
		rows = append(rows, row)
	}
	// 置信度降序
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Confidence > rows[j].Confidence })
	return rows, nil
}

// GraphEdge 可视化边（源/目标用 stable_code，便于前端直连）。
type GraphEdge struct {
	ID            string               `json:"id"`
	Source        string               `json:"source"`
	Target        string               `json:"target"`
	EdgeType      model.EdgeType       `json:"edge_type"`
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
			ID: r.ID, Source: src, Target: dst, EdgeType: r.EdgeType,
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
	EdgeType      model.EdgeType       `json:"edge_type,omitempty"`
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
			EdgeType: r.EdgeType, EvidenceLevel: r.EvidenceLevel, Confidence: r.Confidence, CaseSupport: r.CaseSupportCount,
			Status: r.Status, ReviewFlagged: r.ReviewFlagged,
			UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339)}
		if rev, err := s.store.LatestRevision(model.EntityCausalRule, r.ID); err == nil {
			row.RevisionID = rev.ID
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ---------- 场景 ----------

// ScenarioCard 场景卡片：按场景聚合的统计视图（前端卡片墙用）。
type ScenarioCard struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Domain        string `json:"domain"`
	Description   string `json:"description"`
	NodeCount     int    `json:"node_count"`
	EdgeCount     int    `json:"edge_count"`       // 两端均在场景内的边
	CuratedEdges  int    `json:"curated_edges"`
	DraftEdges    int    `json:"draft_edges"`
	AlertCount    int    `json:"alert_count"`       // 场景内节点关联的 distinct 告警入口
	MitigationCount int  `json:"mitigation_count"` // 场景内 mitigate 边数
}

// ScenarioDetail 场景详情：成员节点与场景内边（行结构与知识清单同构）。
type ScenarioDetail struct {
	Scenario ScenarioCard  `json:"scenario"`
	Nodes    []KnowledgeRow `json:"nodes"`
	Edges    []KnowledgeRow `json:"edges"`
}

// ScenarioCards 全部场景卡片：领域 → 场景 → 知识的组织维度。
func (s *Service) ScenarioCards() ([]ScenarioCard, error) {
	scenarios, err := s.store.ListScenarios()
	if err != nil {
		return nil, fmt.Errorf("list scenarios: %w", err)
	}
	allRules, err := s.store.ListAllCausalRules(0)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	allMaps, _, err := s.store.ListMappingsPage("", "", 1000, 0)
	if err != nil {
		return nil, fmt.Errorf("list mappings: %w", err)
	}

	cards := make([]ScenarioCard, 0, len(scenarios))
	for _, sc := range scenarios {
		members, err := s.store.ListScenarioNodes(sc.ID)
		if err != nil {
			return nil, fmt.Errorf("list scenario nodes: %w", err)
		}
		card := ScenarioCard{Code: sc.Code, Name: sc.Name, Domain: sc.Domain, Description: sc.Description, NodeCount: len(members)}
		memberIDs := make(map[string]bool, len(members))
		for _, m := range members {
			memberIDs[m.AnomalyID] = true
		}
		for _, r := range allRules {
			if memberIDs[r.SourceAnomalyID] && memberIDs[r.TargetAnomalyID] {
				card.EdgeCount++
				if r.Status == model.StatusCurated {
					card.CuratedEdges++
				} else {
					card.DraftEdges++
				}
				if r.EdgeType == model.EdgeMitigate {
					card.MitigationCount++
				}
			}
		}
		alerts := map[string]bool{}
		for _, m := range allMaps {
			if memberIDs[m.AnomalyID] {
				alerts[m.AlertSignature] = true
			}
		}
		card.AlertCount = len(alerts)
		cards = append(cards, card)
	}
	return cards, nil
}

// ScenarioDetailByCode 单场景详情：成员节点行 + 场景内边行。
func (s *Service) ScenarioDetailByCode(code string) (*ScenarioDetail, error) {
	sc, err := s.store.GetScenarioByCode(code)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListScenarioNodes(sc.ID)
	if err != nil {
		return nil, fmt.Errorf("list scenario nodes: %w", err)
	}
	detail := &ScenarioDetail{Nodes: []KnowledgeRow{}, Edges: []KnowledgeRow{}}
	nodeRows := make([]model.AnomalyType, 0, len(members))
	for _, m := range members {
		if n, err := s.store.GetAnomalyByID(m.AnomalyID); err == nil {
			nodeRows = append(nodeRows, *n)
		}
	}
	// 节点行
	for _, n := range nodeRows {
		row := KnowledgeRow{ID: n.ID, Kind: "anomaly", StableCode: n.StableCode, Name: n.Name,
			Category: n.Category, Status: n.Status, UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339)}
		if rev, err := s.store.LatestRevision(model.EntityAnomalyType, n.ID); err == nil {
			row.RevisionID = rev.ID
		}
		detail.Nodes = append(detail.Nodes, row)
	}
	// 场景内边行（复用 ruleRows 的行结构）
	memberIDs := make(map[string]bool, len(members))
	for _, m := range members {
		memberIDs[m.AnomalyID] = true
	}
	allRules, err := s.store.ListAllCausalRules(0)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	inScenario := make([]model.CausalRule, 0)
	for _, r := range allRules {
		if memberIDs[r.SourceAnomalyID] && memberIDs[r.TargetAnomalyID] {
			inScenario = append(inScenario, r)
		}
	}
	edgeRows, err := s.ruleRows(inScenario)
	if err != nil {
		return nil, err
	}
	detail.Edges = edgeRows
	// 卡片统计复用
	cards, err := s.ScenarioCards()
	if err != nil {
		return nil, err
	}
	for _, c := range cards {
		if c.Code == code {
			detail.Scenario = c
			break
		}
	}
	return detail, nil
}

// UpsertScenario 创建/更新场景（专家或管线显式定义；不做自动聚类）。
func (s *Service) UpsertScenario(sc *model.Scenario) error {
	if sc.Code == "" || sc.Name == "" {
		return fmt.Errorf("scenario code and name are required")
	}
	_, err := s.store.GetOrCreateScenario(sc)
	return err
}

// AssignScenarioNodes 把异常节点指派进场景（幂等；不存在的 stable_code 报错不静默跳过）。
func (s *Service) AssignScenarioNodes(scenarioCode string, stableCodes []string, operator string) (int, error) {
	sc, err := s.store.GetScenarioByCode(scenarioCode)
	if err != nil {
		return 0, err
	}
	assigned := 0
	for _, code := range stableCodes {
		n, err := s.store.GetAnomalyByCode(code)
		if err != nil {
			return assigned, fmt.Errorf("anomaly %s: %w", code, err)
		}
		if err := s.store.AddScenarioNode(sc.ID, n.ID); err != nil {
			return assigned, fmt.Errorf("assign %s: %w", code, err)
		}
		assigned++
	}
	_ = operator // 审计记录后置 S3；先保持签名一致
	return assigned, nil
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
	if maxHops <= 0 {
		maxHops = s.cfg.MaxHops // 缺省取配置默认（3）；显式传值允许超过默认
	} else if maxHops > 10 {
		maxHops = 10 // 硬上限防滥用；cfg.MaxHops 是缺省值而非用户上限
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
		// trace 语义 = 根因传播：只遍历 trigger 边，mitigate/indicate/prevent 不入根因链
		edges, err := s.store.ListEdgesFrom(nodeID, status, model.EdgeTrigger)
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
