package service

import (
	"context"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
)

var (
	ErrInventoryInvalid     = errors.New("invalid inventory argument")
	ErrInventoryForbidden   = errors.New("inventory permission denied")
	ErrInventoryNotFound    = errors.New("inventory resource not found")
	ErrInventoryNotReady    = errors.New("inventory not ready")
	ErrInventoryConflict    = errors.New("inventory state conflict")
	ErrInventoryIdempotency = errors.New("inventory idempotency conflict")
)

type InventoryService interface {
	List(ctx context.Context, userID, resource, parentID string, query InventoryQuery) (*InventoryPage, error)
	Enqueue(ctx context.Context, userID, key string, request EnqueueInventoryRun) (*model.SyncRun, error)
	GetRun(ctx context.Context, userID, runID string) (*model.SyncRun, error)
	CancelRun(ctx context.Context, userID, runID string) (*model.SyncRun, bool, error)
}

type InventoryQuery struct {
	Limit    int
	Cursor   string
	Status   string
	SourceID string
}

type InventoryPage struct {
	Items      interface{} `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

type EnqueueInventoryRun struct {
	SourceID string `json:"source_id"`
	Mode     string `json:"mode"`
}

type inventoryGrant struct {
	UserID      string   `mapstructure:"user_id"`
	Permissions []string `mapstructure:"permissions"`
}

type inventoryService struct {
	repo      repository.InventoryRepository
	cursorKey []byte
	grants    []inventoryGrant
	now       func() time.Time
}

func NewInventoryService(repo repository.InventoryRepository, conf *viper.Viper) InventoryService {
	service := &inventoryService{repo: repo, now: time.Now}
	if conf != nil {
		service.cursorKey = []byte(conf.GetString("inventory.cursor_key"))
		// 不通过 GetStringMap 读取授权主体，避免用户标识被转成小写。
		if err := conf.UnmarshalKey("inventory.grants", &service.grants); err != nil {
			service.grants = nil
		}
	}
	return service
}

func inventoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrInventoryNotFound):
		return ErrInventoryNotFound
	case errors.Is(err, repository.ErrInventoryNotReady):
		return ErrInventoryNotReady
	case errors.Is(err, repository.ErrInventoryConflict):
		return ErrInventoryConflict
	case errors.Is(err, repository.ErrInventoryIdempotency):
		return ErrInventoryIdempotency
	case errors.Is(err, repository.ErrInventorySource):
		return ErrInventoryInvalid
	default:
		return err
	}
}

func (s *inventoryService) allowed(userID, permission string) bool {
	for _, grant := range s.grants {
		if grant.UserID != userID {
			continue
		}
		for _, current := range grant.Permissions {
			if current == permission {
				return true
			}
		}
	}
	return false
}

func (s *inventoryService) authorize(userID, resource string) error {
	allowed := false
	switch resource {
	case "sources", "sync-runs":
		allowed = s.allowed(userID, "sync:read")
	case "write":
		allowed = s.allowed(userID, "sync:read") && s.allowed(userID, "sync:write")
	}
	if !allowed {
		return ErrInventoryForbidden
	}
	return nil
}
