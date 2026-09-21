package repository

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func inventoryRepositoryFixture(t *testing.T) (InventoryRepository, *Repository, *gorm.DB) {
	t.Helper()
	return inventoryRepositoryFixtureWithDSN(t, ":memory:", 1)
}

func inventoryRepositoryConcurrentFixture(t *testing.T) (InventoryRepository, *Repository, *gorm.DB) {
	t.Helper()
	return inventoryRepositoryFixtureWithDSN(t, t.TempDir()+"/inventory.db", 8)
}

func inventoryRepositoryFixtureWithDSN(t *testing.T, dsn string, maxOpenConns int) (InventoryRepository, *Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.Source{}, &model.SyncRun{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(nil, db)
	return NewInventoryRepository(repository), repository, db
}

func TestInventoryRepositoryUsesCallerTransaction(t *testing.T) {
	repo, repository, db := inventoryRepositoryFixture(t)
	sourceID := strings.Repeat("b", 32)
	if err := db.Create(&model.Source{ID: sourceID, Name: "primary", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback outer transaction")
	err := repository.Transaction(context.Background(), func(ctx context.Context) error {
		run, createErr := repo.Enqueue(ctx, &model.SyncRun{
			ID: strings.Repeat("c", 32), SourceID: sourceID, Status: "queued", Mode: "full",
			IdempotencyKey: "transaction", RequestHash: strings.Repeat("d", 64), CreatedAt: time.Now().UTC(),
		})
		if createErr != nil {
			return createErr
		}
		if _, getErr := repo.Run(ctx, run.ID); getErr != nil {
			return getErr
		}
		canceled, accepted, cancelErr := repo.Cancel(ctx, run.ID)
		if cancelErr != nil {
			return cancelErr
		}
		if !accepted || canceled.Status != "canceled" {
			t.Fatalf("cancel: %+v accepted=%v", canceled, accepted)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("outer transaction: %v", err)
	}
	var count int64
	if err := db.Model(&model.SyncRun{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("outer rollback: %d %v", count, err)
	}
}

func TestInventoryRepositoryEnqueueIsScopedToSource(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	sourceA := strings.Repeat("a", 32)
	sourceB := strings.Repeat("b", 32)
	for _, source := range []model.Source{
		{ID: sourceA, Name: "a", Enabled: true},
		{ID: sourceB, Name: "b", Enabled: true},
	} {
		if err := db.Create(&source).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	first := &model.SyncRun{ID: strings.Repeat("1", 32), SourceID: sourceA, Status: "queued", Mode: "full", RequestHash: "hash-a", IdempotencyKey: "same", CreatedAt: now}
	created, err := repo.Enqueue(context.Background(), first)
	if err != nil || created.ID != first.ID {
		t.Fatalf("first enqueue: %+v %v", created, err)
	}
	replay, err := repo.Enqueue(context.Background(), &model.SyncRun{ID: strings.Repeat("2", 32), SourceID: sourceA, RequestHash: "hash-a", IdempotencyKey: "same"})
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = repo.Enqueue(context.Background(), &model.SyncRun{ID: strings.Repeat("3", 32), SourceID: sourceA, RequestHash: "different", IdempotencyKey: "same"}); !errors.Is(err, ErrInventoryIdempotency) {
		t.Fatalf("conflicting replay: %v", err)
	}
	second, err := repo.Enqueue(context.Background(), &model.SyncRun{ID: strings.Repeat("4", 32), SourceID: sourceB, Status: "queued", Mode: "full", RequestHash: "hash-b", IdempotencyKey: "same", CreatedAt: now})
	if err != nil || second.SourceID != sourceB {
		t.Fatalf("source-scoped key: %+v %v", second, err)
	}
}

func TestInventoryRepositorySerializesActiveRunsBySource(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	sourceID := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: sourceID, Name: "primary", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first, err := repo.Enqueue(context.Background(), &model.SyncRun{
		ID: strings.Repeat("1", 32), SourceID: sourceID, Status: "queued", Mode: "full",
		RequestHash: "hash-1", IdempotencyKey: "request-1", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(context.Background(), &model.SyncRun{
		ID: strings.Repeat("2", 32), SourceID: sourceID, Status: "queued", Mode: "full",
		RequestHash: "hash-2", IdempotencyKey: "request-2", CreatedAt: now,
	}); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("active run conflict: %v", err)
	}
	if _, accepted, err := repo.Cancel(context.Background(), first.ID); err != nil || !accepted {
		t.Fatalf("cancel first run: accepted=%v err=%v", accepted, err)
	}
	second, err := repo.Enqueue(context.Background(), &model.SyncRun{
		ID: strings.Repeat("2", 32), SourceID: sourceID, Status: "queued", Mode: "full",
		RequestHash: "hash-2", IdempotencyKey: "request-2", CreatedAt: now,
	})
	if err != nil || second.ID != strings.Repeat("2", 32) {
		t.Fatalf("enqueue after terminal run: %+v %v", second, err)
	}
}

func TestInventoryRepositoryConcurrentReplayCreatesOneRun(t *testing.T) {
	repo, _, db := inventoryRepositoryConcurrentFixture(t)
	sourceID := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: sourceID, Name: "primary", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	const workers = 8
	results := make(chan *model.SyncRun, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			run, err := repo.Enqueue(context.Background(), &model.SyncRun{
				ID: strings.Repeat(string(rune('1'+index)), 32), SourceID: sourceID, Status: "queued", Mode: "full",
				RequestHash: "same-hash", IdempotencyKey: "same-request", CreatedAt: time.Now().UTC(),
			})
			results <- run
			errorsCh <- err
		}(index)
	}
	wait.Wait()
	close(results)
	close(errorsCh)

	var runID string
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent replay: %v", err)
		}
	}
	for run := range results {
		if run == nil {
			t.Fatal("concurrent replay returned nil run")
		}
		if runID == "" {
			runID = run.ID
		}
		if run.ID != runID {
			t.Fatalf("concurrent replay returned different runs: %s != %s", run.ID, runID)
		}
	}
	var count int64
	if err := db.Model(&model.SyncRun{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("run count: %d %v", count, err)
	}
}

func TestInventoryRepositoryConcurrentRequestsAllowOnlyOneActiveRun(t *testing.T) {
	repo, _, db := inventoryRepositoryConcurrentFixture(t)
	sourceID := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: sourceID, Name: "primary", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := repo.Enqueue(context.Background(), &model.SyncRun{
				ID: strings.Repeat(string(rune('1'+index)), 32), SourceID: sourceID, Status: "queued", Mode: "full",
				RequestHash: "hash-" + string(rune('1'+index)), IdempotencyKey: "request-" + string(rune('1'+index)), CreatedAt: time.Now().UTC(),
			})
			errorsCh <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsCh)

	succeeded := 0
	conflicted := 0
	for err := range errorsCh {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInventoryConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent enqueue error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestInventoryRepositoryRejectsUnavailableSource(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	disabled := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: disabled, Name: "disabled", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	for _, sourceID := range []string{disabled, strings.Repeat("b", 32)} {
		_, err := repo.Enqueue(context.Background(), &model.SyncRun{ID: strings.Repeat("c", 32), SourceID: sourceID, IdempotencyKey: "key", RequestHash: "hash"})
		if !errors.Is(err, ErrInventorySource) {
			t.Fatalf("source %q: %v", sourceID, err)
		}
	}
}

func TestInventoryRepositoryListsCurrentControlRecords(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	sourceID := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: sourceID, Name: "primary", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)
	for index, id := range []string{strings.Repeat("1", 32), strings.Repeat("2", 32)} {
		if err := db.Create(&model.SyncRun{ID: id, SourceID: sourceID, Status: "running", Mode: "full", IdempotencyKey: id, RequestHash: id + id, CreatedAt: base.Add(time.Duration(index) * time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.List(context.Background(), "sync-runs", InventoryListQuery{Limit: 1, SourceID: sourceID, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	rows := page.Items.([]model.SyncRun)
	if !page.HasMore || len(rows) != 1 || rows[0].ID != strings.Repeat("2", 32) || page.LastCreatedAt == nil {
		t.Fatalf("first page: %+v", page)
	}
	next, err := repo.List(context.Background(), "sync-runs", InventoryListQuery{Limit: 1, SourceID: sourceID, Status: "running", LastID: page.LastID, LastCreatedAt: page.LastCreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	rows = next.Items.([]model.SyncRun)
	if next.HasMore || len(rows) != 1 || rows[0].ID != strings.Repeat("1", 32) {
		t.Fatalf("second page: %+v", next)
	}
	if _, err := repo.List(context.Background(), "devices", InventoryListQuery{Limit: 1}); err == nil {
		t.Fatal("obsolete asset resource was accepted")
	}
}
