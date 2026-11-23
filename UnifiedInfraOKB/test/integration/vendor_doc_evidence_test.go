// TestVendorDocEvidenceChain 查证边的真实文档证据链（2026-11-21 用户核对后修正）：
// 此前查证 3 条边（光衰→CRC 两条 + CRC→Down 条件边）只挂了 doc_explicit 等级，
// 库内 fragment 是合成 JSON、source 无文档标题/URL——按 requirements §3 vendor_doc
// 设计意图（真实文档为 source、原文段落为 fragment、证据指向它）补齐。
// 断言：evidence 回链能查到华为 CloudEngine 文档标题、storage_uri 与原文关键句摘录。
package integration

import (
	"strings"
	"testing"

	"UnifiedInfraOKB/internal/seeder"
)

func TestVendorDocEvidenceChain(t *testing.T) {
	svc := newTestService(t)
	if _, err := seeder.Seed(svc); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 1. 光衰→CRC 边（交换机侧）的证据链：华为维护宝典
	ev, err := svc.Evidence("net.port.crc_error") // 节点证据链含 vendor_doc 来源
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	foundHuawei := false
	for _, it := range ev.Items {
		if it.SourceKind == "vendor_doc" && strings.Contains(it.Content, "CRC") {
			foundHuawei = true
		}
	}
	if !foundHuawei {
		t.Fatalf("net.port.crc_error must carry vendor_doc evidence with original text, got: %+v", ev.Items)
	}

	// 2. source 登记带文档标题与 URL（storage_uri），kind=vendor_doc
	var sources []struct {
		Kind       string
		Title      string
		StorageURI string
	}
	if err := svc.Store().DB.Raw("SELECT kind, title, storage_uri FROM sources WHERE kind = 'vendor_doc'").Scan(&sources).Error; err != nil {
		t.Fatalf("query sources: %v", err)
	}
	if len(sources) < 2 {
		t.Fatalf("expect >=2 vendor_doc sources (huawei cloudengine + fusionserver emm), got %d", len(sources))
	}
	for _, s := range sources {
		if !strings.Contains(s.Title, "华为") && !strings.Contains(s.Title, "CloudEngine") && !strings.Contains(s.Title, "FusionServer") {
			t.Fatalf("vendor_doc source must carry real document title, got %q", s.Title)
		}
		if !strings.HasPrefix(s.StorageURI, "http") {
			t.Fatalf("vendor_doc source must carry document URL, got %q", s.StorageURI)
		}
	}

	// 3. CRC→Down 条件边：证据回链能查到 Error-Down 配置原文
	tr, err := svc.Trace("net.port.crc_error", 3, 5, nil, true)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	var edgeID string
	for _, c := range tr.Chains {
		for _, h := range c.Hops {
			if h.TargetCode == "net.port.down" {
				edgeID = h.EdgeID
			}
		}
	}
	if edgeID == "" {
		t.Fatal("crc_error -> port.down edge must exist")
	}
	evEdge, err := svc.Evidence(edgeID)
	if err != nil {
		t.Fatalf("evidence edge: %v", err)
	}
	foundConfigDoc := false
	for _, it := range evEdge.Items {
		if it.SourceKind == "vendor_doc" && strings.Contains(it.Content, "error-down") {
			foundConfigDoc = true
		}
	}
	if !foundConfigDoc {
		t.Fatalf("crc->down edge must carry vendor_doc evidence with Error-Down config text, got: %+v", evEdge.Items)
	}
}
