package repository

import (
	"context"
	"fmt"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
)

func (r *inventoryRepository) List(ctx context.Context, resource string, q InventoryListQuery) (*InventoryListResult, error) {
	if q.Limit < 1 || q.Limit > 200 {
		return nil, fmt.Errorf("invalid inventory list limit")
	}
	db := r.r.DB(ctx).WithContext(ctx)
	idColumn := "id"
	switch resource {
	case "scopes":
		db = db.Where("id IN ?", q.ScopeIDs)
	case "devices", "interfaces", "addresses":
		return nil, fmt.Errorf("asset resources require the graph inventory repository")
	case "sources", "generations", "sync-runs":
		db = db.Where("scope_id = ?", q.ScopeID)
	default:
		return nil, fmt.Errorf("invalid inventory resource")
	}
	if resource == "generations" {
		db = db.Where("state = ?", "published")
	}
	for _, filter := range []struct{ column, value string }{
		{"device_kind", q.DeviceKind}, {"name", q.Name}, {"lifecycle", q.Lifecycle},
		{"interface_kind", q.InterfaceKind}, {"address_family", q.AddressFamily}, {"status", q.Status},
	} {
		if filter.value != "" {
			db = db.Where(filter.column+" = ?", filter.value)
		}
	}
	if resource == "sync-runs" {
		if q.SourceID != "" {
			db = db.Where("EXISTS (SELECT 1 FROM sync_run_sources WHERE sync_run_sources.run_id = sync_runs.id AND sync_run_sources.source_id = ?)", q.SourceID)
		}
		if q.LastID != "" {
			db = db.Where("created_at < ? OR (created_at = ? AND id < ?)", q.LastCreatedAt, q.LastCreatedAt, q.LastID)
		}
		db = db.Order("created_at DESC").Order("id DESC")
	} else {
		if q.LastID != "" {
			db = db.Where(idColumn+" > ?", q.LastID)
		}
		db = db.Order(idColumn + " ASC")
	}
	db = db.Limit(q.Limit + 1)
	switch resource {
	case "scopes":
		return inventoryRows(db, q.Limit, func(v model.TopologyScope) string { return v.ID })
	case "sources":
		return inventoryRows(db, q.Limit, func(v model.Source) string { return v.ID })
	case "generations":
		return inventoryRows(db, q.Limit, func(v model.Generation) string { return v.ID })
	default:
		result, err := inventoryRows(db, q.Limit, func(v model.SyncRun) string { return v.ID })
		if err == nil {
			rows := result.Items.([]model.SyncRun)
			if len(rows) > 0 {
				last := rows[len(rows)-1].CreatedAt
				result.LastCreatedAt = &last
			}
		}
		return result, err
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
