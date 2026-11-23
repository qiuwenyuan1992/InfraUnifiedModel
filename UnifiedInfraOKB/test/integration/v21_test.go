// v2.1 集成测试：边语义三分（A8）、等级体系与样本优先仲裁（A9）、
// 结构化 signature、新数据源种类与 runtime_verified 证据级。
// 依据 requirements.md v2.1 §2/§4/§10(A8/A9) 与 api-boundaries.md v2.1。
package integration

import (
	"context"
	"strings"
	"testing"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/service"
)

// v21Case 拼装合成片段（每段一个 JSON 对象，空行分隔）。
func v21Case(frags ...string) string { return strings.Join(frags, "\n\n") }

// TestV21SameEventNotTraversed A8：same_event/same_family 不参与因果遍历，
// 以关联标注返回（lookup 的 peers 与 trace 的 relations）。
func TestV21SameEventNotTraversed(t *testing.T) {
	svc := newTestService(t)
	out, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceInternalCase, Title: "合成-跨域掉电对齐",
		Content: v21Case(
			`{"anomaly": "net.port.down", "code": "net.port.down", "name": "端口Down"}`,
			`{"anomaly": "server.nic.link_down", "code": "server.nic.link_down", "name": "链路Down", "alert": "ALERT-LINK"}`,
			`{"anomaly": "server.sys.crash", "code": "server.sys.crash", "name": "宕机", "clock_hint": "seconds"}`,
			`{"source": "net.port.down", "target": "server.nic.link_down", "edge_kind": "same_event"}`,
			`{"source": "server.nic.link_down", "target": "server.sys.crash", "rel": "trigger"}`,
		),
		OperatorID: "tester",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if out.NewRules != 2 {
		t.Fatalf("expect 2 rules, got %d", out.NewRules)
	}

	// 起点 net.port.down 只有 same_event 出边：不可遍历 → 无链，但 relations 返回对端
	tr, err := svc.Trace("net.port.down", 3, 5, nil, true)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(tr.Chains) != 0 {
		t.Fatalf("same_event must not be traversed, got %d chains", len(tr.Chains))
	}
	if len(tr.Relations) != 1 || tr.Relations[0].OtherCode != "server.nic.link_down" || tr.Relations[0].EdgeKind != model.EdgeKindSameEvent {
		t.Fatalf("unexpected relations: %+v", tr.Relations)
	}

	// 起点 server.nic.link_down：causal 边可达宕机；同时 relations 带 same_event 对端
	tr2, err := svc.Trace("server.nic.link_down", 3, 5, nil, true)
	if err != nil {
		t.Fatalf("trace2: %v", err)
	}
	if len(tr2.Chains) == 0 || tr2.Chains[0].Hops[len(tr2.Chains[0].Hops)-1].TargetCode != "server.sys.crash" {
		t.Fatalf("causal chain must reach crash: %+v", tr2.Chains)
	}
	if tr2.Chains[0].Hops[0].PropagationHint != "seconds" {
		t.Fatalf("hop must carry target clock_hint, got %q", tr2.Chains[0].Hops[0].PropagationHint)
	}
	found := false
	for _, r := range tr2.Relations {
		if r.OtherCode == "net.port.down" && r.EdgeKind == model.EdgeKindSameEvent {
			found = true
		}
	}
	if !found {
		t.Fatalf("trace relations must include same_event peer net.port.down: %+v", tr2.Relations)
	}

	// lookup：候选带 same_event_peers 标注
	lr, err := svc.Lookup("ALERT-LINK", true)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(lr.Candidates) != 1 || lr.Candidates[0].StableCode != "server.nic.link_down" {
		t.Fatalf("unexpected candidates: %+v", lr.Candidates)
	}
	if len(lr.Candidates[0].SameEventPeers) != 1 || lr.Candidates[0].SameEventPeers[0] != "net.port.down" {
		t.Fatalf("candidate must carry same_event peer: %+v", lr.Candidates[0])
	}
}

