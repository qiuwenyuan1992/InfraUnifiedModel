// ingest.go 摄取管线：登记来源 → 原件/解析/片段 → LLM 结构化提取 →
// 候选接纳（draft 入图 + 证据回链 + 印证计数升 curated）。
package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"UnifiedInfraOKB/internal/model"
)

// IngestInput 摄取一份资料（internal_case/vendor_doc/expert_input）。
type IngestInput struct {
	Kind       model.SourceKind
	Title      string
	Content    string // 脱敏后正文
	OperatorID string // 服务端识别的操作主体
}

// domainFromCode 从 stable_code 前缀推导领域分类（场景组织维度）。
// 约定编码带领域前缀（如 net.port.* / rack.power.* / gpu.*）；未识别前缀归 other。
// 显式 Category 优先，此推导只是缺省值——不臆造精细分类。
var domainPrefixes = []struct {
	prefix  string
	domains string
}{
	{"net.", "network"},
	{"rack.power.", "power"},
	{"rack.", "rack"},
	{"ups.", "power"},
	{"psu.", "power"},
	{"gpu.", "gpu"},
	{"hca.", "hca"},
	{"pcie.", "pcie"},
	{"server.", "server"},
	{"storage.", "storage"},
	{"action.", "action"},
}

func domainFromCode(code string) string {
	for _, p := range domainPrefixes {
		if strings.HasPrefix(code, p.prefix) {
			return p.domains
		}
	}
	return "other"
}

// IngestOutput 摄取结果：登记与接纳统计。
type IngestOutput struct {
	SourceID        string
	SourceVersionID string
	ParsedVersionID string
	ExtractedModel  string
	NewAnomalies    int
	NewRules        int
	NewMappings     int
	CuratedRules    int // 本次自动升 curated 的边数
}

