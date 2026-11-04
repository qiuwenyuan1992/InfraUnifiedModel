package service

import (
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"context"
	"errors"
	"github.com/spf13/viper"
	"strings"
	"testing"
	"time"
)

type roundRepoStub struct {
	repository.InventoryRoundRepository
	enqueue     func(context.Context, *model.SyncRound) (*model.SyncRound, error)
	list        func(context.Context, repository.InventoryListQuery) (*repository.InventoryListResult, error)
	diagnostics func(context.Context, string, bool, repository.InventoryListQuery) (*repository.InventoryListResult, error)
	get         func(context.Context, string) (*model.SyncRound, []model.SyncRoundPhase, error)
	cancel      func(context.Context, string) (*model.SyncRound, bool, error)
}

func (r roundRepoStub) Enqueue(ctx context.Context, x *model.SyncRound) (*model.SyncRound, error) {
	return r.enqueue(ctx, x)
}
func (r roundRepoStub) List(ctx context.Context, q repository.InventoryListQuery) (*repository.InventoryListResult, error) {
	return r.list(ctx, q)
}
func (r roundRepoStub) Diagnostics(ctx context.Context, id string, round bool, q repository.InventoryListQuery) (*repository.InventoryListResult, error) {
	return r.diagnostics(ctx, id, round, q)
}
func (r roundRepoStub) Get(ctx context.Context, id string) (*model.SyncRound, []model.SyncRoundPhase, error) {
	return r.get(ctx, id)
}
func (r roundRepoStub) Cancel(ctx context.Context, id string) (*model.SyncRound, bool, error) {
	return r.cancel(ctx, id)
}
func roundConfig() *viper.Viper {
	c := viper.New()
	c.Set("inventory.cursor_key", strings.Repeat("k", 32))
	c.Set("inventory.grants", []map[string]interface{}{{"user_id": "Alice", "permissions": []string{"sync:read", "sync:write"}}, {"user_id": "Bob", "permissions": []string{"sync:read"}}})
	return c
}

func TestInventoryRoundEnqueueValidationAndCanonicalHash(t *testing.T) {
	var stored []*model.SyncRound
	svc := NewInventoryRoundService(roundRepoStub{enqueue: func(_ context.Context, r *model.SyncRound) (*model.SyncRound, error) {
		stored = append(stored, r)
		return r, nil
	}}, roundConfig())
	req := EnqueueInventoryRound{SourceID: strings.Repeat("a", 32), Mode: "full"}
	for _, user := range []string{"alice", "Bob"} {
		if _, err := svc.Enqueue(context.Background(), user, "key", req); !errors.Is(err, ErrInventoryForbidden) {
			t.Fatalf("user %s: %v", user, err)
		}
	}
	if _, err := svc.Enqueue(context.Background(), "Alice", "", req); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.Enqueue(context.Background(), "Alice", "key", req); err != nil {
			t.Fatal(err)
		}
	}
	if stored[0].RequestHash == "" || stored[0].RequestHash != stored[1].RequestHash || !inventoryID(stored[0].ID) || stored[0].Status != "queued" {
		t.Fatalf("invalid enqueue %+v", stored)
	}
}

