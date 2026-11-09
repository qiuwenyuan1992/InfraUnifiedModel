package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type PowerSyncResult struct {
	Edges, Objects, Skipped, MissingEndpoints int
	Reasons                                   map[string]int
	ByKind                                    map[string]int
}

type PowerSyncWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
}

func NewPowerSyncWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *PowerSyncWorker {
	return &PowerSyncWorker{source: source, graph: graph}
}

var powerEntityTypes = []model.EntityType{model.EntityCabinet, model.EntityRPP, model.EntityUPS, model.EntityUPSGroup, model.EntityTransformer}

type powerRecords struct {
	records    []adapter.EntityRecord
	byInstance map[int64][]adapter.EntityRecord
	identities map[string]int
}

type powerLink struct {
	field, path string
	target      model.EntityType
	kind        model.RelationKind
}

// Run 仅写入已有端点间的关系；复读来源不一致时报告已写入边可能保留。
func (w *PowerSyncWorker) Run(ctx context.Context, source model.Source, snapshot time.Time) (PowerSyncResult, error) {
	result := PowerSyncResult{Reasons: map[string]int{}, ByKind: map[string]int{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w == nil || w.source == nil || w.graph == nil || snapshot.IsZero() {
		return result, fmt.Errorf("power sync requires collector, graph and nonzero snapshot")
	}
	if _, err := (model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityCabinet, StableID: "scope"}).CanonicalJSON(); err != nil {
		return result, err
	}
	for _, entity := range powerEntityTypes {
		full, err := w.source.CoversFullSource(source, entity)
		if err != nil {
			return result, err
		}
		if full {
			return result, fmt.Errorf("power sync requires configured %s scope condition", entity)
		}
	}
	_, records, objects, err := w.collect(ctx, source)
	result.Objects = objects
	if err != nil {
		return result, err
	}
	candidates, err := powerCandidates(ctx, source, snapshot, records, &result)
	if err != nil {
		return result, err
	}
	existing := map[model.EntityIdentity]bool{}
	checked := map[model.EntityIdentity]bool{}
	valid := make([]model.Relation, 0, len(candidates))
	for _, relation := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		missing := false
		for _, endpoint := range []model.EntityIdentity{relation.Identity.From, relation.Identity.To} {
			if !checked[endpoint] {
				exists, err := w.graph.VertexExists(ctx, endpoint)
				if err != nil {
					return result, fmt.Errorf("power sync endpoint read failed: %w", err)
				}
				checked[endpoint] = true
				existing[endpoint] = exists
			}
			if !existing[endpoint] {
				missing = true
			}
		}
		if missing {
			result.Skipped++
			result.MissingEndpoints++
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
			return result, fmt.Errorf("power sync edge write failed (earlier edges may persist): %w", err)
		}
		result.Edges++
		result.ByKind[string(relation.Identity.Kind)]++
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (w *PowerSyncWorker) collect(ctx context.Context, source model.Source) (string, map[model.EntityType]*powerRecords, int, error) {
	all := map[model.EntityType]*powerRecords{}
	digest := sha256.New()
	objects := 0
	for _, entity := range powerEntityTypes {
		data := &powerRecords{byInstance: map[int64][]adapter.EntityRecord{}, identities: map[string]int{}}
		total := -1
		for start := 0; ; {
			if err := ctx.Err(); err != nil {
				return "", nil, objects, err
			}
			page, err := w.source.CollectEntityPage(ctx, source, entity, start)
			if err != nil {
				return "", nil, objects, err
			}
			if total < 0 {
				total = page.Total
			}
			if total != page.Total || page.NextOffset != start+len(page.Records) || page.NextOffset > total || page.Done != (page.NextOffset == total) || !page.Done && page.NextOffset <= start {
				return "", nil, objects, fmt.Errorf("power sync %s pagination changed", entity)
			}
			data.records = append(data.records, page.Records...)
			objects += len(page.Records)
			if page.Done {
				break
			}
			start = page.NextOffset
		}
		// 摘要与 HTTP 页边界无关；相同数字 ID 的异常记录也确定性排序。
		encoded := make(map[int]string, len(data.records))
		for i, record := range data.records {
			body, err := json.Marshal(record)
			if err != nil {
				return "", nil, objects, err
			}
			encoded[i] = string(body)
		}
		order := make([]int, len(data.records))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(i, j int) bool {
			a, b := data.records[order[i]], data.records[order[j]]
			if a.SourceInstanceID != b.SourceInstanceID {
				return a.SourceInstanceID < b.SourceInstanceID
			}
			return encoded[order[i]] < encoded[order[j]]
		})
		sorted := make([]adapter.EntityRecord, 0, len(order))
		header, _ := json.Marshal([]any{source.ID, source.ConfigRef, entity, total})
		digest.Write(header)
		for _, i := range order {
			if err := ctx.Err(); err != nil {
				return "", nil, objects, err
			}
			record := data.records[i]
			sorted = append(sorted, record)
			digest.Write([]byte(encoded[i]))
			data.byInstance[record.SourceInstanceID] = append(data.byInstance[record.SourceInstanceID], record)
			data.identities[record.Identity]++
		}
		data.records = sorted
		all[entity] = data
	}
	return hex.EncodeToString(digest.Sum(nil)), all, objects, nil
}

func powerReference(value any) (int64, string) {
	if value == nil {
		return 0, "empty_reference"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, "invalid_reference"
	}
	id, err := strconv.ParseInt(string(encoded), 10, 64)
	if err != nil || id <= 0 {
		return 0, "invalid_reference"
	}
	return id, ""
}

func (p *powerRecords) unique(record adapter.EntityRecord) bool {
	return record.SourceInstanceID > 0 && strings.TrimSpace(record.Identity) != "" && record.Identity == strings.TrimSpace(record.Identity) && len(p.byInstance[record.SourceInstanceID]) == 1 && p.identities[record.Identity] == 1
}

func (p *powerRecords) resolve(value any) (adapter.EntityRecord, string) {
	id, reason := powerReference(value)
	if reason != "" {
		return adapter.EntityRecord{}, reason
	}
	records := p.byInstance[id]
	if len(records) == 0 {
		return adapter.EntityRecord{}, "missing_reference"
	}
	if len(records) != 1 || !p.unique(records[0]) {
		return adapter.EntityRecord{}, "ambiguous_reference"
	}
	return records[0], ""
}

func powerCandidates(ctx context.Context, source model.Source, snapshot time.Time, all map[model.EntityType]*powerRecords, result *PowerSyncResult) ([]model.Relation, error) {
	candidates := map[string]model.Relation{}
	diagnostic := func(reason string) {
		result.Reasons[reason]++
		if reason != "empty_reference" && reason != "conflict" {
			result.Skipped++
		}
	}
	for _, entity := range powerEntityTypes {
		data := all[entity]
		for _, record := range data.records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !data.unique(record) {
				diagnostic("ambiguous_identity")
				continue
			}
			refs := []powerLink{}
			switch entity {
			case model.EntityCabinet:
				refs = append(refs, powerLink{"row_switch_id_a", "A", model.EntityRPP, model.RelationPowerUpstream}, powerLink{"row_switch_id_b", "B", model.EntityRPP, model.RelationPowerUpstream})
			case model.EntityRPP:
				refs = append(refs, powerLink{"ups_group_id", "", model.EntityUPSGroup, model.RelationPowerUpstream})
			case model.EntityUPS:
				refs = append(refs, powerLink{"ups_group_id", "", model.EntityUPSGroup, model.RelationMemberOf})
			case model.EntityUPSGroup:
				refs = append(refs, powerLink{"transformer_id_up", "", model.EntityTransformer, model.RelationPowerUpstream})
			case model.EntityTransformer:
				refs = append(refs, powerLink{"standby_transformer_id", "", model.EntityTransformer, model.RelationHasStandby})
			}
			for _, ref := range refs {
				target, reason := all[ref.target].resolve(record.Properties[ref.field])
				if reason != "" {
					diagnostic(reason)
					continue
				}
				if entity == model.EntityRPP || entity == model.EntityUPS {
					field := "transformer_id_up"
					if entity == model.EntityRPP {
						field = "transformer_group_a_id"
					}
					deviceID, deviceReason := powerReference(record.Properties[field])
					groupID, groupReason := powerReference(target.Properties["transformer_id_up"])
					// 设备级引用只校验，不改变正确解析的编组和主链事实。
					if deviceReason == "invalid_reference" {
						result.Reasons["invalid_consistency_reference"]++
					}
					if deviceReason == "" && groupReason == "" && deviceID != groupID {
						diagnostic("conflict")
					}
				}
				from := model.EntityIdentity{SourceID: source.ID, EntityType: entity, StableID: record.Identity}
				to := model.EntityIdentity{SourceID: source.ID, EntityType: ref.target, StableID: target.Identity}
				if ref.kind == model.RelationHasStandby && from == to {
					diagnostic("standby_self_reference")
					continue
				}
				relation := model.Relation{Identity: model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgePower, Kind: ref.kind, From: from, To: to}, CreatedAt: snapshot, SyncedAt: snapshot}
				if ref.path != "" {
					relation.Properties = map[string]any{"power_path": ref.path}
				}
				normalized, err := model.NormalizeRelation(relation)
				if err != nil {
					return nil, err
				}
				id, err := normalized.Identity.ID()
				if err != nil {
					return nil, err
				}
				candidates[id] = normalized
			}
		}
	}
	// 备用关系是配置事实；多台循环不表示主供电流向，自引用已在上方拒绝。
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	resultRelations := make([]model.Relation, 0, len(ids))
	for _, id := range ids {
		resultRelations = append(resultRelations, candidates[id])
	}
	return resultRelations, nil
}
