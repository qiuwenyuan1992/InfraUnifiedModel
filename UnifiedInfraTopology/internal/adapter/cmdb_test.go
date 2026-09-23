package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCMDBValidateLoadsReferencedConfigWithDefaults(t *testing.T) {
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.cmdb_primary.base_url", "https://cmdb.example.test")

	a := NewCMDB(conf)
	require.NoError(t, a.Validate(context.Background(), model.Source{ConfigRef: "cmdb_primary"}))

	cfg, err := a.configFor(model.Source{ConfigRef: "cmdb_primary"})
	require.NoError(t, err)
	require.Equal(t, 20000, cfg.PageSize)
	require.Equal(t, 5, cfg.Concurrency)
	require.Equal(t, 30, cfg.TimeoutSeconds)
	require.Equal(t, int64(defaultCMDBMaxResponseBytes), cfg.MaxResponseBytes)
	require.Equal(t, 30*time.Second, a.clientFor(cfg).Timeout)
}

func TestCMDBValidateRejectsMissingConfigRef(t *testing.T) {
	a := NewCMDB(viper.New())

	err := a.Validate(context.Background(), model.Source{ConfigRef: "missing"})
	require.ErrorIs(t, err, ErrCMDBConfig)
	require.NotContains(t, err.Error(), "Authorization")
}

func TestCMDBValidateHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewCMDB(viper.New()).Validate(ctx, model.Source{ConfigRef: "missing"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCMDBCollectDevicePageReturnsRecordsAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Page struct {
				Start int `json:"start"`
				Limit int `json:"limit"`
			} `json:"page"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, 2, request.Page.Start)
		require.Equal(t, 2, request.Page.Limit)
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":5,"info":[%s,%s]}}`, deviceJSON(3), deviceJSON(4))
	}))
	defer server.Close()

	page, err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevicePage(
		context.Background(), model.Source{ConfigRef: "primary"}, 2,
	)

	require.NoError(t, err)
	require.Equal(t, DevicePage{
		Records: []DeviceRecord{
			{SourceInstanceID: 3, DeviceSN: "SN-3", Name: "SN-3", ParentTypeID: 52, DeviceTypeID: 57, ComputePlane: []ComputePlaneReference{}, AllIPs: []string{}},
			{SourceInstanceID: 4, DeviceSN: "SN-4", Name: "SN-4", ParentTypeID: 52, DeviceTypeID: 57, ComputePlane: []ComputePlaneReference{}, AllIPs: []string{}},
		},
		NextOffset: 4,
		Total:      5,
		Done:       false,
	}, page)
}

func TestCMDBCollectDevicePageMarksCountReachedDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":4,"info":[%s,%s]}}`, deviceJSON(3), deviceJSON(4))
	}))
	defer server.Close()

	page, err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevicePage(
		context.Background(), model.Source{ConfigRef: "primary"}, 2,
	)

	require.NoError(t, err)
	require.Equal(t, 4, page.NextOffset)
	require.Equal(t, 4, page.Total)
	require.True(t, page.Done)
}

func TestCMDBCollectDevicePageMarksShortFinalPageDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":5,"info":[%s]}}`, deviceJSON(5))
	}))
	defer server.Close()

	page, err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevicePage(
		context.Background(), model.Source{ConfigRef: "primary"}, 4,
	)

	require.NoError(t, err)
	require.Equal(t, 5, page.NextOffset)
	require.Equal(t, 5, page.Total)
	require.True(t, page.Done)
}

func TestCMDBCollectDevicePageRejectsNonIncreasingInstanceIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":2,"info":[%s,%s]}}`, deviceJSON(2), deviceJSON(1))
	}))
	defer server.Close()

	_, err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevicePage(
		context.Background(), model.Source{ConfigRef: "primary"}, 0,
	)

	require.ErrorIs(t, err, ErrCMDBResponse)
}

