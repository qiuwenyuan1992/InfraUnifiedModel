package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func newPodPassTestCMDB(t *testing.T, respond func(interfacePassRequest) any) *CMDB {
	t.Helper()
	c := newInterfacePassTestCMDB(t, respond)
	c.conf.Set("inventory.cmdb.sources.primary.entity_type", "pod")
	c.conf.Set("inventory.cmdb.sources.primary.device_condition", map[string]any{"idc_id": 238, "parent_type_id": 53, "device_sn": "narrow"})
	c.conf.Set("inventory.cmdb.sources.primary.cabinet_condition", map[string]any{"idc_id": 238, "inst_id": 999})
	c.conf.Set("inventory.cmdb.sources.primary.pod_condition", map[string]any{"plane": 1})
	return c
}

func podPassItem(id int64, uuid string) map[string]any {
	return map[string]any{"inst_id": id, "uuid": uuid, "name": fmt.Sprintf("POD-%d", id)}
}

func TestCMDBPodRecordPreservesUUIDAndWhitelist(t *testing.T) {
	var item map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(`{"inst_id":7,"uuid":" pod-7 ","basic_code":"  ","name":" N ","full_name":null,"plane":99,"mode":null,"rdma":0,"phy_building_id":10,"idc_logic_id":11,"is_delete":1,"is_sync":0,"created_at":"","create_time":"2026-10-30 08:00:00","updated_at":"2000-01-01 00:00:00","last_time":"2026-10-30T09:00:00.123456789+08:00","extra":"ignored"}`), &item))
	record, err := cmdbEntityRecord(item, model.EntityPod)
	require.NoError(t, err)
	require.Equal(t, "pod-7", record.Identity)
	require.Equal(t, int64(7), record.SourceInstanceID)
	require.Equal(t, map[string]any{"inst_id": int64(7), "basic_code": nil, "name": "N", "full_name": nil, "plane": int64(99), "mode": nil, "rdma": int64(0), "phy_building_id": int64(10), "idc_logic_id": int64(11), "is_delete": int64(1), "is_sync": int64(0), "source_created_at": time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), "source_updated_at": time.Date(2026, 10, 30, 1, 0, 0, 123456000, time.UTC)}, record.Properties)
	for _, field := range []string{"inst_id", "plane", "rdma", "phy_building_id", "idc_logic_id", "is_delete", "is_sync"} {
		copy := make(map[string]json.RawMessage, len(item))
		for k, v := range item {
			copy[k] = v
		}
		copy[field] = json.RawMessage(`"1"`)
		_, err := cmdbEntityRecord(copy, model.EntityPod)
		require.ErrorIs(t, err, ErrCMDBRecord, field)
	}
}

func TestCMDBPodPassDiscoversReferencesAndFetchesLiveSelectedRecords(t *testing.T) {
	var requests []interfacePassRequest
	name := "original"
	c := newPodPassTestCMDB(t, func(req interfacePassRequest) any {
		requests = append(requests, req)
		require.Equal(t, "inst_id", req.body.Page.Sort)
		require.LessOrEqual(t, req.body.Page.Limit, 2)
		switch req.path {
		case deviceViewEndpoint:
			require.Equal(t, map[string]any{"idc_id": float64(238)}, req.body.Condition)
			require.Contains(t, req.body.Fields, "pod_uuid")
			require.Contains(t, req.body.Fields, "compute_plane")
			return interfacePassEnvelope(2, []map[string]any{{"inst_id": 1, "pod_uuid": " pod-b ", "compute_plane": []any{map[string]any{"pod_id": 30}, map[string]any{"pod_id": 404}, map[string]any{"pod_id": 0}}}, {"inst_id": 2, "pod_uuid": " "}})
		case "/pub/api/v3/find/instance/object/cabinet":
			require.Equal(t, map[string]any{"idc_id": float64(238)}, req.body.Condition)
			return interfacePassEnvelope(1, []map[string]any{{"inst_id": 1, "uuid": "cabinet", "pod_ids": []any{10, 30, 0, -1, nil}}})
		case "/pub/api/v3/find/instance/object/pod":
			if len(req.body.Condition) != 0 {
				require.Len(t, req.body.Condition, 1)
				require.Equal(t, 0, req.body.Page.Start)
				id := int64(req.body.Condition["inst_id"].(float64))
				item := podPassItem(id, map[int64]string{10: "pod-a", 20: "pod-b", 30: "pod-c"}[id])
				item["name"] = name
				return interfacePassEnvelope(1, []map[string]any{item})
			}
			items := []map[string]any{podPassItem(10, "pod-a"), podPassItem(20, "pod-b"), podPassItem(30, "pod-c"), podPassItem(40, "outside")}
			end := min(req.body.Page.Start+req.body.Page.Limit, len(items))
			return interfacePassEnvelope(len(items), items[req.body.Page.Start:end])
		default:
			t.Fatalf("unexpected request %s", req.path)
			return nil
		}
	})
	beforeConfig := fmt.Sprint(c.conf.AllSettings())
	source := model.Source{ConfigRef: "primary"}
	selected, err := c.SelectedEntity(source)
	require.NoError(t, err)
	require.Equal(t, model.EntityPod, selected)
	full, err := c.CoversFullSource(source, model.EntityPod)
	require.NoError(t, err)
	require.False(t, full)
	stream, err := c.OpenEntityPass(context.Background(), source, model.EntityPod)
	require.NoError(t, err)
	info, ok := stream.(interface {
		ScopeDigest() string
		ScopeSummary() map[string]int
	})
	require.True(t, ok)
	require.NotEmpty(t, info.ScopeDigest())
	require.Equal(t, 3, info.ScopeSummary()["resolved_targets"])
	require.Equal(t, 1, info.ScopeSummary()["missing_id_references"])
	require.Equal(t, 1, info.ScopeSummary()["empty_uuid_references"])
	before := len(requests)
	name = "live"
	page, err := stream.Page(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, requests, before+2)
	require.Equal(t, 3, page.Total)
	require.Equal(t, 2, page.NextOffset)
	require.False(t, page.Done)
	require.Equal(t, []int64{10, 20}, []int64{page.Records[0].SourceInstanceID, page.Records[1].SourceInstanceID})
	for _, record := range page.Records {
		require.Zero(t, record.Partition)
		require.Equal(t, "live", record.Properties["name"])
	}
	page, err = stream.Page(context.Background(), 2)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Equal(t, "pod-c", page.Records[0].Identity)
	page, err = stream.Page(context.Background(), 3)
	require.NoError(t, err)
	require.Empty(t, page.Records)
	for _, start := range []int{-1, 4} {
		_, err = stream.Page(context.Background(), start)
		require.ErrorIs(t, err, ErrCMDBConfig)
	}
	require.Equal(t, beforeConfig, fmt.Sprint(c.conf.AllSettings()))
	copy := info.ScopeSummary()
	copy["resolved_targets"] = 99
	require.Equal(t, 3, info.ScopeSummary()["resolved_targets"])
}

