// Package model 定义 OKB v2 领域模型：不可变层（来源/原件/解析/片段）、
// 图谱层（异常节点/因果边/告警入口）、证据与修订层（证据/追加式修订/运行时反馈）。
package model

import "time"

// 通用约定：所有 ID 为 ULID 风格字符串（由 repository 层生成）；
// 时间统一 UTC；内容快照/条件使用受 Schema 约束的 JSON 文本。

// ---------- 枚举 ----------

// SourceKind 数据源种类：脱敏工单为一等公民，厂商文档与专家经验为补充源。
type SourceKind string

const (
	SourceInternalCase SourceKind = "internal_case"
	SourceVendorDoc    SourceKind = "vendor_doc"
	SourceExpertInput  SourceKind = "expert_input"
)

// EvidenceLevel 证据等级：生来自源、不事后提升。
type EvidenceLevel string

const (
	EvidenceCaseVerified EvidenceLevel = "case_verified"
	EvidenceDocExplicit  EvidenceLevel = "doc_explicit"
	EvidenceInferred     EvidenceLevel = "inferred"
	EvidenceExpertOnly   EvidenceLevel = "expert_only"
)

// EvidenceKind 证据种类（证据记录指向的来源类型）。
type EvidenceKind string

const (
	EvidenceKindCase   EvidenceKind = "case"
	EvidenceKindDoc    EvidenceKind = "doc"
	EvidenceKindInfer  EvidenceKind = "inferred"
	EvidenceKindExpert EvidenceKind = "expert"
)

// KnowledgeStatus 知识状态仅两态。
type KnowledgeStatus string

const (
	StatusDraft   KnowledgeStatus = "draft"
	StatusCurated KnowledgeStatus = "curated"
)

// EdgeType 边的语义类型（对照 XiHe 因果图的边分类）。
// 方向约定：trigger/mitigate/prevent 从"因"指向"果"（处置手段 → 被缓解异常）；
// indicate 从"根因异常"指向"表现症状"。trace 只遍历 trigger。
type EdgeType string

const (
	// EdgeTrigger A 导致/传播 B：根因链遍历的唯一合法路径
	EdgeTrigger EdgeType = "trigger"
	// EdgeIndicate A 表现为症状 B：正向症状解释（A 发生时会看到 B）
	EdgeIndicate EdgeType = "indicate"
	// EdgeMitigate 手段 A 缓解异常 B：处置建议（更换 X 后 B 恢复）
	EdgeMitigate EdgeType = "mitigate"
	// EdgePrevent 措施 A 预防异常 B：长期运维建议
	EdgePrevent EdgeType = "prevent"
)

// CreatorKind 知识创建者与依据分别记录。
type CreatorKind string

const (
	CreatorModel CreatorKind = "model"
	CreatorExpert CreatorKind = "expert"
)

// EntityType 修订历史覆盖的实体类型。
type EntityType string

const (
	EntityAnomalyType EntityType = "anomaly_type"
	EntityCausalRule  EntityType = "causal_rule"
	EntityAlertMapping EntityType = "alert_mapping"
)

// Verdict 反馈判定：确认或否决；否决记录本身作为反证入库。
type Verdict string

const (
	VerdictConfirmed Verdict = "confirmed"
	VerdictRejected  Verdict = "rejected"
)

// ---------- 不可变层 ----------

// Source 数据源登记。工单入库前须过脱敏校验，通过后打 sensitivity_cleared。
type Source struct {
	ID               string       `gorm:"primaryKey;size:36"`
	Kind             SourceKind   `gorm:"size:20;index"`
	Title            string       `gorm:"size:256"`
	SensitivityCleared bool       `gorm:"default:false"` // 脱敏校验通过标记；未通过不得入图
	PermissionLevel  int          `gorm:"default:0"`    // 读取权限分级
	StorageURI       string       `gorm:"size:512"`     // 原件在对象存储中的位置
	CreatedAt        time.Time
}

