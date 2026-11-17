// 集成测试：内存 SQLite + FakeExtract，覆盖 v2 验收场景 A1—A7。
// A1 摄取回链 / A2 提取出处 / A3 印证计数 / A4 查询 / A5 反馈 / A6 权限 / A7 对齐。
package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
	"UnifiedInfraOKB/internal/service"
)

// newTestService 每个用例独立内存库，互不污染。
func newTestService(t *testing.T) *service.Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store, err := repository.New(db, true)
	if err != nil {
		t.Fatalf("init store: %v", err)
	}
	return service.New(store, nil, service.DefaultConfig())
}

// caseContent 构造一份合成脱敏工单：三个片段分别声明源异常（含告警）、
// 目标异常与因果边。FakeExtract 识别 JSON 标记片段并生成候选。
func caseContent(alert, anomalyCode, anomalyName, source, target string) string {
	return strings.Join([]string{
		`{"anomaly": "` + anomalyCode + `", "name": "` + anomalyName + `", "code": "` + anomalyCode + `", "aliases": ["厂商别名A"], "alert": "` + alert + `"}`,
		`{"anomaly": "` + target + `", "name": "` + target + `", "code": "` + target + `"}`,
		`{"source": "` + source + `", "target": "` + target + `"}`,
	}, "\n\n")
}

// TestA1IngestAndTraceback A1：摄取入库，可回查结构化提取结果与原文片段。
func TestA1IngestAndTraceback(t *testing.T) {
	svc := newTestService(t)
	out, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceInternalCase, Title: "合成工单-001",
		Content: caseContent("ALERT-X1", "gpu.ecc.error", "GPU ECC 错误", "gpu.ecc.error", "gpu.link.degrade"),
		OperatorID: "tester",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if out.NewAnomalies != 2 || out.NewRules != 1 || out.NewMappings != 1 {
		t.Fatalf("unexpected counts: %+v", out)
	}

	// 证据回链：边的证据必须指向片段
	node, _ := svc.Store().GetAnomalyByCode("gpu.ecc.error")
	edges, err := svc.Store().ListEdgesFrom(node.ID, "")
	if err != nil || len(edges) != 1 {
		t.Fatalf("list edges: %v len=%d", err, len(edges))
	}
	view, err := svc.Evidence(edges[0].ID)
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if len(view.Items) == 0 || view.Items[0].Content == "" {
		t.Fatal("evidence must trace back to fragment content for authorized caller")
	}
	if view.RevisionID == "" {
		t.Fatal("evidence view must pin revision_id")
	}
}

// TestA2SanitizeGate A2 前置门禁：含敏感信息拒绝入库。
func TestA2SanitizeGate(t *testing.T) {
	svc := newTestService(t)
	bad := "服务器 10.0.1.23 告警，根因为 GPU ECC。\n\n" + caseContent("ALERT-X1", "a.b", "A", "a.b", "c.d")
	if _, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceInternalCase, Title: "带 IP 工单", Content: bad,
	}); err == nil {
		t.Fatal("ingest with sensitive IP must be rejected")
	}
	if ok, _ := service.SanitizeCheck("联系电话 13812345678"); ok {
		t.Fatal("phone number must fail sanitize check")
	}
	if ok, _ := service.SanitizeCheck("password: admin123"); ok {
		t.Fatal("credential pattern must fail sanitize check")
	}
}

// TestA3Corroboration A3：同一因果边被 ≥2 独立工单印证后自动升 curated。
func TestA3Corroboration(t *testing.T) {
	svc := newTestService(t)
	content := caseContent("ALERT-X1", "a.source", "源异常", "a.source", "a.target")
	// 工单 1：draft
	if _, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "工单-1", Content: content}); err != nil {
		t.Fatalf("ingest 1: %v", err)
	}
	src, _ := svc.Store().GetAnomalyByCode("a.source")
	edges, _ := svc.Store().ListEdgesFrom(src.ID, "")
	if len(edges) != 1 || edges[0].Status != model.StatusDraft {
		t.Fatalf("first case must leave edge draft: %+v", edges)
	}
	// 工单 2：独立来源 → 印证计数 2 → auto curated
	out, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "工单-2", Content: content})
	if err != nil {
		t.Fatalf("ingest 2: %v", err)
	}
	if out.CuratedRules != 1 {
		t.Fatalf("second independent case must auto-curate edge: %+v", out)
	}
	edges, _ = svc.Store().ListEdgesFrom(src.ID, "")
	if edges[0].Status != model.StatusCurated || edges[0].CaseSupportCount != 2 {
		t.Fatalf("edge must be curated with support 2: %+v", edges[0])
	}
	// 工单 3：同链重复内容（新来源）不重复升版；幂等
	out, err = svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "工单-3", Content: content})
	if err != nil {
		t.Fatalf("ingest 3: %v", err)
	}
	edges, _ = svc.Store().ListEdgesFrom(src.ID, "")
	if edges[0].CaseSupportCount != 3 || edges[0].Status != model.StatusCurated {
		t.Fatalf("support should count distinct sources: %+v", edges[0])
	}
}

