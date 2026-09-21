package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/google/uuid"
)

func (s *inventoryService) Enqueue(ctx context.Context, userID, key string, request EnqueueInventoryRun) (*model.SyncRun, error) {
	if err := s.authorize(userID, "write"); err != nil {
		return nil, err
	}
	if len(key) < 1 || len(key) > 128 {
		return nil, ErrInventoryInvalid
	}
	for _, current := range key {
		if current < 32 || current > 126 {
			return nil, ErrInventoryInvalid
		}
	}
	if request.Mode != "full" || !inventoryID(request.SourceID) {
		return nil, ErrInventoryInvalid
	}
	canonical, _ := json.Marshal(request)
	hash := sha256.Sum256(canonical)
	run := &model.SyncRun{
		ID: strings.ReplaceAll(uuid.NewString(), "-", ""), SourceID: request.SourceID,
		Status: "queued", Mode: request.Mode, RequestHash: hex.EncodeToString(hash[:]),
		IdempotencyKey: key, RequestedBy: userID, CreatedAt: s.now().UTC().Truncate(time.Microsecond),
	}
	result, err := s.repo.Enqueue(ctx, run)
	return result, inventoryError(err)
}

func (s *inventoryService) GetRun(ctx context.Context, userID, runID string) (*model.SyncRun, error) {
	if err := s.authorize(userID, "sync-runs"); err != nil {
		return nil, err
	}
	if !inventoryID(runID) {
		return nil, ErrInventoryInvalid
	}
	run, err := s.repo.Run(ctx, runID)
	return run, inventoryError(err)
}

func (s *inventoryService) CancelRun(ctx context.Context, userID, runID string) (*model.SyncRun, bool, error) {
	if err := s.authorize(userID, "write"); err != nil {
		return nil, false, err
	}
	if !inventoryID(runID) {
		return nil, false, ErrInventoryInvalid
	}
	run, accepted, err := s.repo.Cancel(ctx, runID)
	return run, accepted, inventoryError(err)
}
