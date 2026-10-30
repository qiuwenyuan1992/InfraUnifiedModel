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
	"sync"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

const serverUplinkResource = "server_uplinks"
const serverUplinkHeartbeat = 30 * time.Second

type serverUplinkRunner interface {
	Run(context.Context, model.Source, time.Time) (service.ServerUplinkResult, error)
}

func runServerUplink(ctx context.Context, conf *viper.Viper, logger *log.Logger, sourceID string, output io.Writer) error {
	if !inventoryID.MatchString(sourceID) {
		return errors.New("server uplink sync requires a valid source ID")
	}
	if conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("server uplink sync requires MySQL")
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
	var source model.Source
	if err := db.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error; err != nil {
		return errors.New("configured source not found")
	}
	if !source.Enabled || source.AdapterKind != "cmdb" || source.ConfigRef == "" {
		return errors.New("source must be enabled and use the CMDB adapter")
	}
	if conf.GetInt("inventory.cmdb.sources."+source.ConfigRef+".device_condition.idc_id") <= 0 {
		return errors.New("server uplink sync requires a positive device IDC selection")
	}
	// 只限制当前进程的批量大小，不覆盖实体、类别或来源选择。
	conf.Set("inventory.cmdb.sources."+source.ConfigRef+".page_size", 200)
	collector := adapter.NewCMDB(conf)
	if err := collector.Validate(ctx, source); err != nil {
		return errors.New("source configuration invalid")
	}
	var active int64
	if err := db.WithContext(ctx).Model(&model.SyncRun{}).Where("source_id = ? AND status IN ?", sourceID, []string{model.SyncRunStatusQueued, model.SyncRunStatusRunning, model.SyncRunStatusValidating, model.SyncRunStatusPublishing}).Count(&active).Error; err != nil {
		return errors.New("active task inspection failed")
	}
	if active != 0 {
		return errors.New("source has outstanding tasks; finish them before server uplink sync")
	}
	graphConfig, err := graphclient.ParseConfig(conf)
	if err != nil || len(graphConfig.Hosts) == 0 || graphConfig.Space == "" {
		return errors.New("graph must be configured before enqueue")
	}
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	if err != nil {
		return errors.New("graph connection failed")
	}
	defer cleanup()
	client, err := graphclient.New(graphConfig)
	if err != nil {
		return errors.New("graph verification connection failed")
	}
	defer client.Close()
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return errors.New("run ID generation failed")
	}
	runID := hex.EncodeToString(idBytes)
	hash := sha256.Sum256([]byte(sourceID + ":server_uplink"))
	request := &model.SyncRun{ID: runID, SourceID: sourceID, Status: model.SyncRunStatusQueued, CreatedAt: serverUplinkNow(), IdempotencyKey: "cli-server-uplink-" + runID, RequestHash: hex.EncodeToString(hash[:])}
	base := repository.NewRepository(logger, db)
	control := repository.NewInventoryRunRepository(base, runID)
	var claimed *model.SyncRun
	var claimedSource *model.Source
	if err := base.Transaction(ctx, func(txCtx context.Context) error {
		if _, err := control.Enqueue(txCtx, request); err != nil {
			return err
		}
		var err error
		claimed, claimedSource, err = control.ClaimNext(txCtx, serverUplinkNow())
		return err
	}); err != nil {
		return errors.New("server uplink atomic enqueue and claim failed")
	}
	if claimed == nil || claimedSource == nil {
		return errors.New("server uplink task claim invalid")
	}
	worker := service.NewServerUplinkWorker(collector, graph)
	result, err := executeServerUplink(ctx, control, worker, claimed, *claimedSource, serverUplinkHeartbeat, func(ctx context.Context, at time.Time, edges int64) error {
		_, err := verifyServerUplinkGraph(ctx, client, sourceID, at, edges)
		return err
	})
	if err != nil {
		return fmt.Errorf("server uplink run %s failed; partial graph writes are not rolled back: %w", runID, err)
	}
	logger.Info("server uplink sync succeeded", zap.String("run_id", runID), zap.String("source_id", sourceID), zap.Int("edges", result.Edges), zap.Int("servers", result.Servers), zap.Int("skipped", result.Skipped), zap.Int("missing_endpoints", result.MissingEndpoints))
	if err := json.NewEncoder(output).Encode(map[string]interface{}{"status": "succeeded", "run_id": runID, "source_id": sourceID, "resource": serverUplinkResource, "edges": result.Edges, "servers": result.Servers, "skipped": result.Skipped, "missing_endpoints": result.MissingEndpoints}); err != nil {
		return err
	}
	return verifyServerUplink(ctx, conf, sourceID, runID, output)
}

