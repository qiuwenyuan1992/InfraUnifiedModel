package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *inventoryRoundRepository) ClaimNext(ctx context.Context, now time.Time) (*model.SyncRound, *model.Source, error) {
	if now.IsZero() {
		return nil, nil, ErrInventoryConflict
	}
	now = now.UTC().Truncate(time.Microsecond)
	inventoryClaimLock.Lock()
	defer inventoryClaimLock.Unlock()
	if err := r.r.Transaction(ctx, func(txCtx context.Context) error { return expireStaleSyncRounds(r.r.DB(txCtx), now, "") }); err != nil {
		return nil, nil, err
	}
	var candidate model.SyncRound
	if err := r.r.DB(ctx).Where("status = ? AND cancel_requested_at IS NULL", "queued").Order("created_at ASC, id ASC").First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrInventoryNoRun
		}
		return nil, nil, err
	}
	unlock := lockInventorySource(candidate.SourceID)
	defer unlock()
	var claimed *model.SyncRound
	var source *model.Source
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var s model.Source
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", candidate.SourceID).First(&s).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInventorySource
			}
			return err
		}
		if !s.Enabled {
			return ErrInventorySource
		}
		var round model.SyncRound
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ? AND cancel_requested_at IS NULL", candidate.ID, "queued").First(&round).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInventoryNoRun
			}
			return err
		}
		var activeRound model.SyncRound
		err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND id <> ? AND status IN ?", round.SourceID, round.ID, []string{"running"}).First(&activeRound).Error
		if err == nil {
			return ErrInventoryConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var activeRun model.SyncRun
		err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND status IN ?", round.SourceID, activeSyncRunStatuses()).First(&activeRun).Error
		if err == nil {
			return ErrInventoryConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		lease := now.Add(inventoryRunLeaseDuration)
		if err := db.Model(&model.SyncRound{}).Where("id = ?", round.ID).Updates(map[string]interface{}{"status": "running", "started_at": now, "snapshot_at": now, "lease_expires_at": lease, "error_code": ""}).Error; err != nil {
			return err
		}
		round.Status = "running"
		round.StartedAt = &now
		round.SnapshotAt = &now
		round.LeaseExpiresAt = &lease
		round.ErrorCode = ""
		claimed = &round
		source = &s
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return claimed, source, nil
}

func currentSyncRound(db *gorm.DB, id string, now time.Time) (*model.SyncRound, error) {
	var round model.SyncRound
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&round).Error; err != nil {
		return nil, inventoryDBError(err)
	}
	if now.IsZero() || round.Status != "running" || round.LeaseExpiresAt == nil || !round.LeaseExpiresAt.After(now) {
		return nil, ErrInventoryConflict
	}
	return &round, nil
}

func (r *inventoryRoundRepository) Renew(ctx context.Context, id string, now time.Time) error {
	now = now.UTC().Truncate(time.Microsecond)
	return r.mutateRound(ctx, id, now, func(db *gorm.DB, _ *model.SyncRound) error {
		return db.Model(&model.SyncRound{}).Where("id = ?", id).Update("lease_expires_at", now.Add(inventoryRunLeaseDuration)).Error
	})
}

func (r *inventoryRoundRepository) StartPhase(ctx context.Context, id, name string, now time.Time) error {
	now = now.UTC().Truncate(time.Microsecond)
	return r.mutateRound(ctx, id, now, func(db *gorm.DB, round *model.SyncRound) error {
		if round.CancelRequestedAt != nil {
			return ErrInventoryConflict
		}
		var phase model.SyncRoundPhase
		if err := db.Where("round_id = ? AND name = ?", id, name).First(&phase).Error; err != nil {
			return inventoryDBError(err)
		}
		if phase.Status != "queued" {
			return ErrInventoryConflict
		}
		var incomplete int64
		if err := db.Model(&model.SyncRoundPhase{}).Where("round_id = ? AND position < ? AND status <> ?", id, phase.Position, "succeeded").Count(&incomplete).Error; err != nil {
			return err
		}
		if incomplete > 0 {
			return ErrInventoryConflict
		}
		return db.Model(&model.SyncRoundPhase{}).Where("round_id = ? AND name = ?", id, name).Updates(map[string]interface{}{"status": "running", "started_at": now}).Error
	})
}