// SourceVersion 原件版本：内容寻址，不可变。
type SourceVersion struct {
	ID            string    `gorm:"primaryKey;size:36"`
	SourceID      string    `gorm:"size:36;index"`
	ContentHash   string    `gorm:"size:64;uniqueIndex"` // sha256，内容寻址
	StorageURI    string    `gorm:"size:512"`
	CreatedAt     time.Time
}

// ParsedVersion 解析版本：绑定固定原件版本，不可变。
type ParsedVersion struct {
	ID             string    `gorm:"primaryKey;size:36"`
	SourceVersionID string   `gorm:"size:36;index"`
	ParserVersion  string    `gorm:"size:32"`
	ContentHash    string    `gorm:"size:64"`
	CreatedAt      time.Time
}

// Fragment 文本片段：绑定固定解析版本；权限校验到 fragment 级，未授权只返回摘要。
type Fragment struct {
	ID              string    `gorm:"primaryKey;size:36"`
	ParsedVersionID string    `gorm:"size:36;index"`
	CharStart       int
	CharEnd         int
	Page            int       // 页码（PDF 等），无页概念时为 0
	ParagraphPath   string    `gorm:"size:256"`
	Summary         string    `gorm:"size:512"` // 摘要：未授权调用方唯一可见内容
	Content         string    // 原文内容（中文字段注释：脱敏后）
	PermissionLevel int       `gorm:"default:0"`
	CreatedAt       time.Time
}

// ---------- 图谱层 ----------

