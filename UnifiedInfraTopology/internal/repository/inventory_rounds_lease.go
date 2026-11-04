package repository

import (
	"context"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *inventoryRoundRepository) mutateRound(ctx context.Context, id string, now time.Time, work func(*gorm.DB, *model.SyncRound) error) error {
	if now.IsZero() {
		return ErrInventoryConflict
	}
	expired := false
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var round model.SyncRound
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&round).Error; err != nil {
			return inventoryDBError(err)
		}
		if round.Status != "running" {
			return ErrInventoryConflict
		}
		if round.LeaseExpiresAt == nil || !round.LeaseExpiresAt.After(now) {
			if err := terminateSyncRoundWork(db, id, "failed", syncErrorLeaseExpired, now); err != nil {
				return err
			}
			if err := db.Model(&model.SyncRound{}).Where("id = ?", id).Updates(map[string]interface{}{"status": "failed", "error_code": syncErrorLeaseExpired, "finished_at": now, "lease_expires_at": nil}).Error; err != nil {
				return err
			}
			// 先提交过期状态，再向调用者报告租约冲突，避免错误返回回滚失败记录。
			expired = true
			return nil
		}
		return work(db, &round)
	})
	if err == nil && expired {
		return ErrInventoryConflict
	}
	return err
}