type serverUplinkPersistenceReport struct {
	Status          string                   `json:"status"`
	SourceID        string                   `json:"source_id"`
	ConfigRef       string                   `json:"config_ref"`
	Resource        string                   `json:"resource"`
	ActiveRuns      int64                    `json:"active_runs_global"`
	Run             *persistenceRun          `json:"run,omitempty"`
	Checkpoint      *persistenceCheckpoint   `json:"checkpoint,omitempty"`
	Graph           *serverUplinkGraphReport `json:"graph"`
	TopologyPresent bool                     `json:"topology_present"`
	Note            string                   `json:"note,omitempty"`
}

func validateServerUplinkPersistenceReport(r *serverUplinkPersistenceReport) error {
	if r.Run == nil || r.Checkpoint == nil || r.Graph == nil {
		return errors.New("server uplink persisted run or checkpoint missing")
	}
	run, checkpoint, graph := r.Run, r.Checkpoint, r.Graph
	cursor := checkpoint.Cursor
	if run.SourceID != r.SourceID || run.Status != model.SyncRunStatusSucceeded || run.StartedAt == nil || run.FinishedAt == nil || run.LeaseExpiresAt != nil || run.FinishedAt.Before(*run.StartedAt) || !checkpoint.Complete || checkpoint.CompletedAt == nil || checkpoint.CompletedAt.Before(*run.StartedAt) || !checkpoint.CompletedAt.Equal(*run.FinishedAt) || cursor.Version != 1 || cursor.RunID != run.ID || !cursor.SnapshotAt.Equal(*run.StartedAt) || cursor.Total < 0 || cursor.NextOffset != cursor.Total || graph.Current != cursor.Total || graph.ValidEndpointCount != cursor.Total || graph.Total < graph.Current {
		return errors.New("server uplink persisted run, checkpoint, or graph snapshot does not match")
	}
	r.Status = "PASS"
	r.TopologyPresent = graph.Current > 0
	if !r.TopologyPresent {
		r.Note = "verified empty server uplink snapshot; no topology is claimed"
	}
	return nil
}

