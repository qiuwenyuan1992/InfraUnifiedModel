package repository

import (
	"context"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 与未来发布事务保持相同的 scope -> run 加锁顺序。
func (r *inventoryRepository) lockInventoryScope(ctx context.Context, id string) (*model.TopologyScope, error) {
	db := r.r.DB(ctx).WithContext(ctx)
	if db.Dialector.Name() == "sqlite" {
		// SQLite 不支持 FOR UPDATE；先取得写锁，避免读事务升级竞争。
		if err := db.Model(&model.TopologyScope{}).Where("id = ?", id).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return nil, err
		}
	}
	var scope model.TopologyScope
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&scope).Error
	return &scope, inventoryDBError(err)
}

func (r *inventoryRepository) Enqueue(ctx context.Context, request *model.SyncRun, sourceIDs []string) (*model.SyncRun, error) {
	var result *model.SyncRun
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		scope, err := r.lockInventoryScope(txCtx, request.ScopeID)
		if err != nil {
			return err
		}
		db := r.r.DB(txCtx).WithContext(txCtx)
		var existing model.SyncRun
		err = db.Where("scope_id = ? AND idempotency_key = ?", request.ScopeID, request.IdempotencyKey).First(&existing).Error
		if err == nil {
			if existing.RequestHash != request.RequestHash {
				return ErrInventoryIdempotency
			}
			result = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// 重放先于可变来源、活动指针校验；请求哈希仍保留调用者的 null base。
		var sourceCount int64
		if err = db.Model(&model.Source{}).Where("scope_id = ? AND id IN ? AND enabled = ?", request.ScopeID, sourceIDs, true).Count(&sourceCount).Error; err != nil {
			return err
		}
		if sourceCount != int64(len(sourceIDs)) {
			return ErrInventorySource
		}
		run := *request
		if run.BaseGenerationID == nil {
			run.BaseGenerationID = scope.ActiveGenerationID
		}
		if run.BaseGenerationID != nil {
			if scope.ActiveGenerationID == nil || *scope.ActiveGenerationID != *run.BaseGenerationID {
				return ErrInventoryConflict
			}
			if _, err = r.Generation(txCtx, scope.ID, *run.BaseGenerationID); err != nil {
				return err
			}
		}
		if err = db.Create(&run).Error; err != nil {
			return err
		}
		sources := make([]model.SyncRunSource, 0, len(sourceIDs))
		for _, id := range sourceIDs {
			sources = append(sources, model.SyncRunSource{RunID: run.ID, SourceID: id, Status: "queued"})
		}
		if err = db.Create(&sources).Error; err != nil {
			return err
		}
		result = &run
		return nil
	})
	return result, err
}

func (r *inventoryRepository) Cancel(ctx context.Context, scopeID, runID string) (*model.SyncRun, bool, error) {
	var result *model.SyncRun
	accepted := false
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		if _, err := r.lockInventoryScope(txCtx, scopeID); err != nil {
			return err
		}
		db := r.r.DB(txCtx).WithContext(txCtx)
		var run model.SyncRun
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope_id = ? AND id = ?", scopeID, runID).First(&run).Error; err != nil {
			return inventoryDBError(err)
		}
		changes := map[string]interface{}{}
		now := time.Now().UTC()
		switch run.Status {
		case "canceled":
			result = &run
			return nil
		case "queued":
			run.Status = "canceled"
			run.FinishedAt = &now
			changes["status"] = run.Status
			changes["finished_at"] = now
		case "running", "validating", "publishing":
		default:
			return ErrInventoryConflict
		}
		if run.CancelRequestedAt == nil {
			run.CancelRequestedAt = &now
			changes["cancel_requested_at"] = now
		}
		if len(changes) > 0 {
			if err := db.Model(&model.SyncRun{}).Where("scope_id = ? AND id = ?", scopeID, runID).Updates(changes).Error; err != nil {
				return err
			}
			accepted = true
		}
		result = &run
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return result, accepted, nil
}
