package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

func inventoryAssetRead(ctx context.Context, s InventoryService, resource string) error {
	if resource == "device" {
		_, err := s.GetDevice(ctx, "AliceCase", inventoryDeviceA, "")
		return err
	}
	parent := ""
	if resource != "devices" {
		parent = inventoryDeviceA
	}
	_, err := s.List(ctx, "AliceCase", resource, parent, InventoryQuery{})
	return err
}

func TestInventoryUnavailableProjectionNeverCallsGraph(t *testing.T) {
	for _, resource := range []string{"device", "devices", "interfaces", "addresses"} {
		for _, state := range []string{"uninitialized", "updating", "failed"} {
			t.Run(resource+"/"+state, func(t *testing.T) {
				s, db, _ := inventoryFixture(t)
				if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("projection_state", state).Error; err != nil {
					t.Fatal(err)
				}
				if err := inventoryAssetRead(context.Background(), s, resource); !errors.Is(err, ErrInventoryNotReady) {
					t.Fatalf("read: %v", err)
				}
				if calls := s.(*inventoryService).graph.(*inventoryGraphFake).calls; calls != 0 {
					t.Fatalf("graph calls: %d", calls)
				}
			})
		}
	}
}

func TestInventoryRequiresActivePublishedReadyBatch(t *testing.T) {
	for _, condition := range []string{"no_active", "empty_active", "inventory_not_ready", "graph_not_ready", "unpublished"} {
		t.Run(condition, func(t *testing.T) {
			s, db, _ := inventoryFixture(t)
			var err error
			switch condition {
			case "no_active":
				err = db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", nil).Error
			case "empty_active":
				err = db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("active_generation_id", "").Error
			case "inventory_not_ready":
				err = db.Model(&model.Generation{}).Where("id = ?", inventoryGen).Update("inventory_ready", false).Error
			case "graph_not_ready":
				err = db.Model(&model.Generation{}).Where("id = ?", inventoryGen).Update("graph_ready", false).Error
			case "unpublished":
				err = db.Model(&model.Generation{}).Where("id = ?", inventoryGen).Update("state", "building").Error
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, resource := range []string{"device", "devices", "interfaces", "addresses"} {
				if err := inventoryAssetRead(context.Background(), s, resource); !errors.Is(err, ErrInventoryNotReady) {
					t.Fatalf("%s unready batch: %v, want %v", resource, err, ErrInventoryNotReady)
				}
			}
			if s.(*inventoryService).graph.(*inventoryGraphFake).calls != 0 {
				t.Fatal("unready batch called graph")
			}
		})
	}
}

func TestInventoryCursorEpochAndVersion(t *testing.T) {
	s, db, _ := inventoryFixture(t)
	ctx := context.Background()
	first, err := s.List(ctx, "AliceCase", "devices", "", InventoryQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	service := s.(*inventoryService)
	cursor, err := service.decodeCursor(*first.NextCursor)
	if err != nil || cursor.Version != 3 || cursor.ProjectionEpoch != 1 {
		t.Fatalf("cursor: %+v %v", cursor, err)
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), "scope") {
		t.Fatalf("cursor contains scope: %s", data)
	}
	cursor.Version = 2
	if _, err := s.List(ctx, "AliceCase", "devices", "", InventoryQuery{Cursor: service.encodeCursor(cursor)}); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatalf("old cursor: %v", err)
	}
	if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("projection_epoch", 2).Error; err != nil {
		t.Fatal(err)
	}
	fake := service.graph.(*inventoryGraphFake)
	calls := fake.calls
	if _, err := s.List(ctx, "AliceCase", "devices", "", InventoryQuery{Cursor: *first.NextCursor}); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("same-generation epoch drift: %v", err)
	}
	if calls != fake.calls {
		t.Fatal("stale cursor called graph")
	}
}

func TestInventoryRejectsProjectionChangesDuringEveryGraphCall(t *testing.T) {
	for _, resource := range []string{"device", "devices", "interfaces", "addresses"} {
		for _, change := range []string{"updating", "failed", "uninitialized", "epoch", "generation", "update_cycle"} {
			for _, call := range []int{1, 2} {
				if call == 2 && (resource == "device" || resource == "devices") {
					continue
				}
				t.Run(resource+"/"+change+string(rune('0'+call)), func(t *testing.T) {
					s, db, _ := inventoryFixture(t)
					fake := s.(*inventoryService).graph.(*inventoryGraphFake)
					fake.hook = func() {
						if fake.calls != call {
							return
						}
						updates := map[string]interface{}{}
						switch change {
						case "epoch":
							updates["projection_epoch"] = 2
						case "generation":
							updates["active_generation_id"] = inventoryNext
						case "update_cycle":
							if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Updates(map[string]interface{}{"projection_state": "updating", "projection_epoch": 2}).Error; err != nil {
								t.Fatal(err)
							}
							updates["projection_state"] = "ready"
						default:
							updates["projection_state"] = change
						}
						if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Updates(updates).Error; err != nil {
							t.Fatal(err)
						}
					}
					want := ErrInventoryConflict
					if change == "updating" || change == "failed" || change == "uninitialized" {
						want = ErrInventoryNotReady
					}
					if err := inventoryAssetRead(context.Background(), s, resource); !errors.Is(err, want) {
						t.Fatalf("read: %v, want %v", err, want)
					}
					if fake.calls != call {
						t.Fatalf("read continued after projection change: %d", fake.calls)
					}
				})
			}
		}
	}
}

type inventoryStateHook struct {
	repository.InventoryRepository
	calls int
	hook  func(int)
}

func (r *inventoryStateHook) State(ctx context.Context) (*model.InventoryState, error) {
	r.calls++
	r.hook(r.calls)
	return r.InventoryRepository.State(ctx)
}

func TestInventoryChecksProjectionImmediatelyBeforeGraph(t *testing.T) {
	for _, resource := range []string{"device", "devices", "interfaces", "addresses"} {
		t.Run(resource, func(t *testing.T) {
			s, db, _ := inventoryFixture(t)
			service := s.(*inventoryService)
			service.repo = &inventoryStateHook{InventoryRepository: service.repo, hook: func(call int) {
				if call == 2 {
					if err := db.Model(&model.InventoryState{}).Where("id = ?", 1).Update("projection_state", "updating").Error; err != nil {
						t.Fatal(err)
					}
				}
			}}
			if err := inventoryAssetRead(context.Background(), s, resource); !errors.Is(err, ErrInventoryNotReady) {
				t.Fatalf("pre-read change: %v", err)
			}
			if service.graph.(*inventoryGraphFake).calls != 0 {
				t.Fatal("changed projection called graph")
			}
		})
	}
}

func TestInventoryGraphErrorsAndAuthorization(t *testing.T) {
	s, _, _ := inventoryFixture(t)
	fake := s.(*inventoryService).graph.(*inventoryGraphFake)
	if _, err := s.GetDevice(context.Background(), "unknown", inventoryDeviceA, ""); !errors.Is(err, ErrInventoryForbidden) {
		t.Fatalf("unauthorized: %v", err)
	}
	if fake.calls != 0 {
		t.Fatal("unauthorized graph access")
	}
	for _, graphErr := range []error{repository.ErrInventoryNotFound, repository.ErrInventoryNotReady, context.Canceled, errors.New("graph unavailable")} {
		fake.err = graphErr
		for _, resource := range []string{"device", "devices", "interfaces", "addresses"} {
			if err := inventoryAssetRead(context.Background(), s, resource); !errors.Is(err, inventoryError(graphErr)) {
				t.Fatalf("%s graph error: %v", resource, err)
			}
		}
	}
}
