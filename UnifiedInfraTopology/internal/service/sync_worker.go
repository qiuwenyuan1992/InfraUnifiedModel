package service

import (
	"context"
	"errors"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/pkg/log"
)

var ErrSyncNotImplemented = errors.New("sync execution and publication are not implemented")

type SyncWorker struct {
	source adapter.SourceAdapter
	logger *log.Logger
}

func NewSyncWorker(source adapter.SourceAdapter, logger *log.Logger) *SyncWorker {
	return &SyncWorker{source: source, logger: logger}
}

// Run 只维护待机生命周期，不轮询、领取或修改同步任务。
func (w *SyncWorker) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	w.logger.Warn("worker standby: collection and publication are not implemented; no jobs will be claimed")
	<-ctx.Done()
	w.logger.Info("worker stopped")
	return nil
}

// Execute 保留业务编排入口；即使来源检查通过，也不能误报同步成功。
func (w *SyncWorker) Execute(ctx context.Context, source model.Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.source.Validate(ctx, source); err != nil {
		return err
	}
	return ErrSyncNotImplemented
}
