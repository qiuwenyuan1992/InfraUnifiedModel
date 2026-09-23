//go:build wireinject
// +build wireinject

package wire

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"github.com/google/wire"
	"github.com/spf13/viper"
)

func provideSyncRunRepository(repo repository.InventoryRepository) service.SyncRunRepository {
	return repo
}

func provideDeviceGraphRepository(repo repository.TopologyGraphRepository) service.DeviceGraphRepository {
	return repo
}

func NewWire(*viper.Viper, *log.Logger) (*service.SyncWorker, func(), error) {
	panic(wire.Build(
		repository.NewDB,
		repository.NewRepository,
		repository.NewInventoryRepository,
		provideSyncRunRepository,
		repository.NewTopologyGraphRepository,
		provideDeviceGraphRepository,
		adapter.NewCMDB,
		wire.Bind(new(service.DeviceSource), new(*adapter.CMDB)),
		service.NewSyncWorker,
	))
}
