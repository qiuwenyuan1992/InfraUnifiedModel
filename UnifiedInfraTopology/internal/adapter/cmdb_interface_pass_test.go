package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func TestCMDBInterfacePassContract(t *testing.T) {
	require.True(t, reflect.ValueOf(NewCMDB(nil)).MethodByName("OpenEntityPass").IsValid(), "CMDB must expose independent interface passes")
}

type interfacePassRequest struct {
	path string
	body cmdbEntityRequest
}

func newInterfacePassTestCMDB(t *testing.T, respond func(interfacePassRequest) any) *CMDB {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body cmdbEntityRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(respond(interfacePassRequest{r.URL.Path, body}))
	}))
	t.Cleanup(server.Close)
	conf := cmdbTestConfig(server.URL, nil)
	conf.Set("inventory.cmdb.sources.primary.page_size", 2)
	conf.Set("inventory.cmdb.sources.primary.entity_type", "interface")
	conf.Set("inventory.cmdb.sources.primary.interface_condition", map[string]any{"idc_id": 1030})
	conf.Set("inventory.cmdb.sources.primary.device_condition", map[string]any{"parent_type_id": 53, "device_type_id": 999})
	return NewCMDBWithClient(conf, server.Client())
}

func interfacePassEnvelope(count int, items []map[string]any) any {
	return map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": count, "info": items}}
}

func interfacePassDevice(id int64, sn string) map[string]any {
	return map[string]any{"inst_id": id, "device_sn": sn, "parent_type_id": 54}
}

func interfacePassPort(id int64) map[string]any {
	return map[string]any{"inst_id": id, "port_uuid": fmt.Sprintf("port-%d", id)}
}

func TestCMDBInterfacePassFlattensDevicePartitionsWithoutCachingPorts(t *testing.T) {
	var requests []interfacePassRequest
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		requests = append(requests, req)
		if req.path == deviceViewEndpoint {
			require.Equal(t, map[string]any{"idc_id": float64(1030), "parent_type_id": float64(54)}, req.body.Condition)
			if req.body.Page.Start == 0 {
				return interfacePassEnvelope(3, []map[string]any{interfacePassDevice(10, " SN-A "), interfacePassDevice(20, "SN-B")})
			}
			return interfacePassEnvelope(3, []map[string]any{interfacePassDevice(30, "SN-C")})
		}
		require.Equal(t, "/pub/api/v3/find/instance/object/port_view", req.path)
		require.Equal(t, float64(1030), req.body.Condition["idc_id"])
		require.Equal(t, "physics", req.body.Condition["port_type"])
		require.NotContains(t, req.body.Condition, "local_device_sn")
		ports := map[string][]map[string]any{
			"SN-A": {interfacePassPort(100), interfacePassPort(101), interfacePassPort(102)},
			"SN-B": {interfacePassPort(1)},
			"SN-C": {},
		}[req.body.Condition["sn"].(string)]
		end := min(req.body.Page.Start+req.body.Page.Limit, len(ports))
		return interfacePassEnvelope(len(ports), ports[req.body.Page.Start:end])
	})
	stream, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.NoError(t, err)
	for _, req := range requests {
		if req.path != deviceViewEndpoint {
			require.Equal(t, 1, req.body.Page.Limit, "opening must only read tiny count pages")
		}
	}
	for _, tc := range []struct {
		start, next, size int
		partition         int64
		done              bool
	}{
		{0, 2, 2, 10, false}, {2, 3, 1, 10, false}, {3, 4, 1, 20, true},
	} {
		before := len(requests)
		page, err := stream.Page(context.Background(), tc.start)
		require.NoError(t, err)
		require.Len(t, requests, before+1, "pages must fetch live records instead of a cached port snapshot")
		require.Equal(t, 4, page.Total)
		require.Equal(t, tc.next, page.NextOffset)
		require.Equal(t, tc.done, page.Done)
		require.Len(t, page.Records, tc.size)
		for _, record := range page.Records {
			require.Equal(t, tc.partition, record.Partition)
			require.Equal(t, map[int64]string{10: "SN-A", 20: "SN-B"}[tc.partition], record.OwnerDeviceSN)
			require.NotContains(t, record.Properties, "owner_device_sn")
			require.NotContains(t, record.Properties, "OwnerDeviceSN")
		}
	}
	require.Equal(t, map[string]any{"idc_id": 1030}, collector.conf.GetStringMap("inventory.cmdb.sources.primary.interface_condition"))
	page, err := stream.Page(context.Background(), 4)
	require.NoError(t, err)
	require.True(t, page.Done)
	require.Empty(t, page.Records)
	_, err = stream.Page(context.Background(), -1)
	require.ErrorIs(t, err, ErrCMDBConfig)
	_, err = stream.Page(context.Background(), 5)
	require.ErrorIs(t, err, ErrCMDBConfig)
}

