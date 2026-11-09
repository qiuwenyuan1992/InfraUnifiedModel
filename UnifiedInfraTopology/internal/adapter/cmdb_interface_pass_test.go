package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	kind, name := "physics", "Ethernet1"
	if id == 101 {
		kind, name = "virtual", "Bridge-Aggregation10"
	}
	return map[string]any{"inst_id": id, "port_uuid": fmt.Sprintf("port-%d", id), "port_type": kind, "port_name": name}
}

func TestCMDBInterfacePassReusesFirstPagesInDevicePartitionOrder(t *testing.T) {
	var mu sync.Mutex
	var requests []interfacePassRequest
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		if req.path == deviceViewEndpoint {
			require.Equal(t, map[string]any{"idc_id": float64(1030), "parent_type_id": float64(54)}, req.body.Condition)
			if req.body.Page.Start == 0 {
				return interfacePassEnvelope(3, []map[string]any{interfacePassDevice(10, " SN-A "), interfacePassDevice(20, "SN-B")})
			}
			return interfacePassEnvelope(3, []map[string]any{interfacePassDevice(30, "SN-C")})
		}
		require.Equal(t, "/pub/api/v3/find/instance/object/port_view", req.path)
		require.Equal(t, float64(1030), req.body.Condition["idc_id"])
		require.NotContains(t, req.body.Condition, "port_type")
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
	require.Len(t, requests, 5, "two discovery pages and exactly one port request per device")
	for _, req := range requests {
		if req.path != deviceViewEndpoint {
			require.Equal(t, 1000, req.body.Page.Limit, "small configured discovery pages must not shrink per-device port requests")
		}
	}
	for _, tc := range []struct {
		start, next, size int
		partition         int64
		done              bool
	}{
		{0, 3, 3, 10, false}, {3, 4, 1, 20, true},
	} {
		before := len(requests)
		page, err := stream.Page(context.Background(), tc.start)
		require.NoError(t, err)
		require.Len(t, requests, before, "first pages must not be fetched again")
		require.Equal(t, 4, page.Total)
		require.Equal(t, tc.next, page.NextOffset)
		require.Equal(t, tc.done, page.Done)
		require.Len(t, page.Records, tc.size)
		for _, record := range page.Records {
			require.Equal(t, tc.partition, record.Partition)
			if record.Identity == "port-101" {
				require.Equal(t, "virtual", record.Properties["port_type"])
				require.Equal(t, "Bridge-Aggregation10", record.Properties["port_name"])
			}
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

func TestCMDBInterfacePassRejectsInvalidFirstPage(t *testing.T) {
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
		{"count changed", 1004, []map[string]any{interfacePassPort(1001), interfacePassPort(1002), interfacePassPort(1003)}},
		{"empty page", 1003, []map[string]any{}},
		{"short page", 1003, []map[string]any{interfacePassPort(1001)}},
		{"duplicate IDs", 1003, []map[string]any{interfacePassPort(1001), interfacePassPort(1001), interfacePassPort(1003)}},
		{"nonincreasing boundary", 1003, []map[string]any{interfacePassPort(1000), interfacePassPort(1001), interfacePassPort(1002)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
				if req.path == deviceViewEndpoint {
					return interfacePassEnvelope(1, []map[string]any{interfacePassDevice(10, "A")})
				}
				if req.body.Page.Start == 0 {
					items := make([]map[string]any, 1000)
					for i := range items {
						items[i] = interfacePassPort(int64(i + 1))
					}
					return interfacePassEnvelope(1003, items)
				}
				return interfacePassEnvelope(tc.count, tc.items)
			})
			stream, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
			require.NoError(t, err)
			page, err := stream.Page(context.Background(), 0)
			require.NoError(t, err)
			require.Equal(t, 1000, page.NextOffset)
			_, err = stream.Page(context.Background(), 1000)
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
		items := make([]map[string]any, min(1000, count))
		for i := range items {
			items[i] = interfacePassPort(int64(i + 1))
		}
		return interfacePassEnvelope(count, items)
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
	require.Equal(t, "old-uuid", page.Records[0].Identity, "a reopened pass must not replace the first pass's cached first page")
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

func TestCMDBInterfacePassPaginatesOversizedDeviceWithoutRepeatingFirstPage(t *testing.T) {
	var mu sync.Mutex
	var starts []int
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			return interfacePassEnvelope(1, []map[string]any{interfacePassDevice(10, "A")})
		}
		require.Equal(t, 1000, req.body.Page.Limit)
		require.Equal(t, "inst_id", req.body.Page.Sort)
		mu.Lock()
		starts = append(starts, req.body.Page.Start)
		mu.Unlock()
		items := make([]map[string]any, min(1000, 2003-req.body.Page.Start))
		for i := range items {
			items[i] = interfacePassPort(int64(req.body.Page.Start + i + 1))
		}
		return interfacePassEnvelope(2003, items)
	})
	collector.conf.Set("inventory.cmdb.sources.primary.page_size", 20)
	stream, err := collector.OpenEntityPass(context.Background(), model.Source{ConfigRef: "primary"}, model.EntityInterface)
	require.NoError(t, err)
	for _, tc := range []struct {
		start, next, size int
		done              bool
	}{{0, 1000, 1000, false}, {1000, 2000, 1000, false}, {2000, 2003, 3, true}} {
		page, err := stream.Page(context.Background(), tc.start)
		require.NoError(t, err)
		require.Equal(t, 2003, page.Total)
		require.Equal(t, tc.next, page.NextOffset)
		require.Len(t, page.Records, tc.size)
		require.Equal(t, tc.done, page.Done)
		for i, record := range page.Records {
			require.Equal(t, int64(tc.start+i+1), record.SourceInstanceID)
			require.Equal(t, int64(10), record.Partition)
			require.Equal(t, "A", record.OwnerDeviceSN)
		}
	}
	require.Equal(t, []int{0, 1000, 2000}, starts)
}

func TestCMDBInterfacePassBoundsParallelRequestsAndKeepsDeviceOrder(t *testing.T) {
	entered := make(chan string, 6)
	release := make(chan struct{})
	defer close(release)
	var active, peak atomic.Int32
	collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
		if req.path == deviceViewEndpoint {
			items := make([]map[string]any, 2)
			for i := range items {
				id := int64(req.body.Page.Start + i + 1)
				items[i] = interfacePassDevice(id, fmt.Sprintf("SN-%d", 7-id))
			}
			return interfacePassEnvelope(6, items)
		}
		n := active.Add(1)
		defer active.Add(-1)
		for current := peak.Load(); n > current; current = peak.Load() {
			if peak.CompareAndSwap(current, n) {
				break
			}
		}
		entered <- req.body.Condition["sn"].(string)
		<-release
		return interfacePassEnvelope(1, []map[string]any{interfacePassPort(1)})
	})
	collector.conf.Set("inventory.cmdb.sources.primary.concurrency", 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		stream EntityPageStream
		err    error
	}
	opened := make(chan result, 1)
	go func() {
		stream, err := collector.OpenEntityPass(ctx, model.Source{ConfigRef: "primary"}, model.EntityInterface)
		opened <- result{stream, err}
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("configured workers must issue concurrent first-page requests")
		}
	}
	select {
	case <-entered:
		t.Fatal("per-device requests exceeded configured concurrency")
	case <-time.After(20 * time.Millisecond):
	}
	for i := 0; i < 6; i++ {
		release <- struct{}{}
	}
	var got result
	select {
	case got = <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("interface pass did not finish its bounded work")
	}
	require.NoError(t, got.err)
	require.Equal(t, int32(3), peak.Load())
	for start := 0; start < 6; start++ {
		page, err := got.stream.Page(context.Background(), start)
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Equal(t, int64(start+1), page.Records[0].Partition)
		require.Equal(t, fmt.Sprintf("SN-%d", 6-start), page.Records[0].OwnerDeviceSN)
	}
}

