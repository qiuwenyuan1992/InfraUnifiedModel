package main

import (
	"context"
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
)

const spatialSyncResource = "spatial_relations"

type spatialSyncRunner interface {
	Run(context.Context, model.Source, time.Time) (service.SpatialSyncResult, error)
}

type spatialSyncRunnerAdapter struct {
	runner spatialSyncRunner
	result service.SpatialSyncResult
}

func (a *spatialSyncRunnerAdapter) Run(ctx context.Context, source model.Source, at time.Time) (service.ServerUplinkResult, error) {
	r, err := a.runner.Run(ctx, source, at)
	a.result = r
	return service.ServerUplinkResult{Edges: r.Edges, Servers: r.Objects, Skipped: r.Skipped, MissingEndpoints: r.MissingEndpoints}, err
}

func executeSpatialSync(ctx context.Context, control repository.InventoryRepository, runner spatialSyncRunner, run *model.SyncRun, source model.Source, heartbeat time.Duration, verify func(context.Context, time.Time, int64) error) (service.SpatialSyncResult, error) {
	a := &spatialSyncRunnerAdapter{runner: runner}
	_, err := executeServerUplink(ctx, standaloneRelationControl{control, spatialSyncResource}, a, run, source, heartbeat, verify)
	return a.result, err
}

func runSpatialSync(ctx context.Context, conf *viper.Viper, logger *log.Logger, source string, out io.Writer) error {
	return runStandaloneRelation(ctx, conf, logger, source, spatialSyncResource, out, func(collector *adapter.CMDB, graph repository.TopologyGraphRepository) serverUplinkRunner {
		return &spatialSyncRunnerAdapter{runner: service.NewSpatialSyncWorker(collector, graph)}
	}, func(c context.Context, client graphclient.Client, at time.Time, expected int64) error {
		_, err := verifySpatialGraph(c, client, source, at, expected)
		return err
	}, func(runner serverUplinkRunner) any {
		r := runner.(*spatialSyncRunnerAdapter).result
		return map[string]any{"edges": r.Edges, "objects": r.Objects, "skipped": r.Skipped, "missing_endpoints": r.MissingEndpoints, "reasons": r.Reasons}
	}, verifySpatialSync)
}

func verifySpatialSync(ctx context.Context, conf *viper.Viper, source, run string, out io.Writer) error {
	return verifyStandaloneRelation(ctx, conf, source, run, spatialSyncResource, out, func(c context.Context, client graphclient.Client, at time.Time, expected int64) (any, int64, int64, error) {
		r, err := verifySpatialGraph(c, client, source, at, expected)
		if err != nil {
			return r, 0, 0, err
		}
		return r, r.Current, r.ValidEndpointCount, nil
	})
}

type SpatialGraphSample struct {
	Path       string `json:"path"`
	FromID     string `json:"from_stable_id"`
	ToID       string `json:"to_stable_id"`
	RelationID string `json:"relation_id"`
	Rank       int64  `json:"rank"`
}

type SpatialGraphReport struct {
	Total              int64                `json:"total_edges"`
	Current            int64                `json:"current_snapshot"`
	ValidEndpointCount int64                `json:"valid_endpoint_count"`
	KindCounts         map[string]int64     `json:"kind_counts"`
	PathCounts         map[string]int64     `json:"path_counts"`
	Samples            []SpatialGraphSample `json:"samples"`
}

