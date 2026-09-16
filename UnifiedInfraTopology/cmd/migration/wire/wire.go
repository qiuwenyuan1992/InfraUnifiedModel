//go:build wireinject
// +build wireinject

package wire

import (
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/server"
	"UnifiedInfraTopology/pkg/log"
	"github.com/google/wire"
	"github.com/spf13/viper"
)

var repositorySet = wire.NewSet(
	repository.NewDB,
)
var serverSet = wire.NewSet(
	server.NewMigrateServer,
)

func NewWire(*viper.Viper, *log.Logger) (*server.MigrateServer, func(), error) {
	panic(wire.Build(
		repositorySet,
		serverSet,
	))
}