func TestCMDBInterfacePassRejectsInvalidDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []map[string]any
		count int
	}{
		{"wrong category", []map[string]any{{"inst_id": 1, "device_sn": "SN", "parent_type_id": 53}}, 1},
		{"missing SN", []map[string]any{interfacePassDevice(1, " ")}, 1},
		{"nonpositive ID", []map[string]any{interfacePassDevice(0, "SN")}, 1},
		{"duplicate SN", []map[string]any{interfacePassDevice(1, "SN"), interfacePassDevice(2, " SN ")}, 2},
		{"unsorted ID", []map[string]any{interfacePassDevice(2, "SN-A"), interfacePassDevice(1, "SN-B")}, 2},
		{"short device page", []map[string]any{interfacePassDevice(1, "SN")}, 3},
		{"count smaller than page", []map[string]any{interfacePassDevice(1, "SN"), interfacePassDevice(2, "SN-B")}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
				if req.path == deviceViewEndpoint {
					return interfacePassEnvelope(tc.count, tc.items)
				}
				return interfacePassEnvelope(0, []map[string]any{})
			})
			_, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
			require.Error(t, err)
		})
	}
}

func TestCMDBInterfacePassRejectsDeviceCountAndOrderChangesAcrossPages(t *testing.T) {
	for _, changedCount := range []bool{true, false} {
		t.Run(fmt.Sprint(changedCount), func(t *testing.T) {
			collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
				if req.path != deviceViewEndpoint {
					return interfacePassEnvelope(0, []map[string]any{})
				}
				if req.body.Page.Start == 0 {
					return interfacePassEnvelope(3, []map[string]any{interfacePassDevice(10, "A"), interfacePassDevice(20, "B")})
				}
				if changedCount {
					return interfacePassEnvelope(4, []map[string]any{interfacePassDevice(30, "C")})
				}
				return interfacePassEnvelope(3, []map[string]any{interfacePassDevice(20, "C")})
			})
			_, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
			require.ErrorIs(t, err, ErrCMDBResponse)
		})
	}
}

func TestCMDBInterfacePassRejectsInvalidCountPrepass(t *testing.T) {
	for _, payload := range []any{
		map[string]any{"result": true, "error_code": 0, "data": map[string]any{"count": 1}},
		interfacePassEnvelope(1, []map[string]any{}),
		interfacePassEnvelope(0, []map[string]any{interfacePassPort(1)}),
		interfacePassEnvelope(1, []map[string]any{{"inst_id": 1, "port_uuid": " "}}),
		interfacePassEnvelope(1, []map[string]any{{"inst_id": 0, "port_uuid": "uuid"}}),
	} {
		collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
			if req.path == deviceViewEndpoint {
				return interfacePassEnvelope(1, []map[string]any{interfacePassDevice(10, "A")})
			}
			return payload
		})
		_, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
		require.Error(t, err)
	}
}

func TestCMDBInterfacePassRejectsLiveCountAndShortPageChanges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		items []map[string]any
	}{
		{"count changed", 4, []map[string]any{interfacePassPort(1), interfacePassPort(2)}},
		{"empty page", 3, []map[string]any{}},
		{"short page", 3, []map[string]any{interfacePassPort(1)}},
		{"duplicate IDs", 3, []map[string]any{interfacePassPort(1), interfacePassPort(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
				if req.path == deviceViewEndpoint {
					return interfacePassEnvelope(1, []map[string]any{interfacePassDevice(10, "A")})
				}
				if req.body.Page.Limit == 1 {
					return interfacePassEnvelope(3, []map[string]any{interfacePassPort(1)})
				}
				return interfacePassEnvelope(tc.count, tc.items)
			})
			stream, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
			require.NoError(t, err)
			_, err = stream.Page(context.Background(), 0)
			require.ErrorIs(t, err, ErrCMDBResponse)
		})
	}
}