func TestCMDBCollectDevicesPaginatesAndSendsConfiguredHeaders(t *testing.T) {
	var starts []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, deviceViewEndpoint, r.URL.Path)
		require.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var request struct {
			Fields []string `json:"fields"`
			Page   struct {
				Start int    `json:"start"`
				Limit int    `json:"limit"`
				Sort  string `json:"sort"`
			} `json:"page"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Contains(t, request.Fields, "device_sn")
		require.Contains(t, request.Fields, "device_ip_info")
		require.Equal(t, 2, request.Page.Limit)
		require.Equal(t, "inst_id", request.Page.Sort)
		starts = append(starts, request.Page.Start)

		info := []string{deviceJSON(request.Page.Start + 1)}
		if request.Page.Start == 0 {
			info = append(info, deviceJSON(2))
		}
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":3,"info":[%s]}}`, strings.Join(info, ","))
	}))
	defer server.Close()

	conf := cmdbTestConfig(server.URL, map[string]string{"Authorization": "Bearer secret-token"})
	collector := NewCMDBWithClient(conf, server.Client())
	var records []DeviceRecord
	err := collector.CollectDevices(context.Background(), model.Source{ConfigRef: "primary"}, func(record DeviceRecord) error {
		records = append(records, record)
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, []int{0, 2}, starts)
	require.Len(t, records, 3)
}

func TestCMDBCollectDevicesStopsImmediatelyOnVisitorError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":2,"info":[%s,%s]}}`, deviceJSON(1), deviceJSON(2))
	}))
	defer server.Close()

	visitorErr := errors.New("visitor failed")
	err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevices(
		context.Background(),
		model.Source{ConfigRef: "primary"},
		func(DeviceRecord) error { return visitorErr },
	)

	require.ErrorIs(t, err, visitorErr)
	require.Equal(t, 1, requests)
}

func TestCMDBCollectDevicesChecksContextAfterEachPage(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":2,"info":[%s]}}`, deviceJSON(1))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevices(
		ctx,
		model.Source{ConfigRef: "primary"},
		func(DeviceRecord) error {
			cancel()
			return nil
		},
	)

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, requests)
}

func TestCMDBCollectDevicesRejectsInvalidResponsesWithoutLeakingHeaders(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   error
	}{
		{name: "non 2xx", status: http.StatusBadGateway, body: `upstream secret body`, kind: ErrCMDBHTTP},
		{name: "invalid json", status: http.StatusOK, body: `{`, kind: ErrCMDBResponse},
		{name: "business error", status: http.StatusOK, body: `{"result":false,"error_code":42,"error_msg":"secret detail"}`, kind: ErrCMDBResponse},
		{name: "missing data", status: http.StatusOK, body: `{"result":true,"error_code":0}`, kind: ErrCMDBResponse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const secret = "Bearer do-not-leak"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, secret, r.Header.Get("Authorization"))
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			err := NewCMDBWithClient(
				cmdbTestConfig(server.URL, map[string]string{"Authorization": secret}),
				server.Client(),
			).CollectDevices(context.Background(), model.Source{ConfigRef: "primary"}, func(DeviceRecord) error { return nil })

			require.ErrorIs(t, err, tt.kind)
			require.NotContains(t, err.Error(), secret)
			require.NotContains(t, err.Error(), tt.body)
		})
	}
}

