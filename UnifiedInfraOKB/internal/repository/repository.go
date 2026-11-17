// Package repository 管数据一致性：ID 生成、建表与各实体读写。
// 独立知识库连接（MySQL 生产 / 纯 Go SQLite 测试），不与拓扑库共享。
package repository

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"UnifiedInfraOKB/internal/model"
)

// ErrNotFound 实体不存在。
var ErrNotFound = errors.New("entity not found")

// ErrDuplicate 唯一键冲突（幂等场景由调用方区分）。
var ErrDuplicate = errors.New("duplicate entity")

// Store 仓储门面；所有方法均在调用方提供的事务或连接上执行。
type Store struct {
	DB *gorm.DB
}

// New 建表并返回仓储。AutoMigrate 仅用于开发与测试；生产 MySQL 使用 migrations 脚本后可跳过。
func New(db *gorm.DB, migrate bool) (*Store, error) {
	if migrate {
		if err := db.AutoMigrate(
			&model.Source{}, &model.SourceVersion{}, &model.ParsedVersion{}, &model.Fragment{},
			&model.AnomalyType{}, &model.AnomalyAlias{}, &model.CausalRule{}, &model.AlertMapping{},
			&model.Evidence{}, &model.Revision{}, &model.Feedback{},
			&model.Scenario{}, &model.ScenarioNode{},
		); err != nil {
			return nil, fmt.Errorf("automigrate: %w", err)
		}
	}
	return &Store{DB: db}, nil
}

// NewID 生成 16 字节随机 ID（32 位 hex）。
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败属系统级异常，直接 panic 由进程兜底
		panic(err)
	}
	return hex.EncodeToString(b)
}

func now() time.Time { return time.Now().UTC() }

// ---------- 不可变层 ----------

// CreateSource 登记数据源；internal_case 未过脱敏校验不得置 SensitivityCleared。
func (s *Store) CreateSource(src *model.Source) error {
	src.ID = NewID()
	src.CreatedAt = now()
	return s.DB.Create(src).Error
}

