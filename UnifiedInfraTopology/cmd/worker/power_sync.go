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
	"strconv"
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

const powerSyncResource = "power_relations"

func validPowerModes(once, verify, filtered, uplink, verifyUplink, links, verifyLinks, syncPower, verifyPower bool, source, entity, run string) bool {
	if !syncPower && !verifyPower {
		return true
	}
	return !(syncPower && verifyPower) && !once && !verify && !filtered && !uplink && !verifyUplink && !links && !verifyLinks && source != "" && entity == "" && (!syncPower || run == "")
}
func validatePowerSelection(conf *viper.Viper, prefix string) error {
	idc := conf.GetInt(prefix + "cabinet_condition.idc_id")
	if idc <= 0 {
		return errors.New("power sync requires cabinet IDC")
	}
	for _, kind := range []string{"rpp", "ups", "ups_group", "transformer"} {
		if conf.GetInt(prefix+kind+"_condition.idc") != idc {
			return errors.New("power sync entity IDC selections must match")
		}
	}
	return nil
}

type powerRunner interface {
	Run(context.Context, model.Source, time.Time) (service.PowerSyncResult, error)
}
type powerRunnerAdapter struct {
	runner powerRunner
	result service.PowerSyncResult
}

func (a *powerRunnerAdapter) Run(c context.Context, s model.Source, t time.Time) (service.ServerUplinkResult, error) {
	r, e := a.runner.Run(c, s, t)
	a.result = r
	return service.ServerUplinkResult{Edges: r.Edges, Servers: r.Objects, Skipped: r.Skipped, MissingEndpoints: r.MissingEndpoints}, e
}

type powerControlAdapter struct{ repository.InventoryRepository }

func (a powerControlAdapter) BeginCheckpoint(c context.Context, s, _, r string, t, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return a.InventoryRepository.BeginCheckpoint(c, s, powerSyncResource, r, t, n)
}
func (a powerControlAdapter) AdvanceCheckpoint(c context.Context, s, _, r string, cur repository.DeviceSyncCursor, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return a.InventoryRepository.AdvanceCheckpoint(c, s, powerSyncResource, r, cur, n)
}
func (a powerControlAdapter) CompleteCheckpointAndRun(c context.Context, s, _, r, status string, n time.Time) (*model.SyncRun, error) {
	return a.InventoryRepository.CompleteCheckpointAndRun(c, s, powerSyncResource, r, status, n)
}
func executePowerSync(c context.Context, control repository.InventoryRepository, runner powerRunner, run *model.SyncRun, s model.Source, h time.Duration, verify func(context.Context, time.Time, int64) error) (service.PowerSyncResult, error) {
	a := &powerRunnerAdapter{runner: runner}
	_, err := executeServerUplink(c, powerControlAdapter{control}, a, run, s, h, verify)
	return a.result, err
}

func runPowerSync(ctx context.Context, conf *viper.Viper, logger *log.Logger, source string, out io.Writer) error {
	if !inventoryID.MatchString(source) || conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("power sync requires valid source and MySQL")
	}
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return errors.New("power database unavailable")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var s model.Source
	if err := db.WithContext(ctx).Where("id = ?", source).First(&s).Error; err != nil || !s.Enabled || s.AdapterKind != "cmdb" {
		return errors.New("power source unavailable")
	}
	if err := validatePowerSelection(conf, "inventory.cmdb.sources."+s.ConfigRef+"."); err != nil {
		return err
	}
	collector := adapter.NewCMDB(conf)
	if err := collector.Validate(ctx, s); err != nil {
		return errors.New("power source config invalid")
	}
	cfg, err := graphclient.ParseConfig(conf)
	if err != nil || len(cfg.Hosts) == 0 || cfg.Space == "" {
		return errors.New("power graph unconfigured")
	}
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	if err != nil {
		return err
	}
	defer cleanup()
	client, err := graphclient.New(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return err
	}
	id := hex.EncodeToString(idBytes)
	hash := sha256.Sum256([]byte(source + ":power"))
	request := &model.SyncRun{ID: id, SourceID: source, Status: model.SyncRunStatusQueued, CreatedAt: serverUplinkNow(), IdempotencyKey: "cli-power-" + id, RequestHash: hex.EncodeToString(hash[:])}
	base := repository.NewRepository(logger, db)
	control := repository.NewInventoryRunRepository(base, id)
	var run *model.SyncRun
	var claimed *model.Source
	err = base.Transaction(ctx, func(c context.Context) error {
		if _, err := control.Enqueue(c, request); err != nil {
			return err
		}
		var err error
		run, claimed, err = control.ClaimNext(c, serverUplinkNow())
		return err
	})
	if err != nil {
		return errors.New("power enqueue/claim failed")
	}
	_ = json.NewEncoder(out).Encode(map[string]string{"status": "running", "run_id": id, "resource": powerSyncResource})
	result, err := executePowerSync(ctx, control, service.NewPowerSyncWorker(collector, graph), run, *claimed, serverUplinkHeartbeat, func(c context.Context, t time.Time, n int64) error {
		_, err := verifyPowerGraph(c, client, source, t, n)
		return err
	})
	if err != nil {
		return err
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"status": "succeeded", "run_id": id, "edges": result.Edges, "objects": result.Objects, "skipped": result.Skipped, "missing_endpoints": result.MissingEndpoints, "reasons": result.Reasons, "by_kind": result.ByKind})
	return verifyPowerSync(ctx, conf, source, id, out)
}

