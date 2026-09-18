//go:build wireinject
// +build wireinject

package wire

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"github.com/google/wire"
)

// NewWire 不装配 HTTP、数据库、迁移或图客户端；基础版没有外部写入能力。
func NewWire(*log.Logger) (*service.SyncWorker, error) {
	panic(wire.Build(
		adapter.NewCMDB,
		wire.Bind(new(adapter.SourceAdapter), new(*adapter.CMDB)),
		service.NewSyncWorker,
	))
}
