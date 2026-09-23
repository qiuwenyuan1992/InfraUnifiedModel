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
	if err = db.AutoMigrate(&model.Source{}, &model.SyncRun{}, &model.SyncCheckpoint{}); err != nil {
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

func TestInventorySyncRunTransitionWhitelist(t *testing.T) {
	allowed := [][2]string{
		{model.SyncRunStatusRunning, model.SyncRunStatusValidating},
		{model.SyncRunStatusValidating, model.SyncRunStatusPublishing},
		{model.SyncRunStatusPublishing, model.SyncRunStatusSucceeded},
		{model.SyncRunStatusRunning, model.SyncRunStatusFailed},
		{model.SyncRunStatusValidating, model.SyncRunStatusFailed},
		{model.SyncRunStatusPublishing, model.SyncRunStatusFailed},
		{model.SyncRunStatusRunning, model.SyncRunStatusCanceled},
		{model.SyncRunStatusValidating, model.SyncRunStatusCanceled},
	}
	for _, transition := range allowed {
		if !model.SyncRunTransitionAllowed(transition[0], transition[1]) {
			t.Errorf("expected transition %s -> %s to be allowed", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]string{
		{model.SyncRunStatusQueued, model.SyncRunStatusRunning},
		{model.SyncRunStatusRunning, model.SyncRunStatusSucceeded},
		{model.SyncRunStatusPublishing, model.SyncRunStatusCanceled},
		{model.SyncRunStatusSucceeded, model.SyncRunStatusFailed},
		{"unknown", model.SyncRunStatusFailed},
	} {
		if model.SyncRunTransitionAllowed(transition[0], transition[1]) {
			t.Errorf("expected transition %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestInventoryRepositoryClaimNextClaimsEarliestQueuedRun(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	base := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	sources := []model.Source{
		{ID: strings.Repeat("a", 32), Name: "disabled", Enabled: false},
		{ID: strings.Repeat("b", 32), Name: "enabled", Enabled: true},
	}
	if err := db.Create(&sources).Error; err != nil {
		t.Fatal(err)
	}
	runs := []model.SyncRun{
		{ID: strings.Repeat("2", 32), SourceID: sources[1].ID, Status: model.SyncRunStatusQueued, CreatedAt: base},
		{ID: strings.Repeat("1", 32), SourceID: sources[0].ID, Status: model.SyncRunStatusQueued, CreatedAt: base},
		{ID: strings.Repeat("3", 32), SourceID: sources[1].ID, Status: model.SyncRunStatusQueued, CreatedAt: base.Add(-time.Hour)},
	}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 9, 22, 8, 9, 10, 123456789, time.FixedZone("offset", 8*60*60))
	run, source, err := repo.ClaimNext(context.Background(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantTime := startedAt.UTC().Truncate(time.Microsecond)
	if run.ID != runs[2].ID || run.Status != model.SyncRunStatusRunning || run.StartedAt == nil || !run.StartedAt.Equal(wantTime) ||
		run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.Equal(wantTime.Add(inventoryRunLeaseDuration)) {
		t.Fatalf("claimed run: %+v", run)
	}
	if source.ID != sources[1].ID {
		t.Fatalf("claimed source: %+v", source)
	}

	run, source, err = repo.ClaimNext(context.Background(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != runs[1].ID || source.ID != sources[0].ID || source.Enabled {
		t.Fatalf("disabled source claim: run=%+v source=%+v", run, source)
	}
}

func TestInventoryRepositoryEnqueueExpiresStaleActiveRunAndAllowsResumeRequest(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	sourceID := strings.Repeat("a", 32)
	staleRunID := strings.Repeat("1", 32)
	leaseExpiredAt := now.Add(-time.Minute)
	startedAt := now.Add(-time.Hour)
	if err := db.Create(&model.Source{ID: sourceID, Name: "cmdb", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SyncRun{
		ID: staleRunID, SourceID: sourceID, Status: model.SyncRunStatusRunning,
		RequestHash: "old-hash", IdempotencyKey: "old-key", CreatedAt: startedAt, StartedAt: &startedAt,
		LeaseExpiresAt: &leaseExpiredAt,
	}).Error; err != nil {
		t.Fatal(err)
	}

	newRunID := strings.Repeat("2", 32)
	created, err := repo.Enqueue(context.Background(), &model.SyncRun{
		ID: newRunID, SourceID: sourceID, Status: model.SyncRunStatusQueued, Mode: "full",
		RequestHash: "new-hash", IdempotencyKey: "new-key", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != newRunID {
		t.Fatalf("created run: %+v", created)
	}
	stale, err := repo.Run(context.Background(), staleRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != model.SyncRunStatusFailed || stale.ErrorCode != syncErrorLeaseExpired || stale.FinishedAt == nil || stale.LeaseExpiresAt != nil {
		t.Fatalf("expired run: %+v", stale)
	}
}

func TestInventoryRepositoryClaimNextSkipsCanceledQueueAndReportsEmpty(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	now := time.Now().UTC()
	sourceID := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: sourceID, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SyncRun{
		ID: strings.Repeat("1", 32), SourceID: sourceID, Status: model.SyncRunStatusQueued,
		CancelRequestedAt: &now, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if run, source, err := repo.ClaimNext(context.Background(), now); !errors.Is(err, ErrInventoryNoRun) || run != nil || source != nil {
		t.Fatalf("empty claim: run=%+v source=%+v err=%v", run, source, err)
	}
}

func TestInventoryRepositoryClaimNextRejectsMissingSource(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	runID := strings.Repeat("1", 32)
	if err := db.Create(&model.SyncRun{
		ID: runID, SourceID: strings.Repeat("a", 32), Status: model.SyncRunStatusQueued, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if run, source, err := repo.ClaimNext(context.Background(), time.Now()); !errors.Is(err, ErrInventorySource) || run != nil || source != nil {
		t.Fatalf("missing source claim: run=%+v source=%+v err=%v", run, source, err)
	}
	stored, err := repo.Run(context.Background(), runID)
	if err != nil || stored.Status != model.SyncRunStatusQueued || stored.StartedAt != nil {
		t.Fatalf("claim rollback: run=%+v err=%v", stored, err)
	}
}

func TestInventoryRepositoryClaimNextIsUniqueAcrossConcurrentCallers(t *testing.T) {
	repo, _, db := inventoryRepositoryConcurrentFixture(t)
	sourceID := strings.Repeat("a", 32)
	if err := db.Create(&model.Source{ID: sourceID, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SyncRun{
		ID: strings.Repeat("1", 32), SourceID: sourceID, Status: model.SyncRunStatusQueued, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	const workers = 8
	start := make(chan struct{})
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, _, err := repo.ClaimNext(context.Background(), time.Now())
			errorsCh <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsCh)

	claimed := 0
	empty := 0
	for err := range errorsCh {
		switch {
		case err == nil:
			claimed++
		case errors.Is(err, ErrInventoryNoRun):
			empty++
		default:
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if claimed != 1 || empty != workers-1 {
		t.Fatalf("claim results: claimed=%d empty=%d", claimed, empty)
	}
}

func TestInventoryRepositoryTransitionRunCompletesSuccessfulStateMachine(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	runID := strings.Repeat("1", 32)
	at := time.Date(2026, 9, 23, 15, 9, 10, 987654321, time.UTC)
	oldFinishedAt := at.Add(-time.Hour)
	leaseExpiresAt := at.Add(inventoryRunLeaseDuration)
	if err := db.Create(&model.SyncRun{
		ID: runID, SourceID: strings.Repeat("a", 32), Status: model.SyncRunStatusRunning,
		ErrorCode: "stale", FinishedAt: &oldFinishedAt, CreatedAt: at, StartedAt: &at, LeaseExpiresAt: &leaseExpiresAt,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, transition := range [][2]string{
		{model.SyncRunStatusRunning, model.SyncRunStatusValidating},
		{model.SyncRunStatusValidating, model.SyncRunStatusPublishing},
		{model.SyncRunStatusPublishing, model.SyncRunStatusSucceeded},
	} {
		run, err := repo.TransitionRun(context.Background(), runID, transition[0], transition[1], at, "ignored")
		if err != nil {
			t.Fatalf("transition %v: %v", transition, err)
		}
		if run.Status != transition[1] || run.ErrorCode != "" {
			t.Fatalf("transition %v result: %+v", transition, run)
		}
		if transition[1] == model.SyncRunStatusSucceeded {
			want := at.UTC().Truncate(time.Microsecond)
			if run.FinishedAt == nil || !run.FinishedAt.Equal(want) {
				t.Fatalf("finished_at: %v want %v", run.FinishedAt, want)
			}
		} else if run.FinishedAt != nil {
			t.Fatalf("non-terminal finished_at: %v", run.FinishedAt)
		}
	}
}

func TestInventoryRepositoryTransitionRunRejectsInvalidOrStaleTransitions(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	runID := strings.Repeat("1", 32)
	if err := db.Create(&model.SyncRun{ID: runID, Status: model.SyncRunStatusRunning, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]string{
		{model.SyncRunStatusRunning, model.SyncRunStatusSucceeded},
		{model.SyncRunStatusValidating, model.SyncRunStatusPublishing},
		{model.SyncRunStatusQueued, model.SyncRunStatusRunning},
	} {
		if _, err := repo.TransitionRun(context.Background(), runID, transition[0], transition[1], time.Now(), ""); !errors.Is(err, ErrInventoryConflict) {
			t.Fatalf("transition %v: %v", transition, err)
		}
	}
}

func TestInventoryRepositoryTransitionRunValidatesFailureCode(t *testing.T) {
	for _, code := range []string{"", "UPSTREAM FAILURE", "../../secret", strings.Repeat("x", 65)} {
		t.Run(code, func(t *testing.T) {
			repo, _, db := inventoryRepositoryFixture(t)
			runID := strings.Repeat("1", 32)
			if err := db.Create(&model.SyncRun{ID: runID, Status: model.SyncRunStatusRunning, CreatedAt: time.Now().UTC()}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := repo.TransitionRun(context.Background(), runID, model.SyncRunStatusRunning, model.SyncRunStatusFailed, time.Now(), code); !errors.Is(err, ErrInventoryConflict) {
				t.Fatalf("unsafe error code %q: %v", code, err)
			}
		})
	}

	repo, _, db := inventoryRepositoryFixture(t)
	runID := strings.Repeat("2", 32)
	at := time.Date(2026, 9, 23, 1, 2, 3, 456789123, time.UTC)
	leaseExpiresAt := at.Add(inventoryRunLeaseDuration)
	if err := db.Create(&model.SyncRun{ID: runID, Status: model.SyncRunStatusRunning, CreatedAt: at, StartedAt: &at, LeaseExpiresAt: &leaseExpiresAt}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := repo.TransitionRun(context.Background(), runID, model.SyncRunStatusRunning, model.SyncRunStatusFailed, at, "upstream_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if run.ErrorCode != "upstream_timeout" || run.FinishedAt == nil || !run.FinishedAt.Equal(at.Truncate(time.Microsecond)) {
		t.Fatalf("failed run: %+v", run)
	}
}

func TestInventoryRepositoryRunCancellationRequestedReadsLatestState(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	run := model.SyncRun{ID: strings.Repeat("1", 32), Status: model.SyncRunStatusRunning, CreatedAt: time.Now().UTC()}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	requested, err := repo.RunCancellationRequested(context.Background(), run.ID)
	if err != nil || requested {
		t.Fatalf("initial cancellation: requested=%v err=%v", requested, err)
	}
	now := time.Now().UTC()
	if err := db.Model(&model.SyncRun{}).Where("id = ?", run.ID).Update("cancel_requested_at", now).Error; err != nil {
		t.Fatal(err)
	}
	requested, err = repo.RunCancellationRequested(context.Background(), run.ID)
	if err != nil || !requested {
		t.Fatalf("marked cancellation: requested=%v err=%v", requested, err)
	}
	if err := db.Model(&model.SyncRun{}).Where("id = ?", run.ID).Updates(map[string]interface{}{"status": model.SyncRunStatusCanceled, "cancel_requested_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	requested, err = repo.RunCancellationRequested(context.Background(), run.ID)
	if err != nil || !requested {
		t.Fatalf("canceled state: requested=%v err=%v", requested, err)
	}
	if _, err := repo.RunCancellationRequested(context.Background(), strings.Repeat("9", 32)); !errors.Is(err, ErrInventoryNotFound) {
		t.Fatalf("missing run: %v", err)
	}
}

func TestInventoryRepositoryLoadCheckpointDistinguishesNotFoundAndValidatesCursor(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	ctx := context.Background()
	sourceID := strings.Repeat("a", 32)

	checkpoint, err := repo.LoadCheckpoint(ctx, sourceID, "devices")
	if !errors.Is(err, ErrInventoryNotFound) || checkpoint != nil {
		t.Fatalf("missing checkpoint: checkpoint=%+v err=%v", checkpoint, err)
	}

	snapshotAt := time.Date(2026, 9, 23, 1, 2, 3, 456789000, time.UTC)
	updatedAt := snapshotAt.Add(time.Minute)
	completedAt := updatedAt.Add(time.Minute)
	runID := strings.Repeat("1", 32)
	stored := model.SyncCheckpoint{
		SourceID: sourceID,
		Resource: "devices",
		Cursor:   `{"version":1,"run_id":"11111111111111111111111111111111","next_offset":200,"last_instance_id":199,"snapshot_at":"2026-09-23T01:02:03.456789Z","total":450}`,
		Complete: true, CompletedAt: &completedAt, UpdatedAt: updatedAt,
	}
	if err := db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}

	checkpoint, err = repo.LoadCheckpoint(ctx, sourceID, "devices")
	if err != nil {
		t.Fatal(err)
	}
	wantCursor := DeviceSyncCursor{Version: DeviceSyncCursorVersion, RunID: runID, NextOffset: 200, LastInstanceID: 199, SnapshotAt: snapshotAt, Total: 450}
	if checkpoint.Cursor != wantCursor || !checkpoint.Complete || checkpoint.CompletedAt == nil || !checkpoint.CompletedAt.Equal(completedAt) {
		t.Fatalf("checkpoint: %+v want cursor %+v", checkpoint, wantCursor)
	}

	if err := db.Model(&model.SyncCheckpoint{}).Where("source_id = ? AND resource = ?", sourceID, "devices").Update("cursor", `{"version":2}`).Error; err != nil {
		t.Fatal(err)
	}
	if checkpoint, err := repo.LoadCheckpoint(ctx, sourceID, "devices"); !errors.Is(err, ErrInventoryConflict) || checkpoint != nil {
		t.Fatalf("invalid cursor: checkpoint=%+v err=%v", checkpoint, err)
	}
}

func TestInventoryRepositoryBeginAndAdvanceCheckpointGuardsOwnershipAndProgress(t *testing.T) {
	repo, _, db := inventoryRepositoryFixture(t)
	ctx := context.Background()
	sourceID := strings.Repeat("a", 32)
	runID := strings.Repeat("1", 32)
	nextRunID := strings.Repeat("2", 32)
	startedAt := time.Date(2026, 9, 23, 8, 9, 10, 123456789, time.FixedZone("offset", 8*60*60))
	leaseExpiresAt := startedAt.Add(inventoryRunLeaseDuration)
	if err := db.Create(&model.SyncRun{ID: runID, SourceID: sourceID, Status: model.SyncRunStatusRunning, CreatedAt: startedAt, StartedAt: &startedAt, LeaseExpiresAt: &leaseExpiresAt}).Error; err != nil {
		t.Fatal(err)
	}

	checkpoint, err := repo.BeginCheckpoint(ctx, sourceID, "devices", runID, startedAt, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot := startedAt.UTC().Truncate(time.Microsecond)
	if checkpoint.Cursor != (DeviceSyncCursor{Version: DeviceSyncCursorVersion, RunID: runID, SnapshotAt: wantSnapshot}) {
		t.Fatalf("new checkpoint: %+v", checkpoint)
	}

	advancedCursor := DeviceSyncCursor{
		Version: DeviceSyncCursorVersion, RunID: runID, NextOffset: 100,
		LastInstanceID: 199, SnapshotAt: wantSnapshot, Total: 250,
	}
	checkpoint, err = repo.AdvanceCheckpoint(ctx, sourceID, "devices", runID, advancedCursor, startedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Cursor != advancedCursor || checkpoint.Complete || checkpoint.CompletedAt != nil {
		t.Fatalf("advanced checkpoint: %+v", checkpoint)
	}
	storedRun, err := repo.Run(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	wantLease := startedAt.Add(time.Minute).UTC().Truncate(time.Microsecond).Add(inventoryRunLeaseDuration)
	if storedRun.LeaseExpiresAt == nil || !storedRun.LeaseExpiresAt.Equal(wantLease) {
		t.Fatalf("renewed lease: %+v want %v", storedRun.LeaseExpiresAt, wantLease)
	}

	if checkpoint, err := repo.AdvanceCheckpoint(ctx, sourceID, "devices", runID, advancedCursor, startedAt.Add(2*time.Minute)); !errors.Is(err, ErrInventoryConflict) || checkpoint != nil {
		t.Fatalf("non-monotonic advance: checkpoint=%+v err=%v", checkpoint, err)
	}
	wrongOwner := advancedCursor
	wrongOwner.RunID = nextRunID
	wrongOwner.NextOffset = 200
	wrongOwner.LastInstanceID = 299
	if checkpoint, err := repo.AdvanceCheckpoint(ctx, sourceID, "devices", nextRunID, wrongOwner, startedAt.Add(2*time.Minute)); !errors.Is(err, ErrInventoryConflict) || checkpoint != nil {
		t.Fatalf("wrong-owner advance: checkpoint=%+v err=%v", checkpoint, err)
	}

	if err := db.Model(&model.SyncRun{}).Where("id = ?", runID).Update("status", model.SyncRunStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	nextStartedAt := startedAt.Add(3 * time.Minute)
	nextLeaseExpiresAt := nextStartedAt.Add(inventoryRunLeaseDuration)
	if err := db.Create(&model.SyncRun{ID: nextRunID, SourceID: sourceID, Status: model.SyncRunStatusRunning, CreatedAt: nextStartedAt, StartedAt: &nextStartedAt, LeaseExpiresAt: &nextLeaseExpiresAt}).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint, err = repo.BeginCheckpoint(ctx, sourceID, "devices", nextRunID, nextStartedAt, nextStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantRestartSnapshot := startedAt.Add(3 * time.Minute).UTC().Truncate(time.Microsecond)
	if checkpoint.Cursor.RunID != nextRunID || checkpoint.Cursor.NextOffset != 0 || checkpoint.Cursor.LastInstanceID != 0 || checkpoint.Cursor.Total != 0 || !checkpoint.Cursor.SnapshotAt.Equal(wantRestartSnapshot) {
		t.Fatalf("restarted checkpoint: %+v", checkpoint)
	}

	completedAt := startedAt.Add(4 * time.Minute)
	if err := db.Model(&model.SyncCheckpoint{}).Where("source_id = ? AND resource = ?", sourceID, "devices").Updates(map[string]interface{}{
		"complete": true, "completed_at": completedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SyncRun{}).Where("id = ?", nextRunID).Update("status", model.SyncRunStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SyncRun{}).Where("id = ?", runID).Updates(map[string]interface{}{"status": model.SyncRunStatusRunning, "finished_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	reset, err := repo.BeginCheckpoint(ctx, sourceID, "devices", runID, startedAt.Add(5*time.Minute), startedAt.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Cursor.RunID != runID || reset.Cursor.NextOffset != 0 || reset.Cursor.LastInstanceID != 0 || reset.Cursor.Total != 0 || reset.Complete || reset.CompletedAt != nil {
		t.Fatalf("reset checkpoint: %+v", reset)
	}
}

func TestInventoryRepositoryRejectsExpiredLeaseMutations(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-inventoryRunLeaseDuration - time.Minute)
	expiredAt := now.Add(-time.Second)

	t.Run("renew and transition", func(t *testing.T) {
		repo, _, db := inventoryRepositoryFixture(t)
		runID := strings.Repeat("1", 32)
		if err := db.Create(&model.SyncRun{
			ID: runID, Status: model.SyncRunStatusRunning, CreatedAt: startedAt,
			StartedAt: &startedAt, LeaseExpiresAt: &expiredAt,
		}).Error; err != nil {
			t.Fatal(err)
		}

		if err := repo.RenewRunLease(context.Background(), runID, now); !errors.Is(err, ErrInventoryConflict) {
			t.Fatalf("renew expired lease: %v", err)
		}
		if run, err := repo.TransitionRun(context.Background(), runID, model.SyncRunStatusRunning, model.SyncRunStatusValidating, now, ""); !errors.Is(err, ErrInventoryConflict) || run != nil {
			t.Fatalf("transition expired lease: run=%+v err=%v", run, err)
		}
	})

	t.Run("advance checkpoint", func(t *testing.T) {
		repo, _, db := inventoryRepositoryFixture(t)
		sourceID := strings.Repeat("a", 32)
		runID := strings.Repeat("2", 32)
		if err := db.Create(&model.SyncRun{
			ID: runID, SourceID: sourceID, Status: model.SyncRunStatusRunning, CreatedAt: startedAt,
			StartedAt: &startedAt, LeaseExpiresAt: &expiredAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.SyncCheckpoint{
			SourceID: sourceID, Resource: "devices",
			Cursor:   `{"version":1,"run_id":"22222222222222222222222222222222","next_offset":100,"last_instance_id":100,"snapshot_at":"2026-09-23T11:44:00Z","total":200}`,
			Complete: false, UpdatedAt: startedAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		cursor := DeviceSyncCursor{
			Version: DeviceSyncCursorVersion, RunID: runID, NextOffset: 200,
			LastInstanceID: 200, SnapshotAt: startedAt, Total: 200,
		}
		if checkpoint, err := repo.AdvanceCheckpoint(context.Background(), sourceID, "devices", runID, cursor, now); !errors.Is(err, ErrInventoryConflict) || checkpoint != nil {
			t.Fatalf("advance expired lease: checkpoint=%+v err=%v", checkpoint, err)
		}
	})

	t.Run("complete checkpoint", func(t *testing.T) {
		repo, _, db := inventoryRepositoryFixture(t)
		sourceID := strings.Repeat("b", 32)
		runID := strings.Repeat("3", 32)
		if err := db.Create(&model.SyncRun{
			ID: runID, SourceID: sourceID, Status: model.SyncRunStatusPublishing, CreatedAt: startedAt,
			StartedAt: &startedAt, LeaseExpiresAt: &expiredAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.SyncCheckpoint{
			SourceID: sourceID, Resource: "devices",
			Cursor:   `{"version":1,"run_id":"33333333333333333333333333333333","next_offset":200,"last_instance_id":200,"snapshot_at":"2026-09-23T11:44:00Z","total":200}`,
			Complete: false, UpdatedAt: startedAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if run, err := repo.CompleteCheckpointAndRun(context.Background(), sourceID, "devices", runID, model.SyncRunStatusPublishing, now); !errors.Is(err, ErrInventoryConflict) || run != nil {
			t.Fatalf("complete expired lease: run=%+v err=%v", run, err)
		}
		checkpoint, err := repo.LoadCheckpoint(context.Background(), sourceID, "devices")
		if err != nil {
			t.Fatal(err)
		}
		if checkpoint.Complete || checkpoint.CompletedAt != nil {
			t.Fatalf("expired completion changed checkpoint: %+v", checkpoint)
		}
	})
}

func TestInventoryRepositoryCompleteCheckpointAndRunIsAtomicAndGuarded(t *testing.T) {
	t.Run("publishing run succeeds with checkpoint", func(t *testing.T) {
		repo, _, db := inventoryRepositoryFixture(t)
		ctx := context.Background()
		sourceID := strings.Repeat("a", 32)
		runID := strings.Repeat("1", 32)
		updatedAt := time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC)
		if err := db.Create(&model.SyncCheckpoint{
			SourceID: sourceID, Resource: "devices", Cursor: `{"version":1,"run_id":"11111111111111111111111111111111","next_offset":250,"last_instance_id":249,"snapshot_at":"2026-09-23T00:00:00Z","total":250}`,
			Complete: false, UpdatedAt: updatedAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		leaseExpiresAt := updatedAt.Add(24 * time.Hour)
		if err := db.Create(&model.SyncRun{
			ID: runID, SourceID: sourceID, Status: model.SyncRunStatusPublishing,
			ErrorCode: "stale_error", CreatedAt: updatedAt, StartedAt: &updatedAt, LeaseExpiresAt: &leaseExpiresAt,
		}).Error; err != nil {
			t.Fatal(err)
		}

		completedAt := time.Date(2026, 9, 23, 8, 9, 10, 987654321, time.FixedZone("offset", -7*60*60))
		run, err := repo.CompleteCheckpointAndRun(ctx, sourceID, "devices", runID, model.SyncRunStatusPublishing, completedAt)
		if err != nil {
			t.Fatal(err)
		}
		wantCompleted := completedAt.UTC().Truncate(time.Microsecond)
		if run.Status != model.SyncRunStatusSucceeded || run.FinishedAt == nil || !run.FinishedAt.Equal(wantCompleted) || run.ErrorCode != "" || run.CancelRequestedAt != nil || run.LeaseExpiresAt != nil {
			t.Fatalf("completed run: %+v", run)
		}
		checkpoint, err := repo.LoadCheckpoint(ctx, sourceID, "devices")
		if err != nil {
			t.Fatal(err)
		}
		if !checkpoint.Complete || checkpoint.CompletedAt == nil || !checkpoint.CompletedAt.Equal(wantCompleted) || !checkpoint.UpdatedAt.Equal(wantCompleted) {
			t.Fatalf("completed checkpoint: %+v", checkpoint)
		}
	})

	t.Run("wrong run status rolls back checkpoint", func(t *testing.T) {
		repo, _, db := inventoryRepositoryFixture(t)
		ctx := context.Background()
		sourceID := strings.Repeat("b", 32)
		runID := strings.Repeat("2", 32)
		updatedAt := time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC)
		if err := db.Create(&model.SyncCheckpoint{
			SourceID: sourceID, Resource: "devices", Cursor: `{"version":1,"run_id":"22222222222222222222222222222222","next_offset":100,"last_instance_id":99,"snapshot_at":"2026-09-23T00:00:00Z","total":250}`,
			Complete: false, UpdatedAt: updatedAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.SyncRun{
			ID: runID, SourceID: sourceID, Status: model.SyncRunStatusValidating,
			ErrorCode: "preserved", CreatedAt: updatedAt,
		}).Error; err != nil {
			t.Fatal(err)
		}

		if run, err := repo.CompleteCheckpointAndRun(ctx, sourceID, "devices", runID, model.SyncRunStatusPublishing, updatedAt.Add(time.Hour)); !errors.Is(err, ErrInventoryConflict) || run != nil {
			t.Fatalf("wrong-status completion: run=%+v err=%v", run, err)
		}
		checkpoint, err := repo.LoadCheckpoint(ctx, sourceID, "devices")
		if err != nil {
			t.Fatal(err)
		}
		if checkpoint.Complete || checkpoint.CompletedAt != nil || !checkpoint.UpdatedAt.Equal(updatedAt) {
			t.Fatalf("checkpoint changed despite rollback: %+v", checkpoint)
		}
		storedRun, err := repo.Run(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if storedRun.Status != model.SyncRunStatusValidating || storedRun.FinishedAt != nil || storedRun.ErrorCode != "preserved" {
			t.Fatalf("run changed despite guard: %+v", storedRun)
		}
	})
}