type cmdbInterfacePassTransport func(*http.Request) (*http.Response, error)

func (f cmdbInterfacePassTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCMDBInterfacePassCancelsAndJoinsWorkers(t *testing.T) {
	for _, failRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("upstream_failure=%t", failRequest), func(t *testing.T) {
			collector := newInterfacePassTestCMDB(t, func(req interfacePassRequest) any {
				items := []map[string]any{
					interfacePassDevice(int64(req.body.Page.Start+1), fmt.Sprintf("SN-%d", req.body.Page.Start+1)),
					interfacePassDevice(int64(req.body.Page.Start+2), fmt.Sprintf("SN-%d", req.body.Page.Start+2)),
				}
				return interfacePassEnvelope(4, items)
			})
			collector.conf.Set("inventory.cmdb.sources.primary.concurrency", 2)
			transport := collector.httpClient.Transport
			entered := make(chan struct{}, 4)
			fail := make(chan struct{})
			var active, calls atomic.Int32
			collector.httpClient = &http.Client{Transport: cmdbInterfacePassTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == deviceViewEndpoint {
					return transport.RoundTrip(req)
				}
				var body cmdbEntityRequest
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					return nil, err
				}
				active.Add(1)
				calls.Add(1)
				defer active.Add(-1)
				entered <- struct{}{}
				if failRequest && body.Condition["sn"] == "SN-1" {
					select {
					case <-fail:
						return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"result":true,"error_code":0,"data":{"count":1,"info":[]}}`))}, nil
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
				}
				<-req.Context().Done()
				return nil, req.Context().Err()
			})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := collector.OpenEntityPass(ctx, model.Source{ConfigRef: "primary"}, model.EntityInterface)
				finished <- err
			}()
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("both bounded workers must start before cancellation")
				}
			}
			if failRequest {
				close(fail)
			} else {
				cancel()
			}
			select {
			case err := <-finished:
				if failRequest {
					require.ErrorIs(t, err, ErrCMDBResponse)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation must join all workers without hanging")
			}
			require.Zero(t, active.Load(), "OpenEntityPass must not return while request workers remain active")
			require.Equal(t, int32(2), calls.Load(), "queued devices must not be requested after cancellation")
		})
	}
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
