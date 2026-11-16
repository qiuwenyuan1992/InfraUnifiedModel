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

// AnomalyCandidate 异常候选。
type AnomalyCandidate struct {
	StableCode string   `json:"stable_code"`
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Aliases    []string `json:"aliases,omitempty"`
	FragmentID string   `json:"fragment_id"` // 出处回链
}

// CausalChainCandidate 因果边候选。
type CausalChainCandidate struct {
	SourceCode string              `json:"source_code"`
	TargetCode string              `json:"target_code"`
	Conditions []model.ConditionClause `json:"conditions,omitempty"`
	FragmentID string              `json:"fragment_id"`
}

// AlertMappingCandidate 告警映射候选。
type AlertMappingCandidate struct {
	AlertSignature string  `json:"alert_signature"`
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
			Source  string   `json:"source"`
			Target  string   `json:"target"`
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
				Category:   "synthetic",
				Aliases:    payload.Aliases,
				FragmentID: fid,
			})
		}
		if payload.Source != "" && payload.Target != "" {
			res.CausalChains = append(res.CausalChains, CausalChainCandidate{
				SourceCode: payload.Source,
				TargetCode: payload.Target,
				FragmentID: fid,
			})
		}
		if payload.Alert != "" && payload.Anomaly != "" {
			res.AlertMappings = append(res.AlertMappings, AlertMappingCandidate{
				AlertSignature: payload.Alert,
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
