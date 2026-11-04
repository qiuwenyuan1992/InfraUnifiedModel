package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func validQueuedRoundModes(enabled, once bool, others []bool, source, entity, run, snapshot string) bool {
	if !enabled {
		return true
	}
	if source != "" || entity != "" || run != "" || snapshot != "" {
		return false
	}
	for _, on := range others {
		if on {
			return false
		}
	}
	_ = once
	return true
}

func roundStageResult(output *bytes.Buffer, phase, source string, at time.Time) (repository.PhaseResult, error) {
	result := repository.PhaseResult{Reasons: map[string]int{}}
	decoder := json.NewDecoder(output)
	verified := false
	// 旧独立验收DTO有大写字段；这里只规范内部结果键，不转换来源属性。
	var normalize func(map[string]any) map[string]any
	normalize = func(input map[string]any) map[string]any {
		normalized := map[string]any{}
		for key, value := range input {
			if nested, ok := value.(map[string]any); ok {
				value = normalize(nested)
			}
			normalized[strings.ReplaceAll(strings.ToLower(key), "_", "")] = value
		}
		return normalized
	}
	count := func(row map[string]any, key string) int64 {
		if n, ok := row[key].(float64); ok {
			return int64(n)
		}
		return 0
	}
	for {
		var raw map[string]any
		err := decoder.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, errors.New("round stage output invalid")
		}
		event := normalize(raw)
		if id, ok := event["runid"].(string); ok {
			result.RunID = id
		}
		if event["status"] == "succeeded" {
			values := event
			if nested, ok := event["result"].(map[string]any); ok {
				values = nested
			}
			result.Nodes = count(values, "nodes")
			result.Edges = count(values, "edges")
			result.Objects = count(values, "objects")
			result.Skipped = count(values, "skipped")
			result.MissingEndpoints = count(values, "missingendpoints")
			if result.Objects == 0 {
				result.Objects = count(values, "servers")
			}
			// 诊断码保持独立任务的原始拼写，不对诊断键做字段归一化。
			original := raw
			if nested, ok := raw["result"].(map[string]any); ok {
				original = nested
			}
			if reasons, ok := original["reasons"].(map[string]any); ok {
				for code, value := range reasons {
					if n, ok := value.(float64); ok {
						result.Reasons[code] = int(n)
					}
				}
			}
		}
		if event["status"] != "PASS" {
			continue
		}
		if verified || event["sourceid"] != source {
			return result, errors.New("round stage verification source mismatch")
		}
		run, ok := event["run"].(map[string]any)
		if !ok || run["status"] != "succeeded" {
			return result, errors.New("round stage run not verified")
		}
		checkpoint, ok := event["checkpoint"].(map[string]any)
		if !ok || checkpoint["complete"] != true {
			return result, errors.New("round stage checkpoint incomplete")
		}
		cursor, ok := checkpoint["cursor"].(map[string]any)
		if !ok {
			return result, errors.New("round stage checkpoint missing")
		}
		id, ok := cursor["runid"].(string)
		if !ok || !inventoryID.MatchString(id) || run["id"] != id || result.RunID != "" && result.RunID != id {
			return result, errors.New("round stage run identity mismatch")
		}
		snapshot, ok := cursor["snapshotat"].(string)
		if !ok {
			return result, errors.New("round stage snapshot missing")
		}
		parsed, err := time.Parse(time.RFC3339Nano, snapshot)
		if err != nil || !parsed.Equal(at) {
			return result, errors.New("round stage snapshot mismatch")
		}
		result.RunID = id
		total := count(cursor, "total")
		switch phase {
		case "power_relations", "pod_membership", "spatial_relations", "server_uplinks", "links_to":
			if result.Edges == 0 {
				result.Edges = total
			}
		default:
			if result.Nodes == 0 {
				result.Nodes = total
			}
		}
		verified = true
	}
	if !verified {
		return result, errors.New("round stage graph verification missing")
	}
	return result, nil
}

func newQueuedRoundWorker(conf *viper.Viper, logger *log.Logger) (*service.SyncRoundWorker, func(), error) {
	if conf.GetString("data.db.main.driver") != "mysql" {
		return nil, nil, errors.New("round queue requires MySQL")
	}
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, nil, errors.New("round control database unavailable")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, nil, errors.New("round control database unavailable")
	}
	control := repository.NewRepository(logger, db)
	stages := syncRoundPhaseNames()
	executor := func(ctx context.Context, source model.Source, phase string) (repository.PhaseResult, error) {
		configuration, err := cloneRoundConfig(conf)
		if err != nil {
			return repository.PhaseResult{}, err
		}
		if phase == stages[0] {
			if err = validateSyncRoundSelections(configuration, source.ConfigRef); err != nil {
				return repository.PhaseResult{}, err
			}
		}
		var output bytes.Buffer
		err = runSyncRoundStage(ctx, configuration, logger, source.ID, phase, &output)
		if err != nil {
			return repository.PhaseResult{}, err
		}
		return roundStageResult(&output, phase, source.ID, service.SyncSnapshot(ctx, time.Time{}))
	}
	return service.NewSyncRoundWorker(repository.NewInventoryRoundRepository(control), executor), func() { _ = pool.Close() }, nil
}
