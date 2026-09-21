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
	GetDevice(ctx context.Context, userID, deviceID, generationID string) (*InventoryDevice, error)
	Enqueue(ctx context.Context, userID, key string, req EnqueueInventoryRun) (*model.SyncRun, error)
	GetRun(ctx context.Context, userID, runID string) (*model.SyncRun, error)
	CancelRun(ctx context.Context, userID, runID string) (*model.SyncRun, bool, error)
}

type InventoryQuery struct {
	Limit                                                                                             int
	Cursor, GenerationID, DeviceKind, Name, Lifecycle, InterfaceKind, AddressFamily, Status, SourceID string
}

type InventoryPage struct {
	Items          interface{} `json:"items"`
	NextCursor     *string     `json:"next_cursor"`
	GenerationID   *string     `json:"generation_id,omitempty"`
	PublishedAt    *time.Time  `json:"published_at,omitempty"`
	InventoryReady *bool       `json:"inventory_ready,omitempty"`
	GraphReady     *bool       `json:"graph_ready,omitempty"`
	RoutingReady   *bool       `json:"routing_ready,omitempty"`
}

type InventoryDevice struct {
	Device     model.Device
	Generation model.Generation
}

type EnqueueInventoryRun struct {
	SourceIDs        []string `json:"source_ids"`
	Mode             string   `json:"mode"`
	BaseGenerationID *string  `json:"base_generation_id"`
}

type inventoryGrant struct {
	UserID      string   `mapstructure:"user_id"`
	Permissions []string `mapstructure:"permissions"`
}

type inventoryService struct {
	repo      repository.InventoryRepository
	graph     repository.GraphInventoryRepository
	cursorKey []byte
	grants    []inventoryGrant
}

func NewInventoryService(repo repository.InventoryRepository, graph repository.GraphInventoryRepository, conf *viper.Viper) InventoryService {
	s := &inventoryService{repo: repo, graph: graph}
	if conf != nil {
		s.cursorKey = []byte(conf.GetString("inventory.cursor_key"))
		// 不通过 GetStringMap 读取授权主体，避免用户标识被转成小写。
		if err := conf.UnmarshalKey("inventory.grants", &s.grants); err != nil {
			s.grants = nil
		}
	}
	return s
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
	for _, g := range s.grants {
		if g.UserID == userID {
			for _, p := range g.Permissions {
				if p == permission {
					return true
				}
			}
		}
	}
	return false
}

func (s *inventoryService) authorize(userID, resource string) error {
	allowed := false
	switch resource {
	case "devices", "interfaces", "addresses":
		allowed = s.allowed(userID, "inventory:read")
	case "sources", "sync-runs":
		allowed = s.allowed(userID, "sync:read")
	case "generations":
		allowed = s.allowed(userID, "inventory:read") || s.allowed(userID, "topology:read")
	case "write":
		allowed = s.allowed(userID, "sync:read") && s.allowed(userID, "sync:write")
	}
	if !allowed {
		return ErrInventoryForbidden
	}
	return nil
}

func (s *inventoryService) resolveGeneration(ctx context.Context, id string) (*model.InventoryState, *model.Generation, error) {
	state, err := s.repo.State(ctx)
	if err != nil {
		return nil, nil, inventoryError(err)
	}
	if state.ProjectionState != "ready" || state.ActiveGenerationID == nil || *state.ActiveGenerationID == "" {
		return nil, nil, ErrInventoryNotReady
	}
	if id != "" && id != *state.ActiveGenerationID {
		return nil, nil, ErrInventoryConflict
	}
	generation, err := s.repo.Generation(ctx, *state.ActiveGenerationID)
	if errors.Is(err, repository.ErrInventoryNotFound) {
		return nil, nil, ErrInventoryNotReady
	}
	if err != nil {
		return nil, nil, inventoryError(err)
	}
	if generation.State != "published" || !generation.InventoryReady || !generation.GraphReady {
		return nil, nil, ErrInventoryNotReady
	}
	return state, generation, nil
}

func (s *inventoryService) checkProjection(ctx context.Context, before *model.InventoryState) error {
	after, err := s.repo.State(ctx)
	if err != nil {
		return inventoryError(err)
	}
	if after.ProjectionState != "ready" || after.ActiveGenerationID == nil || *after.ActiveGenerationID == "" {
		return ErrInventoryNotReady
	}
	if after.ProjectionEpoch != before.ProjectionEpoch || *after.ActiveGenerationID != *before.ActiveGenerationID {
		return ErrInventoryConflict
	}
	return nil
}

func (s *inventoryService) GetDevice(ctx context.Context, userID, deviceID, generationID string) (*InventoryDevice, error) {
	if err := s.authorize(userID, "devices"); err != nil {
		return nil, err
	}
	if !inventoryID(deviceID) || (generationID != "" && !inventoryID(generationID)) {
		return nil, ErrInventoryInvalid
	}
	state, generation, err := s.resolveGeneration(ctx, generationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkProjection(ctx, state); err != nil {
		return nil, err
	}
	device, err := s.graph.Device(ctx, deviceID)
	if checkErr := s.checkProjection(ctx, state); checkErr != nil {
		return nil, checkErr
	}
	if err != nil {
		return nil, inventoryError(err)
	}
	current := *device
	current.GenerationID = generation.ID
	return &InventoryDevice{Device: current, Generation: *generation}, nil
}
