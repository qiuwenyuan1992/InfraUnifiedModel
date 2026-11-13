package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
)

type syncRoundStage struct {
	Name string
	Run  func(context.Context, *viper.Viper, *bytes.Buffer) error
}
type syncRoundPhaseReport struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	RunID   string            `json:"run_id,omitempty"`
	Details []json.RawMessage `json:"details,omitempty"`
}
type syncRoundReport struct {
	Status     string                 `json:"status"`
	SourceID   string                 `json:"source_id"`
	SnapshotAt time.Time              `json:"snapshot_at"`
	Phases     []syncRoundPhaseReport `json:"phases,omitempty"`
	Note       string                 `json:"note,omitempty"`
}

func syncRoundPhaseNames() []string {
	return model.SyncRoundPhaseNames()
}

func validRoundSpatialModes(round, syncSpatial, verifySpatial bool, others []bool, source, entity, run string) bool {
	count := 0
	for _, on := range []bool{round, syncSpatial, verifySpatial} {
		if on {
			count++
		}
	}
	if count == 0 {
		return true
	}
	if count != 1 || source == "" || entity != "" || (round || syncSpatial) && run != "" {
		return false
	}
	for _, on := range others {
		if on {
			return false
		}
	}
	return true
}

func cloneRoundConfig(conf *viper.Viper) (*viper.Viper, error) {
	if conf == nil {
		return nil, errors.New("round configuration required")
	}
	data, err := json.Marshal(conf.AllSettings())
	if err != nil {
		return nil, errors.New("round configuration cannot be isolated")
	}
	settings := map[string]any{}
	if err = json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	copy := viper.New()
	if err = copy.MergeConfigMap(settings); err != nil {
		return nil, err
	}
	return copy, nil
}

func executeSyncRound(ctx context.Context, conf *viper.Viper, source string, at time.Time, stages []syncRoundStage, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !inventoryID.MatchString(source) || at.IsZero() || conf == nil || out == nil || len(stages) == 0 {
		return errors.New("round requires source, snapshot, configuration and stages")
	}
	at = at.UTC().Truncate(time.Microsecond)
	report := syncRoundReport{Status: "running", SourceID: source, SnapshotAt: at, Note: "shared round timestamp is not an atomic source snapshot; no automatic cleanup or rollback"}
	for _, stage := range stages {
		report.Phases = append(report.Phases, syncRoundPhaseReport{Name: stage.Name, Status: "not_run"})
	}
	encoder := json.NewEncoder(out)
	if err := encoder.Encode(map[string]any{"status": "running", "source_id": source, "snapshot_at": at}); err != nil {
		return err
	}
	ctx = service.WithSyncSnapshot(ctx, at)
	for i, stage := range stages {
		phase := &report.Phases[i]
		phase.Status = "running"
		if err := encoder.Encode(map[string]any{"status": "running", "phase": stage.Name, "source_id": source, "snapshot_at": at}); err != nil {
			return err
		}
		copy, err := cloneRoundConfig(conf)
		var detail bytes.Buffer
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			if stage.Run == nil {
				err = errors.New("round stage missing")
			} else {
				err = stage.Run(ctx, copy, &detail)
			}
		}
		decoder := json.NewDecoder(&detail)
		for {
			var raw json.RawMessage
			decodeErr := decoder.Decode(&raw)
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				if err == nil {
					err = errors.New("round stage output invalid")
				}
				break
			}
			phase.Details = append(phase.Details, raw)
			var event struct {
				RunID string `json:"run_id"`
			}
			if json.Unmarshal(raw, &event) == nil && event.RunID != "" {
				phase.RunID = event.RunID
			}
		}
		if err != nil {
			if phase.RunID == "" {
				if match := regexp.MustCompile(`\brun ([0-9a-f]{32})\b`).FindStringSubmatch(err.Error()); len(match) == 2 {
					phase.RunID = match[1]
				}
			}
			phase.Status = "failed"
			report.Status = "failed"
			if writeErr := encoder.Encode(report); writeErr != nil {
				return writeErr
			}
			return fmt.Errorf("round stopped at %s; prior graph writes remain; inspect stage run and diagnostics", stage.Name)
		}
		phase.Status = "succeeded"
		if err := encoder.Encode(map[string]any{"status": "succeeded", "phase": stage.Name, "run_id": phase.RunID, "snapshot_at": at}); err != nil {
			return err
		}
	}
	report.Status = "PASS"
	return encoder.Encode(report)
}

