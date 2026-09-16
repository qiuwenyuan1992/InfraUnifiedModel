package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestInventoryRepositoryUsesCallerTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.TopologyScope{}, &model.Source{}, &model.Generation{}, &model.SyncRun{}, &model.SyncRunSource{}); err != nil {
		t.Fatal(err)
	}
	r := NewRepository(nil, db)
	repo := NewInventoryRepository(r)
	ctx := context.Background()
	for _, resource := range []string{"devices", "interfaces", "addresses"} {
		if result, err := repo.List(ctx, resource, InventoryListQuery{Limit: 1}); err == nil || result != nil {
			t.Fatalf("SQL accepted graph asset %s: %+v %v", resource, result, err)
		}
	}
	scopeID := strings.Repeat("a", 32)
	sourceID := strings.Repeat("b", 32)
	if err = db.Create(&model.TopologyScope{ID: scopeID, Name: "transaction"}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Source{ID: sourceID, ScopeID: scopeID, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback outer transaction")
	err = r.Transaction(ctx, func(txCtx context.Context) error {
		request := &model.SyncRun{ID: strings.Repeat("c", 32), ScopeID: scopeID, Status: "queued", Mode: "full", IdempotencyKey: "transaction", RequestHash: "hash", CreatedAt: time.Now().UTC()}
		run, createErr := repo.Enqueue(txCtx, request, []string{sourceID})
		if createErr != nil {
			return createErr
		}
		if _, getErr := repo.Run(txCtx, scopeID, run.ID); getErr != nil {
			return getErr
		}
		canceled, accepted, cancelErr := repo.Cancel(txCtx, scopeID, run.ID)
		if cancelErr != nil {
			return cancelErr
		}
		if !accepted || canceled.Status != "canceled" {
			t.Fatalf("cancel: %+v accepted=%v", canceled, accepted)
		}
		again, accepted, cancelErr := repo.Cancel(txCtx, scopeID, run.ID)
		if cancelErr != nil || accepted || !again.FinishedAt.Equal(*canceled.FinishedAt) {
			t.Fatalf("repeat cancel: %+v accepted=%v err=%v", again, accepted, cancelErr)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("outer transaction: %v", err)
	}
	for _, value := range []interface{}{&model.SyncRun{}, &model.SyncRunSource{}} {
		var count int64
		if err = db.Model(value).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("outer rollback %T: %d %v", value, count, err)
		}
	}
}
