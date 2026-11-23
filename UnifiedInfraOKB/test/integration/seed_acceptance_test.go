// 种子验收演练：因果边清单-draft.md 的结构化种子灌入后，
// 四原语回答此前设计讨论定下的真实诊断问题（验收试金石）：
// 1. T30 告警 lookup → 电池放电异常（含 severity/时钟）+ 同柜对端提示
// 2. 掉电链反向 trace → 从服务器 input_lost 走到动环 rack 级
// 3. 制冷失效正向 trace → 5-6 跳级联（max_hops 场景建议的依据）
// 4. same_event 分流语义：双侧同时告 ⇒ 链路本体（net.port.down 对端）
package integration

import (
	"testing"

	"UnifiedInfraOKB/internal/seeder"
)

// TestOpticalDegradationChain 光模块劣化后果链（2026-11-21 查证补充，用户确认）：
// 华为 CloudEngine 维护宝典：接口发光正常但存在 CRC 丢包——可能原因"光模块收光功率异常"；
// 中兴真实案例：100G 光模块 CRC 误码超限致 2G 业务闪断。
// 交换机侧为文档直接结论（doc_explicit），服务器侧为同机理对称推断。
func TestOpticalDegradationChain(t *testing.T) {
	svc := newTestService(t)
	if _, err := seeder.Seed(svc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 交换机侧：端口光功率异常 → 端口 CRC（维护宝典直接结论）
	tr, err := svc.Trace("net.port.optical_power.abnormal", 3, 5, nil, true)
	if err != nil {
		t.Fatalf("trace switch side: %v", err)
	}
	found := false
	for _, c := range tr.Chains {
		for _, h := range c.Hops {
			if h.TargetCode == "net.port.crc_error" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("switch optical power degradation must cause port CRC errors (vendor-documented)")
	}
	// 服务器侧对称：光衰 → 网卡 CRC
	tr2, err := svc.Trace("server.optical.rx_power.abnormal", 3, 5, nil, true)
	if err != nil {
		t.Fatalf("trace server side: %v", err)
	}
	found = false
	for _, c := range tr2.Chains {
		for _, h := range c.Hops {
			if h.TargetCode == "server.nic.crc_error" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("server optical rx degradation must cause NIC CRC errors (symmetric mechanism)")
	}
}

// TestSeedAcceptance 灌种子后跑四原语验收用例。
func TestSeedAcceptance(t *testing.T) {
	svc := newTestService(t)
	report, err := seeder.Seed(svc)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if report.Anomalies < 20 || report.Rules < 30 {
		t.Fatalf("seed scale unexpected: %+v", report)
	}

	// --- 用例 1：T30 告警 lookup → 动环电池放电，携带实测等级与分钟时钟 ---
	lr, err := svc.Lookup("T30alertPsuFault", true)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(lr.Candidates) == 0 {
		t.Fatal("T30alertPsuFault must map to a seeded anomaly")
	}
	cand := lr.Candidates[0]
	if cand.SeverityOnline != "P3" || cand.ClockHint != "minutes" {
		t.Fatalf("battery discharge candidate must carry runtime severity P3 + minutes clock: %+v", cand)
	}

	// --- 用例 2：服务器掉电 trace——causal 链向前可达宕机；rack 掉电以 same_event 对端在 relations 标注 ---
	tr, err := svc.Trace("server.psu.input_lost", 6, 5, nil, true)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if len(tr.Chains) == 0 {
		t.Fatal("trace from input_lost must follow causal edges (forward propagation)")
	}
	reachedCrash := false
	for _, ch := range tr.Chains {
		for _, h := range ch.Hops {
			if h.EdgeKind != "causal" {
				t.Fatalf("chain hop must be causal only, got %q", h.EdgeKind)
			}
			if h.TargetCode == "server.sys.crash.unexpected" {
				reachedCrash = true
			}
		}
	}
	if !reachedCrash {
		t.Fatal("causal chain must reach server.sys.crash.unexpected")
	}
	// same_event 对端（同柜 rack 掉电）以关联标注返回，供 Agent 跨域取证
	hasRackPeer := false
	for _, r := range tr.Relations {
		if r.EdgeKind == "same_event" && r.OtherCode == "power.rack.pdu.output_lost_dual" {
			hasRackPeer = true
		}
	}
	if !hasRackPeer {
		t.Fatalf("trace relations must carry same_event peer power.rack.pdu.output_lost_dual: %+v", tr.Relations)
	}

	// --- 用例 3：制冷失效 → 正向级联需 5 跳（max_hops=6 建议依据）---
	// 清单级联：chilled_water.pump_fault → chiller.fault → temp_mid → temp_high → temp_critical → env.room.smoke
	// top_k 须给足：前缀短链优先占位（S1 输出有界设计），深级联需要更大候选窗。
	tr2, err := svc.Trace("cooling.chilled_water.pump_fault", 6, 10, nil, true)
	if err != nil {
		t.Fatalf("trace leak: %v", err)
	}
	maxLen := 0
	reachedSmoke := false
	for _, ch := range tr2.Chains {
		if len(ch.Hops) > maxLen {
			maxLen = len(ch.Hops)
		}
		for _, h := range ch.Hops {
			if h.TargetCode == "env.room.smoke" {
				reachedSmoke = true
			}
		}
	}
	if !reachedSmoke {
		t.Fatal("cooling cascade must reach env.room.smoke within 6 hops")
	}
	if maxLen < 5 {
		t.Fatalf("cooling cascade is a 5-hop scenario, got max chain %d", maxLen)
	}

	// --- 用例 4：same_event 分流——交换机端口Down查链路对端 ---
	lr2, err := svc.Lookup("ALERT-LINK-DOWN", true)
	if err != nil {
		t.Fatalf("lookup link: %v", err)
	}
	if len(lr2.Candidates) == 0 {
		t.Fatal("ALERT-LINK-DOWN must map to net.port.down")
	}
	if len(lr2.Candidates[0].SameEventPeers) == 0 {
		t.Fatalf("net.port.down candidate must carry same_event peer server.nic.link_down: %+v", lr2.Candidates[0])
	}
}
