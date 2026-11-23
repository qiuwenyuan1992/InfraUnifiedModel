// Package extractor 定义 LLM 结构化提取的适配器边界。
// 真实模型按获准配置接入；FakeExtract 输出固定结果，仅用于离线开发与测试，
// 调用方不得将 Fake 结果宣称为真实模型验证。
package extractor

import (
	"context"
	"encoding/json"
	"fmt"

	"UnifiedInfraOKB/internal/model"
)

// ExtractionResult 一次结构化提取的输出：从一份解析文本中提取的
// 异常候选、已发生因果链、告警映射与片段定位。
// 全部为候选（draft），不直接入图；由 service 层接纳与定级。
type ExtractionResult struct {
	// Anomalies 提取到的异常（stable_code + 名称 + 可选别名）
	Anomalies []AnomalyCandidate `json:"anomalies"`
	// CausalChains 已发生的因果断言：源 → 目标（含条件）
	CausalChains []CausalChainCandidate `json:"causal_chains"`
	// AlertMappings 告警 → 异常对应
	AlertMappings []AlertMappingCandidate `json:"alert_mappings"`
	// FragmentRefs 每条候选对应的片段定位（与片段同序或按 ID 引用）
	ModelVersion string `json:"model_version"`
}

// AnomalyCandidate 异常候选。v2.1 增等级/类型/时钟/域字段，来自结构化源或提取结果。
type AnomalyCandidate struct {
	StableCode string   `json:"stable_code"`
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Aliases    []string `json:"aliases,omitempty"`
	// v2.1 字段（可空：留空由 service 层推导或缺省）
	SeverityOnline string `json:"severity_online,omitempty"` // P0-P4
	SeverityDict   string `json:"severity_dict,omitempty"`   // P1-P3
	NodeKind       string `json:"node_kind,omitempty"`       // atomic/aggregate/placeholder/event
	ClockHint      string `json:"clock_hint,omitempty"`      // seconds/minutes/hours
	Domain         string `json:"domain,omitempty"`          // server/network/dci
	FragmentID     string `json:"fragment_id"`               // 出处回链
}

// CausalChainCandidate 因果边候选。Rel 为边语义类型（trigger/mitigate/indicate/prevent），
// 缺省 trigger；Via 为 mitigate 边的根因挂靠（"当根因是 X 时"），仅 rel=mitigate 时有意义。
// v2.1 增 EdgeKind：causal（缺省）/ same_event / same_family——非 causal 边的 Rel 语义被忽略。
// 由数据源语义决定，不由提取器臆造。
type CausalChainCandidate struct {
	SourceCode string                  `json:"source_code"`
	TargetCode string                  `json:"target_code"`
	EdgeKind   string                  `json:"edge_kind,omitempty"` // v2.1
	Rel        model.EdgeType          `json:"rel,omitempty"`
	Via        string                  `json:"via,omitempty"`
	Conditions []model.ConditionClause `json:"conditions,omitempty"`
	// TimeConstraints v2.1 时间约束 JSON 文本（如 {"delay_tier":"minutes"}）：
	// 传播延迟档 seconds/minutes/hours，供 trace 同窗判定与紧迫性排序。
	TimeConstraints string `json:"time_constraints,omitempty"`
	FragmentID      string `json:"fragment_id"`
}

// AlertMappingCandidate 告警映射候选。v2.1 增结构化 signature 字段；
// AlertSignature 为旧式单串或拼接键（domain|channel|sig_key）。
type AlertMappingCandidate struct {
	AlertSignature string  `json:"alert_signature"`
	Domain         string  `json:"domain,omitempty"`      // v2.1
	Channel        string  `json:"channel,omitempty"`     // v2.1
	SigKey         string  `json:"sig_key,omitempty"`     // v2.1
	UpgradeRef     string  `json:"upgrade_ref,omitempty"` // v2.1
	AnomalyCode    string  `json:"anomaly_code"`
	Confidence     float64 `json:"confidence"`
	FragmentID     string  `json:"fragment_id"`
}

// Extractor 提取器接口：输入解析文本与片段，输出结构化候选。
// ctx 由调用方控制超时；实现不得阻塞后不响应。
type Extractor interface {
	Extract(ctx context.Context, parsedText string, fragments []model.Fragment) (*ExtractionResult, error)
}

// FakeExtract 固定响应适配器：按输入片段构造确定性候选。
// 用于无获准模型配置时的开发与测试路径。
type FakeExtract struct{}

