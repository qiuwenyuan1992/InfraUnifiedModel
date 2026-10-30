package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

var inventoryID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func syncFiltered(ctx context.Context, conf *viper.Viper, logger *log.Logger, sourceID string, entity model.EntityType, output io.Writer) (string, error) {
	if !inventoryID.MatchString(sourceID) || (entity != model.EntityDevice && entity != model.EntityDataCenter && entity != model.EntityCabinet && entity != model.EntityInterface && entity != model.EntityTransformer) {
		return "", errors.New("filtered sync requires a valid source ID and device, data_center, cabinet, interface, or transformer")
	}
	if conf.GetString("data.db.main.driver") != "mysql" {
		return "", errors.New("filtered sync requires the configured MySQL control database")
	}
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return "", errors.New("control database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return "", errors.New("control database unavailable")
	}
	defer sqlDB.Close()
	var source model.Source
	if err := db.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error; err != nil {
		return "", errors.New("configured source not found")
	}
	if !source.Enabled || source.AdapterKind != "cmdb" || source.ConfigRef == "" {
		return "", errors.New("source must be enabled and use the CMDB adapter")
	}
	// 本次覆盖只在当前进程生效，不修改配置文件，也不切换其他来源。
	conf.Set("inventory.cmdb.sources."+source.ConfigRef+".entity_type", string(entity))
	collector := adapter.NewCMDB(conf)
	if err := collector.Validate(ctx, source); err != nil {
		return "", errors.New("source configuration invalid")
	}
	full, err := collector.CoversFullSource(source, entity)
	if err != nil {
		return "", errors.New("entity filter configuration invalid")
	}
	if full {
		return "", errors.New("filtered sync refuses unfiltered collection")
	}
	filterKey := "idc_id"
	if entity == model.EntityDataCenter {
		filterKey = "inst_id"
	} else if entity == model.EntityTransformer {
		filterKey = "idc"
	}
	if conf.GetInt("inventory.cmdb.sources."+source.ConfigRef+"."+string(entity)+"_condition."+filterKey) <= 0 {
		return "", errors.New("filtered sync requires an explicit positive IDC selection")
	}
	var active int64
	if err := db.WithContext(ctx).Model(&model.SyncRun{}).Where("source_id = ? AND status IN ?", sourceID, []string{model.SyncRunStatusQueued, model.SyncRunStatusRunning, model.SyncRunStatusValidating, model.SyncRunStatusPublishing}).Count(&active).Error; err != nil {
		return "", errors.New("active task inspection failed")
	}
	if active != 0 {
		return "", errors.New("source has outstanding tasks; finish them before changing entity")
	}
	graphConfig, err := graphclient.ParseConfig(conf)
	if err != nil || len(graphConfig.Hosts) == 0 || graphConfig.Space == "" {
		return "", errors.New("graph must be configured before enqueue")
	}
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	if err != nil {
		return "", errors.New("graph connection failed")
	}
	defer cleanup()
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", errors.New("run ID generation failed")
	}
	runID := hex.EncodeToString(idBytes)
	requestHash := sha256.Sum256([]byte(sourceID + ":" + string(entity) + ":filtered"))
	request := &model.SyncRun{ID: runID, SourceID: sourceID, Status: model.SyncRunStatusQueued, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), IdempotencyKey: "cli-filtered-" + runID, RequestHash: hex.EncodeToString(requestHash[:])}
	base := repository.NewRepository(logger, db)
	control := repository.NewInventoryRunRepository(base, runID)
	var claimed *model.SyncRun
	var claimedSource *model.Source
	if err := base.Transaction(ctx, func(txCtx context.Context) error {
		if _, err := control.Enqueue(txCtx, request); err != nil {
			return err
		}
		var err error
		claimed, claimedSource, err = control.ClaimNext(txCtx, time.Now().UTC().Truncate(time.Microsecond))
		return err
	}); err != nil {
		return "", errors.New("filtered task atomic enqueue and claim failed")
	}
	if err := json.NewEncoder(output).Encode(map[string]string{"status": "running", "run_id": runID, "source_id": sourceID, "entity_type": string(entity)}); err != nil {
		return runID, err
	}
	worker := service.NewSyncWorker(conf, control, graph, collector, logger)
	if err := worker.ProcessClaimed(ctx, claimed, *claimedSource); err != nil {
		return runID, fmt.Errorf("filtered sync run %s failed: %w", runID, err)
	}
	stored, err := control.Run(ctx, runID)
	if err != nil || stored.Status != model.SyncRunStatusSucceeded {
		return runID, errors.New("target run did not succeed")
	}
	return runID, nil
}
