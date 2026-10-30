package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"UnifiedInfraTopology/cmd/worker/wire"
	"UnifiedInfraTopology/internal/model"
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
	verify := flag.Bool("verify", false, "read-only MySQL and graph verification; omit run-id for preflight")
	syncFilteredRun := flag.Bool("sync-filtered", false, "enqueue and process one selected filtered entity, retain data, then verify persistence")
	syncUplink := flag.Bool("sync-server-uplink", false, "enqueue and process server uplinks for one source, retain existing graph data")
	verifyUplink := flag.Bool("verify-server-uplink", false, "read-only server uplink verification; omit run-id for preflight")
	sourceID := flag.String("source-id", "", "source ID for explicit sync or verification")
	entity := flag.String("entity-type", "", "device, data_center, cabinet, interface, or transformer for verification or filtered sync")
	runID := flag.String("run-id", "", "completed run ID for read-only persistence verification")
	flag.Parse()
	if !validWorkerModes(*verify, *syncFilteredRun, *once, *syncUplink, *verifyUplink, *sourceID, *entity, *runID) {
		fmt.Fprintln(os.Stderr, "invalid combination of worker modes and selection flags")
		os.Exit(2)
	}
	conf := config.NewConfig(*path)
	logger := log.NewLog(conf)
	defer func() { _ = logger.Sync() }()
	if *syncUplink || *verifyUplink {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		var err error
		if *syncUplink {
			err = runServerUplink(ctx, conf, logger, *sourceID, os.Stdout)
		} else {
			err = verifyServerUplink(ctx, conf, *sourceID, *runID, os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *verify || *syncFilteredRun {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		id := *runID
		var err error
		if *syncFilteredRun {
			id, err = syncFiltered(ctx, conf, logger, *sourceID, model.EntityType(*entity), os.Stdout)
		}
		if err == nil {
			err = verifyPersistence(ctx, conf, *sourceID, model.EntityType(*entity), id, os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
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

func validWorkerModes(verify, filtered, once, syncUplink, verifyUplink bool, source, entity, run string) bool {
	modes := 0
	for _, enabled := range []bool{verify, filtered, once, syncUplink, verifyUplink} {
		if enabled {
			modes++
		}
	}
	if modes > 1 || ((filtered || syncUplink) && run != "") {
		return false
	}
	if syncUplink || verifyUplink {
		return source != "" && entity == ""
	}
	return verify || filtered || (source == "" && entity == "" && run == "")
}

func runWorker(ctx context.Context, worker workerRunner, once bool) error {
	if once {
		_, err := worker.ProcessNext(ctx)
		return err
	}
	return worker.Run(ctx)
}
