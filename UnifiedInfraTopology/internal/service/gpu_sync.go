package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"UnifiedInfraTopology/pkg/log"
	"go.uber.org/zap"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type GPUSyncResult struct {
	Nodes, Edges, Objects, Skipped, MissingEndpoints int
	Reasons                                          map[string]int
	ByKind                                           map[string]int
}

type syncRunIDKey struct{}

func WithSyncRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, syncRunIDKey{}, runID)
}

// GPUSyncError 保留内部错误链，公开消息只包含固定阶段与错误码。
type GPUSyncError struct {
	Stage   string
	Code    string
	Details map[string]any
	cause   error
}

func (e *GPUSyncError) Error() string {
	return e.Code + " (stage=" + e.Stage + "; partial graph writes may persist)"
}
func (e *GPUSyncError) Unwrap() error { return e.cause }

func SyncFailureCategory(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, adapter.ErrCMDBConfig):
		return "cmdb_configuration"
	case errors.Is(err, adapter.ErrCMDBResponse):
		return "cmdb_response"
	case errors.Is(err, adapter.ErrCMDBRecord):
		return "cmdb_record"
	case errors.Is(err, errSyncSnapshotChanged):
		return "source_changed"
	default:
		return "dependency_or_validation"
	}
}

type GPUSyncWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
	logger *log.Logger
}

func (w *GPUSyncWorker) WithLogger(logger *log.Logger) *GPUSyncWorker {
	w.logger = logger
	return w
}

func NewGPUSyncWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *GPUSyncWorker {
	return &GPUSyncWorker{source: source, graph: graph}
}

// 单独要求图中真实 GPU 服务器分类，不依赖来源标签或普通服务器上联检查。
type gpuParentReader interface {
	ParentGPUDeviceValid(context.Context, model.EntityIdentity) (bool, error)
}

