package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/glebarez/sqlite"
	"github.com/spf13/viper"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	inventorySource = "55555555555555555555555555555555"
	inventoryOther  = "66666666666666666666666666666666"
	inventoryRunA   = "77777777777777777777777777777777"
	inventoryRunB   = "88888888888888888888888888888888"
)

func inventoryFixture(t *testing.T) (InventoryService, *gorm.DB, *viper.Viper) {
	t.Helper()
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
	if err = db.AutoMigrate(&model.Source{}, &model.SyncRun{}); err != nil {
		t.Fatal(err)
	}
	inventoryCreate(t, db, &model.Source{ID: inventorySource, Name: "primary", Enabled: true})
	conf := viper.New()
	conf.Set("inventory.cursor_key", strings.Repeat("secret", 8))
	conf.Set("inventory.grants", []map[string]interface{}{{"user_id": "AliceCase", "permissions": []string{"sync:read", "sync:write"}}})
	return NewInventoryService(repository.NewInventoryRepository(repository.NewRepository(nil, db)), conf), db, conf
}

func inventoryCreate(t *testing.T, db *gorm.DB, value interface{}) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}

func TestInventoryListsSourcesAndRuns(t *testing.T) {
	service, db, _ := inventoryFixture(t)
	inventoryCreate(t, db, &model.Source{ID: inventoryOther, Name: "secondary", Enabled: true})
	first, err := service.List(context.Background(), "AliceCase", "sources", "", InventoryQuery{Limit: 1})
	if err != nil || first.NextCursor == nil || len(first.Items.([]model.Source)) != 1 {
		t.Fatalf("first source page: %+v %v", first, err)
	}
	second, err := service.List(context.Background(), "AliceCase", "sources", "", InventoryQuery{Limit: 1, Cursor: *first.NextCursor})
	if err != nil || second.NextCursor != nil || len(second.Items.([]model.Source)) != 1 {
		t.Fatalf("second source page: %+v %v", second, err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	for index, id := range []string{inventoryRunA, inventoryRunB} {
		inventoryCreate(t, db, &model.SyncRun{
			ID: id, SourceID: inventorySource, Status: "running", Mode: "full",
			RequestHash: id + id, IdempotencyKey: id, CreatedAt: now.Add(time.Duration(index) * time.Second),
		})
	}
	runs, err := service.List(context.Background(), "AliceCase", "sync-runs", "", InventoryQuery{Limit: 1, SourceID: inventorySource, Status: "running"})
	if err != nil || runs.NextCursor == nil || runs.Items.([]model.SyncRun)[0].ID != inventoryRunB {
		t.Fatalf("run page: %+v %v", runs, err)
	}
}

func TestInventoryEnqueueReplayAndCancellation(t *testing.T) {
	service, db, _ := inventoryFixture(t)
	fixedNow := time.Date(2026, 9, 21, 12, 34, 56, 123456789, time.UTC)
	service.(*inventoryService).now = func() time.Time { return fixedNow }
	request := EnqueueInventoryRun{SourceID: inventorySource, Mode: "full"}
	run, err := service.Enqueue(context.Background(), "AliceCase", "request-1", request)
	if err != nil || run.SourceID != inventorySource || run.Status != "queued" {
		t.Fatalf("enqueue: %+v %v", run, err)
	}
	if want := fixedNow.Truncate(time.Microsecond); !run.CreatedAt.Equal(want) {
		t.Fatalf("created_at = %s, want %s", run.CreatedAt, want)
	}
	replay, err := service.Enqueue(context.Background(), "AliceCase", "request-1", request)
	if err != nil || replay.ID != run.ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = service.Enqueue(context.Background(), "AliceCase", "request-2", request); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("active source conflict: %v", err)
	}
	if _, err = service.Enqueue(context.Background(), "AliceCase", "request-1", EnqueueInventoryRun{SourceID: inventoryOther, Mode: "full"}); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatalf("unavailable source: %v", err)
	}
	canceled, accepted, err := service.CancelRun(context.Background(), "AliceCase", run.ID)
	if err != nil || !accepted || canceled.Status != "canceled" || canceled.FinishedAt == nil {
		t.Fatalf("cancel: %+v accepted=%v err=%v", canceled, accepted, err)
	}
	again, accepted, err := service.CancelRun(context.Background(), "AliceCase", run.ID)
	if err != nil || accepted || !again.FinishedAt.Equal(*canceled.FinishedAt) {
		t.Fatalf("repeat cancel: %+v accepted=%v err=%v", again, accepted, err)
	}
	var count int64
	if err := db.Model(&model.SyncRun{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("run count: %d %v", count, err)
	}
}

func TestInventoryAuthorizationAndValidation(t *testing.T) {
	service, _, conf := inventoryFixture(t)
	ctx := context.Background()
	for _, test := range []struct {
		resource string
		query    InventoryQuery
	}{
		{"devices", InventoryQuery{}},
		{"sources", InventoryQuery{Status: "running"}},
		{"sync-runs", InventoryQuery{SourceID: "invalid"}},
		{"sync-runs", InventoryQuery{Status: "unknown"}},
	} {
		if _, err := service.List(ctx, "AliceCase", test.resource, "", test.query); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("%+v: %v", test, err)
		}
	}
	for _, key := range []string{"", "bad\nkey", strings.Repeat("x", 129)} {
		if _, err := service.Enqueue(ctx, "AliceCase", key, EnqueueInventoryRun{SourceID: inventorySource, Mode: "full"}); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	if _, err := service.Enqueue(ctx, "AliceCase", "bad-mode", EnqueueInventoryRun{SourceID: inventorySource, Mode: "incremental"}); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatalf("mode: %v", err)
	}
	conf.Set("inventory.grants", []map[string]interface{}{{"user_id": "reader", "permissions": []string{"sync:read"}}})
	reader := NewInventoryService(service.(*inventoryService).repo, conf)
	if _, err := reader.List(ctx, "reader", "sources", "", InventoryQuery{}); err != nil {
		t.Fatalf("reader list: %v", err)
	}
	if _, err := reader.Enqueue(ctx, "reader", "write", EnqueueInventoryRun{SourceID: inventorySource, Mode: "full"}); !errors.Is(err, ErrInventoryForbidden) {
		t.Fatalf("reader write: %v", err)
	}
}

func TestInventoryCursorRejectsTamperingAndFilterDrift(t *testing.T) {
	service, db, _ := inventoryFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	for index, id := range []string{inventoryRunA, inventoryRunB} {
		inventoryCreate(t, db, &model.SyncRun{
			ID: id, SourceID: inventorySource, Status: "running", Mode: "full",
			RequestHash: id + id, IdempotencyKey: id, CreatedAt: now.Add(time.Duration(index) * time.Second),
		})
	}
	query := InventoryQuery{Limit: 1, Status: "running"}
	page, err := service.List(context.Background(), "AliceCase", "sync-runs", "", query)
	if err != nil || page.NextCursor == nil {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if _, err := service.List(context.Background(), "AliceCase", "sync-runs", "", InventoryQuery{Limit: 1, Status: "queued", Cursor: *page.NextCursor}); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatalf("filter drift: %v", err)
	}
	query.Cursor = *page.NextCursor + "x"
	if _, err := service.List(context.Background(), "AliceCase", "sync-runs", "", query); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatalf("tampered cursor: %v", err)
	}
}
