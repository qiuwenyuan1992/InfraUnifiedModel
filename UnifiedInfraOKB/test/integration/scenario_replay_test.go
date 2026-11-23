// 全库 v2.1 重放验收：老 demo 数据（v2 合成工单演示，大概率错误，不保留）清空后，
// 全部数据按新种子（因果边清单-draft.md）走一遍：
// 1. 老 demo 码（rack.power.outage / net.link.degraded / gpu.ecc.error 等）不存在
// 2. 场景在新命名空间重建（scenario.power.rack 挂新掉电链成员）
// 3. 新图四原语行为与 TestSeedAcceptance 同源
package integration

import (
	"context"
	"testing"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
	"UnifiedInfraOKB/internal/seeder"
	"UnifiedInfraOKB/internal/service"
)

func TestFullResetRebuild(t *testing.T) {
	svc := newTestService(t)
	// 模拟老 demo 污染：灌入一份 v2 风格的合成工单（老码 + 假 case_verified 路径）
	if _, err := svc.Ingest(context.Background(), demoLegacyInput()); err != nil {
		t.Fatalf("demo ingest (setup): %v", err)
	}

	// 重置：清空全部知识表（保留表结构）
	if err := seeder.ResetAll(svc.Store()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	// 全量重灌：新种子 + 场景重建
	if _, err := seeder.Seed(svc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rep, err := seeder.RebuildScenarios(svc)
	if err != nil {
		t.Fatalf("rebuild scenarios: %v", err)
	}
	if rep.Scenarios < 8 || rep.Assigned < 30 {
		t.Fatalf("scenario rebuild scale unexpected: %+v", rep)
	}

	// --- 1. 老 demo 码全部消失 ---
	for _, legacy := range []string{"rack.power.outage", "net.link.degraded", "gpu.ecc.error", "server.power.off", "net.broadcast.storm", "rack.power.pdu.trip"} {
		if _, err := svc.Store().GetAnomalyByCode(legacy); err != repository.ErrNotFound {
			t.Fatalf("legacy demo code %s must be gone after reset, got err=%v", legacy, err)
		}
	}

	// --- 2. 场景挂新命名空间链 ---
	detail, err := svc.ScenarioDetailByCode("scenario.power.rack")
	if err != nil {
		t.Fatalf("scenario detail: %v", err)
	}
	members := map[string]bool{}
	for _, n := range detail.Nodes {
		members[n.StableCode] = true
	}
	for _, want := range []string{"power.grid.mains.abnormal", "power.ups.output_lost", "power.rack.pdu.output_lost_dual", "server.psu.input_lost", "server.sys.crash.unexpected"} {
		if !members[want] {
			t.Fatalf("scenario.power.rack must include new-chain member %s: %v", want, members)
		}
	}
	// 场景内边跨域连通（电力→服务器→网络放大）
	if detail.Scenario.EdgeCount < 8 {
		t.Fatalf("scenario edges must span cross-domain chain, got %d", detail.Scenario.EdgeCount)
	}

	// --- 3. 制冷场景同源验证（场景卡片含 5 跳级联成员）---
	cool, err := svc.ScenarioDetailByCode("scenario.cooling.chiller")
	if err != nil {
		t.Fatalf("cooling scenario: %v", err)
	}
	coolMembers := map[string]bool{}
	for _, n := range cool.Nodes {
		coolMembers[n.StableCode] = true
	}
	for _, want := range []string{"cooling.chilled_water.pump_fault", "cooling.chiller.fault", "cooling.it_room.temp_critical", "env.room.smoke"} {
		if !coolMembers[want] {
			t.Fatalf("cooling scenario must include %s: %v", want, coolMembers)
		}
	}
}

// demoLegacyInput v2 demo 合成工单（老码 + 假 case_verified 路径）。
func demoLegacyInput() service.IngestInput {
	return service.IngestInput{
		Kind: model.SourceInternalCase, Title: "v2 demo 合成工单（待清除）",
		Content: `{"anomaly": "gpu.ecc.error", "code": "gpu.ecc.error", "name": "GPU ECC 错误"}

{"anomaly": "gpu.link.degrade", "code": "gpu.link.degrade", "name": "GPU 链路降速"}

{"source": "gpu.ecc.error", "target": "gpu.link.degrade"}`,
		OperatorID: "demo",
	}
}
