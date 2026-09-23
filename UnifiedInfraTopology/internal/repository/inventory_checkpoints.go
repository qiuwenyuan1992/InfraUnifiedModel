package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DeviceSyncCursorVersion = 1

type DeviceSyncCursor struct {
	Version        int       `json:"version"`
	RunID          string    `json:"run_id"`
	NextOffset     int       `json:"next_offset"`
	LastInstanceID int64     `json:"last_instance_id"`
	SnapshotAt     time.Time `json:"snapshot_at"`
	Total          int       `json:"total"`
}

type DeviceSyncCheckpoint struct {
	SourceID    string
	Resource    string
	Cursor      DeviceSyncCursor
	Complete    bool
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

func (r *inventoryRepository) LoadCheckpoint(ctx context.Context, sourceID, resource string) (*DeviceSyncCheckpoint, error) {
	var stored model.SyncCheckpoint
	if err := r.r.DB(ctx).Where("source_id = ? AND resource = ?", sourceID, resource).First(&stored).Error; err != nil {
		return nil, inventoryDBError(err)
	}
	return decodeDeviceSyncCheckpoint(stored)
}

func (r *inventoryRepository) BeginCheckpoint(ctx context.Context, sourceID, resource, runID string, startedAt, leaseAt time.Time) (*DeviceSyncCheckpoint, error) {
	if sourceID == "" || resource == "" || runID == "" || startedAt.IsZero() || leaseAt.IsZero() {
		return nil, ErrInventoryConflict
	}
	startedAt = startedAt.UTC().Truncate(time.Microsecond)
	leaseAt = leaseAt.UTC().Truncate(time.Microsecond)

	var result *DeviceSyncCheckpoint
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var run model.SyncRun
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND source_id = ? AND status = ? AND cancel_requested_at IS NULL", runID, sourceID, model.SyncRunStatusRunning).
			Where(syncRunLeaseCurrentCondition, leaseAt, leaseAt.Add(-inventoryRunLeaseDuration)).
			First(&run).Error; err != nil {
			return ErrInventoryConflict
		}
		var stored model.SyncCheckpoint
		err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_id = ? AND resource = ?", sourceID, resource).
			First(&stored).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			cursor := DeviceSyncCursor{
				Version:    DeviceSyncCursorVersion,
				RunID:      runID,
				SnapshotAt: startedAt,
			}
			cursorJSON, marshalErr := json.Marshal(cursor)
			if marshalErr != nil {
				return marshalErr
			}
			stored = model.SyncCheckpoint{
				SourceID:  sourceID,
				Resource:  resource,
				Cursor:    string(cursorJSON),
				Complete:  false,
				UpdatedAt: startedAt,
			}
			if createErr := db.Create(&stored).Error; createErr != nil {
				return createErr
			}
		case err != nil:
			return err
		default:
			checkpoint, decodeErr := decodeDeviceSyncCheckpoint(stored)
			if decodeErr != nil {
				return decodeErr
			}
			cursor := checkpoint.Cursor
			if stored.Complete || cursor.RunID != runID {
				cursor = DeviceSyncCursor{
					Version:    DeviceSyncCursorVersion,
					SnapshotAt: startedAt,
				}
			}
			cursor.RunID = runID
			cursorJSON, marshalErr := json.Marshal(cursor)
			if marshalErr != nil {
				return marshalErr
			}
			if updateErr := db.Model(&model.SyncCheckpoint{}).
				Where("source_id = ? AND resource = ?", sourceID, resource).
				Updates(map[string]interface{}{
					"cursor":       string(cursorJSON),
					"complete":     false,
					"completed_at": nil,
					"updated_at":   startedAt,
				}).Error; updateErr != nil {
				return updateErr
			}
			stored.Cursor = string(cursorJSON)
			stored.Complete = false
			stored.CompletedAt = nil
			stored.UpdatedAt = startedAt
		}

		checkpoint, decodeErr := decodeDeviceSyncCheckpoint(stored)
		if decodeErr != nil {
			return decodeErr
		}
		result = checkpoint
		return nil
	})
	return result, err
}

