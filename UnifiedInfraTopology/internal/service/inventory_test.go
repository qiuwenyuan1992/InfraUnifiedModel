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
	inventoryOther   = "22222222222222222222222222222222"
	inventoryGen     = "33333333333333333333333333333333"
	inventoryNext    = "44444444444444444444444444444444"
	inventorySource  = "55555555555555555555555555555555"
	inventoryDeviceA = "66666666666666666666666666666666"
	inventoryDeviceB = "77777777777777777777777777777777"
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
	err = db.AutoMigrate(&model.InventoryState{}, &model.Source{}, &model.Generation{}, &model.SyncRun{}, &model.SyncRunSource{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inventoryCreate(t, db, &model.InventoryState{ID: 1, ActiveGenerationID: ptrInventory(inventoryGen), ProjectionState: "ready", ProjectionEpoch: 1})
	inventoryCreate(t, db, &model.Generation{ID: inventoryGen, RunID: strings.Repeat("a", 32), State: "published", InventoryReady: true, GraphReady: true, CreatedAt: now, PublishedAt: &now})
	inventoryCreate(t, db, &model.Source{ID: inventorySource, Name: "source", Enabled: true})
	conf := viper.New()
	conf.Set("inventory.cursor_key", strings.Repeat("secret", 8))
	conf.Set("inventory.grants", []map[string]interface{}{{"user_id": "AliceCase", "permissions": []string{"inventory:read", "sync:read", "sync:write"}}})
	return NewInventoryService(repository.NewInventoryRepository(repository.NewRepository(nil, db)), newInventoryGraphFake(), conf), db, conf
}
func inventoryCreate(t *testing.T, db *gorm.DB, value interface{}) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}
func ptrInventory(v string) *string { return &v }

func TestInventoryProjectionReadGating(t *testing.T) {
	for _, state := range []string{"uninitialized", "updating", "failed"} {
		t.Run(state, func(t *testing.T) {
			s, db, _ := inventoryFixture(t)
			if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("projection_state", state).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetDevice(context.Background(), "AliceCase", inventoryDeviceA, ""); !errors.Is(err, ErrInventoryNotReady) {
				t.Fatalf("non-ready projection: %v", err)
			}
			if _, err := s.List(context.Background(), "AliceCase", "devices", "", InventoryQuery{}); !errors.Is(err, ErrInventoryNotReady) {
				t.Fatalf("non-ready projection list: %v", err)
			}
		})
	}
}

func TestInventoryRejectsNonCurrentGeneration(t *testing.T) {
	s, _, _ := inventoryFixture(t)
	if _, err := s.GetDevice(context.Background(), "AliceCase", inventoryDeviceA, inventoryNext); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("non-current selector: %v", err)
	}
}

func TestInventoryAuthorization(t *testing.T) {
	s, _, conf := inventoryFixture(t)
	ctx := context.Background()
	if _, err := s.List(ctx, "alicecase", "devices", "", InventoryQuery{}); !errors.Is(err, ErrInventoryForbidden) {
		t.Fatalf("case-sensitive grant: %v", err)
	}
	grants := conf.Get("inventory.grants").([]map[string]interface{})
	if len(grants) != 1 {
		t.Fatalf("grants: %+v", grants)
	}
	if len(grants[0]) != 2 {
		t.Fatalf("grant contains unexpected fields: %+v", grants[0])
	}
}

func TestInventoryCursorRejectsGenerationDriftAndTampering(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	page, err := s.List(ctx, "AliceCase", "devices", "", InventoryQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil || page.Items.([]model.Device)[0].EntityID != inventoryDeviceA {
		t.Fatalf("first page: %+v", page)
	}
	inventoryCreate(t, db, &model.Generation{ID: inventoryNext, RunID: strings.Repeat("b", 32), State: "published", InventoryReady: true})
	if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", inventoryNext).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, "AliceCase", "devices", "", InventoryQuery{Limit: 1, Cursor: *page.NextCursor}); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("generation drift: %v", err)
	}
	for _, q := range []InventoryQuery{{Cursor: *page.NextCursor + "x"}, {Cursor: *page.NextCursor, Name: "different"}} {
		if _, err = s.List(ctx, "AliceCase", "devices", "", q); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("invalid cursor: %v", err)
		}
	}
	if _, err := s.List(ctx, "AliceCase", "devices", "", InventoryQuery{Cursor: *page.NextCursor, GenerationID: inventoryNext}); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("cursor selector drift: %v", err)
	}
}

