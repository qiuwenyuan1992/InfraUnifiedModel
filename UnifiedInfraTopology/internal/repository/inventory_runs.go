package repository

import (
	"context"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 与未来发布事务保持相同的 state -> run 加锁顺序。
func (r *inventoryRepository) lockInventoryState(ctx context.Context) (*model.InventoryState, error) {
	db := r.r.DB(ctx).WithContext(ctx)
	if db.Dialector.Name() == "sqlite" {
		// SQLite 不支持 FOR UPDATE；先取得写锁，避免读事务升级竞争。
		if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return nil, err
		}
	}
	var state model.InventoryState
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", 1).First(&state).Error
	return &state, inventoryDBError(err)
}

func (r *inventoryRepository) Enqueue(ctx context.Context, request *model.SyncRun, sourceIDs []string) (*model.SyncRun, error) {
	var result *model.SyncRun
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		state, err := r.lockInventoryState(txCtx)
		if err != nil {
			return err
		}
		db := r.r.DB(txCtx).WithContext(txCtx)
		var existing model.SyncRun
		err = db.Where("idempotency_key = ?", request.IdempotencyKey).First(&existing).Error
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
		if err = db.Model(&model.Source{}).Where("id IN ? AND enabled = ?", sourceIDs, true).Count(&sourceCount).Error; err != nil {
			return err
		}
		if sourceCount != int64(len(sourceIDs)) {
			return ErrInventorySource
		}
		run := *request
		if run.BaseGenerationID == nil {
			run.BaseGenerationID = state.ActiveGenerationID
		}
		if run.BaseGenerationID != nil {
			if state.ActiveGenerationID == nil || *state.ActiveGenerationID != *run.BaseGenerationID {
				return ErrInventoryConflict
			}
			if _, err = r.Generation(txCtx, *run.BaseGenerationID); err != nil {
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

func (r *inventoryRepository) Cancel(ctx context.Context, runID string) (*model.SyncRun, bool, error) {
	var result *model.SyncRun
	accepted := false
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		if _, err := r.lockInventoryState(txCtx); err != nil {
			return err
		}
		db := r.r.DB(txCtx).WithContext(txCtx)
		var run model.SyncRun
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", runID).First(&run).Error; err != nil {
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
			if err := db.Model(&model.SyncRun{}).Where("id = ?", runID).Updates(changes).Error; err != nil {
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