// GetSource 按 ID 读取来源。
func (s *Store) GetSource(id string) (*model.Source, error) {
	var src model.Source
	if err := s.DB.First(&src, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &src, nil
}

// CreateSourceVersion 原件版本：内容寻址；同哈希幂等返回既有版本。
func (s *Store) CreateSourceVersion(sv *model.SourceVersion) error {
	var exist model.SourceVersion
	err := s.DB.Where("content_hash = ?", sv.ContentHash).First(&exist).Error
	if err == nil {
		*sv = exist
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	sv.ID = NewID()
	sv.CreatedAt = now()
	return s.DB.Create(sv).Error
}

// CreateParsedVersion 解析版本：绑定固定原件版本。
func (s *Store) CreateParsedVersion(pv *model.ParsedVersion) error {
	pv.ID = NewID()
	pv.CreatedAt = now()
	return s.DB.Create(pv).Error
}

// CreateFragment 片段：绑定固定解析版本。
func (s *Store) CreateFragment(f *model.Fragment) error {
	f.ID = NewID()
	f.CreatedAt = now()
	return s.DB.Create(f).Error
}

// GetFragment 读取片段（evidence 回链用）。
func (s *Store) GetFragment(id string) (*model.Fragment, error) {
	var f model.Fragment
	if err := s.DB.First(&f, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

// ---------- 图谱层 ----------

// GetOrCreateAnomaly 按 stable_code 幂等建节点，返回节点与是否新建。
func (s *Store) GetOrCreateAnomaly(a *model.AnomalyType) (bool, error) {
	var exist model.AnomalyType
	err := s.DB.Where("stable_code = ?", a.StableCode).First(&exist).Error
	if err == nil {
		*a = exist
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	a.ID = NewID()
	ts := now()
	a.CreatedAt, a.UpdatedAt = ts, ts
	if a.Status == "" {
		a.Status = model.StatusDraft
	}
	if err := s.DB.Create(a).Error; err != nil {
		return false, err
	}
	return true, nil
}

// GetAnomalyByCode / GetAnomalyByID 节点读取。
func (s *Store) GetAnomalyByCode(code string) (*model.AnomalyType, error) {
	return s.getAnomaly("stable_code = ?", code)
}

func (s *Store) GetAnomalyByID(id string) (*model.AnomalyType, error) {
	return s.getAnomaly("id = ?", id)
}

func (s *Store) getAnomaly(where string, arg string) (*model.AnomalyType, error) {
	var a model.AnomalyType
	if err := s.DB.First(&a, where, arg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// SearchAnomalies 名称/别名模糊检索（专家界面与对齐建议用）。
func (s *Store) SearchAnomalies(keyword string, limit int) ([]model.AnomalyType, error) {
	if limit <= 0 {
		limit = 20
	}
	var list []model.AnomalyType
	err := s.DB.Where("name LIKE ? OR stable_code LIKE ?", "%"+keyword+"%", "%"+keyword+"%").
		Limit(limit).Find(&list).Error
	return list, err
}

// ListAllAnomalies 全部异常节点（图谱可视化视图用）。
func (s *Store) ListAllAnomalies(limit int) ([]model.AnomalyType, error) {
	if limit <= 0 {
		limit = 500
	}
	var list []model.AnomalyType
	err := s.DB.Limit(limit).Find(&list).Error
	return list, err
}

// ListAllCausalRules 全部因果边（图谱可视化视图用）。
func (s *Store) ListAllCausalRules(limit int) ([]model.CausalRule, error) {
	if limit <= 0 {
		limit = 1000
	}
	var list []model.CausalRule
	err := s.DB.Limit(limit).Find(&list).Error
	return list, err
}

// ListAnomaliesPage 异常节点分页列表（知识清单用）；status/q 可选过滤。
func (s *Store) ListAnomaliesPage(status model.KnowledgeStatus, q string, limit, offset int) ([]model.AnomalyType, int64, error) {
	tx := s.DB.Model(&model.AnomalyType{})
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	if q != "" {
		tx = tx.Where("stable_code LIKE ? OR name LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.AnomalyType
	err := tx.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// ListRulesPage 因果边分页列表（知识清单用）；status/q 可选过滤。
func (s *Store) ListRulesPage(status model.KnowledgeStatus, q string, limit, offset int) ([]model.CausalRule, int64, error) {
	tx := s.DB.Model(&model.CausalRule{})
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	if q != "" {
		tx = tx.Where("source_anomaly_id LIKE ? OR target_anomaly_id LIKE ?", "%"+q+"%", "%"+q+"%")
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.CausalRule
	err := tx.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// ListMappingsPage 告警映射分页列表（知识清单用）；status/q 可选过滤。
func (s *Store) ListMappingsPage(status model.KnowledgeStatus, q string, limit, offset int) ([]model.AlertMapping, int64, error) {
	tx := s.DB.Model(&model.AlertMapping{})
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	if q != "" {
		tx = tx.Where("alert_signature LIKE ?", "%"+q+"%")
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.AlertMapping
	err := tx.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// CreateAlias 厂商别名登记（AI 建议）；ConfirmAlias 人工确认后才参与对齐。
func (s *Store) CreateAlias(al *model.AnomalyAlias) error {
	al.ID = NewID()
	al.CreatedAt = now()
	return s.DB.Create(al).Error
}

func (s *Store) ConfirmAlias(id string) error {
	res := s.DB.Model(&model.AnomalyAlias{}).Where("id = ?", id).Update("confirmed", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetOrCreateCausalRule 按（源,目标,边类型）幂等建边，返回边与是否新建。
// 同一对节点可并存多条不同语义的边（如 trigger + mitigate）。
func (s *Store) GetOrCreateCausalRule(r *model.CausalRule) (bool, error) {
	if r.EdgeType == "" {
		r.EdgeType = model.EdgeTrigger
	}
	var exist model.CausalRule
	err := s.DB.Where("source_anomaly_id = ? AND target_anomaly_id = ? AND edge_type = ?",
		r.SourceAnomalyID, r.TargetAnomalyID, r.EdgeType).First(&exist).Error
	if err == nil {
		*r = exist
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	r.ID = NewID()
	ts := now()
	r.CreatedAt, r.UpdatedAt = ts, ts
	if r.Status == "" {
		r.Status = model.StatusDraft
	}
	if err := s.DB.Create(r).Error; err != nil {
		return false, err
	}
	return true, nil
}

// GetCausalRule 边读取。
func (s *Store) GetCausalRule(id string) (*model.CausalRule, error) {
	var r model.CausalRule
	if err := s.DB.First(&r, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

// UpdateCausalRule 更新边（置信度/状态/复核标记/当前修订）；修订由 service 层追加。
func (s *Store) UpdateCausalRule(r *model.CausalRule) error {
	r.UpdatedAt = now()
	return s.DB.Save(r).Error
}

// ListEdgesFrom 遍历：取某节点的出边（trace BFS / 处置查询用）。
// status/edgeType 为空串时不过滤；draft 是否入结果由 service 决定。
func (s *Store) ListEdgesFrom(anomalyID string, status model.KnowledgeStatus, edgeType model.EdgeType) ([]model.CausalRule, error) {
	q := s.DB.Where("source_anomaly_id = ?", anomalyID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if edgeType != "" {
		q = q.Where("edge_type = ?", edgeType)
	}
	var list []model.CausalRule
	err := q.Find(&list).Error
	return list, err
}

// MitigationsFor 处置查询：指向某异常的全部 mitigate 入边（手段 → 被缓解异常）。
func (s *Store) MitigationsFor(anomalyID string, status model.KnowledgeStatus) ([]model.CausalRule, error) {
	q := s.DB.Where("target_anomaly_id = ? AND edge_type = ?", anomalyID, model.EdgeMitigate)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var list []model.CausalRule
	err := q.Find(&list).Error
	return list, err
}

// GetOrCreateAlertMapping 按（signature,anomaly）幂等建入口。
func (s *Store) GetOrCreateAlertMapping(m *model.AlertMapping) (bool, error) {
	var exist model.AlertMapping
	err := s.DB.Where("alert_signature = ? AND anomaly_id = ?", m.AlertSignature, m.AnomalyID).First(&exist).Error
	if err == nil {
		*m = exist
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	m.ID = NewID()
	ts := now()
	m.CreatedAt, m.UpdatedAt = ts, ts
	if m.Status == "" {
		m.Status = model.StatusDraft
	}
	if err := s.DB.Create(m).Error; err != nil {
		return false, err
	}
	return true, nil
}

// UpdateAlertMapping 更新映射（印证计数升级状态）。
func (s *Store) UpdateAlertMapping(m *model.AlertMapping) error {
	m.UpdatedAt = now()
	return s.DB.Save(m).Error
}

// ListMappingsByAlert lookup 入口：signature → 候选异常映射。
func (s *Store) ListMappingsByAlert(signature string, status model.KnowledgeStatus) ([]model.AlertMapping, error) {
	q := s.DB.Where("alert_signature = ?", signature)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var list []model.AlertMapping
	err := q.Find(&list).Error
	return list, err
}

// ---------- 证据与修订层 ----------

// AddEvidence 追加证据记录（不覆盖）；返回该实体的独立来源印证计数。
// 印证计数按 SourceID 去重——同一工单多条断言只算一次支撑。
func (s *Store) AddEvidence(ev *model.Evidence) (int, error) {
	ev.ID = NewID()
	ev.CreatedAt = now()
	if err := s.DB.Create(ev).Error; err != nil {
		return 0, err
	}
	return s.CountDistinctSupport(ev.EntityType, ev.EntityID)
}

// CountDistinctSupport 统计实体证据的独立来源数。
func (s *Store) CountDistinctSupport(et model.EntityType, entityID string) (int, error) {
	var n int64
	err := s.DB.Model(&model.Evidence{}).
		Where("entity_type = ? AND entity_id = ?", et, entityID).
		Distinct("source_id").Count(&n).Error
	return int(n), err
}

// ListEvidenceByEntity 实体的全部证据（evidence 原语用）。
func (s *Store) ListEvidenceByEntity(et model.EntityType, entityID string) ([]model.Evidence, error) {
	var list []model.Evidence
	err := s.DB.Where("entity_type = ? AND entity_id = ?", et, entityID).Find(&list).Error
	return list, err
}

// AppendRevision 追加式修订：插入新行，不覆盖旧行；返回修订 ID。
func (s *Store) AppendRevision(rev *model.Revision) error {
	rev.ID = NewID()
	rev.CreatedAt = now()
	return s.DB.Create(rev).Error
}

// LatestRevision 实体当前修订（钉 revision_id 用）。
func (s *Store) LatestRevision(et model.EntityType, entityID string) (*model.Revision, error) {
	var rev model.Revision
	err := s.DB.Where("entity_type = ? AND entity_id = ?", et, entityID).
		Order("created_at DESC").First(&rev).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &rev, nil
}

// AddFeedback 反馈飞轮入口：diagnosis_ref+edge 唯一索引保证幂等。
// 返回 ErrDuplicate 表示重复上报，不重复计数。
func (s *Store) AddFeedback(fb *model.Feedback) error {
	fb.ID = NewID()
	fb.CreatedAt = now()
	res := s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(fb)
	if res.Error != nil {
		return res.Error
	}
	// 冲突被 DoNothing 跳过时 RowsAffected=0，即重复上报
	if res.RowsAffected == 0 {
		return ErrDuplicate
	}
	return nil
}

// ListFeedbackByEdge 边的反馈历史（置信度回写与复核队列用）。
func (s *Store) ListFeedbackByEdge(edgeID string) ([]model.Feedback, error) {
	var list []model.Feedback
	err := s.DB.Where("edge_id = ?", edgeID).Find(&list).Error
	return list, err
}

// ListReviewFlagged 复核队列：被否决降级后待人工复核的边。
func (s *Store) ListReviewFlagged(limit int) ([]model.CausalRule, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []model.CausalRule
	err := s.DB.Where("review_flagged = ?", true).Limit(limit).Find(&list).Error
	return list, err
}

// ---------- 场景 ----------

// GetOrCreateScenario 按 code 幂等建场景。
func (s *Store) GetOrCreateScenario(sc *model.Scenario) (bool, error) {
	var exist model.Scenario
	err := s.DB.Where("code = ?", sc.Code).First(&exist).Error
	if err == nil {
		*sc = exist
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	sc.ID = NewID()
	ts := now()
	sc.CreatedAt, sc.UpdatedAt = ts, ts
	if err := s.DB.Create(sc).Error; err != nil {
		return false, err
	}
	return true, nil
}

// ListScenarios 全部场景（卡片视图用）。
func (s *Store) ListScenarios() ([]model.Scenario, error) {
	var list []model.Scenario
	err := s.DB.Order("domain ASC, code ASC").Find(&list).Error
	return list, err
}

// GetScenarioByCode 按 code 取场景。
func (s *Store) GetScenarioByCode(code string) (*model.Scenario, error) {
	var sc model.Scenario
	if err := s.DB.First(&sc, "code = ?", code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sc, nil
}

// AddScenarioNode 场景成员指派（幂等：唯一索引 + 冲突忽略）。
func (s *Store) AddScenarioNode(scenarioID, anomalyID string) error {
	sn := model.ScenarioNode{ID: NewID(), ScenarioID: scenarioID, AnomalyID: anomalyID, CreatedAt: now()}
	if err := s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&sn).Error; err != nil {
		return err
	}
	return nil
}

// ListScenarioNodes 场景成员（含异常信息由 service 组装）。
func (s *Store) ListScenarioNodes(scenarioID string) ([]model.ScenarioNode, error) {
	var list []model.ScenarioNode
	err := s.DB.Where("scenario_id = ?", scenarioID).Find(&list).Error
	return list, err
}
