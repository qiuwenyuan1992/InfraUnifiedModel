package repository

import (
	"context"
	"fmt"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
)

func (r *inventoryRepository) List(ctx context.Context, resource string, query InventoryListQuery) (*InventoryListResult, error) {
	if query.Limit < 1 || query.Limit > 200 {
		return nil, fmt.Errorf("invalid inventory list limit")
	}
	db := r.r.DB(ctx)
	switch resource {
	case "sources":
		if query.Status != "" || query.SourceID != "" || query.LastCreatedAt != nil {
			return nil, fmt.Errorf("invalid source list query")
		}
		if query.LastID != "" {
			db = db.Where("id > ?", query.LastID)
		}
		db = db.Order("id ASC").Limit(query.Limit + 1)
		return inventoryRows(db, query.Limit, func(value model.Source) string { return value.ID })
	case "sync-runs":
		if query.Status != "" {
			db = db.Where("status = ?", query.Status)
		}
		if query.SourceID != "" {
			db = db.Where("source_id = ?", query.SourceID)
		}
		if query.LastID != "" {
			if query.LastCreatedAt == nil {
				return nil, fmt.Errorf("sync run cursor time is required")
			}
			db = db.Where("created_at < ? OR (created_at = ? AND id < ?)", query.LastCreatedAt, query.LastCreatedAt, query.LastID)
		}
		db = db.Order("created_at DESC").Order("id DESC").Limit(query.Limit + 1)
		result, err := inventoryRows(db, query.Limit, func(value model.SyncRun) string { return value.ID })
		if err == nil {
			rows := result.Items.([]model.SyncRun)
			if len(rows) > 0 {
				last := rows[len(rows)-1].CreatedAt
				result.LastCreatedAt = &last
			}
		}
		return result, err
	default:
		return nil, fmt.Errorf("invalid inventory resource")
	}
}

func inventoryRows[T any](db *gorm.DB, limit int, id func(T) string) (*InventoryListResult, error) {
	rows := make([]T, 0)
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := &InventoryListResult{HasMore: len(rows) > limit}
	if result.HasMore {
		rows = rows[:limit]
	}
	result.Items = rows
	if len(rows) > 0 {
		result.LastID = id(rows[len(rows)-1])
	}
	return result, nil
}
