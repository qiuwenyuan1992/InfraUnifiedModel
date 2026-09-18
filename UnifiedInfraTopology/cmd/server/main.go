package main

import (
	"context"
	"flag"
	"fmt"

	"UnifiedInfraTopology/cmd/server/wire"
	"UnifiedInfraTopology/pkg/config"
	"UnifiedInfraTopology/pkg/log"
	"go.uber.org/zap"
)

// @title           UnifiedInfraTopology API
// @version         1.0.0
// @description     Infrastructure inventory and synchronization control API. Generated documentation currently covers user endpoints only; see README for inventory contracts.
// @license.name    MIT
// @BasePath        /v1
// @securityDefinitions.apiKey Bearer
// @in header
// @name Authorization
func main() {
	var envConf = flag.String("conf", "config/local.yml", "config path, eg: -conf ./config/local.yml")
	flag.Parse()
	conf := config.NewConfig(*envConf)

	logger := log.NewLog(conf)

	app, cleanup, err := wire.NewWire(conf, logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()
	logger.Info("server start", zap.String("host", fmt.Sprintf("http://%s:%d", conf.GetString("http.host"), conf.GetInt("http.port"))))
	logger.Info("docs addr", zap.String("addr", fmt.Sprintf("http://%s:%d/swagger/index.html", conf.GetString("http.host"), conf.GetInt("http.port"))))
	if err = app.Run(context.Background()); err != nil {
		panic(err)
	}
}
