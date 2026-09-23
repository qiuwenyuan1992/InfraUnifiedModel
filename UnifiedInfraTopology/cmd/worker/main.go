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

type workerRunner interface {
	Run(context.Context) error
	ProcessNext(context.Context) (bool, error)
}

func main() {
	path := flag.String("conf", "config/local.yml", "configuration file; APP_CONF takes precedence")
	once := flag.Bool("once", false, "process at most one queued task and exit")
	flag.Parse()
	conf := config.NewConfig(*path)
	logger := log.NewLog(conf)
	defer func() { _ = logger.Sync() }()
	w, cleanup, err := wire.NewWire(conf, logger)
	if err != nil {
		logger.Error("worker initialization failed")
		os.Exit(1)
	}
	defer cleanup()
	// 独立管理信号，避免依赖 HTTP 生命周期。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runWorker(ctx, w, *once); err != nil {
		logger.Error("worker stopped with an error")
		os.Exit(1)
	}
}

func runWorker(ctx context.Context, worker workerRunner, once bool) error {
	if once {
		_, err := worker.ProcessNext(ctx)
		return err
	}
	return worker.Run(ctx)
}