// Run 不清理 IDC 子集，旧 contains_gpu 在 GPU 移动后可能保留，明确记录该限制。
func (w *GPUSyncWorker) Run(ctx context.Context, source model.Source, snapshot time.Time) (result GPUSyncResult, runErr error) {
	result = GPUSyncResult{Reasons: map[string]int{}, ByKind: map[string]int{}}
	started, stageStarted := time.Now(), time.Now()
	stage := "source_collect"
	runID, _ := ctx.Value(syncRunIDKey{}).(string)
	logUnmatched := func(record adapter.GPUUnmatched) {
		if w == nil || w.logger == nil {
			return
		}
		w.logger.Warn("GPU uplink unmatched", zap.String("run_id", runID), zap.String("source_id", source.ID), zap.String("server_sn", record.ServerSN), zap.String("gpu_sn", record.GPUSN), zap.String("gpu_port", record.GPUPort), zap.String("tor_sn", record.ToRSN), zap.String("tor_port", record.ToRPort), zap.String("raw_tor_port", record.RawToRPort), zap.String("source_relation_id", record.UUID), zap.String("reason", record.Reason))
	}
	logStage := func(name string, since time.Time) {
		if w == nil || w.logger == nil {
			return
		}
		fields := []zap.Field{zap.String("run_id", runID), zap.String("source_id", source.ID), zap.String("resource", "gpu_sync"), zap.String("stage", name), zap.Float64("duration_ms", float64(time.Since(since))/float64(time.Millisecond)), zap.Int("objects", result.Objects), zap.Int("nodes", result.Nodes), zap.Int("edges", result.Edges), zap.Int("skipped", result.Skipped)}
		if runErr != nil {
			fields = append(fields, zap.String("error_category", SyncFailureCategory(runErr)))
		}
		w.logger.Info("GPU sync stage", fields...)
	}
	nextStage := func(next string) { logStage(stage, stageStarted); stage, stageStarted = next, time.Now() }
	defer func() {
		if runErr != nil {
			var failure *GPUSyncError
			if !errors.As(runErr, &failure) {
				runErr = &GPUSyncError{Stage: stage, Code: "gpu_" + stage + "_failed", cause: runErr}
			}
			errors.As(runErr, &failure)
			if failure.Details == nil {
				failure.Details = map[string]any{}
			}
			failure.Details["run_id"] = runID
			failure.Details["nodes"], failure.Details["edges"] = result.Nodes, result.Edges
			failure.Details["objects"], failure.Details["skipped"], failure.Details["missing_endpoints"] = result.Objects, result.Skipped, result.MissingEndpoints
			if w != nil && w.logger != nil {
				w.logger.Error("GPU sync failed", zap.String("run_id", runID), zap.String("source_id", source.ID), zap.String("resource", "gpu_sync"), zap.String("stage", failure.Stage), zap.String("error_code", failure.Code), zap.String("error_category", SyncFailureCategory(runErr)), zap.Any("details", failure.Details), zap.Float64("duration_ms", float64(time.Since(stageStarted))/float64(time.Millisecond)))
			}
		}
		logStage(stage, stageStarted)
		logStage("complete", started)
	}()
	progress := func() {
		if w != nil && w.logger != nil && (result.Nodes+result.Edges)%1000 == 0 {
			w.logger.Info("GPU graph write progress", zap.String("run_id", runID), zap.String("source_id", source.ID), zap.String("resource", "gpu_sync"), zap.String("stage", stage), zap.Int("graph_records", result.Nodes+result.Edges), zap.Int("nodes", result.Nodes), zap.Int("edges", result.Edges), zap.Float64("duration_ms", float64(time.Since(stageStarted))/float64(time.Millisecond)))
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w == nil || w.source == nil || w.graph == nil || snapshot.IsZero() {
		return result, fmt.Errorf("GPU sync requires collector, graph and nonzero snapshot")
	}
	if w.logger != nil {
		w.logger.Info("GPU sync starting", zap.String("run_id", runID), zap.String("source_id", source.ID), zap.String("resource", "gpu_sync"), zap.String("stage", "source_collect"), zap.Float64("duration_ms", 0))
	}
	collected, err := w.source.CollectGPU(ctx, source)
	result.Objects = collected.Objects
	result.Skipped = collected.Skipped
	for reason, count := range collected.Reasons {
		result.Reasons[reason] = count
	}
	for _, record := range collected.Unmatched {
		logUnmatched(record)
	}
	if err != nil {
		return result, err
	}
	nextStage("endpoint_check")
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
			logUnmatched(adapter.GPUUnmatched{ServerSN: card.ServerSN, GPUSN: card.Identity.StableID, Reason: "gpu_parent_missing_or_invalid"})
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
			gpuPort, _ := candidate.Properties["gpu_port"].(string)
			torPort, _ := candidate.Properties["tor_port"].(string)
			logUnmatched(adapter.GPUUnmatched{ServerSN: candidate.ServerSN, GPUSN: candidate.Identity.From.StableID, GPUPort: gpuPort, ToRSN: candidate.ToRSN, ToRPort: torPort, RawToRPort: candidate.RawToRPort, UUID: candidate.Identity.SourceRelationID, Reason: "gpu_uplink_endpoint_missing_or_invalid"})
			continue
		}
		relation := model.Relation{Identity: candidate.Identity, Properties: candidate.Properties, CreatedAt: snapshot, SyncedAt: snapshot}
		if err := model.ValidateRelation(relation); err != nil {
			return result, err
		}
		links = append(links, relation)
	}
	// 所有来源、分类、接口归属和关系校验完成后才允许首笔图写入。
	nextStage("node_write")
	for _, card := range cards {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		vertex := repository.TopologyVertex{Identity: card.Identity, Properties: card.Properties, CreatedAt: snapshot, SyncedAt: snapshot}
		if err := w.graph.UpsertVertex(ctx, vertex); err != nil {
			return result, fmt.Errorf("GPU node write failed (earlier writes may persist): %w", err)
		}
		result.Nodes++
		progress()
	}
	// contains_gpu 必须先成功写入，随后才允许发布该卡上联。
	for index, relations := range [][]model.Relation{contains, links} {
		if index == 0 {
			nextStage("containment_write")
		} else {
			nextStage("uplink_write")
		}
		for _, relation := range relations {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := w.graph.UpsertRelation(ctx, relation); err != nil {
				return result, fmt.Errorf("GPU relation write failed (earlier writes may persist): %w", err)
			}
			result.Edges++
			result.ByKind[string(relation.Identity.Kind)]++
			progress()
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}