func TestInventoryRoundCursorBindsUserParentAndFilters(t *testing.T) {
	at := time.Now().UTC()
	repo := roundRepoStub{diagnostics: func(_ context.Context, _ string, _ bool, q repository.InventoryListQuery) (*repository.InventoryListResult, error) {
		return &repository.InventoryListResult{Items: []model.SyncDiagnostic{}, HasMore: q.LastID == "", LastID: strings.Repeat("d", 32), LastCreatedAt: &at}, nil
	}}
	svc := NewInventoryRoundService(repo, roundConfig())
	ctx := context.Background()
	parent := strings.Repeat("a", 32)
	page, err := svc.Diagnostics(ctx, "Alice", parent, true, InventoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	for _, tc := range []struct {
		user, id string
		round    bool
		q        InventoryQuery
	}{{"Bob", parent, true, InventoryQuery{Cursor: *page.NextCursor}}, {"Alice", strings.Repeat("b", 32), true, InventoryQuery{Cursor: *page.NextCursor}}, {"Alice", parent, false, InventoryQuery{Cursor: *page.NextCursor}}, {"Alice", parent, true, InventoryQuery{Cursor: *page.NextCursor, SourceID: parent}}} {
		if _, err := svc.Diagnostics(ctx, tc.user, tc.id, tc.round, tc.q); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("cursor reused %+v: %v", tc, err)
		}
	}
	if _, err := svc.Diagnostics(ctx, "Alice", parent, true, InventoryQuery{Cursor: *page.NextCursor}); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryRoundGetCancelAndRepositoryErrors(t *testing.T) {
	id := strings.Repeat("a", 32)
	ctx := context.Background()
	repo := roundRepoStub{get: func(_ context.Context, got string) (*model.SyncRound, []model.SyncRoundPhase, error) {
		return &model.SyncRound{ID: got}, []model.SyncRoundPhase{{Name: "device", Status: "succeeded"}}, nil
	}, cancel: func(_ context.Context, got string) (*model.SyncRound, bool, error) {
		return &model.SyncRound{ID: got, Status: "running"}, true, nil
	}}
	svc := NewInventoryRoundService(repo, roundConfig())
	round, phases, err := svc.Get(ctx, "Bob", id)
	if err != nil || round.ID != id || len(phases) != 1 {
		t.Fatalf("get %v", err)
	}
	if _, _, err := svc.Get(ctx, "bob", id); !errors.Is(err, ErrInventoryForbidden) {
		t.Fatal(err)
	}
	if _, _, err := svc.Get(ctx, "Bob", "bad"); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatal(err)
	}
	if _, _, err := svc.Cancel(ctx, "Bob", id); !errors.Is(err, ErrInventoryForbidden) {
		t.Fatal(err)
	}
	if _, _, err := svc.Cancel(ctx, "Alice", "bad"); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatal(err)
	}
	if round, accepted, err := svc.Cancel(ctx, "Alice", id); err != nil || !accepted || round.Status != "running" {
		t.Fatalf("cancel %+v %v %v", round, accepted, err)
	}
	for _, tc := range []struct{ repo, service error }{{repository.ErrInventoryConflict, ErrInventoryConflict}, {repository.ErrInventoryIdempotency, ErrInventoryIdempotency}, {repository.ErrInventorySource, ErrInventoryInvalid}, {repository.ErrInventoryNotFound, ErrInventoryNotFound}} {
		svc := NewInventoryRoundService(roundRepoStub{enqueue: func(context.Context, *model.SyncRound) (*model.SyncRound, error) { return nil, tc.repo }}, roundConfig())
		if _, err := svc.Enqueue(ctx, "Alice", "key", EnqueueInventoryRound{SourceID: id, Mode: "full"}); !errors.Is(err, tc.service) {
			t.Fatal(err)
		}
	}
}

func TestInventoryRoundListCursorAndValidation(t *testing.T) {
	ctx := context.Background()
	id := strings.Repeat("a", 32)
	at := time.Now().UTC()
	calls := 0
	svc := NewInventoryRoundService(roundRepoStub{list: func(_ context.Context, q repository.InventoryListQuery) (*repository.InventoryListResult, error) {
		calls++
		if q.Limit != 50 {
			t.Fatalf("limit %d", q.Limit)
		}
		return &repository.InventoryListResult{Items: []model.SyncRound{{ID: id}}, HasMore: q.LastID == "", LastID: id, LastCreatedAt: &at}, nil
	}}, roundConfig())
	page, err := svc.List(ctx, "Alice", InventoryQuery{Status: "running", SourceID: id})
	if err != nil || page.NextCursor == nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, "Alice", InventoryQuery{Status: "running", SourceID: id, Cursor: *page.NextCursor}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []InventoryQuery{{Cursor: *page.NextCursor}, {Status: "invalid"}, {Limit: 201}, {SourceID: "bad"}, {Cursor: "invalid"}} {
		if _, err := svc.List(ctx, "Alice", q); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("query %+v: %v", q, err)
		}
	}
	if calls != 2 {
		t.Fatalf("repository called for invalid query %d", calls)
	}
	c := roundConfig()
	c.Set("inventory.cursor_key", "short")
	notReady := NewInventoryRoundService(roundRepoStub{}, c)
	if _, err := notReady.List(ctx, "Alice", InventoryQuery{}); !errors.Is(err, ErrInventoryNotReady) {
		t.Fatal(err)
	}
}