func verifyServerUplink(ctx context.Context, conf *viper.Viper, sourceID, runID string, output io.Writer) error {
	if conf.GetString("data.db.main.driver") != "mysql" {
		return errors.New("server uplink verification requires MySQL")
	}
	if !inventoryID.MatchString(sourceID) || (runID != "" && !inventoryID.MatchString(runID)) {
		return errors.New("server uplink verification requires valid source and run IDs")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", conf.GetString("data.db.main.dsn"))
	if err != nil {
		return errors.New("control database configuration invalid")
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return errors.New("control database connection failed")
	}
	r := &serverUplinkPersistenceReport{Status: "preflight", SourceID: sourceID, Resource: serverUplinkResource}
	var enabled bool
	var adapterKind string
	if err := db.QueryRowContext(ctx, "SELECT config_ref, enabled, adapter_kind FROM sources WHERE id = ?", sourceID).Scan(&r.ConfigRef, &enabled, &adapterKind); err != nil {
		return errors.New("verification source not found")
	}
	if !enabled || adapterKind != "cmdb" || r.ConfigRef == "" {
		return errors.New("verification source must be enabled and use the CMDB adapter")
	}
	if conf.GetInt("inventory.cmdb.sources."+r.ConfigRef+".device_condition.idc_id") <= 0 {
		return errors.New("server uplink verification requires a positive device IDC selection")
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sync_runs WHERE status IN ('queued','running','validating','publishing')").Scan(&r.ActiveRuns); err != nil {
		return errors.New("active run query failed")
	}
	var snapshot time.Time
	var expected int64
	if runID != "" {
		r.Run = &persistenceRun{}
		if err := db.QueryRowContext(ctx, "SELECT id, source_id, status, started_at, finished_at, lease_expires_at FROM sync_runs WHERE id = ?", runID).Scan(&r.Run.ID, &r.Run.SourceID, &r.Run.Status, &r.Run.StartedAt, &r.Run.FinishedAt, &r.Run.LeaseExpiresAt); err != nil {
			return errors.New("verification run not found")
		}
		r.Checkpoint = &persistenceCheckpoint{}
		var cursor string
		if err := db.QueryRowContext(ctx, "SELECT complete, completed_at, `cursor` FROM sync_checkpoints WHERE source_id = ? AND resource = ?", sourceID, serverUplinkResource).Scan(&r.Checkpoint.Complete, &r.Checkpoint.CompletedAt, &cursor); err != nil {
			return errors.New("server uplink verification checkpoint not found")
		}
		if err := json.Unmarshal([]byte(cursor), &r.Checkpoint.Cursor); err != nil {
			return errors.New("server uplink verification cursor invalid")
		}
		snapshot, expected = r.Checkpoint.Cursor.SnapshotAt, r.Checkpoint.Cursor.Total
		if snapshot.IsZero() || expected < 0 {
			return errors.New("server uplink verification snapshot invalid")
		}
	}
	graphConfig, err := graphclient.ParseConfig(conf)
	if err != nil {
		return errors.New("graph configuration invalid")
	}
	client, err := graphclient.New(graphConfig)
	if err != nil {
		return errors.New("graph connection failed")
	}
	defer client.Close()
	r.Graph, err = verifyServerUplinkGraph(ctx, client, sourceID, snapshot, expected)
	if err != nil {
		return err
	}
	if runID != "" {
		if err := validateServerUplinkPersistenceReport(r); err != nil {
			return err
		}
	}
	return json.NewEncoder(output).Encode(r)
}

type serverUplinkGraphReport struct {
	Total              int64                `json:"total"`
	Current            int64                `json:"current_snapshot"`
	ValidEndpointCount int64                `json:"valid_endpoint_count"`
	Samples            []serverUplinkSample `json:"samples"`
}

type serverUplinkSample struct {
	ServerSN          string    `json:"server_sn"`
	PortUUID          string    `json:"port_uuid"`
	OwnerSN           string    `json:"owner_sn"`
	OwnerParentTypeID int64     `json:"owner_parent_type_id"`
	RelationID        string    `json:"relation_id"`
	Rank              int64     `json:"rank"`
	SyncedAt          time.Time `json:"synced_at"`
}

func serverUplinkGraphQuery(current, count bool) string {
	q := "LOOKUP ON network_relation WHERE network_relation.source_id == $source_id AND network_relation.relation_kind == $relation_kind"
	if current {
		q += " AND network_relation.synced_at == datetime($snapshot)"
	}
	if count {
		return q + " YIELD src(edge) AS src | YIELD count(*) AS total;"
	}
	return q + " YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, network_relation.relation_id AS relation_id, toString(network_relation.created_at) AS created_at, toString(network_relation.synced_at) AS synced_at;"
}

func verifyServerUplinkGraph(ctx context.Context, client graphclient.Client, sourceID string, snapshot time.Time, expected int64) (*serverUplinkGraphReport, error) {
	params := map[string]interface{}{"source_id": sourceID, "relation_kind": string(model.RelationServerUplink), "snapshot": snapshot.UTC().Format("2006-01-02T15:04:05.000000+00:00")}
	read := func(query string) ([][]string, error) {
		result, err := client.ExecuteParameter(ctx, query, params)
		if err = persistenceCheckGraphResult(result, err); err != nil {
			return nil, err
		}
		return persistenceTable(result)
	}
	count := func(query string) (int64, error) {
		rows, err := read(query)
		if err != nil {
			return 0, err
		}
		if len(rows) != 2 || len(rows[1]) != 1 {
			return 0, errors.New("invalid server uplink count")
		}
		n, err := strconv.ParseInt(rows[1][0], 10, 64)
		if err != nil || n < 0 {
			return 0, errors.New("invalid server uplink count")
		}
		return n, nil
	}
	r := &serverUplinkGraphReport{Samples: []serverUplinkSample{}}
	var err error
	r.Total, err = count(serverUplinkGraphQuery(false, true))
	if err != nil {
		return nil, err
	}
	if snapshot.IsZero() {
		return r, nil
	}
	rows, err := read(serverUplinkGraphQuery(true, false))
	if err != nil {
		return r, err
	}
	r.Current = int64(len(rows) - 1)
	if r.Current != expected || r.Total < r.Current {
		return r, errors.New("server uplink snapshot count does not match checkpoint")
	}
	seen := make(map[string][]string, len(rows))
	for _, row := range rows[1:] {
		if len(row) != 6 {
			return r, errors.New("invalid server uplink edge response")
		}
		rank, rankErr := strconv.ParseInt(row[2], 10, 64)
		id := persistenceString(row[3])
		created, createdErr := persistenceTime(row[4])
		synced, syncedErr := persistenceTime(row[5])
		if rankErr != nil || rank <= 0 || id == "" || seen[id] != nil || createdErr != nil || syncedErr != nil || created.IsZero() || created.After(synced) || !synced.Equal(snapshot) {
			return r, errors.New("server uplink edge identity, rank, or timestamps invalid")
		}
		seen[id] = row
	}
	// 不先过滤分类：逐边检查全部归属路径，额外错误 owner 不能被 WHERE 隐藏。
	prefix := `MATCH (s:device)-[u:network_relation]->(p:interface)<-[o:composition_relation]-(t:device) WHERE s.device.source_id == $source_id AND p.interface.source_id == $source_id AND u.source_id == $source_id AND o.source_id == $source_id AND u.relation_kind == $relation_kind AND o.relation_kind == "owns_interface" AND u.synced_at == datetime($snapshot)`
	paths, err := read(prefix + " RETURN s.device.device_sn AS server_sn, p.interface.port_uuid AS port_uuid, t.device.device_sn AS owner_sn, t.device.parent_type_id AS owner_parent_type_id, u.relation_id AS relation_id, rank(u) AS rank, toString(u.synced_at) AS synced_at, s.device.parent_type_id AS server_type, p.interface.port_type AS port_type, t.device.source_id AS owner_source;")
	if err != nil {
		return r, err
	}
	matched := make(map[string]bool, len(seen))
	for _, row := range paths[1:] {
		if len(row) != 10 {
			return r, errors.New("invalid server uplink endpoint path")
		}
		sn, port, owner := persistenceString(row[0]), persistenceString(row[1]), persistenceString(row[2])
		ownerType, ownerErr := strconv.ParseInt(row[3], 10, 64)
		serverType, serverErr := strconv.ParseInt(row[7], 10, 64)
		identity := model.RelationIdentity{SourceID: sourceID, EdgeType: model.EdgeNetwork, Kind: model.RelationServerUplink, From: model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityDevice, StableID: sn}, To: model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityInterface, StableID: port}}
		id, idErr := identity.ID()
		rank, rankErr := identity.Rank()
		storedRank, storedErr := strconv.ParseInt(row[5], 10, 64)
		synced, syncedErr := persistenceTime(row[6])
		src, srcErr := identity.From.VID()
		dst, dstErr := identity.To.VID()
		edge := seen[id]
		if idErr != nil || rankErr != nil || storedErr != nil || ownerErr != nil || ownerType != 54 || owner == "" || serverErr != nil || serverType != 52 || persistenceString(row[8]) != "physics" || persistenceString(row[9]) != sourceID || id != persistenceString(row[4]) || rank != storedRank || syncedErr != nil || !synced.Equal(snapshot) || srcErr != nil || dstErr != nil || edge == nil || persistenceString(edge[0]) != src || persistenceString(edge[1]) != dst || edge[2] != row[5] || matched[id] {
			return r, errors.New("server uplink identity, rank, classification, or unique ownership mismatch")
		}
		matched[id] = true
		if len(r.Samples) < 5 {
			r.Samples = append(r.Samples, serverUplinkSample{ServerSN: sn, PortUUID: port, OwnerSN: owner, OwnerParentTypeID: ownerType, RelationID: id, Rank: rank, SyncedAt: synced})
		}
	}
	r.ValidEndpointCount = int64(len(matched))
	if r.ValidEndpointCount != expected || len(matched) != len(seen) {
		return r, errors.New("server uplink edges have missing or ambiguous endpoint paths")
	}
	return r, nil
}

func serverUplinkNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func executeServerUplink(ctx context.Context, control repository.InventoryRepository, worker serverUplinkRunner, run *model.SyncRun, source model.Source, heartbeat time.Duration, validate func(context.Context, time.Time, int64) error) (result service.ServerUplinkResult, err error) {
	if run == nil || run.SourceID != source.ID || run.Status != model.SyncRunStatusRunning || run.StartedAt == nil || run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(serverUplinkNow()) {
		return result, errors.New("server uplink claimed run invalid")
	}
	leaseCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	var leaseErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(context.WithoutCancel(leaseCtx), 5*time.Second)
				canceled, checkErr := control.RunCancellationRequested(renewCtx, run.ID)
				if checkErr != nil || canceled {
					renewCancel()
					cancel(errors.New("server uplink cancellation requested or control unavailable"))
					return
				}
				leaseErr = control.RenewRunLease(renewCtx, run.ID, serverUplinkNow())
				renewCancel()
				if leaseErr != nil {
					cancel(errors.New("server uplink lease lost"))
					return
				}
			}
		}
	}()
	stopHeartbeat := func() { once.Do(func() { close(stop); <-done }) }
	defer stopHeartbeat()
	status := model.SyncRunStatusRunning
	defer func() {
		if err == nil {
			return
		}
		stopHeartbeat()
		terminal, code := model.SyncRunStatusFailed, "server_uplink_failed"
		if ctx.Err() != nil {
			terminal, code = model.SyncRunStatusCanceled, ""
		}
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer finishCancel()
		if canceled, checkErr := control.RunCancellationRequested(finishCtx, run.ID); checkErr == nil && canceled {
			terminal, code = model.SyncRunStatusCanceled, ""
		}
		if _, transitionErr := control.TransitionRun(finishCtx, run.ID, status, terminal, serverUplinkNow(), code); transitionErr != nil {
			err = errors.New("server uplink failed; terminal status update failed")
		}
	}()
	checkCancel := func() error {
		if err := leaseCtx.Err(); err != nil {
			return errors.New("server uplink execution canceled or lease lost")
		}
		canceled, err := control.RunCancellationRequested(leaseCtx, run.ID)
		if err != nil {
			return errors.New("server uplink cancellation check failed")
		}
		if canceled {
			return errors.New("server uplink cancellation requested")
		}
		return nil
	}
	if err = checkCancel(); err != nil {
		return result, err
	}
	snapshot := run.StartedAt.UTC().Truncate(time.Microsecond)
	if _, err = control.BeginCheckpoint(leaseCtx, source.ID, serverUplinkResource, run.ID, snapshot, serverUplinkNow()); err != nil {
		return result, errors.New("server uplink checkpoint begin failed")
	}
	if result, err = worker.Run(leaseCtx, source, snapshot); err != nil {
		return result, errors.New("server uplink collection or graph write failed")
	}
	if err = checkCancel(); err != nil {
		return result, err
	}
	cursor := repository.DeviceSyncCursor{Version: repository.DeviceSyncCursorVersion, RunID: run.ID, SnapshotAt: snapshot, NextOffset: result.Edges, Total: result.Edges}
	if result.Edges > 0 {
		if _, err = control.AdvanceCheckpoint(leaseCtx, source.ID, serverUplinkResource, run.ID, cursor, serverUplinkNow()); err != nil {
			return result, errors.New("server uplink checkpoint advance failed")
		}
	}
	if _, err = control.TransitionRun(leaseCtx, run.ID, status, model.SyncRunStatusValidating, serverUplinkNow(), ""); err != nil {
		return result, errors.New("server uplink validating transition failed")
	}
	status = model.SyncRunStatusValidating
	if err = validate(leaseCtx, snapshot, int64(result.Edges)); err != nil {
		return result, errors.New("server uplink graph validation failed")
	}
	if err = checkCancel(); err != nil {
		return result, err
	}
	if _, err = control.TransitionRun(leaseCtx, run.ID, status, model.SyncRunStatusPublishing, serverUplinkNow(), ""); err != nil {
		return result, errors.New("server uplink publishing transition failed")
	}
	status = model.SyncRunStatusPublishing
	// 等待在途续租结束后再提交成功，避免终态被心跳误判为失租。
	stopHeartbeat()
	if leaseErr != nil {
		return result, errors.New("server uplink lease lost")
	}
	if err = checkCancel(); err != nil {
		return result, err
	}
	if _, err = control.CompleteCheckpointAndRun(leaseCtx, source.ID, serverUplinkResource, run.ID, status, serverUplinkNow()); err != nil {
		return result, errors.New("server uplink atomic completion failed")
	}
	return result, nil
}
