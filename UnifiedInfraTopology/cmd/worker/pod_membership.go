package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
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

const podMembershipResource = "pod_membership"

type podMembershipRunner interface {
	Run(context.Context, model.Source, time.Time) (service.PODMembershipResult, error)
}
type podMembershipRunnerAdapter struct {
	runner podMembershipRunner
	result service.PODMembershipResult
}

func (a *podMembershipRunnerAdapter) Run(c context.Context, s model.Source, t time.Time) (service.ServerUplinkResult, error) {
	r, e := a.runner.Run(c, s, t)
	a.result = r
	return service.ServerUplinkResult{Edges: r.Edges, Servers: r.Objects, Skipped: r.Skipped, MissingEndpoints: r.MissingEndpoints}, e
}

// 两种独立任务共用已有执行器，只映射检查点资源与计量。
type standaloneRelationControl struct {
	repository.InventoryRepository
	resource string
}

func (a standaloneRelationControl) BeginCheckpoint(c context.Context, s, _, r string, t, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return a.InventoryRepository.BeginCheckpoint(c, s, a.resource, r, t, n)
}
func (a standaloneRelationControl) AdvanceCheckpoint(c context.Context, s, _, r string, cur repository.DeviceSyncCursor, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return a.InventoryRepository.AdvanceCheckpoint(c, s, a.resource, r, cur, n)
}
func (a standaloneRelationControl) CompleteCheckpointAndRun(c context.Context, s, _, r, status string, n time.Time) (*model.SyncRun, error) {
	return a.InventoryRepository.CompleteCheckpointAndRun(c, s, a.resource, r, status, n)
}
func executePODMembership(c context.Context, control repository.InventoryRepository, runner podMembershipRunner, run *model.SyncRun, s model.Source, h time.Duration, verify func(context.Context, time.Time, int64) error) (service.PODMembershipResult, error) {
	a := &podMembershipRunnerAdapter{runner: runner}
	_, err := executeServerUplink(c, standaloneRelationControl{control, podMembershipResource}, a, run, s, h, verify)
	return a.result, err
}

func validStandaloneTopologyModes(pod, vpod, gpu, vgpu bool, other []bool, source, entity, run string) bool {
	n := 0
	for _, on := range []bool{pod, vpod, gpu, vgpu} {
		if on {
			n++
		}
	}
	if n == 0 {
		return true
	}
	if n != 1 {
		return false
	}
	for _, on := range other {
		if on {
			return false
		}
	}
	return source != "" && entity == "" && (!(pod || gpu) || run == "")
}

func runPODMembership(ctx context.Context, conf *viper.Viper, logger *log.Logger, source string, out io.Writer) error {
	return runStandaloneRelation(ctx, conf, logger, source, podMembershipResource, out, func(collector *adapter.CMDB, graph repository.TopologyGraphRepository) serverUplinkRunner {
		return &podMembershipRunnerAdapter{runner: service.NewPODMembershipWorker(collector, graph)}
	}, func(c context.Context, client graphclient.Client, t time.Time, n int64) error {
		_, e := verifyPODMembershipGraph(c, client, source, t, n)
		return e
	}, func(a serverUplinkRunner) any {
		r := a.(*podMembershipRunnerAdapter).result
		return map[string]any{"edges": r.Edges, "objects": r.Objects, "skipped": r.Skipped, "missing_endpoints": r.MissingEndpoints, "reasons": r.Reasons}
	}, verifyPODMembership)
}

