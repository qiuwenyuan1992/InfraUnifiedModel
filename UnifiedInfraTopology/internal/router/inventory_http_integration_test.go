package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/migration"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func TestInventoryManagementRealHTTPPersistence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	defer pool.Close()
	require.NoError(t, migration.Apply(context.Background(), db))
	source := strings.Repeat("a", 32)
	require.NoError(t, db.Create(&model.Source{ID: source, Name: "HTTP test", AdapterKind: "cmdb", ConfigRef: "fixture", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)
	conf := viper.New()
	conf.Set("security.jwt.key", strings.Repeat("jwt-test", 8))
	conf.Set("inventory.cursor_key", strings.Repeat("cursor-test", 8))
	conf.Set("inventory.grants", []map[string]any{{"user_id": "operator", "permissions": []string{"sync:read", "sync:write"}}, {"user_id": "reader", "permissions": []string{"sync:read"}}})
	j := jwt.NewJwt(conf)
	operator, err := j.GenToken("operator", time.Now().Add(time.Hour))
	require.NoError(t, err)
	reader, err := j.GenToken("reader", time.Now().Add(time.Hour))
	require.NoError(t, err)
	l := &log.Logger{Logger: zap.NewNop()}
	base := repository.NewRepository(l, db)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	InitInventoryRouter(RouterDeps{JWT: j, Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(handler.NewHandler(l), service.NewInventoryService(repository.NewInventoryRepository(base), conf)), InventoryRoundHandler: handler.NewInventoryRoundHandler(handler.NewHandler(l), service.NewInventoryRoundService(repository.NewInventoryRoundRepository(base), conf))}, r.Group("/v1"))
	server := httptest.NewServer(r)
	defer server.Close()
	call := func(method, path, body, token, key string, status int) (string, string) {
		req, e := http.NewRequest(method, server.URL+"/v1/inventory"+path, strings.NewReader(body))
		require.NoError(t, e)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, e := server.Client().Do(req)
		require.NoError(t, e)
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		require.NoError(t, e)
		require.Equal(t, status, resp.StatusCode, "%s %s body=%s", method, path, data)
		require.NotEmpty(t, resp.Header.Get("X-Request-ID"))
		require.NotContains(t, string(data), "fixture")
		t.Logf("%s %s -> %d", method, path, resp.StatusCode)
		return string(data), resp.Header.Get("Location")
	}
	body := `{"source_id":"` + source + `","mode":"full"}`
	call("GET", "/sync-rounds", "", "", "", 401)
	call("POST", "/sync-rounds", body, reader, "read-denied", 403)
	call("POST", "/sync-rounds", body, operator, "", 400)
	call("POST", "/sync-rounds", `{"source_id":"`+source+`","mode":"full","mode":"full"}`, operator, "invalid", 400)
	first, location := call("POST", "/sync-rounds", body, operator, "http-round-1", 202)
	require.True(t, strings.HasPrefix(location, "/v1/inventory/sync-rounds/"))
	id := strings.TrimPrefix(location, "/v1/inventory/sync-rounds/")
	require.Len(t, id, 32)
	replay, replayLocation := call("POST", "/sync-rounds", body, operator, "http-round-1", 202)
	require.Equal(t, location, replayLocation)
	require.Equal(t, first, replay)
	var persisted model.SyncRound
	require.NoError(t, db.First(&persisted, "id = ?", id).Error)
	require.Equal(t, "queued", persisted.Status)
	var phases []model.SyncRoundPhase
	require.NoError(t, db.Where("round_id = ?", id).Order("position").Find(&phases).Error)
	require.Len(t, phases, 15)
	call("POST", "/sync-rounds", body, operator, "http-round-2", 409)
	call("GET", "/sync-rounds?limit=2", "", reader, "", 200)
	detail, _ := call("GET", "/sync-rounds/"+id, "", reader, "", 200)
	require.Contains(t, detail, `"total_phases":15`)
	require.Contains(t, detail, `"completed_phases":0`)
	require.NoError(t, db.Table("sync_diagnostics").Create(map[string]any{"id": strings.Repeat("b", 32), "round_id": id, "source_id": source, "resource": "spatial_relations", "severity": "warning", "code": "missing_reference", "object_ref": "test-device", "detail": "private-diagnostic-detail", "created_at": time.Now().UTC().Truncate(time.Microsecond)}).Error)
	diagnostic, _ := call("GET", "/sync-rounds/"+id+"/diagnostics", "", reader, "", 200)
	require.Contains(t, diagnostic, "missing_reference")
	require.NotContains(t, diagnostic, "private-diagnostic-detail")
	call("POST", "/sync-rounds/"+id+"/cancel", "", reader, "", 403)
	call("POST", "/sync-rounds/"+id+"/cancel", "", operator, "", 200)
	require.NoError(t, db.First(&persisted, "id = ?", id).Error)
	require.Equal(t, "canceled", persisted.Status)
	call("POST", "/sync-rounds/"+id+"/cancel", "", operator, "", 200)
	call("GET", "/sync-rounds/"+strings.Repeat("f", 32), "", reader, "", 404)
	_, secondLocation := call("POST", "/sync-rounds", body, operator, "http-round-2", 202)
	require.NotEqual(t, location, secondLocation)
	var count int64
	require.NoError(t, db.Model(&model.SyncRun{}).Count(&count).Error)
	require.Zero(t, count, "HTTP只入队整轮，不能自行触发子任务采集")
	require.True(t, json.Valid([]byte(detail)))
	t.Run("免鉴权真实服务使用固定操作身份", func(t *testing.T) {
		conf.Set("inventory.auth.enabled", false)
		conf.Set("inventory.grants", []any{})
		noAuth := gin.New()
		InitInventoryRouter(RouterDeps{JWT: j, Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(handler.NewHandler(l), service.NewInventoryService(repository.NewInventoryRepository(base), conf)), InventoryRoundHandler: handler.NewInventoryRoundHandler(handler.NewHandler(l), service.NewInventoryRoundService(repository.NewInventoryRoundRepository(base), conf))}, noAuth.Group("/v1"))
		request := httptest.NewRequest(http.MethodPost, secondLocation+"/cancel", nil)
		response := httptest.NewRecorder()
		noAuth.ServeHTTP(response, request)
		require.Equal(t, 200, response.Code)
		request = httptest.NewRequest(http.MethodPost, "/v1/inventory/sync-rounds", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "anonymous-round")
		request.Header.Set("Authorization", "forged-client-identity")
		response = httptest.NewRecorder()
		noAuth.ServeHTTP(response, request)
		require.Equal(t, 202, response.Code)
		anonymousID := strings.TrimPrefix(response.Header().Get("Location"), "/v1/inventory/sync-rounds/")
		var anonymous model.SyncRound
		require.NoError(t, db.First(&anonymous, "id = ?", anonymousID).Error)
		require.Equal(t, service.InventoryAnonymousUser, anonymous.RequestedBy)
	})
}
