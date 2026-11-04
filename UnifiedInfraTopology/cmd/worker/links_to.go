package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const linksToResource = "links_to"

func validLinksToModes(once, verify, sync, serverSync, serverVerify, linksSync, linksVerify bool, source, entity, run string) bool {
	if !linksSync && !linksVerify {
		return true
	}
	return !(linksSync && linksVerify) && !once && !verify && !sync && !serverSync && !serverVerify && source != "" && entity == "" && (!linksSync || run == "")
}

type linksToRunner interface {
	Run(context.Context, model.Source, time.Time) (service.LinksToResult, error)
}
type linksToRunnerAdapter struct {
	runner linksToRunner
	result service.LinksToResult
}

func (a *linksToRunnerAdapter) Run(ctx context.Context, s model.Source, at time.Time) (service.ServerUplinkResult, error) {
	r, err := a.runner.Run(ctx, s, at)
	a.result = r
	return service.ServerUplinkResult{Edges: r.Edges, Servers: r.Devices, Skipped: r.Skipped, MissingEndpoints: r.MissingEndpoints}, err
}

type linksToControlAdapter struct{ repository.InventoryRepository }

func (a linksToControlAdapter) BeginCheckpoint(c context.Context, s, _, r string, t, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return a.InventoryRepository.BeginCheckpoint(c, s, linksToResource, r, t, n)
}
func (a linksToControlAdapter) AdvanceCheckpoint(c context.Context, s, _, r string, cur repository.DeviceSyncCursor, n time.Time) (*repository.DeviceSyncCheckpoint, error) {
	return a.InventoryRepository.AdvanceCheckpoint(c, s, linksToResource, r, cur, n)
}
func (a linksToControlAdapter) CompleteCheckpointAndRun(c context.Context, s, _, r, status string, n time.Time) (*model.SyncRun, error) {
	return a.InventoryRepository.CompleteCheckpointAndRun(c, s, linksToResource, r, status, n)
}
func executeLinksTo(c context.Context, control repository.InventoryRepository, runner linksToRunner, run *model.SyncRun, s model.Source, heartbeat time.Duration, verify func(context.Context, time.Time, int64) error) (service.LinksToResult, error) {
	a := &linksToRunnerAdapter{runner: runner}
	_, err := executeServerUplink(c, linksToControlAdapter{control}, a, run, s, heartbeat, verify)
	return a.result, err
}

func runLinksTo(ctx context.Context, conf *viper.Viper, logger *log.Logger, sourceID string, out io.Writer) error {
	if !inventoryID.MatchString(sourceID) || conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("links_to requires valid source and MySQL")
	}
	db, err := gorm.Open(mysql.Open(conf.GetString("data.db.main.dsn")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		return errors.New("links_to control database unavailable")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var source model.Source
	if err := db.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error; err != nil {
		return errors.New("links_to source not found")
	}
	if !source.Enabled || source.AdapterKind != "cmdb" || source.ConfigRef == "" {
		return errors.New("links_to source invalid")
	}
	prefix := "inventory.cmdb.sources." + source.ConfigRef + "."
	if conf.GetInt(prefix+"interface_condition.idc_id") <= 0 {
		return errors.New("links_to requires source IDC selection")
	}
	pageSize := conf.GetInt(prefix + "page_size")
	if pageSize <= 0 {
		pageSize = 2000
	}
	conf.Set(prefix+"page_size", min(pageSize, 2000))
	collector := adapter.NewCMDB(conf)
	if collector.Validate(ctx, source) != nil {
		return errors.New("links_to source config invalid")
	}
	gcfg, err := graphclient.ParseConfig(conf)
	if err != nil || len(gcfg.Hosts) == 0 || gcfg.Space == "" {
		return errors.New("links_to graph not configured")
	}
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	if err != nil {
		return errors.New("links_to graph unavailable")
	}
	defer cleanup()
	client, err := graphclient.New(gcfg)
	if err != nil {
		return err
	}
	defer client.Close()
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return err
	}
	runID := hex.EncodeToString(idBytes)
	hash := sha256.Sum256([]byte(sourceID + ":links_to:physics"))
	request := &model.SyncRun{ID: runID, SourceID: sourceID, Status: model.SyncRunStatusQueued, CreatedAt: serverUplinkNow(), IdempotencyKey: "cli-links-to-" + runID, RequestHash: hex.EncodeToString(hash[:])}
	base := repository.NewRepository(logger, db)
	control := repository.NewInventoryRunRepository(base, runID)
	var run *model.SyncRun
	var claimedSource *model.Source
	if err := base.Transaction(ctx, func(tx context.Context) error {
		if _, err := control.Enqueue(tx, request); err != nil {
			return err
		}
		var err error
		run, claimedSource, err = control.ClaimNext(tx, serverUplinkNow())
		return err
	}); err != nil {
		return errors.New("links_to enqueue/claim failed")
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"status": "running", "run_id": runID, "source_id": sourceID, "relation_kind": "links_to"})
	result, err := executeLinksTo(ctx, control, service.NewLinksToWorker(collector, graph), run, *claimedSource, serverUplinkHeartbeat, func(c context.Context, at time.Time, n int64) error {
		_, err := verifyLinksToGraph(c, client, sourceID, at, n)
		return err
	})
	if err != nil {
		return err
	}
	_ = json.NewEncoder(out).Encode(map[string]any{"status": "succeeded", "run_id": runID, "devices": result.Devices, "edges": result.Edges, "skipped": result.Skipped, "missing_endpoints": result.MissingEndpoints, "reasons": result.Reasons})
	return verifyLinksTo(ctx, conf, sourceID, runID, out)
}