// 图连接必须在原子入队/领取前建立，不能留下无法执行的活动任务。
func runStandaloneRelation(ctx context.Context, conf *viper.Viper, logger *log.Logger, source, resource string, out io.Writer, newRunner func(*adapter.CMDB, repository.TopologyGraphRepository) serverUplinkRunner, validate func(context.Context, graphclient.Client, time.Time, int64) error, report func(serverUplinkRunner) any, verify func(context.Context, *viper.Viper, string, string, io.Writer) error) error {
	if !inventoryID.MatchString(source) || conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("standalone topology sync requires valid source and MySQL")
	}
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return errors.New("control database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New("control database unavailable")
	}
	defer sqlDB.Close()
	var s model.Source
	if err := db.WithContext(ctx).Where("id = ?", source).First(&s).Error; err != nil || !s.Enabled || s.AdapterKind != "cmdb" || s.ConfigRef == "" {
		return errors.New("source must be enabled and use CMDB")
	}
	prefix := "inventory.cmdb.sources." + s.ConfigRef + "."
	if conf.GetInt(prefix+"device_condition.idc_id") <= 0 {
		return errors.New("topology sync requires a positive device IDC selection")
	}
	conf.Set(prefix+"page_size", 200)
	collector := adapter.NewCMDB(conf)
	if err := collector.Validate(ctx, s); err != nil {
		return errors.New("source configuration invalid")
	}
	cfg, err := graphclient.ParseConfig(conf)
	if err != nil || len(cfg.Hosts) == 0 || cfg.Space == "" {
		return errors.New("graph must be configured before enqueue")
	}
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	if err != nil {
		return errors.New("graph connection failed")
	}
	defer cleanup()
	client, err := graphclient.New(cfg)
	if err != nil {
		return errors.New("graph verification connection failed")
	}
	defer client.Close()
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return errors.New("run ID generation failed")
	}
	id := hex.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(source + ":" + resource))
	request := &model.SyncRun{ID: id, SourceID: source, Status: model.SyncRunStatusQueued, CreatedAt: serverUplinkNow(), IdempotencyKey: "cli-" + resource + "-" + id, RequestHash: hex.EncodeToString(hash[:])}
	base := repository.NewRepository(logger, db)
	control := repository.NewInventoryRunRepository(base, id)
	var run *model.SyncRun
	var claimed *model.Source
	if err := base.Transaction(ctx, func(c context.Context) error {
		if _, err := control.Enqueue(c, request); err != nil {
			return err
		}
		var err error
		run, claimed, err = control.ClaimNext(c, serverUplinkNow())
		return err
	}); err != nil {
		return errors.New("standalone topology atomic enqueue and claim failed")
	}
	if run == nil || claimed == nil {
		return errors.New("standalone topology task claim invalid")
	}
	runner := newRunner(collector, graph)
	_, err = executeServerUplink(ctx, standaloneRelationControl{control, resource}, runner, run, *claimed, serverUplinkHeartbeat, func(c context.Context, t time.Time, n int64) error { return validate(c, client, t, n) })
	if err != nil {
		return fmt.Errorf("%s run %s failed; partial graph writes are not rolled back: %w", resource, id, err)
	}
	if err := json.NewEncoder(out).Encode(map[string]any{"status": "succeeded", "run_id": id, "source_id": source, "resource": resource, "result": report(runner)}); err != nil {
		return err
	}
	return verify(ctx, conf, source, id, out)
}

type podMembershipSample struct {
	DeviceSN   string `json:"device_sn"`
	PODUUID    string `json:"pod_uuid"`
	Plane      int64  `json:"plane"`
	RelationID string `json:"relation_id"`
	Rank       int64  `json:"rank"`
}
type podMembershipGraphReport struct {
	Total              int64                 `json:"total_edges"`
	Current            int64                 `json:"current_snapshot"`
	ValidEndpointCount int64                 `json:"valid_endpoint_count"`
	Samples            []podMembershipSample `json:"samples"`
}

