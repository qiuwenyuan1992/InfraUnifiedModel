package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/google/uuid"
)

func (s *inventoryService) Enqueue(ctx context.Context, userID, key string, req EnqueueInventoryRun) (*model.SyncRun, error) {
	if err := s.authorize(userID, "write"); err != nil {
		return nil, err
	}
	if len(key) < 1 || len(key) > 128 {
		return nil, ErrInventoryInvalid
	}
	for _, c := range key {
		if c < 32 || c > 126 {
			return nil, ErrInventoryInvalid
		}
	}
	if req.Mode != "full" || len(req.SourceIDs) == 0 || len(req.SourceIDs) > 100 {
		return nil, ErrInventoryInvalid
	}
	if req.BaseGenerationID != nil && !inventoryID(*req.BaseGenerationID) {
		return nil, ErrInventoryInvalid
	}
	// 拷贝后排序去重，既不修改调用者切片，也保证来源顺序和重复项不影响幂等性。
	sourceIDs := append([]string(nil), req.SourceIDs...)
	sort.Strings(sourceIDs)
	req.SourceIDs = sourceIDs[:0]
	for _, id := range sourceIDs {
		if !inventoryID(id) {
			return nil, ErrInventoryInvalid
		}
		if len(req.SourceIDs) == 0 || id != req.SourceIDs[len(req.SourceIDs)-1] {
			req.SourceIDs = append(req.SourceIDs, id)
		}
	}
	canonical, _ := json.Marshal(req)
	hash := sha256.Sum256(canonical)
	run := &model.SyncRun{
		ID: strings.ReplaceAll(uuid.NewString(), "-", ""), Status: "queued", Mode: req.Mode,
		BaseGenerationID: req.BaseGenerationID, RequestHash: hex.EncodeToString(hash[:]), IdempotencyKey: key,
		RequestedBy: userID, CreatedAt: time.Now().UTC(),
	}
	result, err := s.repo.Enqueue(ctx, run, req.SourceIDs)
	return result, inventoryError(err)
}

func (s *inventoryService) GetRun(ctx context.Context, userID, runID string) (*model.SyncRun, error) {
	if err := s.authorize(userID, "sync-runs"); err != nil {
		return nil, err
	}
	if !inventoryID(runID) {
		return nil, ErrInventoryInvalid
	}
	if _, err := s.repo.State(ctx); err != nil {
		return nil, inventoryError(err)
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