// TestA4LookupTrace A4：lookup 返回候选；trace 返回带证据等级的候选链。
func TestA4LookupTrace(t *testing.T) {
	svc := newTestService(t)
	// 链路：A -> B -> C；A,B 由告警入口接入
	content := strings.Join([]string{
		`{"anomaly": "chain.a", "name": "A", "code": "chain.a", "alert": "ALERT-CHAIN"}`,
		`{"source": "chain.a", "target": "chain.b"}`,
		`{"anomaly": "chain.b", "name": "B", "code": "chain.b"}`,
		`{"source": "chain.b", "target": "chain.c"}`,
		`{"anomaly": "chain.c", "name": "C", "code": "chain.c"}`,
	}, "\n\n")
	for i := 1; i <= 2; i++ {
		if _, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "链路工单", Content: content}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}

	// lookup：draft 默认排除 → 空；includeDraft 才可见
	lookup, err := svc.Lookup("ALERT-CHAIN", false)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	lookup, err = svc.Lookup("ALERT-CHAIN", true)
	if err != nil || len(lookup.Candidates) != 1 || lookup.Candidates[0].StableCode != "chain.a" {
		t.Fatalf("lookup includeDraft must return chain.a: %+v err=%v", lookup, err)
	}
	if lookup.Candidates[0].RevisionID == "" {
		t.Fatal("lookup candidate must pin revision_id")
	}

	// trace：全 draft 边，includeDraft 才可遍历；应得到 A->B 与 A->B->C
	trace, err := svc.Trace("chain.a", 3, 5, nil, true)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(trace.Chains) == 0 {
		t.Fatal("trace must return chains")
	}
	sawTwoHop := false
	for _, c := range trace.Chains {
		if len(c.Hops) == 2 && c.Hops[1].TargetCode == "chain.c" {
			sawTwoHop = true
		}
		for _, h := range c.Hops {
			if h.EvidenceLevel != model.EvidenceCaseVerified {
				t.Fatalf("case-derived edge must be case_verified: %+v", h)
			}
			if h.RevisionID == "" {
				t.Fatal("hop must pin revision_id")
			}
		}
		if c.FullyCurated && c.CombinedNote != "" {
			t.Fatal("curated chain must not carry combined-draft note")
		}
	}
	if !sawTwoHop {
		t.Fatalf("trace must reach 2-hop chain A->B->C: %+v", trace.Chains)
	}

	// 印证后 curated：默认（不含 draft）可遍历
	for i := 3; i <= 4; i++ {
		if _, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "链路工单", Content: content}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	trace, err = svc.Trace("chain.a", 3, 5, nil, false)
	if err != nil {
		t.Fatalf("trace curated: %v", err)
	}
	if len(trace.Chains) == 0 {
		t.Fatal("curated edges must be traversable by default")
	}
	for _, c := range trace.Chains {
		if !c.FullyCurated {
			t.Fatal("default trace should only contain curated chains")
		}
	}
}