type linksToSample struct {
	FromDeviceSN string `json:"from_device_sn"`
	FromPortUUID string `json:"from_port_uuid"`
	ToDeviceSN   string `json:"to_device_sn"`
	ToPortUUID   string `json:"to_port_uuid"`
	RelationID   string `json:"relation_id"`
	Rank         int64  `json:"rank"`
}
type linksToGraphReport struct {
	Total              int64           `json:"total"`
	Current            int64           `json:"current_snapshot"`
	ValidEndpointCount int64           `json:"valid_endpoint_count"`
	Samples            []linksToSample `json:"samples"`
}

func verifyLinksToGraph(ctx context.Context, client graphclient.Client, source string, at time.Time, expected int64) (*linksToGraphReport, error) {
	params := map[string]interface{}{"source_id": source, "relation_kind": "links_to"}
	read := func(q string) ([][]string, error) {
		r, err := client.ExecuteParameter(ctx, q, params)
		if err = persistenceCheckGraphResult(r, err); err != nil {
			return nil, err
		}
		return persistenceTable(r)
	}
	rows, err := read("LOOKUP ON network_relation WHERE network_relation.source_id == $source_id AND network_relation.relation_kind == $relation_kind YIELD src(edge) AS src | YIELD count(*) AS total;")
	if err != nil {
		return nil, err
	}
	if len(rows) != 2 || len(rows[1]) != 1 {
		return nil, errors.New("invalid links_to total")
	}
	total, err := strconv.ParseInt(rows[1][0], 10, 64)
	if err != nil {
		return nil, err
	}
	report := &linksToGraphReport{Total: total, Samples: []linksToSample{}}
	if at.IsZero() {
		return report, nil
	}
	params["snapshot"] = at.UTC().Format("2006-01-02T15:04:05.000000+00:00")
	raw, err := read("LOOKUP ON network_relation WHERE network_relation.source_id == $source_id AND network_relation.relation_kind == $relation_kind AND network_relation.synced_at == datetime($snapshot) YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, network_relation.relation_id AS relation_id, toString(network_relation.created_at) AS created_at, toString(network_relation.synced_at) AS synced_at;")
	if err != nil {
		return report, err
	}
	report.Current = int64(len(raw) - 1)
	if report.Current != expected {
		return report, errors.New("links_to snapshot count mismatch")
	}
	seen := map[string][]string{}
	for _, row := range raw[1:] {
		if len(row) != 6 {
			return report, errors.New("invalid links_to edge")
		}
		created, e1 := persistenceTime(row[4])
		synced, e2 := persistenceTime(row[5])
		id := persistenceString(row[3])
		rank, e3 := strconv.ParseInt(row[2], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || created.IsZero() || created.After(synced) || !synced.Equal(at) || rank <= 0 || seen[id] != nil {
			return report, errors.New("invalid links_to edge time or identity")
		}
		seen[id] = row
	}
	q := `MATCH (d1:device)-[o1:composition_relation]->(p1:interface)-[l:network_relation]->(p2:interface)<-[o2:composition_relation]-(d2:device) WHERE p1.interface.source_id == $source_id AND p2.interface.source_id == $source_id AND o1.source_id == $source_id AND o2.source_id == $source_id AND o1.relation_kind == "owns_interface" AND o2.relation_kind == "owns_interface" AND l.source_id == $source_id AND l.relation_kind == $relation_kind AND l.synced_at == datetime($snapshot) RETURN d1.device.device_sn AS from_sn, d1.device.parent_type_id AS from_type, d1.device.source_id AS from_source, p1.interface.port_uuid AS from_port, p1.interface.port_type AS from_port_type, d2.device.device_sn AS to_sn, d2.device.parent_type_id AS to_type, d2.device.source_id AS to_source, p2.interface.port_uuid AS to_port, p2.interface.port_type AS to_port_type, l.relation_id AS relation_id, rank(l) AS rank, toString(l.created_at) AS created_at, toString(l.synced_at) AS synced_at, id(p1) AS from_vid, id(p2) AS to_vid, id(d1) AS from_device_vid, id(d2) AS to_device_vid;`
	paths, err := read(q)
	if err != nil {
		return report, err
	}
	matched := map[string]bool{}
	for _, row := range paths[1:] {
		if len(row) != 18 {
			return report, errors.New("invalid links_to path")
		}
		a, b := persistenceString(row[3]), persistenceString(row[8])
		da, db := persistenceString(row[0]), persistenceString(row[5])
		ca, e1 := strconv.ParseInt(row[1], 10, 64)
		cb, e2 := strconv.ParseInt(row[6], 10, 64)
		identity := model.RelationIdentity{SourceID: source, EdgeType: model.EdgeNetwork, Kind: model.RelationLinksTo, From: model.EntityIdentity{SourceID: source, EntityType: model.EntityInterface, StableID: a}, To: model.EntityIdentity{SourceID: source, EntityType: model.EntityInterface, StableID: b}}
		normalized, normErr := model.NormalizeRelation(model.Relation{Identity: identity, CreatedAt: at, SyncedAt: at})
		if normErr != nil || normalized.Identity != identity || identity.From == identity.To {
			return report, errors.New("links_to direction is not model-normalized")
		}
		id, e3 := identity.ID()
		rank, e4 := identity.Rank()
		stored, e5 := strconv.ParseInt(row[11], 10, 64)
		av, e6 := identity.From.VID()
		bv, e7 := identity.To.VID()
		dav, e8 := (model.EntityIdentity{SourceID: source, EntityType: model.EntityDevice, StableID: da}).VID()
		dbv, e9 := (model.EntityIdentity{SourceID: source, EntityType: model.EntityDevice, StableID: db}).VID()
		edge := seen[id]
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || e7 != nil || e8 != nil || e9 != nil || ca != 54 || cb != 54 || persistenceString(row[2]) != source || persistenceString(row[7]) != source || persistenceString(row[4]) != "physics" || persistenceString(row[9]) != "physics" || rank != stored || id != persistenceString(row[10]) || av != persistenceString(row[14]) || bv != persistenceString(row[15]) || dav != persistenceString(row[16]) || dbv != persistenceString(row[17]) || matched[id] || edge == nil || persistenceString(edge[0]) != av || persistenceString(edge[1]) != bv || edge[2] != row[11] || edge[4] != row[12] || edge[5] != row[13] {
			return report, errors.New("links_to path identity or unique ownership mismatch")
		}
		matched[id] = true
		if len(report.Samples) < 5 {
			report.Samples = append(report.Samples, linksToSample{FromDeviceSN: da, FromPortUUID: a, ToDeviceSN: db, ToPortUUID: b, RelationID: id, Rank: rank})
		}
	}
	report.ValidEndpointCount = int64(len(matched))
	if report.ValidEndpointCount != expected || len(matched) != len(seen) {
		return report, errors.New("links_to missing or ambiguous endpoint paths")
	}
	return report, nil
}

type linksToPersistenceReport struct {
	Status          string                 `json:"status"`
	SourceID        string                 `json:"source_id"`
	Run             *persistenceRun        `json:"run,omitempty"`
	Checkpoint      *persistenceCheckpoint `json:"checkpoint,omitempty"`
	Graph           *linksToGraphReport    `json:"graph"`
	TopologyPresent bool                   `json:"topology_present"`
	Note            string                 `json:"note,omitempty"`
}

func validateLinksToPersistenceReport(r *linksToPersistenceReport) error {
	return validateLinksToPersistenceReportAt(r, time.Time{})
}

func validateLinksToPersistenceReportAt(r *linksToPersistenceReport, expected time.Time) error {
	if r.Run == nil || r.Checkpoint == nil || r.Graph == nil {
		return errors.New("links_to verification missing data")
	}
	run, cp := r.Run, r.Checkpoint
	c := cp.Cursor
	if run.SourceID != r.SourceID || run.Status != "succeeded" || run.StartedAt == nil || run.FinishedAt == nil || run.LeaseExpiresAt != nil || !cp.Complete || cp.CompletedAt == nil || c.Version != 1 || c.RunID != run.ID || !persistenceSnapshotMatches(run.StartedAt, c.SnapshotAt, expected) || c.Total < 0 || c.NextOffset != c.Total || r.Graph.Current != c.Total || r.Graph.ValidEndpointCount != c.Total {
		return errors.New("links_to persisted task/checkpoint mismatch")
	}
	r.Status = "PASS"
	r.TopologyPresent = c.Total > 0
	if !r.TopologyPresent {
		r.Note = "verified empty links_to snapshot; no topology created"
	}
	return nil
}
func verifyLinksTo(ctx context.Context, conf *viper.Viper, source, runID string, out io.Writer) error {
	if !inventoryID.MatchString(source) || (runID != "" && !inventoryID.MatchString(runID)) || conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("links_to verify requires valid source/run/MySQL")
	}
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	if err != nil {
		return err
	}
	defer db.Close()
	var enabled bool
	if err := db.QueryRowContext(ctx, "SELECT enabled FROM sources WHERE id = ?", source).Scan(&enabled); err != nil || !enabled {
		return errors.New("links_to source unavailable")
	}
	report := &linksToPersistenceReport{Status: "preflight", SourceID: source}
	at := time.Time{}
	expected := int64(0)
	if runID != "" {
		report.Run = &persistenceRun{}
		r := report.Run
		if err := db.QueryRowContext(ctx, "SELECT id,source_id,status,started_at,finished_at,lease_expires_at FROM sync_runs WHERE id = ?", runID).Scan(&r.ID, &r.SourceID, &r.Status, &r.StartedAt, &r.FinishedAt, &r.LeaseExpiresAt); err != nil {
			return err
		}
		cp := &persistenceCheckpoint{}
		report.Checkpoint = cp
		var cursor string
		if err := db.QueryRowContext(ctx, "SELECT complete,completed_at,`cursor` FROM sync_checkpoints WHERE source_id = ? AND resource = ?", source, linksToResource).Scan(&cp.Complete, &cp.CompletedAt, &cursor); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(cursor), &cp.Cursor); err != nil {
			return err
		}
		at = cp.Cursor.SnapshotAt
		expected = cp.Cursor.Total
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
	report.Graph, err = verifyLinksToGraph(ctx, client, source, at, expected)
	if err != nil {
		return err
	}
	if runID != "" {
		if err := validateLinksToPersistenceReportAt(report, service.SyncSnapshot(ctx, time.Time{})); err != nil {
			return err
		}
	}
	return json.NewEncoder(out).Encode(report)
}
