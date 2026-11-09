package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
	"io"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveResumeIDC1028AfterInterruption(t *testing.T) {
	path := os.Getenv("IDC1028_RESUME_CONFIG")
	if path == "" {
		t.Skip("仅确认原执行进程停止后显式续跑")
	}
	require.Equal(t, "RESUME_1028_RETAIN_GRAPH", os.Getenv("IDC1028_RESUME_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	source := "00000000000000000000000000000238"
	var cp model.SyncCheckpoint
	require.NoError(t, db.Where("source_id = ? AND resource = ?", source, "data_centers").First(&cp).Error)
	var cursor struct {
		SnapshotAt time.Time `json:"snapshot_at"`
		RunID      string    `json:"run_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(cp.Cursor), &cursor))
	require.Equal(t, "8405c6af547fc3f7aab348c77cbfde54", cursor.RunID)
	interrupted := os.Getenv("IDC1028_INTERRUPTED_RUN")
	require.True(t, inventoryID.MatchString(interrupted), "必须指定已无执行进程的中断任务")
	control := repository.NewInventoryRepository(repository.NewRepository(nil, db))
	run, err := control.Run(ctx, interrupted)
	require.NoError(t, err)
	require.Equal(t, source, run.SourceID)
	if run.Status == model.SyncRunStatusRunning {
		_, err = control.TransitionRun(ctx, interrupted, model.SyncRunStatusRunning, model.SyncRunStatusFailed, serverUplinkNow(), "worker_interrupted")
		require.NoError(t, err)
	} else {
		require.Equal(t, model.SyncRunStatusFailed, run.Status)
	}
	for _, key := range []string{"device_condition.idc_id", "data_center_condition.inst_id", "cabinet_condition.idc_id", "interface_condition.idc_id", "transformer_condition.idc", "ups_group_condition.idc", "ups_condition.idc", "rpp_condition.idc"} {
		conf.Set("inventory.cmdb.sources.idc238."+key, 1028)
	}
	logger := log.NewLog(conf)
	defer logger.Sync()
	ctx = service.WithSyncSnapshot(ctx, cursor.SnapshotAt)
	for _, phase := range []string{"gpu_sync", "server_uplinks", "links_to"} {
		stageConf, e := cloneRoundConfig(conf)
		require.NoError(t, e)
		var output bytes.Buffer
		e = runSyncRoundStage(ctx, stageConf, logger, source, phase, &output)
		t.Logf("phase=%s output=%s", phase, output.String())
		require.NoError(t, e)
	}
	t.Logf("IDC1028 interrupted stages resumed at snapshot=%s; no data deleted", cursor.SnapshotAt.Format(time.RFC3339Nano))
}

func TestLiveIDC1028StatusReadOnly(t *testing.T) {
	path := os.Getenv("IDC1028_STATUS_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读检查，不执行同步")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Query("SELECT id,status,error_code,started_at,finished_at FROM sync_runs WHERE source_id=? ORDER BY created_at DESC LIMIT 15", "00000000000000000000000000000238")
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var code sql.NullString
		var started, finished sql.NullTime
		require.NoError(t, rows.Scan(&id, &status, &code, &started, &finished))
		t.Logf("run=%s status=%s code=%s started=%v finished=%v", id, status, code.String, started.Time, finished.Time)
	}
	reader, cleanup, err := repository.NewTopologyReadRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	page, err := reader.Roots(context.Background(), "00000000000000000000000000000238", model.EntityDataCenter, repository.TopologyDirectoryQuery{Limit: 100})
	require.NoError(t, err)
	for _, node := range page.Items {
		t.Logf("IDC label=%s inst_id=%v uuid=%s", node.Label, node.Properties["inst_id"], node.StableID)
	}
}

func TestLiveSyncIDC1028RetainIDC238(t *testing.T) {
	path := os.Getenv("IDC1028_SYNC_CONFIG")
	if path == "" {
		t.Skip("仅显式授权后采集IDC1028，不清理历史数据")
	}
	require.Equal(t, "SYNC_1028_NO_DELETE", os.Getenv("IDC1028_SYNC_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	source := "00000000000000000000000000000238"
	prefix := "inventory.cmdb.sources.idc238."
	for _, key := range []string{"device_condition.idc_id", "data_center_condition.inst_id", "cabinet_condition.idc_id", "interface_condition.idc_id", "transformer_condition.idc", "ups_group_condition.idc", "ups_condition.idc", "rpp_condition.idc"} {
		conf.Set(prefix+key, 1028)
	}
	// 只改变本次执行的内存配置，不改来源身份、配置文件或触发旧数据清理。
	logger := log.NewLog(conf)
	defer logger.Sync()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	var output bytes.Buffer
	err := runSyncRound(ctx, conf, logger, source, &output)
	decoder := json.NewDecoder(&output)
	var report syncRoundReport
	for {
		var event syncRoundReport
		e := decoder.Decode(&event)
		if e == io.EOF {
			break
		}
		require.NoError(t, e)
		if len(event.Phases) > 0 {
			report = event
		}
	}
	t.Logf("IDC1028 round=%s snapshot=%s", report.Status, report.SnapshotAt.Format(time.RFC3339Nano))
	for _, phase := range report.Phases {
		t.Logf("phase=%s status=%s run_id=%s", phase.Name, phase.Status, phase.RunID)
	}
	require.NoError(t, err)
	require.Equal(t, "PASS", report.Status)
	require.Len(t, report.Phases, 15)
	verificationCtx := service.WithSyncSnapshot(ctx, report.SnapshotAt)
	for _, phase := range report.Phases {
		configuration, e := cloneRoundConfig(conf)
		require.NoError(t, e)
		var result bytes.Buffer
		switch phase.Name {
		case "power_relations":
			e = verifyPowerSync(verificationCtx, configuration, source, phase.RunID, &result)
		case "pod_membership":
			e = verifyPODMembership(verificationCtx, configuration, source, phase.RunID, &result)
		case "spatial_relations":
			e = verifySpatialSync(verificationCtx, configuration, source, phase.RunID, &result)
		case "gpu_sync":
			e = verifyGPUSync(verificationCtx, configuration, source, phase.RunID, &result)
		case "server_uplinks":
			e = verifyServerUplink(verificationCtx, configuration, source, phase.RunID, &result)
		case "links_to":
			e = verifyLinksTo(verificationCtx, configuration, source, phase.RunID, &result)
		default:
			e = verifyPersistence(verificationCtx, configuration, source, model.EntityType(phase.Name), phase.RunID, &result)
		}
		require.NoError(t, e, "phase=%s", phase.Name)
	}
	reader, cleanup, e := repository.NewTopologyReadRepository(conf)
	require.NoError(t, e)
	defer cleanup()
	roots, e := reader.Roots(ctx, source, model.EntityDataCenter, repository.TopologyDirectoryQuery{Limit: 100})
	require.NoError(t, e)
	present := map[string]bool{}
	for _, node := range roots.Items {
		t.Logf("图中IDC identity=%s label=%s inst_id=%v", node.StableID, node.Label, node.Properties["inst_id"])
		switch value := node.Properties["inst_id"].(type) {
		case int64:
			if value == 238 {
				present["238"] = true
			}
			if value == 1028 {
				present["1028"] = true
			}
		case string:
			present[value] = true
		}
	}
	require.True(t, present["238"], "原IDC238必须保留")
	require.True(t, present["1028"], "新IDC1028必须可浏览")
	t.Log("IDC238与IDC1028同一来源共存，未执行删除，所有15阶段独立验收通过")
}
