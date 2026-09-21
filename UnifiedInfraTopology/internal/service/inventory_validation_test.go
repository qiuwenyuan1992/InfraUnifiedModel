package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

func TestInventoryRejectsInvalidQueriesAndMissingKey(t *testing.T) {
	s, db, conf := inventoryFixture(t)
	ctx := context.Background()
	cases := []struct {
		resource, parent string
		q                InventoryQuery
	}{
		{"devices", "", InventoryQuery{Limit: 201}}, {"devices", "", InventoryQuery{Limit: -1}},
		{"devices", "", InventoryQuery{GenerationID: strings.Repeat("A", 32)}},
		{"devices", "", InventoryQuery{Status: "queued"}}, {"sources", "", InventoryQuery{Name: "x"}},
		{"devices", "", InventoryQuery{Lifecycle: "invented"}}, {"interfaces", inventoryDeviceA, InventoryQuery{InterfaceKind: "invented"}},
		{"addresses", inventoryDeviceA, InventoryQuery{AddressFamily: "ipv4"}}, {"unknown", "", InventoryQuery{}},
		{"interfaces", "", InventoryQuery{}}, {"devices", inventoryDeviceA, InventoryQuery{}},
	}
	for _, tc := range cases {
		if _, err := s.List(ctx, "AliceCase", tc.resource, tc.parent, tc.q); !errors.Is(err, ErrInventoryInvalid) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
	conf.Set("inventory.cursor_key", "")
	noKey := NewInventoryService(repository.NewInventoryRepository(repository.NewRepository(nil, db)), newInventoryGraphFake(), conf)
	if _, err := noKey.List(ctx, "AliceCase", "devices", "", InventoryQuery{}); !errors.Is(err, ErrInventoryNotReady) {
		t.Fatalf("missing cursor key: %v", err)
	}
	if _, err := noKey.GetDevice(ctx, "AliceCase", inventoryDeviceA, ""); err != nil {
		t.Fatalf("non-paginated read: %v", err)
	}
}

func TestInventoryParentAndDuplicateAddressReads(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	graph := s.(*inventoryService).graph.(*inventoryGraphFake)
	graph.addresses = nil
	graph.interfaces = nil
	for _, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		graph.addresses = append(graph.addresses, model.Address{EntityID: id, DeviceID: inventoryDeviceA, AddressFamily: 4, Address: []byte{192, 0, 2, 1}, AddressScopeKey: "unknown", Lifecycle: "active"})
	}
	page, err := s.List(ctx, "AliceCase", "addresses", inventoryDeviceA, InventoryQuery{AddressFamily: "4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items.([]model.Address)) != 2 {
		t.Fatalf("duplicate IP assignments collapsed: %+v", page.Items)
	}
	page, err = s.List(ctx, "AliceCase", "interfaces", inventoryDeviceA, InventoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items.([]model.Interface) == nil || len(page.Items.([]model.Interface)) != 0 {
		t.Fatalf("empty list: %+v", page.Items)
	}
	if _, err = s.List(ctx, "AliceCase", "addresses", strings.Repeat("c", 32), InventoryQuery{}); !errors.Is(err, ErrInventoryNotFound) {
		t.Fatalf("parent missing: %v", err)
	}
	if err = db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.List(ctx, "AliceCase", "devices", "", InventoryQuery{}); !errors.Is(err, ErrInventoryNotReady) {
		t.Fatalf("no active generation: %v", err)
	}
}

func TestInventoryRunValidationRollbackAndPrivateRun(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	cases := []EnqueueInventoryRun{
		{Mode: "incremental", SourceIDs: []string{inventorySource}}, {Mode: "full"},
		{Mode: "full", SourceIDs: []string{inventorySource, "invalid"}},
		{Mode: "full", SourceIDs: []string{inventorySource, inventoryOther}},
	}
	for _, req := range cases {
		if _, err := s.Enqueue(ctx, "AliceCase", "invalid", req); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("invalid request: %v", err)
		}
	}
	req := EnqueueInventoryRun{Mode: "full", SourceIDs: []string{inventorySource}}
	for _, key := range []string{"", "bad\nkey", strings.Repeat("x", 129)} {
		if _, err := s.Enqueue(ctx, "AliceCase", key, req); !errors.Is(err, ErrInventoryInvalid) {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	if err := db.Exec("CREATE TRIGGER fail_inventory_sources BEFORE INSERT ON sync_run_sources BEGIN SELECT RAISE(ABORT, 'source write failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, "AliceCase", "rollback", req); err == nil {
		t.Fatal("source insert failure swallowed")
	}
	var count int64
	if err := db.Model(&model.SyncRun{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial run persisted: %d %v", count, err)
	}
	if _, err := s.GetRun(ctx, "AliceCase", inventoryDeviceA); !errors.Is(err, ErrInventoryNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	if _, accepted, err := s.CancelRun(ctx, "AliceCase", inventoryDeviceA); accepted || !errors.Is(err, ErrInventoryNotFound) {
		t.Fatalf("missing cancellation: %v", err)
	}
}

func TestInventoryCancellationFailureIsNotAccepted(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	inventoryCreate(t, db, &model.SyncRun{ID: inventoryDeviceA, Status: "queued", IdempotencyKey: "cancel-failure"})
	if err := db.Exec("CREATE TRIGGER fail_inventory_cancel BEFORE UPDATE ON sync_runs BEGIN SELECT RAISE(ABORT, 'cancel write failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	run, accepted, err := s.CancelRun(context.Background(), "AliceCase", inventoryDeviceA)
	if err == nil || accepted || run != nil {
		t.Fatalf("failed cancellation: run=%+v accepted=%v err=%v", run, accepted, err)
	}
	var persisted model.SyncRun
	if err := db.Where("id = ?", inventoryDeviceA).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "queued" || persisted.FinishedAt != nil || persisted.CancelRequestedAt != nil {
		t.Fatalf("failed cancellation persisted: %+v", persisted)
	}
}

func TestInventoryRunPaginationAndPendingCancellation(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{inventoryDeviceA, inventoryDeviceB} {
		inventoryCreate(t, db, &model.SyncRun{ID: id, Status: "running", IdempotencyKey: id, CreatedAt: now})
	}
	first, err := s.List(ctx, "AliceCase", "sync-runs", "", InventoryQuery{Limit: 1, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == nil || first.Items.([]model.SyncRun)[0].ID != inventoryDeviceB {
		t.Fatalf("descending first: %+v", first)
	}
	next, err := s.List(ctx, "AliceCase", "sync-runs", "", InventoryQuery{Limit: 1, Status: "running", Cursor: *first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if next.NextCursor != nil || next.Items.([]model.SyncRun)[0].ID != inventoryDeviceA {
		t.Fatalf("descending next: %+v", next)
	}
	run, accepted, err := s.CancelRun(ctx, "AliceCase", inventoryDeviceA)
	if err != nil || !accepted || run.Status != "running" || run.CancelRequestedAt == nil || run.FinishedAt != nil {
		t.Fatalf("running cancellation: %+v %v", run, err)
	}
	again, accepted, err := s.CancelRun(ctx, "AliceCase", inventoryDeviceA)
	if err != nil || accepted || !again.CancelRequestedAt.Equal(*run.CancelRequestedAt) {
		t.Fatalf("flag changed: %+v %v", again, err)
	}
}
