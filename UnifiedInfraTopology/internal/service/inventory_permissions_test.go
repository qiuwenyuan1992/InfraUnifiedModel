package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

func TestInventoryPermissionMatrix(t *testing.T) {
	_, db, conf := inventoryFixture(t)
	cases := []struct {
		permission, resource string
		allowed              bool
	}{
		{"inventory:read", "devices", true}, {"inventory:read", "sources", false},
		{"sync:read", "sources", true}, {"sync:read", "devices", false},
		{"topology:read", "generations", true}, {"topology:read", "devices", false},
		{"sync:write", "sync-runs", false},
	}
	for _, tc := range cases {
		conf.Set("inventory.grants", []map[string]interface{}{{"user_id": "reader", "permissions": []string{tc.permission}}})
		s := NewInventoryService(repository.NewInventoryRepository(repository.NewRepository(nil, db)), newInventoryGraphFake(), conf)
		_, err := s.List(context.Background(), "reader", tc.resource, "", InventoryQuery{})
		if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrInventoryForbidden) {
			t.Fatalf("%+v: %v", tc, err)
		}
		if _, err = s.Enqueue(context.Background(), "reader", "write", EnqueueInventoryRun{Mode: "full", SourceIDs: []string{inventorySource}}); !errors.Is(err, ErrInventoryForbidden) {
			t.Fatalf("write requires both permissions: %v", err)
		}
	}
}

func TestInventoryConcurrentCancellationAcceptsOnce(t *testing.T) {
	for _, status := range []string{"queued", "running", "validating", "publishing"} {
		t.Run(status, func(t *testing.T) {
			s, db, _ := inventoryFixture(t)
			inventoryCreate(t, db, &model.SyncRun{ID: inventoryDeviceA, Status: status, IdempotencyKey: "cancel"})
			const callers = 8
			type outcome struct {
				run      *model.SyncRun
				accepted bool
				err      error
			}
			results := make(chan outcome, callers)
			var wg sync.WaitGroup
			for i := 0; i < callers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					run, accepted, err := s.CancelRun(context.Background(), "AliceCase", inventoryDeviceA)
					results <- outcome{run, accepted, err}
				}()
			}
			wg.Wait()
			close(results)
			acceptedCount := 0
			var first *model.SyncRun
			for result := range results {
				if result.err != nil || result.run == nil || result.run.CancelRequestedAt == nil {
					t.Fatalf("cancel: %+v", result)
				}
				if result.accepted {
					acceptedCount++
				}
				if first == nil {
					first = result.run
				}
				if !result.run.CancelRequestedAt.Equal(*first.CancelRequestedAt) {
					t.Fatal("repeated cancellation changed timestamp")
				}
				if status == "queued" {
					if result.run.Status != "canceled" || result.run.FinishedAt == nil {
						t.Fatalf("queued cancellation: %+v", result.run)
					}
				} else if result.run.Status != status || result.run.FinishedAt != nil {
					t.Fatalf("pending cancellation: %+v", result.run)
				}
			}
			if acceptedCount != 1 {
				t.Fatalf("accepted %d cancellations; want 1", acceptedCount)
			}
		})
	}
}

func TestInventoryConcurrentIdempotency(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	const callers = 8
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := s.Enqueue(context.Background(), "AliceCase", "concurrent", EnqueueInventoryRun{Mode: "full", SourceIDs: []string{inventorySource}})
			if err != nil {
				errs <- err
				return
			}
			ids <- run.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	expected := ""
	for id := range ids {
		if expected == "" {
			expected = id
		}
		if id != expected {
			t.Fatalf("multiple idempotent runs: %s %s", expected, id)
		}
	}
	var count int64
	if err := db.Model(&model.SyncRun{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("run count: %d %v", count, err)
	}
}
