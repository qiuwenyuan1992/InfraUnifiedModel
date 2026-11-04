package repository

import (
	"context"
	"fmt"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
)

func roundListQuery(db *gorm.DB, query InventoryListQuery) (*gorm.DB, error) {
	if query.Limit < 1 || query.Limit > 200 {
		return nil, fmt.Errorf("invalid inventory list limit")
	}
	if query.LastID != "" {
		if query.LastCreatedAt == nil {
			return nil, fmt.Errorf("inventory cursor time is required")
		}
		db = db.Where("created_at < ? OR (created_at = ? AND id < ?)", query.LastCreatedAt, query.LastCreatedAt, query.LastID)
	} else if query.LastCreatedAt != nil {
		return nil, fmt.Errorf("inventory cursor ID is required")
	}
	return db.Order("created_at DESC, id DESC").Limit(query.Limit + 1), nil
}

func (r *inventoryRoundRepository) List(ctx context.Context, query InventoryListQuery) (*InventoryListResult, error) {
	db := r.r.DB(ctx)
	if query.SourceID != "" {
		db = db.Where("source_id = ?", query.SourceID)
	}
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	db, err := roundListQuery(db, query)
	if err != nil {
		return nil, err
	}
	result, err := inventoryRows(db, query.Limit, func(row model.SyncRound) string { return row.ID })
	if err == nil {
		rows := result.Items.([]model.SyncRound)
		if len(rows) > 0 {
			last := rows[len(rows)-1].CreatedAt
			result.LastCreatedAt = &last
		}
	}
	return result, err
}

func (r *inventoryRoundRepository) Diagnostics(ctx context.Context, id string, isRound bool, query InventoryListQuery) (*InventoryListResult, error) {
	db := r.r.DB(ctx)
	if isRound {
		var round model.SyncRound
		if err := db.Where("id = ?", id).First(&round).Error; err != nil {
			return nil, inventoryDBError(err)
		}
		db = db.Where("round_id = ? OR run_id IN (?)", id, r.r.DB(ctx).Model(&model.SyncRoundPhase{}).Select("run_id").Where("round_id = ?", id))
	} else {
		var run model.SyncRun
		if err := db.Where("id = ?", id).First(&run).Error; err != nil {
			return nil, inventoryDBError(err)
		}
		db = db.Where("run_id = ?", id)
	}
	if query.SourceID != "" {
		db = db.Where("source_id = ?", query.SourceID)
	}
	if query.Status != "" {
		db = db.Where("severity = ?", query.Status)
	}
	db, err := roundListQuery(db, query)
	if err != nil {
		return nil, err
	}
	result, err := inventoryRows(db, query.Limit, func(row model.SyncDiagnostic) string { return row.ID })
	if err == nil {
		rows := result.Items.([]model.SyncDiagnostic)
		if len(rows) > 0 {
			last := rows[len(rows)-1].CreatedAt
			result.LastCreatedAt = &last
		}
	}
	return result, err
}
