package repository

import (
	"context"
	"errors"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
)

var (
	ErrInventoryNotFound    = errors.New("inventory resource not found")
	ErrInventoryNotReady    = errors.New("inventory generation not ready")
	ErrInventoryConflict    = errors.New("inventory state conflict")
	ErrInventoryIdempotency = errors.New("inventory idempotency conflict")
	ErrInventorySource      = errors.New("inventory source unavailable")
)

type InventoryRepository interface {
	Scope(context.Context, string) (*model.TopologyScope, error)
	Generation(context.Context, string, string) (*model.Generation, error)
	List(context.Context, string, InventoryListQuery) (*InventoryListResult, error)
	Enqueue(context.Context, *model.SyncRun, []string) (*model.SyncRun, error)
	Run(context.Context, string, string) (*model.SyncRun, error)
	Cancel(context.Context, string, string) (*model.SyncRun, bool, error)
}

type GraphInventoryRepository interface {
	Device(ctx context.Context, scopeID, id string) (*model.Device, error)
	List(ctx context.Context, resource string, q InventoryListQuery) (*InventoryListResult, error)
}

type InventoryListQuery struct {
	ScopeID, ParentID, GenerationID                                             string
	ScopeIDs                                                                    []string
	Limit                                                                       int
	LastID                                                                      string
	LastCreatedAt                                                               *time.Time
	DeviceKind, Name, Lifecycle, InterfaceKind, AddressFamily, Status, SourceID string
}

type InventoryListResult struct {
	Items         interface{}
	HasMore       bool
	LastID        string
	LastCreatedAt *time.Time
}

type inventoryRepository struct{ r *Repository }

func NewInventoryRepository(r *Repository) InventoryRepository { return &inventoryRepository{r: r} }

func inventoryDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInventoryNotFound
	}
	return err
}

func (r *inventoryRepository) Scope(ctx context.Context, id string) (*model.TopologyScope, error) {
	var scope model.TopologyScope
	err := r.r.DB(ctx).WithContext(ctx).Where("id = ?", id).First(&scope).Error
	return &scope, inventoryDBError(err)
}

func (r *inventoryRepository) Generation(ctx context.Context, scopeID, id string) (*model.Generation, error) {
	var generation model.Generation
	err := r.r.DB(ctx).WithContext(ctx).Where("id = ? AND scope_id = ? AND state = ?", id, scopeID, "published").First(&generation).Error
	return &generation, inventoryDBError(err)
}

func (r *inventoryRepository) Run(ctx context.Context, scopeID, id string) (*model.SyncRun, error) {
	var run model.SyncRun
	err := r.r.DB(ctx).WithContext(ctx).Where("id = ? AND scope_id = ?", id, scopeID).First(&run).Error
	return &run, inventoryDBError(err)
}