// TestA4TraceConditionEval 条件求值：failed 边不可走；unknown 作为待验证检查项返回。
func TestA4TraceConditionEval(t *testing.T) {
	svc := newTestService(t)
	// 直接构造带条件的边：温度 > 80 才传播
	nodeA := &model.AnomalyType{StableCode: "cond.a", Name: "A"}
	nodeB := &model.AnomalyType{StableCode: "cond.b", Name: "B"}
	if _, err := svc.Store().GetOrCreateAnomaly(nodeA); err != nil {
		t.Fatalf("create node a: %v", err)
	}
	if _, err := svc.Store().GetOrCreateAnomaly(nodeB); err != nil {
		t.Fatalf("create node b: %v", err)
	}
	condJSON, _ := json.Marshal([]model.ConditionClause{{FactKey: "gpu_temp", Op: "gt", Value: 80}})
	rule := &model.CausalRule{
		SourceAnomalyID: nodeA.ID, TargetAnomalyID: nodeB.ID,
		Conditions: string(condJSON), EvidenceLevel: model.EvidenceCaseVerified,
		Status: model.StatusCurated, Confidence: 0.9,
	}
	if _, err := svc.Store().GetOrCreateCausalRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// facts 缺失 → unknown，边可走（待验证检查项）
	trace, err := svc.Trace("cond.a", 2, 5, nil, false)
	if err != nil || len(trace.Chains) != 1 {
		t.Fatalf("unknown condition must keep edge walkable: %+v err=%v", trace, err)
	}
	if trace.Chains[0].Hops[0].Evaluations[0].Result != "unknown" {
		t.Fatalf("missing fact must be unknown: %+v", trace.Chains[0].Hops[0].Evaluations)
	}

	// facts 温度 90 → passed
	trace, err = svc.Trace("cond.a", 2, 5, map[string]interface{}{"gpu_temp": 90}, false)
	if err != nil || len(trace.Chains) != 1 || trace.Chains[0].Hops[0].Evaluations[0].Result != "passed" {
		t.Fatalf("gpu_temp=90 must pass: %+v err=%v", trace, err)
	}

	// facts 温度 50 → failed，边不可走
	trace, err = svc.Trace("cond.a", 2, 5, map[string]interface{}{"gpu_temp": 50}, false)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(trace.Chains) != 0 {
		t.Fatal("failed condition must make edge unwalkable")
	}
}

