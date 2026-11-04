package service

import (
	"context"
	"fmt"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type GPUSyncResult struct {
	Nodes, Edges, Objects, Skipped, MissingEndpoints int
	Reasons                                          map[string]int
	ByKind                                           map[string]int
}

type GPUSyncWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
}

func NewGPUSyncWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *GPUSyncWorker {
	return &GPUSyncWorker{source: source, graph: graph}
}

// 单独要求图中真实 GPU 服务器分类，不依赖来源标签或普通服务器上联检查。
type gpuParentReader interface {
	ParentGPUDeviceValid(context.Context, model.EntityIdentity) (bool, error)
}

// Run 不清理 IDC 子集，旧 contains_gpu 在 GPU 移动后可能保留，明确记录该限制。
func (w *GPUSyncWorker) Run(ctx context.Context, source model.Source, snapshot time.Time) (GPUSyncResult, error) {
	result := GPUSyncResult{Reasons: map[string]int{}, ByKind: map[string]int{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w == nil || w.source == nil || w.graph == nil || snapshot.IsZero() {
		return result, fmt.Errorf("GPU sync requires collector, graph and nonzero snapshot")
	}
	collected, err := w.source.CollectGPU(ctx, source)
	result.Objects = collected.Objects
	result.Skipped = collected.Skipped
	for reason, count := range collected.Reasons {
		result.Reasons[reason] = count
	}
	if err != nil {
		return result, err
	}
	result.Reasons["scoped_cleanup_disabled"] = 1
	result.MissingEndpoints = collected.Reasons["gpu_endpoint_not_unique"] + collected.Reasons["tor_reference_missing"] + collected.Reasons["tor_port_not_unique"]
	if len(collected.Cards) == 0 && len(collected.Servers) > 0 {
		result.Reasons["empty_gpu_cards"] = 1
	}
	if len(collected.Cards) > 0 {
		result.Reasons["gpu_move_old_contains_may_remain"] = 1
	}
	parentReader, ok := w.graph.(gpuParentReader)
	if !ok {
		return result, fmt.Errorf("GPU graph cannot verify parent device classification 58")
	}
	endpointReader, ok := w.graph.(repository.ServerUplinkEndpointReader)
	if !ok && len(collected.Uplinks) > 0 {
		return result, fmt.Errorf("GPU graph cannot verify ToR interface ownership")
	}
	parents := map[string]bool{}
	for _, sn := range collected.Servers {
		identity := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: sn}
		exists, err := w.graph.VertexExists(ctx, identity)
		if err != nil {
			return result, fmt.Errorf("GPU parent existence read failed: %w", err)
		}
		if exists {
			exists, err = parentReader.ParentGPUDeviceValid(ctx, identity)
			if err != nil {
				return result, fmt.Errorf("GPU parent classification read failed: %w", err)
			}
		}
		parents[sn] = exists
	}
	cards := []adapter.GPUCard{}
	contains := []model.Relation{}
	links := []model.Relation{}
	for _, card := range collected.Cards {
		if !parents[card.ServerSN] {
			result.Skipped++
			result.MissingEndpoints++
			result.Reasons["gpu_parent_missing_or_invalid"]++
			continue
		}
		cards = append(cards, card)
		relation := model.Relation{CreatedAt: snapshot, SyncedAt: snapshot, Identity: model.RelationIdentity{SourceID: source.ID, EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: card.ServerSN}, To: card.Identity}}
		if err := model.ValidateRelation(relation); err != nil {
			return result, err
		}
		contains = append(contains, relation)
	}
	for _, candidate := range collected.Uplinks {
		valid := parents[candidate.ServerSN]
		tor := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.ToRSN}
		if valid {
			for _, endpoint := range []model.EntityIdentity{tor, candidate.Identity.To} {
				exists, err := w.graph.VertexExists(ctx, endpoint)
				if err != nil {
					return result, fmt.Errorf("GPU uplink endpoint read failed: %w", err)
				}
				valid = valid && exists
			}
		}
		if valid {
			server := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.ServerSN}
			var err error
			valid, err = endpointReader.ServerUplinkEndpointsValid(ctx, server, tor, candidate.Identity.To)
			if err != nil {
				return result, fmt.Errorf("GPU uplink ownership read failed: %w", err)
			}
		}
		if !valid {
			result.Skipped++
			result.MissingEndpoints++
			result.Reasons["gpu_uplink_endpoint_missing_or_invalid"]++
			continue
		}
		relation := model.Relation{Identity: candidate.Identity, Properties: candidate.Properties, CreatedAt: snapshot, SyncedAt: snapshot}
		if err := model.ValidateRelation(relation); err != nil {
			return result, err
		}
		links = append(links, relation)
	}
	// 所有来源、分类、接口归属和关系校验完成后才允许首笔图写入。
	for _, card := range cards {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		vertex := repository.TopologyVertex{Identity: card.Identity, Properties: card.Properties, CreatedAt: snapshot, SyncedAt: snapshot}
		if err := w.graph.UpsertVertex(ctx, vertex); err != nil {
			return result, fmt.Errorf("GPU node write failed (earlier writes may persist): %w", err)
		}
		result.Nodes++
	}
	// contains_gpu 必须先成功写入，随后才允许发布该卡上联。
	for _, relations := range [][]model.Relation{contains, links} {
		for _, relation := range relations {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := w.graph.UpsertRelation(ctx, relation); err != nil {
				return result, fmt.Errorf("GPU relation write failed (earlier writes may persist): %w", err)
			}
			result.Edges++
			result.ByKind[string(relation.Identity.Kind)]++
		}
	}
	verified, err := w.source.CollectGPU(ctx, source)
	if err != nil {
		return result, fmt.Errorf("GPU verification failed (written graph persists): %w", err)
	}
	if verified.SourceDigest != collected.SourceDigest {
		return result, fmt.Errorf("GPU source changed between independent passes (written graph persists)")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}
