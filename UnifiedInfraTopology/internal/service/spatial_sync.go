package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type SpatialSyncResult struct {
	Edges, Objects, Skipped, MissingEndpoints int
	Reasons                                   map[string]int
}

type SpatialSyncWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
}

func NewSpatialSyncWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *SpatialSyncWorker {
	return &SpatialSyncWorker{source: source, graph: graph}
}

// 完整采集和全部端点预检后只写空间边；复读失败不清理已写入的边。
func (w *SpatialSyncWorker) Run(ctx context.Context, source model.Source, snapshot time.Time) (SpatialSyncResult, error) {
	result := SpatialSyncResult{Reasons: map[string]int{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w == nil || w.source == nil || w.graph == nil || source.ID == "" || snapshot.IsZero() {
		return result, fmt.Errorf("spatial sync requires collector, graph, source and nonzero snapshot")
	}
	snapshot = snapshot.UTC().Truncate(time.Microsecond)
	collection, err := w.source.CollectSpatial(ctx, source)
	result.Objects, result.Skipped, result.Reasons = collection.Objects, collection.Skipped, collection.Reasons
	if err != nil {
		return result, err
	}
	relations, err := normalizeSpatialRelations(collection.Relations, source.ID, snapshot)
	if err != nil {
		return result, err
	}
	checked := map[model.EntityIdentity]bool{}
	existing := map[model.EntityIdentity]bool{}
	valid := []model.Relation{}
	for _, relation := range relations {
		missing := false
		for _, endpoint := range []model.EntityIdentity{relation.Identity.From, relation.Identity.To} {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if !checked[endpoint] {
				exists, err := w.graph.VertexExists(ctx, endpoint)
				if err != nil {
					return result, fmt.Errorf("spatial sync endpoint read failed: %w", err)
				}
				checked[endpoint], existing[endpoint] = true, exists
				if !exists {
					result.MissingEndpoints++
				}
			}
			missing = missing || !existing[endpoint]
		}
		if missing {
			result.Skipped++
			result.Reasons["missing_endpoint"]++
			continue
		}
		valid = append(valid, relation)
	}
	for _, relation := range valid {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := w.graph.UpsertRelation(ctx, relation); err != nil {
			return result, fmt.Errorf("spatial sync edge write failed (earlier edges may persist): %w", err)
		}
		result.Edges++
	}
	// 独立来源复读必须包含未生成边的目录与引用，不能只比较输出边数。
	verified, err := w.source.CollectSpatial(ctx, source)
	if err != nil {
		return result, fmt.Errorf("spatial sync verification failed (written edges persist): %w", err)
	}
	if verified.SourceDigest != collection.SourceDigest {
		return result, fmt.Errorf("spatial sync source changed between passes (written edges persist)")
	}
	return result, ctx.Err()
}

func normalizeSpatialRelations(candidates []model.Relation, sourceID string, snapshot time.Time) ([]model.Relation, error) {
	byID := map[string]model.Relation{}
	for _, candidate := range candidates {
		identity := candidate.Identity
		allowed := identity.Kind == model.RelationLocatedIn && (identity.From.EntityType == model.EntityDevice && identity.To.EntityType == model.EntityCabinet || identity.From.EntityType == model.EntityCabinet && identity.To.EntityType == model.EntityDataCenter) || identity.Kind == model.RelationTaggedWith && identity.From.EntityType == model.EntityCabinet && identity.To.EntityType == model.EntityPod
		if !allowed || identity.EdgeType != model.EdgeSpatial || identity.SourceID != sourceID || identity.From.SourceID != sourceID || identity.To.SourceID != sourceID {
			return nil, fmt.Errorf("spatial sync invalid relation source or endpoints")
		}
		candidate.CreatedAt, candidate.SyncedAt = snapshot, snapshot
		relation, err := model.NormalizeRelation(candidate)
		if err != nil {
			return nil, err
		}
		id, err := relation.Identity.ID()
		if err != nil {
			return nil, err
		}
		byID[id] = relation
	}
	keys := make([]string, 0, len(byID))
	for id := range byID {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	relations := make([]model.Relation, 0, len(keys))
	for _, id := range keys {
		relations = append(relations, byID[id])
	}
	return relations, nil
}