func TestCMDBCollectDevicesSendsConfiguredQueryAndCondition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "cmdb_all", r.URL.Query().Get("user"))
		require.Equal(t, "1", r.URL.Query().Get("timestamp"))
		require.Equal(t, "1", r.URL.Query().Get("auth"))
		var request struct {
			Condition map[string]any `json:"condition"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, float64(481679), request.Condition["inst_id"])
		fmt.Fprint(w, `{"result":true,"error_code":0,"data":{"count":0,"info":[]}}`)
	}))
	defer server.Close()

	conf := cmdbTestConfig(server.URL, nil)
	conf.Set("inventory.cmdb.sources.primary.query", map[string]string{
		"user": "cmdb_all", "timestamp": "1", "auth": "1",
	})
	conf.Set("inventory.cmdb.sources.primary.device_condition", map[string]any{"inst_id": 481679})

	err := NewCMDBWithClient(conf, server.Client()).CollectDevices(
		context.Background(), model.Source{ConfigRef: "primary"}, func(DeviceRecord) error { return nil },
	)
	require.NoError(t, err)
}

func TestCMDBCollectDevicesRejectsNilVisitor(t *testing.T) {
	collector := NewCMDB(cmdbTestConfig("https://cmdb.example.test", nil))
	err := collector.CollectDevices(context.Background(), model.Source{ConfigRef: "primary"}, nil)
	require.ErrorIs(t, err, ErrCMDBConfig)
}

func TestCMDBCollectDevicesRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":1,"info":[]},"padding":"%s"}`, strings.Repeat("x", 256))
	}))
	defer server.Close()
	conf := cmdbTestConfig(server.URL, nil)
	conf.Set("inventory.cmdb.sources.primary.max_response_bytes", 128)

	err := NewCMDBWithClient(conf, server.Client()).CollectDevices(
		context.Background(), model.Source{ConfigRef: "primary"}, func(DeviceRecord) error { return nil },
	)
	require.ErrorIs(t, err, ErrCMDBResponse)
}

func TestCMDBCollectDevicesRejectsNonIncreasingInstanceIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"result":true,"error_code":0,"data":{"count":2,"info":[%s,%s]}}`, deviceJSON(2), deviceJSON(1))
	}))
	defer server.Close()

	err := NewCMDBWithClient(cmdbTestConfig(server.URL, nil), server.Client()).CollectDevices(
		context.Background(), model.Source{ConfigRef: "primary"}, func(DeviceRecord) error { return nil },
	)
	require.ErrorIs(t, err, ErrCMDBResponse)
}

func TestCMDBDoesNotFollowRedirects(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalled = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	err := NewCMDBWithClient(
		cmdbTestConfig(server.URL, map[string]string{"X-CMDB-Token": "secret"}),
		server.Client(),
	).CollectDevices(context.Background(), model.Source{ConfigRef: "primary"}, func(DeviceRecord) error { return nil })
	require.ErrorIs(t, err, ErrCMDBHTTP)
	require.False(t, targetCalled)
}

func TestCMDBValidateRejectsInsecureHTTPWithoutExplicitOptIn(t *testing.T) {
	conf := cmdbTestConfig("http://cmdb.example.test", nil)
	conf.Set("inventory.cmdb.sources.primary.allow_insecure_http", false)
	require.ErrorIs(t, NewCMDB(conf).Validate(context.Background(), model.Source{ConfigRef: "primary"}), ErrCMDBConfig)
}

func TestCMDBValidateRejectsInvalidBaseURLAndPageSize(t *testing.T) {
	conf := cmdbTestConfig("file:///tmp/cmdb", nil)
	require.ErrorIs(t, NewCMDB(conf).Validate(context.Background(), model.Source{ConfigRef: "primary"}), ErrCMDBConfig)

	conf = cmdbTestConfig("https://cmdb.example.test", nil)
	conf.Set("inventory.cmdb.sources.primary.page_size", defaultCMDBPageSize+1)
	require.ErrorIs(t, NewCMDB(conf).Validate(context.Background(), model.Source{ConfigRef: "primary"}), ErrCMDBConfig)
}

func cmdbTestConfig(baseURL string, headers map[string]string) *viper.Viper {
	conf := viper.New()
	conf.Set("inventory.cmdb.sources.primary.base_url", baseURL)
	conf.Set("inventory.cmdb.sources.primary.allow_insecure_http", strings.HasPrefix(baseURL, "http://"))
	conf.Set("inventory.cmdb.sources.primary.page_size", 2)
	conf.Set("inventory.cmdb.sources.primary.headers", headers)
	return conf
}

func deviceJSON(id int) string {
	return fmt.Sprintf(`{"inst_id":%d,"device_id":%d,"device_sn":"SN-%d","parent_type_id":52,"device_type_id":57}`, id, id, id)
}