func standaloneGraphReader(ctx context.Context, client graphclient.Client, source string, at time.Time) func(string) ([][]string, error) {
	params := map[string]interface{}{"source_id": source, "snapshot": at.UTC().Format("2006-01-02T15:04:05.000000+00:00")}
	return func(q string) ([][]string, error) {
		r, e := client.ExecuteParameter(ctx, q, params)
		if e = persistenceCheckGraphResult(r, e); e != nil {
			return nil, e
		}
		return persistenceTable(r)
	}
}
func standaloneGraphCount(rows [][]string) (int64, error) {
	if len(rows) != 2 || len(rows[1]) != 1 {
		return 0, errors.New("invalid topology count")
	}
	n, e := strconv.ParseInt(rows[1][0], 10, 64)
	if e != nil || n < 0 {
		return 0, errors.New("invalid topology count")
	}
	return n, nil
}
func verifyPODMembershipGraph(ctx context.Context, client graphclient.Client, source string, at time.Time, expected int64) (*podMembershipGraphReport, error) {
	read := standaloneGraphReader(ctx, client, source, at)
	scope := "LOOKUP ON spatial_relation WHERE spatial_relation.source_id == $source_id AND spatial_relation.relation_kind == \"member_of\""
	rows, e := read(scope + " YIELD src(edge) AS src | YIELD count(*) AS total;")
	if e != nil {
		return nil, e
	}
	total, e := standaloneGraphCount(rows)
	if e != nil {
		return nil, e
	}
	r := &podMembershipGraphReport{Total: total, Samples: []podMembershipSample{}}
	if at.IsZero() {
		return r, nil
	}
	raw, e := read(scope + " AND spatial_relation.synced_at == datetime($snapshot) YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, spatial_relation.relation_id AS relation_id, spatial_relation.plane AS plane, toString(spatial_relation.created_at) AS created_at, toString(spatial_relation.synced_at) AS synced_at, spatial_relation.source_id AS source;")
	if e != nil {
		return r, e
	}
	seen := map[string][]string{}
	for _, row := range raw[1:] {
		if len(row) != 8 {
			return r, errors.New("invalid POD membership edge")
		}
		id := persistenceString(row[3])
		created, e1 := persistenceTime(row[5])
		synced, e2 := persistenceTime(row[6])
		plane, e3 := strconv.ParseInt(row[4], 10, 64)
		if id == "" || seen[id] != nil || e1 != nil || e2 != nil || e3 != nil || plane <= 0 || created.IsZero() || created.After(synced) || !synced.Equal(at) || persistenceString(row[7]) != source {
			return r, errors.New("invalid POD membership edge identity, plane, or timestamp")
		}
		seen[id] = row
	}
	r.Current = int64(len(seen))
	if r.Current != expected || r.Total < r.Current {
		return r, errors.New("POD membership checkpoint count mismatch")
	}
	paths, e := read("MATCH (d:device)-[e:spatial_relation]->(p:pod) WHERE e.source_id == $source_id AND e.relation_kind == \"member_of\" AND e.synced_at == datetime($snapshot) RETURN e.relation_id AS relation_id, rank(e) AS rank, id(d) AS device_vid, id(p) AS pod_vid, d.device.device_sn AS device_sn, d.device.source_id AS device_source, p.pod.uuid AS pod_uuid, p.pod.source_id AS pod_source, p.pod.plane AS pod_plane;")
	if e != nil {
		return r, e
	}
	matched := map[string]bool{}
	for _, row := range paths[1:] {
		if len(row) != 9 {
			return r, errors.New("invalid POD membership path")
		}
		id := persistenceString(row[0])
		edge := seen[id]
		plane, e := strconv.ParseInt(row[8], 10, 64)
		if edge == nil || matched[id] || e != nil || plane <= 0 || persistenceString(row[5]) != source || persistenceString(row[7]) != source {
			return r, errors.New("POD membership missing, ambiguous, or invalid endpoint")
		}
		relation, e := model.NormalizeRelation(model.Relation{Identity: model.RelationIdentity{SourceID: source, EdgeType: model.EdgeSpatial, Kind: model.RelationMemberOf, From: model.EntityIdentity{SourceID: source, EntityType: model.EntityDevice, StableID: persistenceString(row[4])}, To: model.EntityIdentity{SourceID: source, EntityType: model.EntityPod, StableID: persistenceString(row[6])}}, Properties: map[string]any{"plane": plane}, CreatedAt: at, SyncedAt: at})
		if e != nil {
			return r, e
		}
		actual, e := relation.Identity.ID()
		rank, e2 := relation.Identity.Rank()
		from, e3 := relation.Identity.From.VID()
		to, e4 := relation.Identity.To.VID()
		if e != nil || e2 != nil || e3 != nil || e4 != nil || actual != id || row[1] != strconv.FormatInt(rank, 10) || edge[2] != row[1] || persistenceString(edge[0]) != from || persistenceString(edge[1]) != to || persistenceString(row[2]) != from || persistenceString(row[3]) != to || edge[4] != strconv.FormatInt(plane, 10) {
			return r, errors.New("POD membership identity, rank, VID, or actual POD plane mismatch")
		}
		matched[id] = true
		if len(r.Samples) < 5 {
			r.Samples = append(r.Samples, podMembershipSample{DeviceSN: persistenceString(row[4]), PODUUID: persistenceString(row[6]), Plane: plane, RelationID: id, Rank: rank})
		}
	}
	r.ValidEndpointCount = int64(len(matched))
	if len(matched) != len(seen) {
		return r, errors.New("POD membership edge lacks a unique typed path")
	}
	return r, nil
}

func verifyPODMembership(ctx context.Context, conf *viper.Viper, source, run string, out io.Writer) error {
	return verifyStandaloneRelation(ctx, conf, source, run, podMembershipResource, out, func(c context.Context, client graphclient.Client, t time.Time, n int64) (any, int64, int64, error) {
		r, e := verifyPODMembershipGraph(c, client, source, t, n)
		if e != nil {
			return r, 0, 0, e
		}
		return r, r.Current, r.ValidEndpointCount, nil
	})
}

type standaloneRelationPersistenceReport struct {
	Status          string                 `json:"status"`
	SourceID        string                 `json:"source_id"`
	ConfigRef       string                 `json:"config_ref"`
	Resource        string                 `json:"resource"`
	ActiveRuns      int64                  `json:"active_runs_global"`
	Run             *persistenceRun        `json:"run,omitempty"`
	Checkpoint      *persistenceCheckpoint `json:"checkpoint,omitempty"`
	Graph           any                    `json:"graph"`
	TopologyPresent bool                   `json:"topology_present"`
	Note            string                 `json:"note,omitempty"`
}

