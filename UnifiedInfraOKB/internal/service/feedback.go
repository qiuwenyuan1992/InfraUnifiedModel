// feedback.go 运行时反馈飞轮：report 上报诊断使用的边与判定；
// confirmed 升置信度，rejected 降级入复核队列；同一 diagnosis_ref+edge 幂等。
package service

import (
	"fmt"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
)

// ReportInput report 原语输入。
type ReportInput struct {
	DiagnosisRef string     // 调用方诊断引用（幂等键之一）
	EdgeID       string     // 使用的因果边
	Verdict      model.Verdict
	OperatorID   string     // 服务端识别
	Notes        string
}

// ReportResult report 原语输出。
type ReportResult struct {
	Recorded     bool   // false = 重复上报（幂等忽略）
	NewConfidence float64
	NewStatus    model.KnowledgeStatus
	ReviewFlagged bool
}

// Report 处理一条反馈：
//  1. 幂等：diagnosis_ref+edge 唯一，重复上报不重复计数；
//  2. confirmed：置信度 +0.05（上限 1.0）；
//  3. rejected：置信度 -0.10（下限 0.0），draft/curated 均降级为 draft 并打复核标记；
//  4. 每次生效变更追加修订（内容快照 + reason），不覆盖历史。
func (s *Service) Report(in ReportInput) (*ReportResult, error) {
	if in.DiagnosisRef == "" || in.EdgeID == "" {
		return nil, fmt.Errorf("diagnosis_ref and edge_id are required")
	}
	if in.Verdict != model.VerdictConfirmed && in.Verdict != model.VerdictRejected {
		return nil, fmt.Errorf("verdict must be confirmed or rejected")
	}
	edge, err := s.store.GetCausalRule(in.EdgeID)
	if err != nil {
		return nil, fmt.Errorf("get edge: %w", err)
	}

	fb := &model.Feedback{
		DiagnosisRef: in.DiagnosisRef, EdgeID: in.EdgeID,
		Verdict: in.Verdict, OperatorID: in.OperatorID, Notes: in.Notes,
	}
	if err := s.store.AddFeedback(fb); err != nil {
		if err == repository.ErrDuplicate {
			// 幂等：同一诊断同一边的重复上报静默忽略
			return &ReportResult{Recorded: false, NewConfidence: edge.Confidence, NewStatus: edge.Status, ReviewFlagged: edge.ReviewFlagged}, nil
		}
		return nil, fmt.Errorf("add feedback: %w", err)
	}

	// 置信度回写与降级
	switch in.Verdict {
	case model.VerdictConfirmed:
		edge.Confidence += 0.05
		if edge.Confidence > 1.0 {
			edge.Confidence = 1.0
		}
	case model.VerdictRejected:
		edge.Confidence -= 0.10
		if edge.Confidence < 0 {
			edge.Confidence = 0
		}
		edge.Status = model.StatusDraft
		edge.ReviewFlagged = true
	}
	if err := s.store.UpdateCausalRule(edge); err != nil {
		return nil, fmt.Errorf("update edge: %w", err)
	}
	if err := s.appendRevision(model.EntityCausalRule, edge.ID, edge, in.OperatorID,
		fmt.Sprintf("feedback %s via report", in.Verdict)); err != nil {
		return nil, err
	}
	return &ReportResult{
		Recorded: true, NewConfidence: edge.Confidence,
		NewStatus: edge.Status, ReviewFlagged: edge.ReviewFlagged,
	}, nil
}

// ReviewQueue 复核队列：被否决降级后待人工复核的因果边（与知识清单同构的行）。
func (s *Service) ReviewQueue(limit int) ([]KnowledgeRow, error) {
	flagged, err := s.store.ListReviewFlagged(limit)
	if err != nil {
		return nil, fmt.Errorf("list review flagged: %w", err)
	}
	return s.ruleRows(flagged)
}

// ConfirmEdge 专家确认边（S2 治理操作）：升 curated、清复核标记、追加修订。
func (s *Service) ConfirmEdge(edgeID, operatorID string) error {
	edge, err := s.store.GetCausalRule(edgeID)
	if err != nil {
		return err
	}
	edge.Status = model.StatusCurated
	edge.ReviewFlagged = false
	if err := s.store.UpdateCausalRule(edge); err != nil {
		return err
	}
	return s.appendRevision(model.EntityCausalRule, edge.ID, edge, operatorID, "expert confirmed")
}

// RejectEdge 专家否决边（S2 治理操作）：降 draft、打复核标记、追加修订。
func (s *Service) RejectEdge(edgeID, operatorID string) error {
	edge, err := s.store.GetCausalRule(edgeID)
	if err != nil {
		return err
	}
	edge.Status = model.StatusDraft
	edge.ReviewFlagged = true
	if err := s.store.UpdateCausalRule(edge); err != nil {
		return err
	}
	return s.appendRevision(model.EntityCausalRule, edge.ID, edge, operatorID, "expert rejected")
}

// ConfirmAliasByID 专家确认别名归并建议（节点对齐：AI 建议、人确认）。
func (s *Service) ConfirmAliasByID(aliasID, operatorID string) error {
	if err := s.store.ConfirmAlias(aliasID); err != nil {
		return err
	}
	return s.appendRevision(model.EntityAnomalyType, aliasID, map[string]string{"alias_confirmed": aliasID}, operatorID, "alias confirmed")
}