func TestCMDBInterfacePassEmptyAndIndependentPasses(t *testing.T) {
	devices := []map[string]any{}
	portCount := 0
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			return interfacePassEnvelope(len(devices), devices)
		}
		items := []map[string]any{}
		for i := 0; i < min(req.body.Page.Limit, portCount-req.body.Page.Start); i++ {
			items = append(items, interfacePassPort(int64(req.body.Page.Start+i+1)))
		}
		return interfacePassEnvelope(portCount, items)
	})
	for _, count := range []int{0, 0, 1, 2} {
		if count > 0 {
			portCount = count
			devices = []map[string]any{interfacePassDevice(10, "A")}
		}
		stream, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
		require.NoError(t, err)
		page, err := stream.Page(context.Background(), 0)
		require.NoError(t, err)
		require.Equal(t, count, page.Total)
		require.Len(t, page.Records, count)
		require.True(t, page.Done)
		devices = []map[string]any{interfacePassDevice(10, "A")}
	}
}

func TestCMDBInterfacePassTotalOverflow(t *testing.T) {
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			return interfacePassEnvelope(2, []map[string]any{interfacePassDevice(10, "A"), interfacePassDevice(20, "B")})
		}
		count := 1
		if req.body.Condition["sn"] == "A" {
			count = int(^uint(0) >> 1)
		}
		return interfacePassEnvelope(count, []map[string]any{interfacePassPort(1)})
	})
	_, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.ErrorIs(t, err, ErrCMDBResponse)
}

func TestCMDBInterfacePassReopensDevicesAndReadsFreshUUIDs(t *testing.T) {
	deviceID := int64(10)
	identity := "old-uuid"
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			return interfacePassEnvelope(1, []map[string]any{interfacePassDevice(deviceID, "A")})
		}
		return interfacePassEnvelope(1, []map[string]any{{"inst_id": 1, "port_uuid": identity}})
	})
	first, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.NoError(t, err)
	page, err := first.Page(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, "old-uuid", page.Records[0].Identity)
	deviceID, identity = 20, "new-uuid"
	second, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.NoError(t, err)
	page, err = second.Page(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, int64(20), page.Records[0].Partition)
	require.Equal(t, "new-uuid", page.Records[0].Identity)
	page, err = first.Page(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, int64(10), page.Records[0].Partition, "existing pass keeps its independent device manifest")
	require.Equal(t, "new-uuid", page.Records[0].Identity, "records must not be cached between page reads")
}

func TestCMDBInterfacePassExplicitSNRemainsNarrowWhenDiscoveryIgnoresFilter(t *testing.T) {
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			return interfacePassEnvelope(2, []map[string]any{interfacePassDevice(10, "A"), interfacePassDevice(20, "B")})
		}
		require.Equal(t, "B", req.body.Condition["sn"])
		return interfacePassEnvelope(1, []map[string]any{interfacePassPort(1)})
	})
	collector.conf.Set("inventory.cmdb.sources.primary.interface_condition", map[string]any{"idc_id": 1030, "sn": "B"})
	stream, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.NoError(t, err)
	page, err := stream.Page(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Equal(t, int64(20), page.Records[0].Partition)
}

func TestCMDBInterfacePassPreservesExplicitSNAndPortType(t *testing.T) {
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			require.Equal(t, "A", req.body.Condition["device_sn"])
			return interfacePassEnvelope(1, []map[string]any{interfacePassDevice(10, "A")})
		}
		require.Equal(t, map[string]any{"idc_id": float64(1030), "sn": "A", "port_type": "logical", "group_type": float64(7)}, req.body.Condition)
		return interfacePassEnvelope(0, []map[string]any{})
	})
	collector.conf.Set("inventory.cmdb.sources.primary.interface_condition", map[string]any{"idc_id": 1030, "sn": " A ", "port_type": "logical", "group_type": 7})
	_, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.NoError(t, err)
	_, err = collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityCabinet)
	require.ErrorIs(t, err, ErrCMDBConfig)
	collector.conf.Set("inventory.cmdb.sources.primary.interface_condition", map[string]any{})
	_, err = collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.ErrorIs(t, err, ErrCMDBConfig)
}
