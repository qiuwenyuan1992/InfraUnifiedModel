package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type PODMembershipResult struct {
	Edges, Objects, Skipped, MissingEndpoints int
	Reasons                                   map[string]int
}

type PODMembershipWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
}

func NewPODMembershipWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *PODMembershipWorker {
	return &PODMembershipWorker{source: source, graph: graph}
}

// 完整采集与端点预检后仅写成员边，不执行节点写入或清理。
func (w *PODMembershipWorker) Run(ctx context.Context, source model.Source, snapshot time.Time) (PODMembershipResult, error) {
	result := PODMembershipResult{Reasons: map[string]int{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w == nil || w.source == nil || w.graph == nil || source.ID == "" || snapshot.IsZero() {
		return result, fmt.Errorf("POD membership requires collector, graph, source and nonzero snapshot")
	}
	_, relations, collected, err := w.collect(ctx, source, snapshot, true)
	result.Objects, result.Skipped, result.Reasons = collected.Objects, collected.Skipped, collected.Reasons
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
					return result, fmt.Errorf("POD membership endpoint read failed: %w", err)
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
			return result, fmt.Errorf("POD membership edge write failed (earlier edges may persist): %w", err)
		}
		result.Edges++
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (w *PODMembershipWorker) collect(ctx context.Context, source model.Source, snapshot time.Time, retain bool) (string, []model.Relation, PODMembershipResult, error) {
	result := PODMembershipResult{Reasons: map[string]int{}}
	pass, err := w.source.OpenPODMembershipPass(ctx, source)
	if err != nil {
		return "", nil, result, err
	}
	digest := sha256.New()
	digest.Write([]byte(pass.ScopeDigest()))
	relations := []model.Relation{}
	seenSN := map[string]bool{}
	seenRelations := map[string]bool{}
	planes := map[[2]model.EntityIdentity]int64{}
	total := -1
	for start := 0; ; {
		page, err := pass.Page(ctx, start)
		if err != nil {
			return "", nil, result, err
		}
		if total < 0 {
			total = page.Total
			result.Objects = total
		}
		if total < 0 || page.Total != total || page.NextOffset < start || page.NextOffset > total || !page.Done && page.NextOffset == start || page.Done != (page.NextOffset == total) || page.SourceDigest != pass.ScopeDigest() {
			return "", nil, result, fmt.Errorf("POD membership source pagination changed")
		}
		for _, sn := range page.DeviceSNs {
			if sn == "" || seenSN[sn] {
				return "", nil, result, fmt.Errorf("POD membership duplicate device SN across pages")
			}
			seenSN[sn] = true
		}
		result.Skipped += page.Skipped
		for k, n := range page.Reasons {
			result.Reasons[k] += n
		}
		encoded, err := json.Marshal([]any{start, page.NextOffset, page.Total, page.Done, page.DeviceSNs, page.Skipped, page.Reasons, page.SourceDigest})
		if err != nil {
			return "", nil, result, err
		}
		digest.Write(encoded)
		for _, candidate := range page.Relations {
			candidate.CreatedAt, candidate.SyncedAt = snapshot, snapshot
			relation, err := model.NormalizeRelation(candidate)
			if err != nil {
				return "", nil, result, err
			}
			if relation.Identity.SourceID != source.ID || relation.Identity.EdgeType != model.EdgeSpatial || relation.Identity.Kind != model.RelationMemberOf || relation.Identity.From.EntityType != model.EntityDevice || relation.Identity.To.EntityType != model.EntityPod {
				return "", nil, result, fmt.Errorf("POD membership invalid relation scope")
			}
			pair := [2]model.EntityIdentity{relation.Identity.From, relation.Identity.To}
			plane := relation.Properties["plane"].(int64)
			if previous, ok := planes[pair]; ok && previous != plane {
				return "", nil, result, fmt.Errorf("POD membership conflicting planes for same endpoints")
			}
			planes[pair] = plane
			id, err := relation.Identity.ID()
			if err != nil {
				return "", nil, result, err
			}
			digest.Write([]byte(id))
			if seenRelations[id] {
				continue
			}
			seenRelations[id] = true
			if retain {
				relations = append(relations, relation)
			}
		}
		if err := ctx.Err(); err != nil {
			return "", nil, result, err
		}
		if page.Done {
			break
		}
		start = page.NextOffset
	}
	return hex.EncodeToString(digest.Sum(nil)), relations, result, nil
}
