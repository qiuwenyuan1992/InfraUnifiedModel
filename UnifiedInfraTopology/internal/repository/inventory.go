package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"UnifiedInfraTopology/internal/model"
	"gorm.io/gorm"
)

type InventoryRepository interface {
	List(context.Context, string, InventoryListQuery) (*InventoryListResult, error)
	Enqueue(context.Context, *model.SyncRun) (*model.SyncRun, error)
	Run(context.Context, string) (*model.SyncRun, error)
	Cancel(context.Context, string) (*model.SyncRun, bool, error)
	ClaimNext(context.Context, time.Time) (*model.SyncRun, *model.Source, error)
	TransitionRun(context.Context, string, string, string, time.Time, string) (*model.SyncRun, error)
	RenewRunLease(context.Context, string, time.Time) error
	RunCancellationRequested(context.Context, string) (bool, error)
	LoadCheckpoint(context.Context, string, string) (*DeviceSyncCheckpoint, error)
	BeginCheckpoint(context.Context, string, string, string, time.Time, time.Time) (*DeviceSyncCheckpoint, error)
	AdvanceCheckpoint(context.Context, string, string, string, DeviceSyncCursor, time.Time) (*DeviceSyncCheckpoint, error)
	CompleteCheckpointAndRun(context.Context, string, string, string, string, time.Time) (*model.SyncRun, error)
}

type InventoryListQuery struct {
	Limit         int
	LastID        string
	LastCreatedAt *time.Time
	Status        string
	SourceID      string
}

type InventoryListResult struct {
	Items         interface{}
	HasMore       bool
	LastID        string
	LastCreatedAt *time.Time
}

type inventoryRepository struct{ r *Repository }

var (
	inventorySourceLocks sync.Map
	inventoryClaimLock   sync.Mutex
)

func NewInventoryRepository(r *Repository) InventoryRepository { return &inventoryRepository{r: r} }

func lockInventorySource(sourceID string) func() {
	value, _ := inventorySourceLocks.LoadOrStore(sourceID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func inventoryDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInventoryNotFound
	}
	return err
}

func (r *inventoryRepository) Run(ctx context.Context, id string) (*model.SyncRun, error) {
	var run model.SyncRun
	err := r.r.DB(ctx).Where("id = ?", id).First(&run).Error
	return &run, inventoryDBError(err)
}