// TestV21SeverityArbitration A9：severity 随证据落档；样本优先仲裁——
// runtime_verified（alert_event）覆盖配置级（alert_strategy）；配置级不得覆盖已落值。
func TestV21SeverityArbitration(t *testing.T) {
	svc := newTestService(t)
	// 1) 策略库源（doc 级）：P3
	if _, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceAlertStrategy, Title: "策略库-双路掉电",
		Content:    v21Case(`{"anomaly": "power.rack.pdu.output_lost_dual", "code": "power.rack.pdu.output_lost_dual", "name": "PDU双路掉电", "severity_online": "P3", "node_kind": "aggregate", "alert": "T30_PDU_DUAL"}`),
		OperatorID: "tester",
	}); err != nil {
		t.Fatalf("ingest strategy: %v", err)
	}
	node, err := svc.Store().GetAnomalyByCode("power.rack.pdu.output_lost_dual")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.SeverityOnline != "P3" || node.NodeKind != model.NodeKindAggregate || node.Domain != "dci" {
		t.Fatalf("strategy ingest must set severity/node_kind/domain: %+v", node)
	}
	// 策略源证据为 doc_explicit
	ev, _ := svc.Evidence("power.rack.pdu.output_lost_dual")
	if len(ev.Items) == 0 || ev.Items[0].Level != model.EvidenceDocExplicit {
		t.Fatalf("alert_strategy evidence must be doc_explicit: %+v", ev.Items)
	}

	// 2) 真实告警源（runtime 级）：实测 P2 → 覆盖 P3，走新修订
	if _, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceAlertEvent, Title: "真实告警样本-双路掉电实测",
		Content:    v21Case(`{"anomaly": "power.rack.pdu.output_lost_dual", "code": "power.rack.pdu.output_lost_dual", "name": "PDU双路掉电", "severity_online": "P2"}`),
		OperatorID: "tester",
	}); err != nil {
		t.Fatalf("ingest event: %v", err)
	}
	node, _ = svc.Store().GetAnomalyByCode("power.rack.pdu.output_lost_dual")
	if node.SeverityOnline != "P2" {
		t.Fatalf("runtime_verified must override config severity, got %q", node.SeverityOnline)
	}
	rev, err := svc.Store().LatestRevision(model.EntityAnomalyType, node.ID)
	if err != nil || !strings.Contains(rev.Reason, "arbitration") {
		t.Fatalf("arbitration must append revision with reason, got %+v err=%v", rev, err)
	}
	// runtime 证据存在
	ev, _ = svc.Evidence("power.rack.pdu.output_lost_dual")
	hasRuntime := false
	for _, it := range ev.Items {
		if it.Level == model.EvidenceRuntimeVerified {
			hasRuntime = true
		}
	}
	if !hasRuntime {
		t.Fatalf("alert_event evidence must be runtime_verified: %+v", ev.Items)
	}

	// 3) 再来一份策略源声称 P4：配置不得覆盖运行实测
	if _, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceAlertStrategy, Title: "策略库修订-双路掉电复查",
		Content:    v21Case(`{"anomaly": "power.rack.pdu.output_lost_dual", "code": "power.rack.pdu.output_lost_dual", "name": "PDU双路掉电", "severity_online": "P4"}`),
		OperatorID: "tester",
	}); err != nil {
		t.Fatalf("ingest strategy 2: %v", err)
	}
	node, _ = svc.Store().GetAnomalyByCode("power.rack.pdu.output_lost_dual")
	if node.SeverityOnline != "P2" {
		t.Fatalf("config must not override runtime severity, got %q", node.SeverityOnline)
	}

	// lookup 候选携带 severity
	lr, err := svc.Lookup("T30_PDU_DUAL", true)
	if err != nil || len(lr.Candidates) != 1 {
		t.Fatalf("lookup: %v %+v", err, lr)
	}
	if lr.Candidates[0].SeverityOnline != "P2" || lr.Candidates[0].NodeKind != model.NodeKindAggregate {
		t.Fatalf("candidate must carry severity/node_kind: %+v", lr.Candidates[0])
	}
}

// TestV21StructuredSignature 结构化 signature：域+通道+键值查询与拼接键兼容查询。
func TestV21StructuredSignature(t *testing.T) {
	svc := newTestService(t)
	sigKey := "GPU_XID_154_GPU Reset Required"
	if _, err := svc.Ingest(context.Background(), service.IngestInput{
		Kind: model.SourceAlertStrategy, Title: "GPU XID 154 策略",
		Content: v21Case(
			`{"anomaly": "gpu.xid.154", "code": "gpu.xid.154", "name": "GPU 需恢复动作", "alert": "GPU_XID_154", "alert_domain": "server", "alert_channel": "agent_syslog", "sig_key": "` + sigKey + `"}`,
		),
		OperatorID: "tester",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// 结构化查询命中
	lr, err := svc.LookupStructured("server", "agent_syslog", sigKey, true)
	if err != nil || len(lr.Candidates) != 1 || lr.Candidates[0].StableCode != "gpu.xid.154" {
		t.Fatalf("structured lookup: %v %+v", err, lr)
	}
	// 拼接键兼容查询命中（domain|channel|sig_key）
	lr2, err := svc.Lookup("server|agent_syslog|"+sigKey, true)
	if err != nil || len(lr2.Candidates) != 1 {
		t.Fatalf("composed legacy lookup: %v %+v", err, lr2)
	}
}