func verifySpatialGraph(ctx context.Context, client graphclient.Client, source string, at time.Time, expected int64) (*SpatialGraphReport, error) {
	read := standaloneGraphReader(ctx, client, source, at)
	scope := "LOOKUP ON spatial_relation WHERE spatial_relation.source_id == $source_id AND (spatial_relation.relation_kind == \"located_in\" OR spatial_relation.relation_kind == \"tagged_with\")"
	rows, err := read(scope + " YIELD src(edge) AS from_vertex | YIELD count(*) AS edge_count;")
	if err != nil {
		return nil, err
	}
	total, err := standaloneGraphCount(rows)
	if err != nil {
		return nil, err
	}
	r := &SpatialGraphReport{Total: total, KindCounts: map[string]int64{"located_in": 0, "tagged_with": 0}, PathCounts: map[string]int64{"device_cabinet": 0, "cabinet_data_center": 0, "cabinet_pod": 0}, Samples: []SpatialGraphSample{}}
	if at.IsZero() {
		return r, nil
	}
	raw, err := read(scope + " AND spatial_relation.synced_at == datetime($snapshot) YIELD src(edge) AS from_vertex, dst(edge) AS to_vertex, rank(edge) AS edge_rank, spatial_relation.relation_id AS relation_id, spatial_relation.relation_kind AS relation_kind, toString(spatial_relation.created_at) AS created_at, toString(spatial_relation.synced_at) AS synced_at, spatial_relation.source_id AS edge_source;")
	if err != nil {
		return r, err
	}
	if len(raw) == 0 {
		return r, errors.New("invalid spatial edge table")
	}
	seen := map[string][]string{}
	for _, row := range raw[1:] {
		if len(row) != 8 {
			return r, errors.New("invalid spatial edge")
		}
		id, kind := persistenceString(row[3]), persistenceString(row[4])
		created, e1 := persistenceTime(row[5])
		synced, e2 := persistenceTime(row[6])
		if id == "" || seen[id] != nil || (kind != "located_in" && kind != "tagged_with") || e1 != nil || e2 != nil || created.IsZero() || created.After(synced) || !synced.Equal(at) || persistenceString(row[7]) != source {
			return r, errors.New("invalid spatial edge identity, kind, source, or timestamp")
		}
		seen[id] = row
		r.KindCounts[kind]++
	}
	r.Current = int64(len(seen))
	if expected < 0 || r.Current != expected || r.Total < r.Current {
		return r, errors.New("spatial checkpoint count mismatch")
	}
	paths := []struct {
		name                     string
		from, to                 model.EntityType
		kind                     model.RelationKind
		fromProperty, toProperty string
	}{
		{"device_cabinet", model.EntityDevice, model.EntityCabinet, model.RelationLocatedIn, "device_sn", "uuid"},
		{"cabinet_data_center", model.EntityCabinet, model.EntityDataCenter, model.RelationLocatedIn, "uuid", "uuid"},
		{"cabinet_pod", model.EntityCabinet, model.EntityPod, model.RelationTaggedWith, "uuid", "uuid"},
	}
	matched := map[string]bool{}
	matchedKinds := map[string]int64{}
	for _, path := range paths {
		// 查询结构只取权威模型常量；来源及时间通过参数绑定。
		query := "MATCH (f:" + string(path.from) + ")-[e:spatial_relation]->(t:" + string(path.to) + ") WHERE e.source_id == $source_id AND e.relation_kind == \"" + string(path.kind) + "\" AND e.synced_at == datetime($snapshot) RETURN e.relation_id AS relation_id, rank(e) AS edge_rank, id(f) AS from_vertex, id(t) AS to_vertex, f." + string(path.from) + "." + path.fromProperty + " AS from_identity, f." + string(path.from) + ".source_id AS from_source, t." + string(path.to) + "." + path.toProperty + " AS to_identity, t." + string(path.to) + ".source_id AS to_source, size(tags(f)) AS from_tag_count, size(tags(t)) AS to_tag_count ORDER BY relation_id ASC;"
		rows, err := read(query)
		if err != nil {
			return r, err
		}
		if len(rows) == 0 {
			return r, errors.New("invalid spatial path table")
		}
		for _, row := range rows[1:] {
			if len(row) != 10 {
				return r, errors.New("invalid spatial path")
			}
			id := persistenceString(row[0])
			edge := seen[id]
			if edge == nil || matched[id] || persistenceString(edge[4]) != string(path.kind) || persistenceString(row[5]) != source || persistenceString(row[7]) != source || row[8] != "1" || row[9] != "1" {
				return r, errors.New("spatial missing, ambiguous, or wrongly tagged endpoint")
			}
			identity := model.RelationIdentity{SourceID: source, EdgeType: model.EdgeSpatial, Kind: path.kind, From: model.EntityIdentity{SourceID: source, EntityType: path.from, StableID: persistenceString(row[4])}, To: model.EntityIdentity{SourceID: source, EntityType: path.to, StableID: persistenceString(row[6])}}
			actual, e1 := identity.ID()
			rank, e2 := identity.Rank()
			from, e3 := identity.From.VID()
			to, e4 := identity.To.VID()
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || actual != id || row[1] != strconv.FormatInt(rank, 10) || edge[2] != row[1] || persistenceString(edge[0]) != from || persistenceString(edge[1]) != to || persistenceString(row[2]) != from || persistenceString(row[3]) != to {
				return r, errors.New("spatial identity, rank, or VID mismatch")
			}
			matched[id] = true
			r.PathCounts[path.name]++
			matchedKinds[string(path.kind)]++
			if len(r.Samples) < 5 {
				r.Samples = append(r.Samples, SpatialGraphSample{Path: path.name, FromID: identity.From.StableID, ToID: identity.To.StableID, RelationID: id, Rank: rank})
			}
		}
	}
	r.ValidEndpointCount = int64(len(matched))
	if len(matched) != len(seen) || matchedKinds["located_in"] != r.KindCounts["located_in"] || matchedKinds["tagged_with"] != r.KindCounts["tagged_with"] {
		return r, errors.New("spatial edge lacks a unique approved typed path")
	}
	return r, nil
}
