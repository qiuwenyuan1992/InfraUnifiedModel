package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type inventoryStub struct {
	service.InventoryService
	called   bool
	err      error
	accepted bool
}

func (s *inventoryStub) List(ctx context.Context, user, resource, parent string, q service.InventoryQuery) (*service.InventoryPage, error) {
	s.called = true
	if s.err != nil {
		return nil, s.err
	}
	return &service.InventoryPage{Items: []model.Source{{ID: "source", ConfigRef: "secret-config"}}}, nil
}
func (s *inventoryStub) Enqueue(ctx context.Context, user, key string, req service.EnqueueInventoryRun) (*model.SyncRun, error) {
	s.called = true
	return &model.SyncRun{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: "queued", IdempotencyKey: key, RequestHash: "private-hash"}, s.err
}
func (s *inventoryStub) GetRun(ctx context.Context, user, id string) (*model.SyncRun, error) {
	s.called = true
	return &model.SyncRun{ID: id, Status: "queued", RequestHash: "private-hash"}, s.err
}
func (s *inventoryStub) CancelRun(ctx context.Context, user, id string) (*model.SyncRun, bool, error) {
	s.called = true
	return &model.SyncRun{ID: id, Status: "canceled"}, s.accepted, s.err
}

func inventoryRouter(t *testing.T, s *inventoryStub) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	conf := viper.New()
	conf.Set("security.jwt.key", "test-key-for-inventory-router")
	j := jwt.NewJwt(conf)
	token, err := j.GenToken("reader", time.Now().Add(time.Hour))
	require.NoError(t, err)
	l := &log.Logger{Logger: zap.NewNop()}
	r := gin.New()
	InitInventoryRouter(RouterDeps{JWT: j, Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(handler.NewHandler(l), s)}, r.Group("/v1"))
	return r, token
}
func TestInventoryRoutesRequireAuthAndHideSecrets(t *testing.T) {
	s := &inventoryStub{}
	r, token := inventoryRouter(t, s)
	path := "/v1/inventory/sources"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	require.Equal(t, 401, w.Code)
	require.Contains(t, w.Body.String(), "40101")
	require.False(t, s.called)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"))
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "secret-config")
	require.Contains(t, w.Body.String(), "next_cursor")
}
func TestInventoryRejectsMalformedInputBeforeService(t *testing.T) {
	for _, query := range []string{"limit=0", "limit=no", "limit=201", "limit=1&limit=2", "unknown=1", "limit=", "cursor=%zz"} {
		t.Run(query, func(t *testing.T) {
			s := &inventoryStub{}
			r, token := inventoryRouter(t, s)
			req := httptest.NewRequest("GET", "/v1/inventory/sources?"+query, nil)
			req.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.False(t, s.called)
		})
	}
	for _, body := range []string{`{"source_ids":[],"mode":"full","unexpected":true}`, `{} {}`, `null`, `{"mode":"full","mode":"incremental"}`} {
		t.Run(body, func(t *testing.T) {
			s := &inventoryStub{}
			r, token := inventoryRouter(t, s)
			req := httptest.NewRequest("POST", "/v1/inventory/sync-runs", strings.NewReader(body))
			req.Header.Set("Authorization", token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "key")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.False(t, s.called)
		})
	}
}
func TestInventoryAcceptedAndCancellationStatus(t *testing.T) {
	s := &inventoryStub{}
	r, token := inventoryRouter(t, s)
	path := "/v1/inventory/sync-runs"
	req := httptest.NewRequest("POST", path, strings.NewReader(`{"source_id":"cccccccccccccccccccccccccccccccc","mode":"full"}`))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	runPath := path + "/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	require.Equal(t, runPath, w.Header().Get("Location"))
	require.NotContains(t, w.Body.String(), "private-hash")
	req = httptest.NewRequest("GET", runPath, nil)
	req.Header.Set("Authorization", token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "private-hash")
	for _, accepted := range []bool{true, false} {
		s.accepted = accepted
		req = httptest.NewRequest("POST", path+"/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/cancel", nil)
		req.Header.Set("Authorization", token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		want := 200
		if accepted {
			want = 202
		}
		require.Equal(t, want, w.Code)
	}
}
func TestInventoryObsoleteAssetRoutesAreNotRegistered(t *testing.T) {
	s := &inventoryStub{}
	router, token := inventoryRouter(t, s)
	for _, path := range []string{
		"/v1/inventory/devices",
		"/v1/inventory/generations",
		"/v1/inventory/devices/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/interfaces",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusNotFound, response.Code, path)
	}
	require.False(t, s.called)
}

func TestInventoryErrorSanitization(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{service.ErrInventoryForbidden, 403, "40301"}, {service.ErrInventoryIdempotency, 409, "40904"}, {context.DeadlineExceeded, 504, "50401"}} {
		s := &inventoryStub{err: tc.err}
		r, token := inventoryRouter(t, s)
		req := httptest.NewRequest("GET", "/v1/inventory/sources", nil)
		req.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, tc.status, w.Code)
		require.Contains(t, w.Body.String(), tc.code)
	}
}
