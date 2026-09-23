package repository

import (
	"context"
	"errors"
	"regexp"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	inventoryRunLeaseDuration    = 15 * time.Minute
	syncErrorLeaseExpired        = "worker_lease_expired"
	syncRunLeaseCurrentCondition = "(lease_expires_at > ? OR (lease_expires_at IS NULL AND started_at IS NOT NULL AND started_at > ?))"
)

var syncRunErrorCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

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
		if err := expireStaleSyncRuns(db, time.Now().UTC().Truncate(time.Microsecond), request.SourceID); err != nil {
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
		err = db.Where("source_id = ? AND status IN ?", request.SourceID, []string{
			model.SyncRunStatusQueued,
			model.SyncRunStatusRunning,
			model.SyncRunStatusValidating,
			model.SyncRunStatusPublishing,
		}).First(&active).Error

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
		now := time.Now().UTC().Truncate(time.Microsecond)
		switch run.Status {
		case model.SyncRunStatusCanceled:
			result = &run
			return nil
		case model.SyncRunStatusQueued:
			run.Status = model.SyncRunStatusCanceled
			run.FinishedAt = &now
			changes["status"] = run.Status
			changes["finished_at"] = now
		case model.SyncRunStatusRunning, model.SyncRunStatusValidating:
		case model.SyncRunStatusPublishing:
			return ErrInventoryConflict
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

func (r *inventoryRepository) ClaimNext(ctx context.Context, startedAt time.Time) (*model.SyncRun, *model.Source, error) {
	if startedAt.IsZero() {
		return nil, nil, ErrInventoryConflict
	}
	inventoryClaimLock.Lock()
	defer inventoryClaimLock.Unlock()

	startedAt = startedAt.UTC().Truncate(time.Microsecond)
	var claimed *model.SyncRun
	var source *model.Source
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		if err := expireStaleSyncRuns(db, startedAt, ""); err != nil {
			return err
		}
		var run model.SyncRun
		if err := db.Where("status = ? AND cancel_requested_at IS NULL", model.SyncRunStatusQueued).
			Order("created_at ASC, id ASC").First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInventoryNoRun
			}
			return err
		}

		var runSource model.Source
		if err := db.Where("id = ?", run.SourceID).First(&runSource).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInventorySource
			}
			return err
		}

		leaseExpiresAt := startedAt.Add(inventoryRunLeaseDuration)
		result := db.Model(&model.SyncRun{}).
			Where("id = ? AND status = ? AND cancel_requested_at IS NULL", run.ID, model.SyncRunStatusQueued).
			Updates(map[string]interface{}{
				"status":           model.SyncRunStatusRunning,
				"started_at":       startedAt,
				"error_code":       "",
				"lease_expires_at": leaseExpiresAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInventoryNoRun
		}
		run.Status = model.SyncRunStatusRunning
		run.StartedAt = &startedAt
		run.ErrorCode = ""
		run.LeaseExpiresAt = &leaseExpiresAt
		claimed = &run
		source = &runSource
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return claimed, source, nil
}

func (r *inventoryRepository) TransitionRun(ctx context.Context, runID, from, to string, at time.Time, errorCode string) (*model.SyncRun, error) {
	if runID == "" || at.IsZero() || !model.SyncRunTransitionAllowed(from, to) {
		return nil, ErrInventoryConflict
	}
	if to == model.SyncRunStatusFailed {
		if !syncRunErrorCodePattern.MatchString(errorCode) {
			return nil, ErrInventoryConflict
		}
	} else {
		errorCode = ""
	}
	at = at.UTC().Truncate(time.Microsecond)
	changes := map[string]interface{}{
		"status":           to,
		"error_code":       errorCode,
		"finished_at":      nil,
		"lease_expires_at": at.Add(inventoryRunLeaseDuration),
	}
	if model.SyncRunStatusTerminal(to) {
		changes["finished_at"] = at
		changes["lease_expires_at"] = nil
	}
	query := r.r.DB(ctx).Model(&model.SyncRun{}).
		Where("id = ? AND status = ?", runID, from).
		Where(syncRunLeaseCurrentCondition, at, at.Add(-inventoryRunLeaseDuration))
	if to == model.SyncRunStatusPublishing {
		query = query.Where("cancel_requested_at IS NULL")
	}
	result := query.Updates(changes)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrInventoryConflict
	}
	return r.Run(ctx, runID)
}

func (r *inventoryRepository) RenewRunLease(ctx context.Context, runID string, at time.Time) error {
	if runID == "" || at.IsZero() {
		return ErrInventoryConflict
	}
	at = at.UTC().Truncate(time.Microsecond)
	result := r.r.DB(ctx).Model(&model.SyncRun{}).
		Where("id = ? AND status IN ?", runID, []string{
			model.SyncRunStatusRunning,
			model.SyncRunStatusValidating,
			model.SyncRunStatusPublishing,
		}).
		Where(syncRunLeaseCurrentCondition, at, at.Add(-inventoryRunLeaseDuration)).
		Update("lease_expires_at", at.Add(inventoryRunLeaseDuration))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInventoryConflict
	}
	return nil
}

func (r *inventoryRepository) RunCancellationRequested(ctx context.Context, runID string) (bool, error) {
	var run model.SyncRun
	if err := r.r.DB(ctx).Select("status", "cancel_requested_at").Where("id = ?", runID).First(&run).Error; err != nil {
		return false, inventoryDBError(err)
	}
	return run.Status == model.SyncRunStatusCanceled || run.CancelRequestedAt != nil, nil
}

func expireStaleSyncRuns(db *gorm.DB, now time.Time, sourceID string) error {
	activeStatuses := []string{
		model.SyncRunStatusRunning,
		model.SyncRunStatusValidating,
		model.SyncRunStatusPublishing,
	}
	staleLegacyBefore := now.Add(-inventoryRunLeaseDuration)
	stale := func() *gorm.DB {
		query := db.Model(&model.SyncRun{}).
			Where("status IN ?", activeStatuses).
			Where("lease_expires_at <= ? OR (lease_expires_at IS NULL AND started_at IS NOT NULL AND started_at <= ?)", now, staleLegacyBefore)
		if sourceID != "" {
			query = query.Where("source_id = ?", sourceID)
		}
		return query
	}
	if err := stale().Where("cancel_requested_at IS NOT NULL").Updates(map[string]interface{}{
		"status":           model.SyncRunStatusCanceled,
		"finished_at":      now,
		"error_code":       "",
		"lease_expires_at": nil,
	}).Error; err != nil {
		return err
	}
	return stale().Where("cancel_requested_at IS NULL").Updates(map[string]interface{}{
		"status":           model.SyncRunStatusFailed,
		"finished_at":      now,
		"error_code":       syncErrorLeaseExpired,
		"lease_expires_at": nil,
	}).Error
}
