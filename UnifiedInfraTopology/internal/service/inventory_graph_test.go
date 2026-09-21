package service

import (
	"context"
	"errors"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type inventoryGraphFake struct {
	devices    []model.Device
	interfaces []model.Interface
	addresses  []model.Address
	calls      int
	hook       func()
	err        error
}

func newInventoryGraphFake() *inventoryGraphFake {
	return &inventoryGraphFake{
		devices: []model.Device{
			{EntityID: inventoryDeviceA, Name: "a", DeviceKind: "router", Lifecycle: "active"},
			{EntityID: inventoryDeviceB, Name: "b", DeviceKind: "switch", Lifecycle: "active"},
		},
		interfaces: []model.Interface{{EntityID: inventoryOther, DeviceID: inventoryDeviceA, InterfaceKind: "physical"}},
		addresses:  []model.Address{{EntityID: inventorySource, DeviceID: inventoryDeviceA, AddressFamily: 4}},
	}
}

func (f *inventoryGraphFake) call(ctx context.Context) error {
	f.calls++
	if f.hook != nil {
		f.hook()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.err
}

func (f *inventoryGraphFake) Device(ctx context.Context, id string) (*model.Device, error) {
	if err := f.call(ctx); err != nil {
		return nil, err
	}
	for _, d := range f.devices {
		if d.EntityID == id {
			return &d, nil
		}
	}
	return nil, repository.ErrInventoryNotFound
}

func (f *inventoryGraphFake) List(ctx context.Context, resource string, q repository.InventoryListQuery) (*repository.InventoryListResult, error) {
	if err := f.call(ctx); err != nil {
		return nil, err
	}
	if q.GenerationID != "" {
		return nil, errors.New("graph assets must not be batch-filtered")
	}
	switch resource {
	case "devices":
		rows := []model.Device{}
		for _, d := range f.devices {
			if d.EntityID > q.LastID && (q.Name == "" || d.Name == q.Name) && (q.DeviceKind == "" || d.DeviceKind == q.DeviceKind) && (q.Lifecycle == "" || d.Lifecycle == q.Lifecycle) {
				rows = append(rows, d)
			}
		}
		result := &repository.InventoryListResult{HasMore: len(rows) > q.Limit}
		if result.HasMore {
			rows = rows[:q.Limit]
		}
		result.Items = rows
		if len(rows) > 0 {
			result.LastID = rows[len(rows)-1].EntityID
		}
		return result, nil
	case "interfaces":
		rows := []model.Interface{}
		for _, item := range f.interfaces {
			if item.DeviceID == q.ParentID {
				rows = append(rows, item)
			}
		}
		return &repository.InventoryListResult{Items: rows}, nil
	case "addresses":
		rows := []model.Address{}
		for _, item := range f.addresses {
			if item.DeviceID == q.ParentID {
				rows = append(rows, item)
			}
		}
		return &repository.InventoryListResult{Items: rows}, nil
	default:
		return nil, errors.New("unexpected graph resource")
	}
}

func TestInventoryCurrentGraphResponses(t *testing.T) {
	s, _, _ := inventoryFixture(t)
	ctx := context.Background()
	d, err := s.GetDevice(ctx, "AliceCase", inventoryDeviceA, inventoryGen)
	if err != nil || d.Device.GenerationID != inventoryGen {
		t.Fatalf("device batch: %+v %v", d, err)
	}
	for _, resource := range []string{"devices", "interfaces", "addresses"} {
		parent := ""
		if resource != "devices" {
			parent = inventoryDeviceA
		}
		page, err := s.List(ctx, "AliceCase", resource, parent, InventoryQuery{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if *page.GenerationID != inventoryGen || !*page.GraphReady {
			t.Fatalf("metadata: %+v", page)
		}
		switch rows := page.Items.(type) {
		case []model.Device:
			if rows[0].GenerationID != inventoryGen {
				t.Fatal("device batch missing")
			}
		case []model.Interface:
			if rows[0].GenerationID != inventoryGen {
				t.Fatal("interface batch missing")
			}
		case []model.Address:
			if rows[0].GenerationID != inventoryGen {
				t.Fatal("address batch missing")
			}
		}
		if resource == "devices" {
			next, err := s.List(ctx, "AliceCase", resource, "", InventoryQuery{Limit: 1, Cursor: *page.NextCursor})
			if err != nil || next.NextCursor != nil || next.Items.([]model.Device)[0].EntityID != inventoryDeviceB {
				t.Fatalf("current cursor: %+v %v", next, err)
			}
		}
	}
	fake := s.(*inventoryService).graph.(*inventoryGraphFake)
	if fake.devices[0].GenerationID != "" || fake.interfaces[0].GenerationID != "" || fake.addresses[0].GenerationID != "" {
		t.Fatal("response mutated graph records")
	}
}