func runSyncRound(ctx context.Context, conf *viper.Viper, logger *log.Logger, source string, out io.Writer) error {
	if !inventoryID.MatchString(source) || conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("round requires valid source and MySQL")
	}
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	if err != nil {
		return errors.New("control database configuration invalid")
	}
	defer db.Close()
	var ref, kind string
	var enabled bool
	if err = db.QueryRowContext(ctx, "SELECT config_ref, adapter_kind, enabled FROM sources WHERE id = ?", source).Scan(&ref, &kind, &enabled); err != nil || ref == "" || kind != "cmdb" || !enabled {
		return errors.New("round source unavailable")
	}
	if err = validateSyncRoundSelections(conf, ref); err != nil {
		return err
	}
	var pendingRounds int64
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sync_rounds WHERE source_id = ? AND status IN ('queued','running')", source).Scan(&pendingRounds); err != nil || pendingRounds != 0 {
		return errors.New("round source has an outstanding API round or cannot inspect it")
	}
	var active int64
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE source_id = ? AND status IN (\x27queued\x27,\x27running\x27,\x27validating\x27,\x27publishing\x27)", source).Scan(&active); err != nil || active != 0 {
		return errors.New("round source has outstanding tasks or cannot inspect them")
	}
	stages := []syncRoundStage{}
	for _, name := range syncRoundPhaseNames() {
		phase := name
		stages = append(stages, syncRoundStage{Name: phase, Run: func(c context.Context, v *viper.Viper, b *bytes.Buffer) error {
			return runSyncRoundStage(c, v, logger, source, phase, b)
		}})
	}
	return executeSyncRound(ctx, conf, source, serverUplinkNow(), stages, out)
}

func validateSyncRoundSelections(conf *viper.Viper, ref string) error {
	if ref == "" {
		return errors.New("round source configuration missing")
	}
	prefix := "inventory.cmdb.sources." + ref + "."
	idc := conf.GetInt(prefix + "device_condition.idc_id")
	for _, key := range []string{"room_condition.idc_id", "cabinet_condition.idc_id", "data_center_condition.inst_id", "interface_condition.idc_id", "transformer_condition.idc", "ups_group_condition.idc", "ups_condition.idc", "rpp_condition.idc"} {
		if idc <= 0 || conf.GetInt(prefix+key) != idc {
			return errors.New("round requires compatible positive IDC selections for every entity")
		}
	}
	return nil
}

func runSyncRoundStage(ctx context.Context, conf *viper.Viper, logger *log.Logger, source, phase string, out *bytes.Buffer) error {
	switch phase {
	case "power_relations":
		return runPowerSync(ctx, conf, logger, source, out)
	case "pod_membership":
		return runPODMembership(ctx, conf, logger, source, out)
	case "spatial_relations":
		return runSpatialSync(ctx, conf, logger, source, out)
	case "gpu_sync":
		return runGPUSync(ctx, conf, logger, source, out)
	case "server_uplinks":
		return runServerUplink(ctx, conf, logger, source, out)
	case "links_to":
		return runLinksTo(ctx, conf, logger, source, out)
	default:
		entity := model.EntityType(phase)
		id, err := syncFiltered(ctx, conf, logger, source, entity, out)
		if err != nil {
			return err
		}
		return verifyPersistence(ctx, conf, source, entity, id, out)
	}
}