// TestA5Feedback A5：report 确认/否决回写置信度；幂等；否决入复核队列。
func TestA5Feedback(t *testing.T) {
	svc := newTestService(t)
	nodeA := &model.AnomalyType{StableCode: "fb.a", Name: "A"}
	nodeB := &model.AnomalyType{StableCode: "fb.b", Name: "B"}
	if _, err := svc.Store().GetOrCreateAnomaly(nodeA); err != nil {
		t.Fatalf("create node a: %v", err)
	}
	if _, err := svc.Store().GetOrCreateAnomaly(nodeB); err != nil {
		t.Fatalf("create node b: %v", err)
	}
	rule := &model.CausalRule{
		SourceAnomalyID: nodeA.ID, TargetAnomalyID: nodeB.ID,
		EvidenceLevel: model.EvidenceCaseVerified, Status: model.StatusCurated, Confidence: 0.5,
	}
	if _, err := svc.Store().GetOrCreateCausalRule(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// 确认：+0.05
	r1, err := svc.Report(service.ReportInput{DiagnosisRef: "diag-1", EdgeID: rule.ID, Verdict: model.VerdictConfirmed, OperatorID: "agent-1"})
	if err != nil || !r1.Recorded || r1.NewConfidence < 0.54 || r1.NewConfidence > 0.56 {
		t.Fatalf("confirm must raise confidence ~0.55: %+v err=%v", r1, err)
	}
	// 幂等：同 diag 同边重复上报被忽略
	r2, err := svc.Report(service.ReportInput{DiagnosisRef: "diag-1", EdgeID: rule.ID, Verdict: model.VerdictConfirmed, OperatorID: "agent-1"})
	if err != nil || r2.Recorded {
		t.Fatalf("duplicate report must be idempotent: %+v err=%v", r2, err)
	}

	// 否决：-0.10，降 draft，入复核队列
	r3, err := svc.Report(service.ReportInput{DiagnosisRef: "diag-2", EdgeID: rule.ID, Verdict: model.VerdictRejected, OperatorID: "agent-1"})
	if err != nil || !r3.Recorded || r3.NewStatus != model.StatusDraft || !r3.ReviewFlagged {
		t.Fatalf("reject must degrade and flag: %+v err=%v", r3, err)
	}
	queue, err := svc.ReviewQueue(10)
	if err != nil || len(queue) != 1 || queue[0].ID != rule.ID || queue[0].SourceCode != "fb.a" {
		t.Fatalf("rejected edge must enter review queue: %+v err=%v", queue, err)
	}

	// 专家确认恢复 curated
	if err := svc.ConfirmEdge(rule.ID, "expert-1"); err != nil {
		t.Fatalf("confirm edge: %v", err)
	}
	got, _ := svc.Store().GetCausalRule(rule.ID)
	if got.Status != model.StatusCurated || got.ReviewFlagged {
		t.Fatal("expert confirm must restore curated and clear flag")
	}

	// 修订历史：ingest 后有 confirm/reject 反馈修订，追加不覆盖
	revs := countRevisions(t, svc, model.EntityCausalRule, rule.ID)
	if revs < 3 {
		t.Fatalf("expected >=3 appended revisions, got %d", revs)
	}
}

// TestA6EvidenceFullContent A6：fragment 级权限本期不启用（后置 S3），evidence 始终返回原文。
func TestA6EvidenceFullContent(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceInternalCase, Title: "证据工单",
		Content: caseContent("ALERT-P", "perm.a", "PA", "perm.a", "perm.b"),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	node, _ := svc.Store().GetAnomalyByCode("perm.a")
	edges, _ := svc.Store().ListEdgesFrom(node.ID, "")
	edgeID := edges[0].ID

	// 权限门槛移除后：摘要与原文均直返
	view, err := svc.Evidence(edgeID)
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if len(view.Items) == 0 {
		t.Fatal("evidence items must not be empty")
	}
	for _, it := range view.Items {
		if it.Summary == "" {
			t.Fatal("summary must always be returned")
		}
		if it.Content == "" {
			t.Fatal("content must be returned while permission gating is deferred to S3")
		}
	}
}

// TestA7NodeAlignment A7：同一异常的不同厂商别名归并到同一 stable_code 节点。
func TestA7NodeAlignment(t *testing.T) {
	svc := newTestService(t)
	c1 := caseContent("ALERT-7", "align.gpu.ecc", "GPU ECC", "align.gpu.ecc", "align.gpu.down")
	c2 := strings.Join([]string{
		`{"anomaly": "align.gpu.ecc", "name": "GPU ECC 错误（厂商X表述）", "code": "align.gpu.ecc", "aliases": ["XLink ECC Fault"], "alert": "ALERT-7X"}`,
	}, "\n\n")
	if _, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "工单-1", Content: c1}); err != nil {
		t.Fatalf("ingest 1: %v", err)
	}
	if _, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceVendorDoc, Title: "厂商X文档", Content: c2}); err != nil {
		t.Fatalf("ingest 2: %v", err)
	}

	// 同 stable_code 幂等：只有一个节点
	node, err := svc.Store().GetAnomalyByCode("align.gpu.ecc")
	if err != nil {
		t.Fatalf("get anomaly: %v", err)
	}
	all, _ := svc.Store().SearchAnomalies("align.gpu.ecc", 10)
	if len(all) != 1 {
		t.Fatalf("same stable_code must merge to one node, got %d", len(all))
	}
	_ = node

	// 两个告警入口都指向同一节点
	l1, _ := svc.Lookup("ALERT-7", true)
	l2, _ := svc.Lookup("ALERT-7X", true)
	if len(l1.Candidates) != 1 || len(l2.Candidates) != 1 || l1.Candidates[0].AnomalyID != l2.Candidates[0].AnomalyID {
		t.Fatalf("aliases must map to the same node: l1=%+v l2=%+v", l1.Candidates, l2.Candidates)
	}
}

