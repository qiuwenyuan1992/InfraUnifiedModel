// reset.go 全库重置：清空全部知识表（保留表结构）。
// 用途：老 demo 数据大概率错误且带被污染的 case_verified/curated 等级（v2 演示期
// 合成工单自动升级所致），按 OKB 纪律不应保留——"全部数据按更新后的 OKB 走一遍"
// 即 ResetAll + Seed + RebuildScenarios。仅限开发库使用。
package seeder

import (
	"fmt"

	"gorm.io/gorm"

	"UnifiedInfraOKB/internal/model"
	"UnifiedInfraOKB/internal/repository"
)

// ResetAll 清空全部知识表（依赖表序无所谓，TRUNCATE/DELETE 均不带外键约束）。
// 只清数据不删表；生产库禁止执行（AGENTS：不得自动连接生产数据库）。
func ResetAll(store *repository.Store) error {
	// 清空顺序：先证据/修订/反馈（引用边），再图，最后不可变层
	tables := []any{
		&model.Evidence{}, &model.Revision{}, &model.Feedback{},
		&model.AnomalyAlias{}, &model.CausalRule{}, &model.AlertMapping{}, &model.AnomalyType{},
		&model.ScenarioNode{}, &model.Scenario{},
		&model.Fragment{}, &model.ParsedVersion{}, &model.SourceVersion{}, &model.Source{},
	}
	for _, t := range tables {
		if err := store.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(t).Error; err != nil {
			return fmt.Errorf("reset %T: %w", t, err)
		}
	}
	return nil
}