func TestCMDBPodPassRequiresEqualPositiveIDC(t *testing.T) {
	for _, idc := range []any{nil, 0, -1, 239, "invalid", 238.5, true} {
		t.Run(fmt.Sprint(idc), func(t *testing.T) {
			c := newPodPassTestCMDB(t, func(interfacePassRequest) any { t.Fatal("invalid scope must not issue requests"); return nil })
			c.conf.Set("inventory.cmdb.sources.primary.cabinet_condition", map[string]any{"idc_id": idc})
			_, err := c.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityPod)
			require.ErrorIs(t, err, ErrCMDBConfig)
		})
	}
}

func TestCMDBPodPassRejectsDirectoryAndTargetChanges(t *testing.T) {
	for _, mode := range []string{"duplicate id", "duplicate uuid", "count drift", "premature short page", "excess records", "target missing", "target identity"} {
		t.Run(mode, func(t *testing.T) {
			c := newPodPassTestCMDB(t, func(req interfacePassRequest) any {
				if req.path == deviceViewEndpoint {
					return interfacePassEnvelope(1, []map[string]any{{"inst_id": 1, "pod_uuid": "pod-a"}})
				}
				if req.path == "/pub/api/v3/find/instance/object/cabinet" {
					return interfacePassEnvelope(0, []map[string]any{})
				}
				if len(req.body.Condition) != 0 {
					if mode == "target missing" {
						return interfacePassEnvelope(0, []map[string]any{})
					}
					return interfacePassEnvelope(1, []map[string]any{podPassItem(10, "changed")})
				}
				if req.body.Page.Start == 0 {
					if mode == "premature short page" {
						return interfacePassEnvelope(1800, []map[string]any{podPassItem(10, "pod-a")})
					}
					if mode == "excess records" {
						return interfacePassEnvelope(1, []map[string]any{podPassItem(10, "pod-a"), podPassItem(20, "pod-b")})
					}
					if mode == "duplicate id" {
						return interfacePassEnvelope(2, []map[string]any{podPassItem(10, "pod-a"), podPassItem(10, "other")})
					}
					if mode == "duplicate uuid" {
						return interfacePassEnvelope(2, []map[string]any{podPassItem(10, "pod-a"), podPassItem(20, "pod-a")})
					}
					if mode == "count drift" {
						return interfacePassEnvelope(3, []map[string]any{podPassItem(10, "pod-a"), podPassItem(20, "b")})
					}
					return interfacePassEnvelope(1, []map[string]any{podPassItem(10, "pod-a")})
				}
				return interfacePassEnvelope(4, []map[string]any{podPassItem(30, "c")})
			})
			stream, err := c.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityPod)
			if mode == "target missing" || mode == "target identity" {
				require.NoError(t, err)
				_, err = stream.Page(context.Background(), 0)
			}
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrCMDBResponse) || errors.Is(err, ErrCMDBRecord))
		})
	}
}

func TestCMDBPodPassFreshReferencesChangeDigest(t *testing.T) {
	uuid := "missing-a"
	c := newPodPassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			return interfacePassEnvelope(1, []map[string]any{{"inst_id": 1, "pod_uuid": uuid}})
		}
		return interfacePassEnvelope(0, []map[string]any{})
	})
	first, err := c.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityPod)
	require.NoError(t, err)
	uuid = "missing-b"
	second, err := c.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityPod)
	require.NoError(t, err)
	require.NotEqual(t, first.(interface{ ScopeDigest() string }).ScopeDigest(), second.(interface{ ScopeDigest() string }).ScopeDigest())
	page, err := second.Page(context.Background(), 0)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Zero(t, page.Total)
	require.Empty(t, page.Records)
	require.True(t, reflect.ValueOf(second).MethodByName("ScopeSummary").IsValid())
}