func TestInventoryEnqueueReplayUsesUnresolvedRequest(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	req := EnqueueInventoryRun{SourceIDs: []string{inventorySource}, Mode: "full"}
	run, err := s.Enqueue(ctx, "AliceCase", "request-1", req)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || run.BaseGenerationID == nil || *run.BaseGenerationID != inventoryGen {
		t.Fatalf("queued run: %+v", run)
	}
	if err = db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&model.Source{}).Where("id = ?", inventorySource).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := s.Enqueue(ctx, "AliceCase", "request-1", req)
	if err != nil || replay.ID != run.ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	req.BaseGenerationID = ptrInventory(inventoryGen)
	if _, err = s.Enqueue(ctx, "AliceCase", "request-1", req); !errors.Is(err, ErrInventoryIdempotency) {
		t.Fatalf("conflicting hash: %v", err)
	}
	var count int64
	if err = db.Model(&model.SyncRunSource{}).Where("run_id = ?", run.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("sources: %d %v", count, err)
	}
}

func TestInventoryEnqueueCanonicalizesDuplicateSources(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	inventoryCreate(t, db, &model.Source{ID: inventoryOther, Name: "second", Enabled: true})
	sources := []string{inventorySource, inventoryOther, inventorySource}
	run, err := s.Enqueue(ctx, "AliceCase", "deduplicated", EnqueueInventoryRun{Mode: "full", SourceIDs: sources})
	if err != nil {
		t.Fatalf("duplicate sources must be accepted: %v", err)
	}
	if sources[0] != inventorySource || sources[1] != inventoryOther || len(sources) != 3 {
		t.Fatalf("caller sources mutated: %v", sources)
	}
	replay, err := s.Enqueue(ctx, "AliceCase", "deduplicated", EnqueueInventoryRun{Mode: "full", SourceIDs: []string{inventoryOther, inventorySource}})
	if err != nil || replay.ID != run.ID || replay.RequestHash != run.RequestHash {
		t.Fatalf("canonical replay: %+v %v", replay, err)
	}
	canonical, err := s.Enqueue(ctx, "AliceCase", "canonical", EnqueueInventoryRun{Mode: "full", SourceIDs: []string{inventoryOther, inventorySource}})
	if err != nil || canonical.RequestHash != run.RequestHash {
		t.Fatalf("canonical request hash: %+v %v", canonical, err)
	}
	var count int64
	if err = db.Model(&model.SyncRunSource{}).Where("run_id = ?", run.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("deduplicated source rows: %d %v", count, err)
	}
}

func TestInventoryCancelAndContext(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	run, err := s.Enqueue(ctx, "AliceCase", "cancel", EnqueueInventoryRun{Mode: "full", SourceIDs: []string{inventorySource}})
	if err != nil {
		t.Fatal(err)
	}
	canceled, accepted, err := s.CancelRun(ctx, "AliceCase", run.ID)
	if err != nil || !accepted || canceled.Status != "canceled" || canceled.FinishedAt == nil {
		t.Fatalf("cancel: %+v %v", canceled, err)
	}
	again, accepted, err := s.CancelRun(ctx, "AliceCase", run.ID)
	if err != nil || accepted || !again.FinishedAt.Equal(*canceled.FinishedAt) {
		t.Fatalf("terminal mutated: %+v %v", again, err)
	}
	if err = db.Model(&model.SyncRun{}).Where("id = ?", run.ID).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	if _, accepted, err = s.CancelRun(ctx, "AliceCase", run.ID); accepted || !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("succeeded: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.GetRun(canceledCtx, "AliceCase", run.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("context: %v", err)
	}
}