// Extract 从片段摘要中生成确定性的最小候选集：
// 片段 Summary 含 "ANOMALY:" 前缀时生成异常，含 "CAUSES:" 生成因果边，
// 含 "ALERT:" 生成告警映射。输入不含标记时返回空结果（不捏造事实）。
func (f *FakeExtract) Extract(_ context.Context, _ string, fragments []model.Fragment) (*ExtractionResult, error) {
	res := &ExtractionResult{ModelVersion: "fake-extractor/v0"}
	for _, frag := range fragments {
		var payload struct {
			Anomaly string   `json:"anomaly"`
			Name    string   `json:"name"`
			Code    string   `json:"code"`
			Aliases []string `json:"aliases"`
			Alert   string   `json:"alert"`
			// v2.1 结构化字段
			SeverityOnline  string                  `json:"severity_online"`
			SeverityDict    string                  `json:"severity_dict"`
			NodeKind        string                  `json:"node_kind"`
			ClockHint       string                  `json:"clock_hint"`
			Domain          string                  `json:"domain"`
			EdgeKind        string                  `json:"edge_kind"`
			AlertDomain     string                  `json:"alert_domain"`
			AlertChannel    string                  `json:"alert_channel"`
			SigKey          string                  `json:"sig_key"`
			UpgradeRef      string                  `json:"upgrade_ref"`
			Conditions      []model.ConditionClause `json:"conditions"`
			TimeConstraints string                  `json:"time_constraints"`
			Source          string                  `json:"source"`
			Target          string                  `json:"target"`
			Rel             string                  `json:"rel"`
			Via             string                  `json:"via"`
		}
		if err := json.Unmarshal([]byte(frag.Content), &payload); err != nil {
			// 片段不是结构化标记，跳过，不生成候选
			continue
		}
		fid := frag.ID
		if payload.Anomaly != "" && payload.Code != "" {
			res.Anomalies = append(res.Anomalies, AnomalyCandidate{
				StableCode: payload.Code,
				Name:       payload.Name,
				// Category 留空：由 service 按编码前缀推导领域，不在提取器臆造
				Aliases:        payload.Aliases,
				SeverityOnline: payload.SeverityOnline,
				SeverityDict:   payload.SeverityDict,
				NodeKind:       payload.NodeKind,
				ClockHint:      payload.ClockHint,
				Domain:         payload.Domain,
				FragmentID:     fid,
			})
		}
		if payload.Source != "" && payload.Target != "" {
			// edge_kind 仅接受受控枚举，非法值回退 causal（不臆造语义）
			ek := model.EdgeKind(payload.EdgeKind)
			switch ek {
			case model.EdgeKindCausal, model.EdgeKindSameEvent, model.EdgeKindSameFamily:
			default:
				ek = model.EdgeKindCausal
			}
			// rel 仅接受受控枚举，非法值回退 trigger；非 causal 边忽略 rel
			rel := model.EdgeType(payload.Rel)
			if ek == model.EdgeKindCausal {
				switch rel {
				case model.EdgeTrigger, model.EdgeMitigate, model.EdgeIndicate, model.EdgePrevent:
				default:
					rel = model.EdgeTrigger
				}
			} else {
				rel = "" // 非因果关联不带 EdgeType 语义
			}
			res.CausalChains = append(res.CausalChains, CausalChainCandidate{
				SourceCode:      payload.Source,
				TargetCode:      payload.Target,
				EdgeKind:        string(ek),
				Rel:             rel,
				Via:             payload.Via, // mitigate 根因挂靠；非 mitigate 边由 service 忽略
				Conditions:      payload.Conditions,
				TimeConstraints: payload.TimeConstraints,
				FragmentID:      fid,
			})
		}
		if payload.Alert != "" && payload.Anomaly != "" {
			sig := payload.Alert
			// v2.1 结构化 signature：拼接键 domain|channel|sig_key（缺 sig_key 用 alert 原串）
			if payload.SigKey != "" {
				sig = payload.AlertDomain + "|" + payload.AlertChannel + "|" + payload.SigKey
			}
			res.AlertMappings = append(res.AlertMappings, AlertMappingCandidate{
				AlertSignature: sig,
				Domain:         payload.AlertDomain,
				Channel:        payload.AlertChannel,
				SigKey:         payload.SigKey,
				UpgradeRef:     payload.UpgradeRef,
				AnomalyCode:    payload.Anomaly,
				Confidence:     0.8,
				FragmentID:     fid,
			})
		}
	}
	return res, nil
}

// Validate 校验提取结果的必填完整性；缺失字段的候选被丢弃而非补造。
func (r *ExtractionResult) Validate() error {
	for i, a := range r.Anomalies {
		if a.StableCode == "" || a.Name == "" || a.FragmentID == "" {
			return fmt.Errorf("anomaly candidate %d missing stable_code/name/fragment_id", i)
		}
	}
	for i, c := range r.CausalChains {
		if c.SourceCode == "" || c.TargetCode == "" || c.FragmentID == "" {
			return fmt.Errorf("causal chain candidate %d missing source/target/fragment_id", i)
		}
	}
	for i, m := range r.AlertMappings {
		if m.AlertSignature == "" || m.AnomalyCode == "" || m.FragmentID == "" {
			return fmt.Errorf("alert mapping candidate %d missing signature/anomaly/fragment_id", i)
		}
	}
	return nil
}
