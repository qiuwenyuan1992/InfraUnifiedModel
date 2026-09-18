package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"UnifiedInfraTopology/cmd/worker/wire"
	"UnifiedInfraTopology/pkg/config"
	"UnifiedInfraTopology/pkg/log"
)

func main() {
	path := flag.String("conf", "config/local.yml", "configuration file; APP_CONF takes precedence")
	flag.Parse()
	conf := config.NewConfig(*path)
	logger := log.NewLog(conf)
	defer func() { _ = logger.Sync() }()
	w, err := wire.NewWire(logger)
	if err != nil {
		logger.Error("worker initialization failed")
		os.Exit(1)
	}
	// 独立管理信号，避免依赖 HTTP 生命周期或连接数据库。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := w.Run(ctx); err != nil {
		logger.Error("worker stopped with an error")
		os.Exit(1)
	}
}