func verifyStandaloneRelation(ctx context.Context, conf *viper.Viper, source, run, resource string, out io.Writer, graphVerify func(context.Context, graphclient.Client, time.Time, int64) (any, int64, int64, error)) error {
	if conf.GetString("data.db.main.driver") != "mysql" || !inventoryID.MatchString(source) || (run != "" && !inventoryID.MatchString(run)) {
		return errors.New("topology verification requires MySQL and valid source/run IDs")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	db, e := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	if e != nil {
		return errors.New("control database configuration invalid")
	}
	defer db.Close()
	if e := db.PingContext(ctx); e != nil {
		return errors.New("control database connection failed")
	}
	r := &standaloneRelationPersistenceReport{Status: "preflight", SourceID: source, Resource: resource}
	var enabled bool
	var kind string
	if e := db.QueryRowContext(ctx, "SELECT config_ref, enabled, adapter_kind FROM sources WHERE id = ?", source).Scan(&r.ConfigRef, &enabled, &kind); e != nil || !enabled || kind != "cmdb" || r.ConfigRef == "" {
		return errors.New("verification source unavailable")
	}
	if conf.GetInt("inventory.cmdb.sources."+r.ConfigRef+".device_condition.idc_id") <= 0 {
		return errors.New("topology verification requires positive device IDC selection")
	}
	if e := db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE status IN ('queued','running','validating','publishing')").Scan(&r.ActiveRuns); e != nil {
		return errors.New("active run query failed")
	}
	var at time.Time
	var expected int64
	if run != "" {
		r.Run = &persistenceRun{}
		if e := db.QueryRowContext(ctx, "SELECT id, source_id, status, started_at, finished_at, lease_expires_at FROM sync_runs WHERE id = ?", run).Scan(&r.Run.ID, &r.Run.SourceID, &r.Run.Status, &r.Run.StartedAt, &r.Run.FinishedAt, &r.Run.LeaseExpiresAt); e != nil {
			return errors.New("verification run not found")
		}
		r.Checkpoint = &persistenceCheckpoint{}
		var cursor string
		if e := db.QueryRowContext(ctx, "SELECT complete, completed_at, `cursor` FROM sync_checkpoints WHERE source_id = ? AND resource = ?", source, resource).Scan(&r.Checkpoint.Complete, &r.Checkpoint.CompletedAt, &cursor); e != nil {
			return errors.New("verification checkpoint not found")
		}
		if e := json.Unmarshal([]byte(cursor), &r.Checkpoint.Cursor); e != nil {
			return errors.New("verification cursor invalid")
		}
		at, expected = r.Checkpoint.Cursor.SnapshotAt, r.Checkpoint.Cursor.Total
		if at.IsZero() || expected < 0 {
			return errors.New("verification snapshot invalid")
		}
	}
	cfg, e := graphclient.ParseConfig(conf)
	if e != nil {
		return errors.New("graph configuration invalid")
	}
	client, e := graphclient.New(cfg)
	if e != nil {
		return errors.New("graph connection failed")
	}
	defer client.Close()
	var current, valid int64
	r.Graph, current, valid, e = graphVerify(ctx, client, at, expected)
	if e != nil {
		return e
	}
	if run != "" {
		if e := validateStandaloneRelationPersistenceAt(r, current, valid, service.SyncSnapshot(ctx, time.Time{})); e != nil {
			return e
		}
	}
	return json.NewEncoder(out).Encode(r)
}
func validateStandaloneRelationPersistence(r *standaloneRelationPersistenceReport, current, valid int64) error {
	return validateStandaloneRelationPersistenceAt(r, current, valid, time.Time{})
}

func validateStandaloneRelationPersistenceAt(r *standaloneRelationPersistenceReport, current, valid int64, expected time.Time) error {
	if r.Run == nil || r.Checkpoint == nil || r.Graph == nil {
		return errors.New("persisted topology run, checkpoint, or graph missing")
	}
	run, cp := r.Run, r.Checkpoint
	c := cp.Cursor
	if run.SourceID != r.SourceID || run.Status != model.SyncRunStatusSucceeded || run.StartedAt == nil || run.FinishedAt == nil || run.LeaseExpiresAt != nil || run.FinishedAt.Before(*run.StartedAt) || !cp.Complete || cp.CompletedAt == nil || cp.CompletedAt.Before(*run.StartedAt) || !cp.CompletedAt.Equal(*run.FinishedAt) || c.Version != 1 || c.RunID != run.ID || !persistenceSnapshotMatches(run.StartedAt, c.SnapshotAt, expected) || c.Total < 0 || c.NextOffset != c.Total || current != c.Total || valid != c.Total {
		return errors.New("persisted topology run, checkpoint, or graph snapshot mismatch")
	}
	r.Status = "PASS"
	r.TopologyPresent = current > 0
	if !r.TopologyPresent {
		r.Note = "verified empty snapshot; no topology is claimed"
	}
	return nil
}