// Ingest 一条龙摄取：脱敏校验门禁 → 登记来源 → 固定版本 → 提取 → 接纳候选。
// 校验失败立即拒绝，不入库（F4）；全部候选为 draft，案例印证达标才升 curated。
func (s *Service) Ingest(ctx context.Context, in IngestInput) (*IngestOutput, error) {
	if in.Title == "" || in.Content == "" {
		return nil, fmt.Errorf("title and content are required")
	}
	if ok, reason := SanitizeCheck(in.Content); !ok {
		return nil, fmt.Errorf("sensitivity check failed: %s", reason)
	}

	// 1. 登记来源（过门禁后 sensitivity_cleared=true）
	src := &model.Source{
		Kind:              in.Kind,
		Title:             in.Title,
		SensitivityCleared: true,
		PermissionLevel:    1,
	}
	if err := s.store.CreateSource(src); err != nil {
		return nil, fmt.Errorf("create source: %w", err)
	}

	// 2. 原件版本（内容寻址）+ 解析版本
	sum := sha256.Sum256([]byte(in.Content))
	sv := &model.SourceVersion{SourceID: src.ID, ContentHash: fmt.Sprintf("%x", sum)}
	if err := s.store.CreateSourceVersion(sv); err != nil {
		return nil, fmt.Errorf("create source version: %w", err)
	}
	pv := &model.ParsedVersion{SourceVersionID: sv.ID, ParserVersion: "raw/v1", ContentHash: sv.ContentHash}
	if err := s.store.CreateParsedVersion(pv); err != nil {
		return nil, fmt.Errorf("create parsed version: %w", err)
	}

	// 3. 片段：按空行切块，保持原文可回链
	fragments, err := s.createFragments(pv.ID, in.Content)
	if err != nil {
		return nil, err
	}

	// 4. 提取
	res, err := s.ext.Extract(ctx, in.Content, fragments)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("extractor returned nil result")
	}
	if err := res.Validate(); err != nil {
		return nil, fmt.Errorf("invalid extraction: %w", err)
	}

	out := &IngestOutput{SourceID: src.ID, SourceVersionID: sv.ID, ParsedVersionID: pv.ID, ExtractedModel: res.ModelVersion}
	// 证据等级/种类生来自源，一次计算全程复用
	level := levelFromKind(in.Kind)
	ekind := evidenceKindFromLevel(level)
	fragByID := make(map[string]model.Fragment, len(fragments))
	for _, f := range fragments {
		fragByID[f.ID] = f
	}

	// 5. 接纳异常候选（幂等），别名登记为 AI 建议（待人工确认）
	for _, a := range res.Anomalies {
		category := a.Category
		if category == "" {
			category = domainFromCode(a.StableCode)
		}
		node := &model.AnomalyType{StableCode: a.StableCode, Name: a.Name, Category: category}
		created, err := s.store.GetOrCreateAnomaly(node)
		if err != nil {
			return nil, fmt.Errorf("accept anomaly %s: %w", a.StableCode, err)
		}
		if created {
			out.NewAnomalies++
			if err := s.appendRevision(model.EntityAnomalyType, node.ID, node, in.OperatorID, "ingest accept"); err != nil {
				return nil, err
			}
		}
		if frag, ok := fragByID[a.FragmentID]; ok {
			if _, err := s.store.AddEvidence(&model.Evidence{
				Kind: ekind, EntityType: model.EntityAnomalyType,
				EntityID: node.ID, FragmentID: frag.ID, SourceID: src.ID, CreatedBy: model.CreatorModel,
			}); err != nil {
				return nil, fmt.Errorf("add anomaly evidence: %w", err)
			}
		}
		for _, alias := range a.Aliases {
			_ = s.store.CreateAlias(&model.AnomalyAlias{AnomalyID: node.ID, Alias: alias, Vendor: "synthetic"})
		}
	}

	// 6. 接纳因果边候选 + 印证计数升 curated
	for _, c := range res.CausalChains {
		srcNode, err := s.store.GetAnomalyByCode(c.SourceCode)
		if err != nil {
			return nil, fmt.Errorf("causal source %s unresolved: %w", c.SourceCode, err)
		}
		dstNode, err := s.store.GetAnomalyByCode(c.TargetCode)
		if err != nil {
			return nil, fmt.Errorf("causal target %s unresolved: %w", c.TargetCode, err)
		}
		var condJSON string
		if len(c.Conditions) > 0 {
			b, err := json.Marshal(c.Conditions)
			if err != nil {
				return nil, fmt.Errorf("marshal conditions: %w", err)
			}
			condJSON = string(b)
		}
		rule := &model.CausalRule{
			SourceAnomalyID: srcNode.ID, TargetAnomalyID: dstNode.ID,
			EdgeType: c.Rel, Conditions: condJSON, EvidenceLevel: level, Confidence: 0.5,
		}
		created, err := s.store.GetOrCreateCausalRule(rule)
		if err != nil {
			return nil, fmt.Errorf("accept causal rule %s->%s: %w", c.SourceCode, c.TargetCode, err)
		}
		if created {
			out.NewRules++
			if err := s.appendRevision(model.EntityCausalRule, rule.ID, rule, in.OperatorID, "ingest accept"); err != nil {
				return nil, err
			}
		}
		if frag, ok := fragByID[c.FragmentID]; ok {
			support, err := s.store.AddEvidence(&model.Evidence{
				Kind: ekind, EntityType: model.EntityCausalRule,
				EntityID: rule.ID, FragmentID: frag.ID, SourceID: src.ID, CreatedBy: model.CreatorModel,
			})
			if err != nil {
				return nil, fmt.Errorf("add causal evidence: %w", err)
			}
			// 印证计数：独立来源支撑达到阈值自动升 curated（requirements.md 5.2）
			rule.CaseSupportCount = support
			if support >= s.cfg.CaseCurateThreshold && rule.Status == model.StatusDraft {
				rule.Status = model.StatusCurated
				out.CuratedRules++
				if err := s.appendRevision(model.EntityCausalRule, rule.ID, rule, in.OperatorID,
					fmt.Sprintf("auto-curated: %d independent sources", support)); err != nil {
					return nil, err
				}
			}
			if err := s.store.UpdateCausalRule(rule); err != nil {
				return nil, fmt.Errorf("update causal rule: %w", err)
			}
		}
	}

	// 7. 接纳告警映射候选
	for _, m := range res.AlertMappings {
		node, err := s.store.GetAnomalyByCode(m.AnomalyCode)
		if err != nil {
			return nil, fmt.Errorf("alert mapping anomaly %s unresolved: %w", m.AnomalyCode, err)
		}
		mapping := &model.AlertMapping{
			AlertSignature: m.AlertSignature, AnomalyID: node.ID,
			Confidence: m.Confidence, EvidenceLevel: level,
		}
		created, err := s.store.GetOrCreateAlertMapping(mapping)
		if err != nil {
			return nil, fmt.Errorf("accept alert mapping %s: %w", m.AlertSignature, err)
		}
		if created {
			out.NewMappings++
			if err := s.appendRevision(model.EntityAlertMapping, mapping.ID, mapping, in.OperatorID, "ingest accept"); err != nil {
				return nil, err
			}
		}
		if frag, ok := fragByID[m.FragmentID]; ok {
			support, err := s.store.AddEvidence(&model.Evidence{
				Kind: ekind, EntityType: model.EntityAlertMapping,
				EntityID: mapping.ID, FragmentID: frag.ID, SourceID: src.ID, CreatedBy: model.CreatorModel,
			})
			if err != nil {
				return nil, fmt.Errorf("add mapping evidence: %w", err)
			}
			// 印证计数：告警映射与因果边同规则升级（lookup 默认只返回 curated）
			if support >= s.cfg.CaseCurateThreshold && mapping.Status == model.StatusDraft {
				mapping.Status = model.StatusCurated
				if err := s.appendRevision(model.EntityAlertMapping, mapping.ID, mapping, in.OperatorID,
					fmt.Sprintf("auto-curated: %d independent sources", support)); err != nil {
					return nil, err
				}
			}
			if err := s.store.UpdateAlertMapping(mapping); err != nil {
				return nil, fmt.Errorf("update mapping: %w", err)
			}
		}
	}

	return out, nil
}