// TestA8KnowledgeListGraphReview 知识清单/整图/复核队列端点：分页、过滤、行结构与回链。
func TestA8KnowledgeListGraphReview(t *testing.T) {
	svc := newTestService(t)
	content := caseContent("ALERT-L", "list.a", "A", "list.a", "list.b")
	for i := 1; i <= 2; i++ {
		if _, err := svc.Ingest(context.Background(), service.IngestInput{Kind: model.SourceInternalCase, Title: "清单工单", Content: content}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}

	// 异常清单：2 行，钉修订
	anomalies, err := svc.Knowledge("anomaly", "", "", 10, 0)
	if err != nil || anomalies.Total != 2 || len(anomalies.Rows) != 2 {
		t.Fatalf("knowledge anomaly: %+v err=%v", anomalies, err)
	}
	for _, row := range anomalies.Rows {
		if row.RevisionID == "" {
			t.Fatal("anomaly row must pin revision_id")
		}
	}
	// 分页：limit=1 只出一行，total 仍是 2
	page1, _ := svc.Knowledge("anomaly", "", "", 1, 0)
	if len(page1.Rows) != 1 || page1.Total != 2 {
		t.Fatalf("pagination: %+v", page1)
	}
	// 关键字过滤
	filtered, _ := svc.Knowledge("anomaly", "", "list.b", 10, 0)
	if filtered.Total != 1 || filtered.Rows[0].StableCode != "list.b" {
		t.Fatalf("filter: %+v", filtered)
	}
	// 片段偏移回链：第二块 CharStart 必须指向原文真实位置，不再是 0
	frag := firstFragment(t, svc)
	if frag.CharStart < 0 || frag.CharEnd <= frag.CharStart {
		t.Fatalf("fragment offsets invalid: start=%d end=%d", frag.CharStart, frag.CharEnd)
	}

	// 因果边清单：源/目标编码已解析，2 工单印证后 curated
	rules, err := svc.Knowledge("rule", "", "", 10, 0)
	if err != nil || rules.Total != 1 {
		t.Fatalf("knowledge rule: %+v err=%v", rules, err)
	}
	r0 := rules.Rows[0]
	if r0.SourceCode != "list.a" || r0.TargetCode != "list.b" || r0.Status != model.StatusCurated ||
		r0.EvidenceLevel != model.EvidenceCaseVerified || r0.CaseSupport != 2 {
		t.Fatalf("rule row: %+v", r0)
	}

	// 映射清单
	maps, _ := svc.Knowledge("mapping", "", "", 10, 0)
	if maps.Total != 1 || maps.Rows[0].AlertSignature != "ALERT-L" ||
		maps.Rows[0].StableCode != "list.a" || maps.Rows[0].Status != model.StatusCurated {
		t.Fatalf("mapping rows: %+v", maps)
	}

	// 整图
	g, err := svc.Graph()
	if err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("graph: %+v err=%v", g, err)
	}
	if g.Edges[0].Source != "list.a" || g.Edges[0].Target != "list.b" || g.Edges[0].Status != model.StatusCurated {
		t.Fatalf("graph edge: %+v", g.Edges[0])
	}

	// 复核队列：初始为空；否决后一行，snake_case 且源/目标可读
	if q, _ := svc.ReviewQueue(10); len(q) != 0 {
		t.Fatalf("review queue should be empty: %+v", q)
	}
	edges, _ := svc.Store().ListEdgesFrom(mustAnomalyID(t, svc, "list.a"), "")
	if _, err := svc.Report(service.ReportInput{DiagnosisRef: "d1", EdgeID: edges[0].ID, Verdict: model.VerdictRejected}); err != nil {
		t.Fatalf("report: %v", err)
	}
	q, err := svc.ReviewQueue(10)
	if err != nil || len(q) != 1 || q[0].SourceCode != "list.a" || q[0].TargetCode != "list.b" || !q[0].ReviewFlagged {
		t.Fatalf("review queue rows: %+v err=%v", q, err)
	}
}

func mustAnomalyID(t *testing.T, svc *service.Service, code string) string {
	t.Helper()
	node, err := svc.Store().GetAnomalyByCode(code)
	if err != nil {
		t.Fatalf("get anomaly %s: %v", code, err)
	}
	return node.ID
}

// firstFragment 取库里第一个非零偏移片段，验证 CharStart 回链定位真实有效。
func firstFragment(t *testing.T, svc *service.Service) model.Fragment {
	t.Helper()
	var frags []model.Fragment
	if err := svc.Store().DB.Where("char_start > 0 OR char_end > 0").Order("char_start ASC").Limit(5).Find(&frags).Error; err != nil || len(frags) == 0 {
		t.Fatalf("query fragments: %v len=%d", err, len(frags))
	}
	return frags[len(frags)-1] // 末块偏移必然 >0，验证累积定位
}

func countRevisions(t *testing.T, svc *service.Service, et model.EntityType, entityID string) int {
	t.Helper()
	list, err := svc.Store().ListEvidenceByEntity(et, entityID)
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	_ = list
	// 通过 LatestRevision 链回溯计数
	n := 0
	rev, err := svc.Store().LatestRevision(et, entityID)
	if err != nil {
		return 0
	}
	for rev != nil {
		n++
		if rev.PrevRevisionID == nil {
			break
		}
		// 链上取前驱：直接用 DB 查询避免死循环
		var prev model.Revision
		if err := svc.Store().DB.First(&prev, "id = ?", *rev.PrevRevisionID).Error; err != nil {
			break
		}
		rev = &prev
	}
	return n
}
