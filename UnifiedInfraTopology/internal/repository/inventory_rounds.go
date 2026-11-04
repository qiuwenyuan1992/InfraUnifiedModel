package repository

import (
	"context"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InventoryRoundRepository interface {
	Enqueue(context.Context, *model.SyncRound) (*model.SyncRound, error)
	List(context.Context, InventoryListQuery) (*InventoryListResult, error)
	Get(context.Context, string) (*model.SyncRound, []model.SyncRoundPhase, error)
	Cancel(context.Context, string) (*model.SyncRound, bool, error)
	ClaimNext(context.Context, time.Time) (*model.SyncRound, *model.Source, error)
	Renew(context.Context, string, time.Time) error
	CancellationRequested(context.Context, string) (bool, error)
	StartPhase(context.Context, string, string, time.Time) error
	CompletePhase(context.Context, string, string, PhaseResult, time.Time) error
	Finish(context.Context, string, string, string, time.Time) error
	Diagnostics(context.Context, string, bool, InventoryListQuery) (*InventoryListResult, error)
}

type PhaseResult struct {
	RunID                                            string
	Nodes, Edges, Objects, Skipped, MissingEndpoints int64
	ErrorCode                                        string
	Reasons                                          map[string]int
}

type roundStageKey struct{}
type roundStage struct{ roundID, phase string }

func WithRoundStage(ctx context.Context, roundID, phase string) context.Context {
	return context.WithValue(ctx, roundStageKey{}, roundStage{roundID, phase})
}
func RoundStage(ctx context.Context) (string, string) {
	stage, _ := ctx.Value(roundStageKey{}).(roundStage)
	return stage.roundID, stage.phase
}

type inventoryRoundRepository struct{ r *Repository }

func NewInventoryRoundRepository(r *Repository) InventoryRoundRepository {
	return &inventoryRoundRepository{r: r}
}

func (r *inventoryRoundRepository) Enqueue(ctx context.Context, request *model.SyncRound) (*model.SyncRound, error) {
	unlock := lockInventorySource(request.SourceID)
	defer unlock()
	var result *model.SyncRound
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var source model.Source
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", request.SourceID).First(&source).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInventorySource
			}
			return err
		}
		var existing model.SyncRound
		err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND idempotency_key = ?", request.SourceID, request.IdempotencyKey).First(&existing).Error
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
		now := time.Now().UTC().Truncate(time.Microsecond)
		if err := expireStaleSyncRounds(db, now, request.SourceID); err != nil {
			return err
		}
		if err := expireStaleSyncRuns(db, now, request.SourceID); err != nil {
			return err
		}
		var activeRound model.SyncRound
		err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND status IN ?", request.SourceID, []string{"queued", "running"}).First(&activeRound).Error
		if err == nil {
			return ErrInventoryConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var activeRun model.SyncRun
		err = db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND status IN ?", request.SourceID, activeSyncRunStatuses()).First(&activeRun).Error
		if err == nil {
			return ErrInventoryConflict
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		round := *request
		round.Status = "queued"
		round.StartedAt = nil
		round.FinishedAt = nil
		round.SnapshotAt = nil
		round.CancelRequestedAt = nil
		round.LeaseExpiresAt = nil
		round.ErrorCode = ""
		if err := db.Create(&round).Error; err != nil {
			return err
		}
		for i, name := range model.SyncRoundPhaseNames() {
			if err := db.Table("sync_round_phases").Create(map[string]interface{}{"round_id": round.ID, "name": name, "position": i + 1, "status": "queued"}).Error; err != nil {
				return err
			}
		}
		result = &round
		return nil
	})
	return result, err
}

func activeSyncRunStatuses() []string {
	return []string{"queued", "running", "validating", "publishing"}
}

func (r *inventoryRoundRepository) Get(ctx context.Context, id string) (*model.SyncRound, []model.SyncRoundPhase, error) {
	var round model.SyncRound
	if err := r.r.DB(ctx).Where("id = ?", id).First(&round).Error; err != nil {
		return nil, nil, inventoryDBError(err)
	}
	phases := make([]model.SyncRoundPhase, 0, 15)
	err := r.r.DB(ctx).Where("round_id = ?", id).Order("position ASC").Find(&phases).Error
	return &round, phases, err
}

func (r *inventoryRoundRepository) Cancel(ctx context.Context, id string) (*model.SyncRound, bool, error) {
	var initial model.SyncRound
	if err := r.r.DB(ctx).Where("id = ?", id).First(&initial).Error; err != nil {
		return nil, false, inventoryDBError(err)
	}
	unlock := lockInventorySource(initial.SourceID)
	defer unlock()
	var result *model.SyncRound
	accepted := false
	err := r.r.Transaction(ctx, func(txCtx context.Context) error {
		db := r.r.DB(txCtx)
		var source model.Source
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", initial.SourceID).First(&source).Error; err != nil {
			return err
		}
		var round model.SyncRound
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&round).Error; err != nil {
			return inventoryDBError(err)
		}
		if round.Status == "canceled" {
			result = &round
			return nil
		}
		if round.Status != "queued" && round.Status != "running" {
			return ErrInventoryConflict
		}
		if round.CancelRequestedAt != nil {
			result = &round
			return nil
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		round.CancelRequestedAt = &now
		changes := map[string]interface{}{"cancel_requested_at": now}
		if round.Status == "queued" {
			round.Status = "canceled"
			round.FinishedAt = &now
			changes["status"] = "canceled"
			changes["finished_at"] = now
			changes["lease_expires_at"] = nil
			if err := db.Model(&model.SyncRoundPhase{}).Where("round_id = ? AND status = ?", id, "queued").Update("status", "not_run").Error; err != nil {
				return err
			}
		}
		if err := db.Model(&model.SyncRound{}).Where("id = ?", id).Updates(changes).Error; err != nil {
			return err
		}
		if err := db.Model(&model.SyncRun{}).Where("id IN (?) AND status IN ?", db.Model(&model.SyncRoundPhase{}).Select("run_id").Where("round_id = ?", id), activeSyncRunStatuses()).Where("cancel_requested_at IS NULL").Update("cancel_requested_at", now).Error; err != nil {
			return err
		}
		result = &round
		accepted = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return result, accepted, nil
}

func (r *inventoryRoundRepository) CancellationRequested(ctx context.Context, id string) (bool, error) {
	var round model.SyncRound
	err := r.r.DB(ctx).Where("id = ?", id).First(&round).Error
	return round.CancelRequestedAt != nil, inventoryDBError(err)
}
