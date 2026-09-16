package main

import (
	"UnifiedInfraTopology/cmd/migration/wire"
	"UnifiedInfraTopology/pkg/config"
	"UnifiedInfraTopology/pkg/log"
	"context"
	"flag"
)

func main() {
	var envConf = flag.String("conf", "config/local.yml", "config path, eg: -conf ./config/local.yml")
	flag.Parse()
	conf := config.NewConfig(*envConf)

	logger := log.NewLog(conf)

	migrateServer, cleanup, err := wire.NewWire(conf, logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()
	if err = migrateServer.Start(context.Background()); err != nil {
		panic(err)
	}
}