func (r *inventoryRepository) AdvanceCheckpoint(ctx context.Context, sourceID, resource, runID string, cursor DeviceSyncCursor, updatedAt time.Time) (*DeviceSyncCheckpoint, error) {
	if sourceID == "" || resource == "" || runID == "" || updatedAt.IsZero() || cursor.RunID != runID {
		return nil, ErrInventoryConflict
	}
	cursor.SnapshotAt = cursor.SnapshotAt.UTC().Truncate(time.Microsecond)
	updatedAt = updatedAt.UTC().Truncate(time.Microsecond)
	if err := validateDeviceSyncCursor(cursor); err != nil {
		return nil, err
	}

	var result *DeviceSyncCheckpoint
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var run model.SyncRun
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND source_id = ? AND status = ? AND cancel_requested_at IS NULL", runID, sourceID, model.SyncRunStatusRunning).
			Where(syncRunLeaseCurrentCondition, updatedAt, updatedAt.Add(-inventoryRunLeaseDuration)).
			First(&run).Error; err != nil {
			return ErrInventoryConflict
		}
		var stored model.SyncCheckpoint
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_id = ? AND resource = ?", sourceID, resource).
			First(&stored).Error; err != nil {
			return inventoryDBError(err)
		}
		checkpoint, err := decodeDeviceSyncCheckpoint(stored)
		if err != nil {
			return err
		}
		previous := checkpoint.Cursor
		if stored.Complete || previous.RunID != runID || !previous.SnapshotAt.Equal(cursor.SnapshotAt) ||
			cursor.NextOffset <= previous.NextOffset || cursor.LastInstanceID <= previous.LastInstanceID ||
			(previous.Total != 0 && cursor.Total != previous.Total) {
			return ErrInventoryConflict
		}

		cursorJSON, err := json.Marshal(cursor)
		if err != nil {
			return err
		}
		if err := db.Model(&model.SyncCheckpoint{}).
			Where("source_id = ? AND resource = ?", sourceID, resource).
			Updates(map[string]interface{}{
				"cursor":       string(cursorJSON),
				"complete":     false,
				"completed_at": nil,
				"updated_at":   updatedAt,
			}).Error; err != nil {
			return err
		}
		leaseResult := db.Model(&model.SyncRun{}).
			Where("id = ? AND source_id = ? AND status = ? AND cancel_requested_at IS NULL", runID, sourceID, model.SyncRunStatusRunning).
			Update("lease_expires_at", updatedAt.Add(inventoryRunLeaseDuration))
		if leaseResult.Error != nil {
			return leaseResult.Error
		}
		if leaseResult.RowsAffected != 1 {
			return ErrInventoryConflict
		}

		stored.Cursor = string(cursorJSON)
		stored.Complete = false
		stored.CompletedAt = nil
		stored.UpdatedAt = updatedAt
		result, err = decodeDeviceSyncCheckpoint(stored)
		return err
	})
	return result, err
}

func (r *inventoryRepository) CompleteCheckpointAndRun(ctx context.Context, sourceID, resource, runID, expectedStatus string, completedAt time.Time) (*model.SyncRun, error) {
	if sourceID == "" || resource == "" || runID == "" || expectedStatus != model.SyncRunStatusPublishing || completedAt.IsZero() {
		return nil, ErrInventoryConflict
	}
	completedAt = completedAt.UTC().Truncate(time.Microsecond)

	var completedRun *model.SyncRun
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var currentRun model.SyncRun
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND source_id = ? AND status = ? AND cancel_requested_at IS NULL", runID, sourceID, expectedStatus).
			Where(syncRunLeaseCurrentCondition, completedAt, completedAt.Add(-inventoryRunLeaseDuration)).
			First(&currentRun).Error; err != nil {
			return ErrInventoryConflict
		}
		var stored model.SyncCheckpoint
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_id = ? AND resource = ?", sourceID, resource).
			First(&stored).Error; err != nil {
			return inventoryDBError(err)
		}
		checkpoint, err := decodeDeviceSyncCheckpoint(stored)
		if err != nil {
			return err
		}
		if stored.Complete || checkpoint.Cursor.RunID != runID || checkpoint.Cursor.NextOffset != checkpoint.Cursor.Total {
			return ErrInventoryConflict
		}

		checkpointResult := db.Model(&model.SyncCheckpoint{}).
			Where("source_id = ? AND resource = ?", sourceID, resource).
			Updates(map[string]interface{}{
				"complete":     true,
				"completed_at": completedAt,
				"updated_at":   completedAt,
			})
		if checkpointResult.Error != nil {
			return checkpointResult.Error
		}
		if checkpointResult.RowsAffected != 1 {
			return ErrInventoryNotFound
		}

		runResult := db.Model(&model.SyncRun{}).
			Where("id = ? AND source_id = ? AND status = ?", runID, sourceID, expectedStatus).
			Updates(map[string]interface{}{
				"status":              model.SyncRunStatusSucceeded,
				"finished_at":         completedAt,
				"error_code":          "",
				"cancel_requested_at": nil,
				"lease_expires_at":    nil,
			})

		if runResult.Error != nil {
			return runResult.Error
		}
		if runResult.RowsAffected != 1 {
			return ErrInventoryConflict
		}

		var run model.SyncRun
		if err := db.Where("id = ?", runID).First(&run).Error; err != nil {
			return inventoryDBError(err)
		}
		completedRun = &run
		return nil
	})
	if err != nil {
		return nil, err
	}
	return completedRun, nil
}

func decodeDeviceSyncCheckpoint(stored model.SyncCheckpoint) (*DeviceSyncCheckpoint, error) {
	var cursor DeviceSyncCursor
	if err := json.Unmarshal([]byte(stored.Cursor), &cursor); err != nil {
		return nil, ErrInventoryConflict
	}
	cursor.SnapshotAt = cursor.SnapshotAt.UTC().Truncate(time.Microsecond)
	if err := validateDeviceSyncCursor(cursor); err != nil {
		return nil, err
	}
	return &DeviceSyncCheckpoint{
		SourceID:    stored.SourceID,
		Resource:    stored.Resource,
		Cursor:      cursor,
		Complete:    stored.Complete,
		CompletedAt: stored.CompletedAt,
		UpdatedAt:   stored.UpdatedAt,
	}, nil
}

func validateDeviceSyncCursor(cursor DeviceSyncCursor) error {
	if cursor.Version != DeviceSyncCursorVersion || cursor.RunID == "" || cursor.NextOffset < 0 ||
		cursor.LastInstanceID < 0 || cursor.SnapshotAt.IsZero() || cursor.Total < 0 ||
		(cursor.NextOffset == 0 && cursor.LastInstanceID != 0) ||
		(cursor.NextOffset > 0 && cursor.LastInstanceID == 0) ||
		(cursor.Total > 0 && cursor.NextOffset > cursor.Total) {
		return ErrInventoryConflict
	}
	return nil
}
