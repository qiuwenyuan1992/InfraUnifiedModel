// Package service 组织业务流程：摄取/提取/查询/反馈。
// handler 不直接访问数据库或调用模型；全部业务规则在此层。
package service

import (
	"fmt"
	"regexp"
	"strings"

	"UnifiedInfraOKB/internal/extractor"
	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
)

// Config service 层业务参数（印证阈值、抽样比例、trace 默认界）。
type Config struct {
	// CaseCurateThreshold 升 curated 需要的独立工单印证数，默认 2
	CaseCurateThreshold int
	// SampleRate 案例边抽样审核比例（0-1），仅记录用于复核抽样，不阻塞入图
	SampleRate float64
	// MaxHops trace 默认跳数上限
	MaxHops int
	// TopK trace 默认候选链数
	TopK int
}

// DefaultConfig 基线默认值（requirements.md 第 5 节：≥2 独立工单印证、10% 抽样）。
func DefaultConfig() Config {
	return Config{CaseCurateThreshold: 2, SampleRate: 0.10, MaxHops: 3, TopK: 5}
}

// Service 业务门面。
type Service struct {
	store *repository.Store
	ext   extractor.Extractor
	cfg   Config
}

// New 装配 service；ext 为 nil 时使用 FakeExtract（仅限开发/测试）。
func New(store *repository.Store, ext extractor.Extractor, cfg Config) *Service {
	if ext == nil {
		ext = &extractor.FakeExtract{}
	}
	if cfg.CaseCurateThreshold <= 0 {
		cfg.CaseCurateThreshold = 2
	}
	if cfg.MaxHops <= 0 {
		cfg.MaxHops = 3
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 5
	}
	return &Service{store: store, ext: ext, cfg: cfg}
}

// Store 暴露只读仓储给 handler 做简单透传（如健康检查）。
func (s *Service) Store() *repository.Store { return s.store }

// ---------- 脱敏校验 ----------

// sensitivePatterns 脱敏校验黑名单：命中任一模式即拒绝入库。
// 工单上游已脱敏；此校验是入库前最后一道防线（requirements.md 第 6 节 F4）。
var sensitivePatterns = []*regexp.Regexp{
	// IPv4（含内网任意段）
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	// 身份证（18 位含校验位 X）
	regexp.MustCompile(`\b\d{17}[\dXx]\b`),
	// 手机号
	regexp.MustCompile(`\b1[3-9]\d{9}\b`),
	// 邮箱
	regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`),
	// 常见凭据形态
	regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+`),
	// MAC 地址
	regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`),
}

// SanitizeCheck 校验文本是否含敏感信息。
// 返回 (通过, 首个命中的模式说明)；不修改原文——脱敏是上游职责，OKB 只做门禁。
func SanitizeCheck(text string) (bool, string) {
	for _, p := range sensitivePatterns {
		if p.MatchString(text) {
			return false, fmt.Sprintf("sensitive pattern matched: %s", strings.TrimSpace(p.String()))
		}
	}
	return true, ""
}

// 证据等级映射：来源种类 → 默认证据等级（生来自源，不事后提升）。
func levelFromKind(kind model.SourceKind) model.EvidenceLevel {
	switch kind {
	case model.SourceInternalCase:
		return model.EvidenceCaseVerified
	case model.SourceVendorDoc:
		return model.EvidenceDocExplicit
	case model.SourceExpertInput:
		return model.EvidenceExpertOnly
	default:
		return model.EvidenceInferred
	}
}

func evidenceKindFromLevel(l model.EvidenceLevel) model.EvidenceKind {
	switch l {
	case model.EvidenceCaseVerified:
		return model.EvidenceKindCase
	case model.EvidenceDocExplicit:
		return model.EvidenceKindDoc
	case model.EvidenceExpertOnly:
		return model.EvidenceKindExpert
	default:
		return model.EvidenceKindInfer
	}
}
