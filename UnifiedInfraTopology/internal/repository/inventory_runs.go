package repository

import (
	"context"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *inventoryRepository) Enqueue(ctx context.Context, request *model.SyncRun) (*model.SyncRun, error) {
	unlock := lockInventorySource(request.SourceID)
	defer unlock()

	var result *model.SyncRun
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var source model.Source
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", request.SourceID).First(&source).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInventorySource
			}
			return err
		}

		var existing model.SyncRun
		err := db.Where("source_id = ? AND idempotency_key = ?", request.SourceID, request.IdempotencyKey).First(&existing).Error
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

		if !source.Enabled {
			return ErrInventorySource
		}
		var active model.SyncRun
		err = db.Where("source_id = ? AND status IN ?", request.SourceID, []string{"queued", "running", "validating", "publishing"}).First(&active).Error
		if err == nil {
			return ErrInventoryConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		run := *request
		if err := db.Create(&run).Error; err != nil {
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
		db := r.r.DB(txCtx)
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