// AnomalyType 图节点：stable_code 唯一键。
type AnomalyType struct {
	ID                 string    `gorm:"primaryKey;size:36"`
	StableCode         string    `gorm:"size:128;uniqueIndex"`
	Name               string    `gorm:"size:256"`
	Category           string    `gorm:"size:64"`
	CurrentRevisionID  string    `gorm:"size:36"`
	Status             KnowledgeStatus `gorm:"size:16;default:draft"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// AnomalyAlias 厂商别名 → stable_code 的节点对齐表：AI 出建议、人确认。
type AnomalyAlias struct {
	ID         string    `gorm:"primaryKey;size:36"`
	AnomalyID  string    `gorm:"size:36;index"`
	Alias      string    `gorm:"size:256;index"`
	Vendor     string    `gorm:"size:64"`
	Confirmed  bool      `gorm:"default:false"` // 人工确认后才参与对齐
	CreatedAt  time.Time
}

// ConditionClause 受 Schema 约束的条件子句（JSON 反序列化用）。
// FactKey 为观测事实键；Op 仅支持受控枚举，不执行任意脚本。
type ConditionClause struct {
	FactKey string `json:"fact_key"`
	Op      string `json:"op"` // eq/ne/gt/lt/gte/lte/exists
	Value   interface{} `json:"value"`
}

// CausalRule 图边：源异常 → 目标异常，语义由 EdgeType 决定。
// Conditions / TimeConstraints 为受 Schema 约束的 JSON 数组文本（[]ConditionClause）。
// 印证计数：>=2 个独立工单证据支持才可升 curated。
type CausalRule struct {
	ID                string    `gorm:"primaryKey;size:36"`
	SourceAnomalyID   string    `gorm:"size:36;index"`
	TargetAnomalyID   string    `gorm:"size:36;index"`
	EdgeType          EdgeType  `gorm:"size:16;default:trigger;index"` // trigger / indicate / mitigate / prevent
	// MitigatedViaRootCause：mitigate 边的根因挂靠——"处置手段 A 缓解异常 B（当根因是 C 时）"。
	// 空 = 无条件适用；非空 = 仅当诊断结论的根因路径包含 C 时该处置建议生效（防张冠李戴）。
	MitigatedViaRootCause *string   `gorm:"size:36"`
	Conditions        string    // JSON []ConditionClause，可为空（无条件边）
	TimeConstraints   string    `gorm:"type:text"` // JSON，时间约束
	EvidenceLevel     EvidenceLevel `gorm:"size:20"`
	Confidence        float64   // 反馈飞轮回写
	CaseSupportCount  int       // 独立工单印证计数
	CurrentRevisionID string    `gorm:"size:36"`
	Status            KnowledgeStatus `gorm:"size:16;default:draft"`
	ReviewFlagged     bool      `gorm:"default:false"` // 否决降级后的复核标记
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Scenario 场景：领域内有主题的知识分组（组织维度：领域 → 场景 → 知识）。
// 场景由专家或提取管线显式定义与指派，不做自动聚类臆造。
type Scenario struct {
	ID          string    `gorm:"primaryKey;size:36"`
	Code        string    `gorm:"size:128;uniqueIndex"` // scenario.net.link.port 等
	Name        string    `gorm:"size:128"`
	Domain      string    `gorm:"size:32;index"`      // 归属领域（network/gpu/power...）
	Description string    `gorm:"size:512"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ScenarioNode 场景成员：节点可属多个场景；跨场景边自然存在于图上。
type ScenarioNode struct {
	ID         string    `gorm:"primaryKey;size:36"`
	ScenarioID string    `gorm:"size:36;uniqueIndex:idx_scenario_node"`
	AnomalyID  string    `gorm:"size:36;uniqueIndex:idx_scenario_node"`
	CreatedAt  time.Time
}

// AlertMapping 入口：告警 signature → 候选异常。
type AlertMapping struct {
	ID                string    `gorm:"primaryKey;size:36"`
	AlertSignature    string    `gorm:"size:256;index"`
	AnomalyID         string    `gorm:"size:36;index"`
	Confidence        float64
	EvidenceLevel     EvidenceLevel `gorm:"size:20"`
	CurrentRevisionID string    `gorm:"size:36"`
	Status            KnowledgeStatus `gorm:"size:16;default:draft"`
	ReviewFlagged     bool      `gorm:"default:false"` // 否决降级后的复核标记（与边同规则）
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ---------- 证据与修订层 ----------

// Evidence 统一证据记录：种类 → fragment 回链 → 核验状态。
type Evidence struct {
	ID          string       `gorm:"primaryKey;size:36"`
	Kind        EvidenceKind `gorm:"size:20"`
	EntityType  EntityType   `gorm:"size:20"`
	EntityID    string       `gorm:"size:36;index"` // 指向 AnomalyType/CausalRule/AlertMapping
	FragmentID  string       `gorm:"size:36;index"` // 出处回链
	SourceID    string       `gorm:"size:36"`      // 独立工单印证计数按此去重
	CreatedBy   CreatorKind  `gorm:"size:10"`
	Note        string       `gorm:"size:512"`
	CreatedAt   time.Time
}

// Revision 追加式修订历史：每次变更插入新行，不覆盖旧行。
type Revision struct {
	ID              string      `gorm:"primaryKey;size:36"`
	EntityType      EntityType   `gorm:"size:20;index"`
	EntityID        string      `gorm:"size:36;index"`
	ContentSnapshot string      `gorm:"type:text"` // 变更时点内容 JSON 快照
	PrevRevisionID  *string     `gorm:"size:36"`   // 前驱修订，首版为 NULL
	ChangedBy       string      `gorm:"size:128"`  // 服务端识别的操作主体
	Reason          string      `gorm:"size:512"`
	CreatedAt       time.Time
}

// Feedback 运行时反馈：report 上报的诊断使用边与判定；同一 diagnosis_ref+edge 幂等。
type Feedback struct {
	ID            string    `gorm:"primaryKey;size:36"`
	DiagnosisRef  string    `gorm:"size:128;uniqueIndex:idx_feedback_unique"`
	EdgeID        string    `gorm:"size:36;uniqueIndex:idx_feedback_unique"`
	Verdict       Verdict   `gorm:"size:12"`
	OperatorID    string    `gorm:"size:128"` // 服务端识别
	Notes         string    `gorm:"size:512"`
	CreatedAt     time.Time
}
