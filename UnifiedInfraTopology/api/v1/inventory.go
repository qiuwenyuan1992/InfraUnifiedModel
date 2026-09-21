package v1

import (
	"fmt"
	"time"

	"UnifiedInfraTopology/internal/model"
)

type InventorySourceDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AdapterKind string `json:"adapter_kind"`
	Enabled     bool   `json:"enabled"`
}

type InventoryRunDTO struct {
	ID                string     `json:"id"`
	SourceID          string     `json:"source_id"`
	Status            string     `json:"status"`
	Mode              string     `json:"mode"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	ErrorCode         string     `json:"error_code"`
}

func InventoryItems(items interface{}) (interface{}, error) {
	switch items := items.(type) {
	case []model.Source:
		result := make([]InventorySourceDTO, len(items))
		for i, item := range items {
			result[i] = InventorySourceDTO{
				ID: item.ID, Name: item.Name,
				AdapterKind: item.AdapterKind, Enabled: item.Enabled,
			}
		}
		return result, nil
	case []model.SyncRun:
		result := make([]InventoryRunDTO, len(items))
		for i, item := range items {
			result[i] = InventoryRun(item)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported inventory items type %T", items)
	}
}

func InventoryRun(item model.SyncRun) InventoryRunDTO {
	return InventoryRunDTO{
		ID: item.ID, SourceID: item.SourceID, Status: item.Status, Mode: item.Mode,
		CancelRequestedAt: inventoryUTCTime(item.CancelRequestedAt), CreatedAt: item.CreatedAt.UTC(),
		StartedAt: inventoryUTCTime(item.StartedAt), FinishedAt: inventoryUTCTime(item.FinishedAt),
		ErrorCode: item.ErrorCode,
	}
}

func inventoryUTCTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
