package router

import (
	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
)

type RouterDeps struct {
	Logger           *log.Logger
	Config           *viper.Viper
	JWT              *jwt.JWT
	UserHandler      *handler.UserHandler
	InventoryHandler *handler.InventoryHandler
}