type powerGraphSample struct {
	FromType  model.EntityType   `json:"from_type"`
	FromCode  string             `json:"from_code"`
	ToType    model.EntityType   `json:"to_type"`
	ToCode    string             `json:"to_code"`
	Kind      model.RelationKind `json:"relation_kind"`
	PowerPath string             `json:"power_path,omitempty"`
}
type powerGraphReport struct {
	Total, Current, ValidEndpointCount int64
	ByKind                             map[string]int64
	Samples                            []powerGraphSample
}

func verifyPowerGraph(ctx context.Context, client graphclient.Client, source string, at time.Time, expected int64) (*powerGraphReport, error) {
	params := map[string]interface{}{"source_id": source}
	read := func(q string) ([][]string, error) {
		r, e := client.ExecuteParameter(ctx, q, params)
		if e = persistenceCheckGraphResult(r, e); e != nil {
			return nil, e
		}
		return persistenceTable(r)
	}
	rows, err := read("LOOKUP ON power_relation WHERE power_relation.source_id == $source_id YIELD src(edge) AS src | YIELD count(*) AS total;")
	if err != nil {
		return nil, err
	}
	if len(rows) != 2 || len(rows[1]) != 1 {
		return nil, errors.New("invalid power count")
	}
	total, err := strconv.ParseInt(rows[1][0], 10, 64)
	if err != nil {
		return nil, err
	}
	report := &powerGraphReport{Total: total, ByKind: map[string]int64{}, Samples: []powerGraphSample{}}
	if at.IsZero() {
		return report, nil
	}
	params["snapshot"] = at.UTC().Format("2006-01-02T15:04:05.000000+00:00")
	raw, err := read("LOOKUP ON power_relation WHERE power_relation.source_id == $source_id YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, power_relation.relation_id AS id, power_relation.relation_kind AS kind, power_relation.power_path AS power_path_value, toString(power_relation.created_at) AS created_at, toString(power_relation.synced_at) AS synced_at, power_relation.source_id AS source;")
	if err != nil {
		return report, err
	}
	all := map[string][]string{}
	current := map[string]bool{}
	for _, row := range raw[1:] {
		if len(row) != 9 {
			return report, errors.New("invalid power edge")
		}
		id := persistenceString(row[3])
		created, e1 := persistenceTime(row[6])
		synced, e2 := persistenceTime(row[7])
		if e1 != nil || e2 != nil || created.IsZero() || created.After(synced) || persistenceString(row[8]) != source || all[id] != nil {
			return report, errors.New("invalid power edge timestamps or identity")
		}
		all[id] = row
		if synced.Equal(at) {
			current[id] = true
		}
	}
	report.Current = int64(len(current))
	if report.Current != expected {
		return report, errors.New("power snapshot count mismatch")
	}
	matched := map[string]bool{}
	pairs := [][2]model.EntityType{{model.EntityCabinet, model.EntityRPP}, {model.EntityRPP, model.EntityUPSGroup}, {model.EntityUPS, model.EntityUPSGroup}, {model.EntityUPSGroup, model.EntityTransformer}, {model.EntityTransformer, model.EntityTransformer}}
	for _, pair := range pairs {
		a, b := string(pair[0]), string(pair[1])
		aIdentity, bIdentity := "inst_id", "inst_id"
		if pair[0] == model.EntityCabinet {
			aIdentity = "uuid"
		}
		if pair[1] == model.EntityCabinet {
			bIdentity = "uuid"
		}
		q := fmt.Sprintf("MATCH (a:%s)-[e:power_relation]->(b:%s) WHERE e.source_id == $source_id RETURN e.relation_id AS id, rank(e) AS rank, id(a) AS a_vid, id(b) AS b_vid, a.%s.%s AS a_identity, a.%s.source_id AS a_source, a.%s.code AS a_code, b.%s.%s AS b_identity, b.%s.source_id AS b_source, b.%s.code AS b_code;", a, b, a, aIdentity, a, a, b, bIdentity, b, b)
		paths, err := read(q)
		if err != nil {
			return report, err
		}
		for _, row := range paths[1:] {
			if len(row) != 10 {
				return report, errors.New("invalid power path")
			}
			id := persistenceString(row[0])
			edge := all[id]
			if edge == nil || matched[id] {
				return report, errors.New("power missing or multiple endpoint path")
			}
			kind := model.RelationKind(persistenceString(edge[4]))
			path := persistenceString(edge[5])
			if path == "__NULL__" || path == "null" {
				path = ""
			}
			identity := model.RelationIdentity{SourceID: source, EdgeType: model.EdgePower, Kind: kind, From: model.EntityIdentity{SourceID: source, EntityType: pair[0], StableID: persistenceString(row[4])}, To: model.EntityIdentity{SourceID: source, EntityType: pair[1], StableID: persistenceString(row[7])}}
			relation := model.Relation{Identity: identity, CreatedAt: at, SyncedAt: at}
			if path != "" {
				relation.Properties = map[string]any{"power_path": path}
			}
			normalized, err := model.NormalizeRelation(relation)
			if err != nil {
				return report, err
			}
			actualID, err := normalized.Identity.ID()
			if err != nil {
				return report, err
			}
			rank, err := normalized.Identity.Rank()
			if err != nil {
				return report, err
			}
			rankValue, err := strconv.ParseInt(edge[2], 10, 64)
			if err != nil {
				return report, err
			}
			av, _ := identity.From.VID()
			bv, _ := identity.To.VID()
			if actualID != id || rankValue != rank || row[1] != edge[2] || av != persistenceString(row[2]) || bv != persistenceString(row[3]) || av != persistenceString(edge[0]) || bv != persistenceString(edge[1]) || persistenceString(row[5]) != source || persistenceString(row[8]) != source {
				return report, errors.New("power endpoint identity/rank/source mismatch")
			}
			matched[id] = true
			if current[id] {
				report.ValidEndpointCount++
				report.ByKind[string(kind)]++
				if len(report.Samples) < 5 {
					report.Samples = append(report.Samples, powerGraphSample{FromType: pair[0], FromCode: persistenceString(row[6]), ToType: pair[1], ToCode: persistenceString(row[9]), Kind: kind, PowerPath: path})
				}
			}
		}
	}
	if len(matched) != len(all) || report.ValidEndpointCount != expected {
		return report, errors.New("power edges have missing or invalid endpoints")
	}
	return report, nil
}

