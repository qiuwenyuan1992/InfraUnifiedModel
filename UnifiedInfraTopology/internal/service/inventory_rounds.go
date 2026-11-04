package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/google/uuid"
	"github.com/spf13/viper"
)

type EnqueueInventoryRound struct {
	SourceID string `json:"source_id"`
	Mode     string `json:"mode"`
}

type InventoryRoundService interface {
	Enqueue(context.Context, string, string, EnqueueInventoryRound) (*model.SyncRound, error)
	List(context.Context, string, InventoryQuery) (*InventoryPage, error)
	Get(context.Context, string, string) (*model.SyncRound, []model.SyncRoundPhase, error)
	Cancel(context.Context, string, string) (*model.SyncRound, bool, error)
	Diagnostics(context.Context, string, string, bool, InventoryQuery) (*InventoryPage, error)
}

type inventoryRoundService struct {
	*inventoryService
	rounds repository.InventoryRoundRepository
}

func NewInventoryRoundService(repo repository.InventoryRoundRepository, conf *viper.Viper) InventoryRoundService {
	return &inventoryRoundService{inventoryService: NewInventoryService(nil, conf).(*inventoryService), rounds: repo}
}

func (s *inventoryRoundService) Enqueue(ctx context.Context, userID, key string, request EnqueueInventoryRound) (*model.SyncRound, error) {
	if err := s.authorize(userID, "write"); err != nil {
		return nil, err
	}
	if len(key) < 1 || len(key) > 128 || request.Mode != "full" || !inventoryID(request.SourceID) {
		return nil, ErrInventoryInvalid
	}
	for _, c := range key {
		if c < 32 || c > 126 {
			return nil, ErrInventoryInvalid
		}
	}
	canonical, _ := json.Marshal(request)
	sum := sha256.Sum256(canonical)
	round := &model.SyncRound{ID: strings.ReplaceAll(uuid.NewString(), "-", ""), SourceID: request.SourceID, Status: "queued", Mode: request.Mode, RequestHash: hex.EncodeToString(sum[:]), IdempotencyKey: key, RequestedBy: userID, CreatedAt: s.now().UTC().Truncate(time.Microsecond)}
	result, err := s.rounds.Enqueue(ctx, round)
	return result, inventoryError(err)
}
func (s *inventoryRoundService) Get(ctx context.Context, userID, id string) (*model.SyncRound, []model.SyncRoundPhase, error) {
	if err := s.authorize(userID, "sync-runs"); err != nil {
		return nil, nil, err
	}
	if !inventoryID(id) {
		return nil, nil, ErrInventoryInvalid
	}
	round, phases, err := s.rounds.Get(ctx, id)
	return round, phases, inventoryError(err)
}
func (s *inventoryRoundService) Cancel(ctx context.Context, userID, id string) (*model.SyncRound, bool, error) {
	if err := s.authorize(userID, "write"); err != nil {
		return nil, false, err
	}
	if !inventoryID(id) {
		return nil, false, ErrInventoryInvalid
	}
	round, accepted, err := s.rounds.Cancel(ctx, id)
	return round, accepted, inventoryError(err)
}
func (s *inventoryRoundService) List(ctx context.Context, userID string, q InventoryQuery) (*InventoryPage, error) {
	return s.page(ctx, userID, "sync-rounds", "", q)
}
func (s *inventoryRoundService) Diagnostics(ctx context.Context, userID, id string, isRound bool, q InventoryQuery) (*InventoryPage, error) {
	if !inventoryID(id) {
		return nil, ErrInventoryInvalid
	}
	resource := "run-diagnostics"
	if isRound {
		resource = "round-diagnostics"
	}
	return s.page(ctx, userID, resource, id, q)
}
func (s *inventoryRoundService) page(ctx context.Context, userID, resource, parent string, q InventoryQuery) (*InventoryPage, error) {
	if err := s.authorize(userID, "sync-runs"); err != nil {
		return nil, err
	}
	if q.Limit < 0 || q.Limit > 200 || len(q.Cursor) > 4096 || (q.SourceID != "" && !inventoryID(q.SourceID)) || !inventoryOneOf(q.Status, "queued", "running", "validating", "publishing", "succeeded", "failed", "canceled") {
		return nil, ErrInventoryInvalid
	}
	if resource != "sync-rounds" && q.Status != "" {
		return nil, ErrInventoryInvalid
	}
	if len(s.cursorKey) < 32 {
		return nil, ErrInventoryNotReady
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	// 独立命名空间同时绑定父任务，禁止旧清单与跨任务游标复用。
	binding := inventoryCursor{Version: 1, UserID: userID, Resource: "management-v1/" + resource + "/" + parent, Filters: inventoryFilterHash(q)}
	if q.Cursor != "" {
		previous, err := s.decodeCursor(q.Cursor)
		if err != nil {
			return nil, err
		}
		if previous.UserID != binding.UserID || previous.Resource != binding.Resource || previous.Filters != binding.Filters || previous.LastCreatedAt == nil {
			return nil, ErrInventoryInvalid
		}
		binding = previous
	}
	query := repository.InventoryListQuery{Limit: q.Limit, LastID: binding.LastID, LastCreatedAt: binding.LastCreatedAt, Status: q.Status, SourceID: q.SourceID}
	var result *repository.InventoryListResult
	var err error
	if resource == "sync-rounds" {
		result, err = s.rounds.List(ctx, query)
	} else {
		result, err = s.rounds.Diagnostics(ctx, parent, resource == "round-diagnostics", query)
	}
	if err != nil {
		return nil, inventoryError(err)
	}
	page := &InventoryPage{Items: result.Items}
	if result.HasMore {
		binding.LastID = result.LastID
		binding.LastCreatedAt = result.LastCreatedAt
		cursor := s.encodeCursor(binding)
		page.NextCursor = &cursor
	}
	return page, nil
}
