package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/router"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func TestLiveHTTPRoundStateReadOnly(t *testing.T) {
	path := os.Getenv("SYNC_HTTP_INSPECT_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读检查HTTP任务，不执行采集")
	}
	id := os.Getenv("SYNC_HTTP_INSPECT_ID")
	require.True(t, inventoryID.MatchString(id))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	var round model.SyncRound
	require.NoError(t, db.First(&round, "id = ?", id).Error)
	var phases []model.SyncRoundPhase
	require.NoError(t, db.Where("round_id = ?", id).Order("position").Find(&phases).Error)
	t.Logf("round=%s status=%s code=%s snapshot=%v", round.ID, round.Status, round.ErrorCode, round.SnapshotAt)
	for _, phase := range phases {
		if phase.Status != "failed" {
			continue
		}
		var run model.SyncRun
		require.NoError(t, db.First(&run, "id = ?", phase.RunID).Error)
		var checkpoints []model.SyncCheckpoint
		require.NoError(t, db.Where("source_id = ? AND resource = ?", round.SourceID, phase.Name).Find(&checkpoints).Error)
		t.Logf("phase=%s child=%s status=%s code=%s checkpoint=%+v", phase.Name, run.ID, run.Status, run.ErrorCode, checkpoints)
	}
}

func TestLiveMySQLHTTPFullSync(t *testing.T) {
	path := os.Getenv("SYNC_HTTP_LIVE_CONFIG")
	if path == "" {
		t.Skip("显式授权后真实HTTP/MySQL/CMDB/图库联调；默认不访问外部服务")
	}
	require.Equal(t, "IDC238_REAL_SYNC", os.Getenv("SYNC_HTTP_LIVE_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	require.Equal(t, "mysql", conf.GetString("data.db.main.driver"))
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	source := "00000000000000000000000000000238"
	var active int64
	require.NoError(t, db.WithContext(ctx).Table("sync_rounds").Where("source_id = ? AND status IN (?, ?)", source, "queued", "running").Count(&active).Error)
	require.Zero(t, active, "已有整轮任务时停止，不能误执行其他任务")
	require.NoError(t, db.WithContext(ctx).Table("sync_runs").Where("source_id = ? AND status IN (?, ?, ?, ?)", source, "queued", "running", "validating", "publishing").Count(&active).Error)
	require.Zero(t, active)
	// 权限和JWT仅用于这个临时HTTP进程，不修改本地配置文件或用户数据。
	// 此测试专门验证JWT模式，不能继承开发免鉴权开关。
	conf.Set("inventory.auth.enabled", true)
	conf.Set("inventory.cursor_key", strings.Repeat("live-http-test", 4))
	conf.Set("inventory.grants", []map[string]any{{"user_id": "live_http_operator", "permissions": []string{"sync:read", "sync:write"}}, {"user_id": "live_http_reader", "permissions": []string{"sync:read"}}})
	j := jwt.NewJwt(conf)
	operator, err := j.GenToken("live_http_operator", time.Now().Add(time.Hour))
	require.NoError(t, err)
	reader, err := j.GenToken("live_http_reader", time.Now().Add(time.Hour))
	require.NoError(t, err)
	l := &log.Logger{Logger: zap.NewNop()}
	base := repository.NewRepository(l, db)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router.InitInventoryRouter(router.RouterDeps{JWT: j, Logger: l, Config: conf, InventoryHandler: handler.NewInventoryHandler(handler.NewHandler(l), service.NewInventoryService(repository.NewInventoryRepository(base), conf)), InventoryRoundHandler: handler.NewInventoryRoundHandler(handler.NewHandler(l), service.NewInventoryRoundService(repository.NewInventoryRoundRepository(base), conf))}, engine.Group("/v1"))
	server := httptest.NewServer(engine)
	defer server.Close()
	client := server.Client()
	client.Timeout = 15 * time.Second
	call := func(method, path, body, token, key string, status int) ([]byte, string) {
		request, e := http.NewRequestWithContext(ctx, method, server.URL+"/v1/inventory"+path, strings.NewReader(body))
		require.NoError(t, e)
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response, e := client.Do(request)
		require.NoError(t, e)
		defer response.Body.Close()
		data, e := io.ReadAll(response.Body)
		require.NoError(t, e)
		require.Equal(t, status, response.StatusCode, "%s %s: %s", method, path, data)
		require.NotEmpty(t, response.Header.Get("X-Request-ID"))
		return data, response.Header.Get("Location")
	}
	body := `{"source_id":"` + source + `","mode":"full"}`
	call("GET", "/sync-rounds", "", "", "", 401)
	call("POST", "/sync-rounds", body, reader, "read-denied", 403)
	call("POST", "/sync-rounds", body, operator, "", 400)
	call("GET", "/sync-rounds?limit=201", "", reader, "", 400)
	key := fmt.Sprintf("http-live-%d", time.Now().UnixNano())
	_, cancelLocation := call("POST", "/sync-rounds", body, operator, key+"-cancel", 202)
	cancelID := strings.TrimPrefix(cancelLocation, "/v1/inventory/sync-rounds/")
	require.True(t, inventoryID.MatchString(cancelID))
	call("POST", "/sync-rounds/"+cancelID+"/cancel", "", operator, "", 200)
	_, location := call("POST", "/sync-rounds", body, operator, key, 202)
	id := strings.TrimPrefix(location, "/v1/inventory/sync-rounds/")
	require.True(t, inventoryID.MatchString(id))
	_, replay := call("POST", "/sync-rounds", body, operator, key, 202)
	require.Equal(t, location, replay)
	call("POST", "/sync-rounds", body, operator, key+"-conflict", 409)
	t.Logf("真实HTTP鉴权/参数/幂等/冲突/排队取消通过；MySQL整轮ID=%s", id)
	decode := func(data []byte) v1.InventoryRoundDTO {
		var envelope struct {
			Data v1.InventoryRoundDTO `json:"data"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		return envelope.Data
	}
	initial, _ := call("GET", "/sync-rounds/"+id, "", reader, "", 200)
	require.Equal(t, "queued", decode(initial).Status)
	worker, cleanup, err := newQueuedRoundWorker(conf, l)
	require.NoError(t, err)
	defer cleanup()
	result := make(chan error, 1)
	go func() {
		processed, e := worker.ProcessNext(ctx)
		if e == nil && !processed {
			e = fmt.Errorf("未领取API任务")
		}
		result <- e
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	completed := -1
	for {
		select {
		case e := <-result:
			data, _ := call("GET", "/sync-rounds/"+id, "", reader, "", 200)
			round := decode(data)
			if e != nil {
				t.Logf("失败阶段状态: %s", data)
			}
			require.NoError(t, e)
			require.Equal(t, "succeeded", round.Status)
			require.Equal(t, 15, round.CompletedPhases)
			require.Len(t, round.Phases, 15)
			for _, phase := range round.Phases {
				require.Equal(t, "succeeded", phase.Status)
				require.NotNil(t, phase.RunID)
			}
			diagnostic, _ := call("GET", "/sync-rounds/"+id+"/diagnostics?limit=200", "", reader, "", 200)
			require.NotContains(t, string(diagnostic), `"detail"`)
			var diagnosticEnvelope struct {
				Data struct {
					Items      []json.RawMessage `json:"items"`
					NextCursor string            `json:"next_cursor"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(diagnostic, &diagnosticEnvelope))
			t.Logf("真实API整轮成功 id=%s 阶段=%d/15 诊断第一页=%d条 有后续页=%v", id, round.CompletedPhases, len(diagnosticEnvelope.Data.Items), diagnosticEnvelope.Data.NextCursor != "")
			for _, phase := range round.Phases {
				t.Logf("阶段=%s 子任务=%s 节点=%d 边=%d 跳过=%d", phase.Name, *phase.RunID, phase.Nodes, phase.Edges, phase.Skipped)
			}
			return
		case <-ticker.C:
			data, _ := call("GET", "/sync-rounds/"+id, "", reader, "", 200)
			round := decode(data)
			if completed != round.CompletedPhases {
				completed = round.CompletedPhases
				t.Logf("HTTP进度 id=%s 状态=%s 完成=%d/15", id, round.Status, completed)
			}
		case <-ctx.Done():
			t.Fatal("真实HTTP同步超时，保留任务历史以便诊断")
		}
	}
}