// textBlock 文本块及其在原文中的起始偏移（出处回链定位用）。
type textBlock struct {
	start int
	text  string
}

// createFragments 按空行切块登记片段；返回片段列表用于提取与回链。
func (s *Service) createFragments(parsedVersionID, content string) ([]model.Fragment, error) {
	blocks := splitBlocks(content)
	fragments := make([]model.Fragment, 0, len(blocks))
	for _, b := range blocks {
		f := model.Fragment{
			ParsedVersionID: parsedVersionID,
			CharStart:       b.start,
			CharEnd:         b.start + len(b.text),
			Summary:         truncate(b.text, 120),
			Content:         b.text,
			PermissionLevel: 1,
		}
		if err := s.store.CreateFragment(&f); err != nil {
			return nil, fmt.Errorf("create fragment: %w", err)
		}
		fragments = append(fragments, f)
	}
	return fragments, nil
}

// splitBlocks 按空行切块，记录每块在原文中的字符偏移；空白块丢弃。
func splitBlocks(content string) []textBlock {
	var out []textBlock
	start := 0
	for i := 0; i+1 < len(content); i++ {
		if content[i] == '\n' && content[i+1] == '\n' {
			if blk := strings.TrimSpace(content[start:i]); blk != "" {
				out = append(out, textBlock{start: start, text: blk})
			}
			start = i + 2
			i++
		}
	}
	if blk := strings.TrimSpace(content[start:]); blk != "" {
		out = append(out, textBlock{start: start, text: blk})
	}
	return out
}

// truncate 按字符（非字节）截断，避免中文被切半个字。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// appendRevision 追加式修订：插入新行，不覆盖。
func (s *Service) appendRevision(et model.EntityType, entityID string, snapshot any, operator, reason string) error {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	latest, lerr := s.store.LatestRevision(et, entityID)
	rev := &model.Revision{
		EntityType: et, EntityID: entityID,
		ContentSnapshot: string(b),
		ChangedBy:       operator, Reason: reason,
		CreatedAt: time.Now().UTC(),
	}
	if lerr == nil && latest != nil {
		rev.PrevRevisionID = &latest.ID
	}
	return s.store.AppendRevision(rev)
}