func (r *inventoryRoundRepository) CompletePhase(ctx context.Context, id, name string, result PhaseResult, now time.Time) error {
	if result.Nodes < 0 || result.Edges < 0 || result.Objects < 0 || result.Skipped < 0 || result.MissingEndpoints < 0 {
		return ErrInventoryConflict
	}
	if result.ErrorCode != "" && !syncRunErrorCodePattern.MatchString(result.ErrorCode) {
		return ErrInventoryConflict
	}
	now = now.UTC().Truncate(time.Microsecond)
	return r.mutateRound(ctx, id, now, func(db *gorm.DB, round *model.SyncRound) error {
		var phase model.SyncRoundPhase
		if err := db.Where("round_id = ? AND name = ?", id, name).First(&phase).Error; err != nil {
			return inventoryDBError(err)
		}
		if phase.Status != "running" {
			return ErrInventoryConflict
		}
		if phase.RunID == "" || result.RunID != phase.RunID {
			return ErrInventoryConflict
		}
		runID := phase.RunID
		if runID != "" {
			var run model.SyncRun
			if err := db.Where("id = ? AND source_id = ?", runID, round.SourceID).First(&run).Error; err != nil {
				return ErrInventoryConflict
			}
			var owned int64
			if err := db.Model(&model.SyncRoundPhase{}).Where("run_id = ? AND NOT (round_id = ? AND name = ?)", runID, id, name).Count(&owned).Error; err != nil {
				return err
			}
			if owned > 0 {
				return ErrInventoryConflict
			}
			if result.ErrorCode == "" && run.Status != "succeeded" {
				return ErrInventoryConflict
			}
		}
		status := "succeeded"
		if result.ErrorCode != "" {
			status = "failed"
		}
		changes := map[string]interface{}{"status": status, "finished_at": now, "nodes": result.Nodes, "edges": result.Edges, "objects": result.Objects, "skipped": result.Skipped, "missing_endpoints": result.MissingEndpoints, "error_code": result.ErrorCode}
		if err := db.Model(&model.SyncRoundPhase{}).Where("round_id = ? AND name = ?", id, name).Updates(changes).Error; err != nil {
			return err
		}
		for code, count := range result.Reasons {
			if count <= 0 || !syncRunErrorCodePattern.MatchString(code) {
				continue
			}
			var random [16]byte
			if _, err := rand.Read(random[:]); err != nil {
				return err
			}
			diagnostic := map[string]interface{}{"id": hex.EncodeToString(random[:]), "round_id": id, "run_id": nil, "source_id": round.SourceID, "resource": name, "severity": "warning", "code": code, "object_ref": "", "field_path": "", "detail": strconv.Itoa(count), "created_at": now}
			if runID != "" {
				diagnostic["run_id"] = runID
			}
			if err := db.Table("sync_diagnostics").Create(diagnostic).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *inventoryRoundRepository) Finish(ctx context.Context, id, status, errorCode string, now time.Time) error {
	if status != "succeeded" && status != "failed" && status != "canceled" {
		return ErrInventoryConflict
	}
	if status == "failed" {
		if !syncRunErrorCodePattern.MatchString(errorCode) {
			return ErrInventoryConflict
		}
	} else {
		errorCode = ""
	}
	now = now.UTC().Truncate(time.Microsecond)
	return r.mutateRound(ctx, id, now, func(db *gorm.DB, round *model.SyncRound) error {
		if status == "succeeded" {
			if round.CancelRequestedAt != nil {
				return ErrInventoryConflict
			}
			var phases []model.SyncRoundPhase
			if err := db.Where("round_id = ?", id).Order("position").Find(&phases).Error; err != nil {
				return err
			}
			names := model.SyncRoundPhaseNames()
			if len(phases) != len(names) {
				return ErrInventoryConflict
			}
			for i, phase := range phases {
				if phase.Name != names[i] || phase.Position != i+1 || phase.Status != "succeeded" {
					return ErrInventoryConflict
				}
			}
		} else {
			if err := terminateSyncRoundWork(db, id, status, errorCode, now); err != nil {
				return err
			}
		}
		return db.Model(&model.SyncRound{}).Where("id = ?", id).Updates(map[string]interface{}{"status": status, "error_code": errorCode, "finished_at": now, "lease_expires_at": nil}).Error
	})
}

func terminateSyncRoundWork(db *gorm.DB, id, status, errorCode string, now time.Time) error {
	if err := db.Model(&model.SyncRun{}).Where("id IN (?) AND status IN ?", db.Model(&model.SyncRoundPhase{}).Select("run_id").Where("round_id = ?", id), activeSyncRunStatuses()).Updates(map[string]interface{}{"status": status, "error_code": errorCode, "finished_at": now, "lease_expires_at": nil}).Error; err != nil {
		return err
	}
	if err := db.Model(&model.SyncRoundPhase{}).Where("round_id = ? AND status = ?", id, "running").Updates(map[string]interface{}{"status": status, "error_code": errorCode, "finished_at": now}).Error; err != nil {
		return err
	}
	return db.Model(&model.SyncRoundPhase{}).Where("round_id = ? AND status = ?", id, "queued").Update("status", "not_run").Error
}

func expireStaleSyncRounds(db *gorm.DB, now time.Time, sourceID string) error {
	query := db.Model(&model.SyncRound{}).Where("status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", "running", now)
	if sourceID != "" {
		query = query.Where("source_id = ?", sourceID)
	}
	var rounds []model.SyncRound
	if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&rounds).Error; err != nil {
		return err
	}
	for _, round := range rounds {
		if err := terminateSyncRoundWork(db, round.ID, "failed", syncErrorLeaseExpired, now); err != nil {
			return err
		}
		if err := db.Model(&model.SyncRound{}).Where("id = ?", round.ID).Updates(map[string]interface{}{"status": "failed", "error_code": syncErrorLeaseExpired, "finished_at": now, "lease_expires_at": nil}).Error; err != nil {
			return err
		}
	}
	return nil
}

func validateRoundStage(db *gorm.DB, ctx context.Context, sourceID string, now time.Time) (*model.SyncRoundPhase, error) {
	id, name := RoundStage(ctx)
	if id == "" || name == "" {
		return nil, ErrInventoryConflict
	}
	round, err := currentSyncRound(db, id, now)
	if err != nil {
		return nil, ErrInventoryConflict
	}
	if round.SourceID != sourceID || round.CancelRequestedAt != nil {
		return nil, ErrInventoryConflict
	}
	var phase model.SyncRoundPhase
	if err := db.Where("round_id = ? AND name = ? AND status = ?", id, name, "running").First(&phase).Error; err != nil {
		return nil, ErrInventoryConflict
	}
	return &phase, nil
}