func validatePowerSyncCheckpointAt(source string, run *model.SyncRun, cp *repository.DeviceSyncCheckpoint, expected time.Time) error {
	if run == nil || cp == nil || !cp.Complete || run.SourceID != source || run.Status != model.SyncRunStatusSucceeded || run.StartedAt == nil || run.FinishedAt == nil || run.LeaseExpiresAt != nil || cp.Cursor.RunID != run.ID || !persistenceSnapshotMatches(run.StartedAt, cp.Cursor.SnapshotAt, expected) || cp.Cursor.NextOffset != cp.Cursor.Total {
		return errors.New("power task/checkpoint mismatch")
	}
	return nil
}

func verifyPowerSync(ctx context.Context, conf *viper.Viper, source, runID string, out io.Writer) error {
	if !inventoryID.MatchString(source) || (runID != "" && !inventoryID.MatchString(runID)) || conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("power verification requires valid source/run/MySQL")
	}
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var s model.Source
	if err := db.WithContext(ctx).Where("id = ?", source).First(&s).Error; err != nil {
		return errors.New("power source missing")
	}
	at := time.Time{}
	expected := int64(0)
	control := repository.NewInventoryRepository(repository.NewRepository(nil, db))
	var run *model.SyncRun
	var cp *repository.DeviceSyncCheckpoint
	if runID != "" {
		run, err = control.Run(ctx, runID)
		if err != nil {
			return err
		}
		cp, err = control.LoadCheckpoint(ctx, source, powerSyncResource)
		if err != nil {
			return err
		}
		if err := validatePowerSyncCheckpointAt(source, run, cp, service.SyncSnapshot(ctx, time.Time{})); err != nil {
			return err
		}
		at = cp.Cursor.SnapshotAt
		expected = int64(cp.Cursor.Total)
	}
	cfg, err := graphclient.ParseConfig(conf)
	if err != nil {
		return err
	}
	client, err := graphclient.New(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	report, err := verifyPowerGraph(ctx, client, source, at, expected)
	if err != nil {
		return err
	}
	status := "preflight"
	if runID != "" {
		status = "PASS"
	}
	return json.NewEncoder(out).Encode(map[string]any{"status": status, "source_id": source, "run": run, "checkpoint": cp, "graph": report, "topology_present": report.Current > 0})
}
