package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"UnifiedInfraTopology/internal/service"

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
	syncLinks := flag.Bool("sync-links-to", false, "synchronize physical network interface connections for one source")
	verifyLinks := flag.Bool("verify-links-to", false, "read-only physical connection verification")
	syncPower := flag.Bool("sync-power", false, "synchronize modeled power relationships")
	verifyPower := flag.Bool("verify-power", false, "read-only modeled power verification")
	syncPODMembership := flag.Bool("sync-pod-membership", false, "synchronize device POD membership for one source")
	verifyPODMembershipMode := flag.Bool("verify-pod-membership", false, "read-only POD membership verification; omit run-id for preflight")
	syncGPU := flag.Bool("sync-gpu", false, "synchronize GPUs and their ownership and uplinks for one source")
	verifyGPU := flag.Bool("verify-gpu", false, "read-only GPU verification; omit run-id for preflight")
	syncSpatial := flag.Bool("sync-spatial", false, "synchronize device/cabinet/room/RPP/IDC/POD spatial relations")
	verifySpatial := flag.Bool("verify-spatial", false, "read-only spatial relation verification")
	syncRound := flag.Bool("sync-round", false, "synchronize and verify all entity and relation phases sequentially")
	processRounds := flag.Bool("process-rounds", false, "process API-enqueued complete rounds; combine with once to process one round")
	snapshotAt := flag.String("snapshot-at", "", "expected shared round timestamp for completed-run read-only verification (RFC3339)")
	sourceID := flag.String("source-id", "", "source ID for explicit sync or verification")
	entity := flag.String("entity-type", "", "device, data_center, room, cabinet, interface, transformer, ups_group, ups, rpp, or pod for verification or filtered sync")
	runID := flag.String("run-id", "", "completed run ID for read-only persistence verification")
	flag.Parse()
	roundSpatial := *syncRound || *syncSpatial || *verifySpatial
	validRoundSpatial := validRoundSpatialModes(*syncRound, *syncSpatial, *verifySpatial, []bool{*once, *verify, *syncFilteredRun, *syncUplink, *verifyUplink, *syncLinks, *verifyLinks, *syncPower, *verifyPower, *syncPODMembership, *verifyPODMembershipMode, *syncGPU, *verifyGPU}, *sourceID, *entity, *runID)
	baseCtx := context.Background()
	if *snapshotAt != "" {
		at, err := time.Parse(time.RFC3339Nano, *snapshotAt)
		readOnly := *verify || *verifyUplink || *verifyLinks || *verifyPower || *verifyPODMembershipMode || *verifyGPU || *verifySpatial
		if err != nil || at.IsZero() || at.After(time.Now()) || !readOnly || *runID == "" {
			fmt.Fprintln(os.Stderr, "snapshot-at requires a completed-run verification mode and a valid nonfuture RFC3339 timestamp")
			os.Exit(2)
		}
		baseCtx = service.WithSyncSnapshot(baseCtx, at)
	}
	standaloneTopology := *syncPODMembership || *verifyPODMembershipMode || *syncGPU || *verifyGPU
	validStandalone := validStandaloneTopologyModes(*syncPODMembership, *verifyPODMembershipMode, *syncGPU, *verifyGPU, []bool{*once, *verify, *syncFilteredRun, *syncUplink, *verifyUplink, *syncLinks, *verifyLinks, *syncPower, *verifyPower}, *sourceID, *entity, *runID)
	validQueuedRounds := validQueuedRoundModes(*processRounds, *once, []bool{*verify, *syncFilteredRun, *syncUplink, *verifyUplink, *syncLinks, *verifyLinks, *syncPower, *verifyPower, *syncPODMembership, *verifyPODMembershipMode, *syncGPU, *verifyGPU, *syncRound, *syncSpatial, *verifySpatial}, *sourceID, *entity, *runID, *snapshotAt)
	if !validQueuedRounds || (!*processRounds && (!validRoundSpatial || (!roundSpatial && (!validStandalone || (!standaloneTopology && (!validPowerModes(*once, *verify, *syncFilteredRun, *syncUplink, *verifyUplink, *syncLinks, *verifyLinks, *syncPower, *verifyPower, *sourceID, *entity, *runID) || (!*syncPower && !*verifyPower && (!validLinksToModes(*once, *verify, *syncFilteredRun, *syncUplink, *verifyUplink, *syncLinks, *verifyLinks, *sourceID, *entity, *runID) || (!*syncLinks && !*verifyLinks && !validWorkerModes(*verify, *syncFilteredRun, *once, *syncUplink, *verifyUplink, *sourceID, *entity, *runID)))))))))) {
		fmt.Fprintln(os.Stderr, "invalid combination of worker modes and selection flags")
		os.Exit(2)
	}
	conf := config.NewConfig(*path)
	logger := log.NewLog(conf)
	defer func() { _ = logger.Sync() }()
	if *processRounds {
		worker, cleanup, err := newQueuedRoundWorker(conf, logger)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer cleanup()
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err = runWorker(ctx, worker, *once); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if roundSpatial {
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		var err error
		switch {
		case *syncRound:
			err = runSyncRound(ctx, conf, logger, *sourceID, os.Stdout)
		case *syncSpatial:
			err = runSpatialSync(ctx, conf, logger, *sourceID, os.Stdout)
		case *verifySpatial:
			err = verifySpatialSync(ctx, conf, *sourceID, *runID, os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if standaloneTopology {
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		var err error
		switch {
		case *syncPODMembership:
			err = runPODMembership(ctx, conf, logger, *sourceID, os.Stdout)
		case *verifyPODMembershipMode:
			err = verifyPODMembership(ctx, conf, *sourceID, *runID, os.Stdout)
		case *syncGPU:
			err = runGPUSync(ctx, conf, logger, *sourceID, os.Stdout)
		case *verifyGPU:
			err = verifyGPUSync(ctx, conf, *sourceID, *runID, os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *syncPower || *verifyPower {
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		var err error
		if *syncPower {
			err = runPowerSync(ctx, conf, logger, *sourceID, os.Stdout)
		} else {
			err = verifyPowerSync(ctx, conf, *sourceID, *runID, os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *syncLinks || *verifyLinks {
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		var err error
		if *syncLinks {
			err = runLinksTo(ctx, conf, logger, *sourceID, os.Stdout)
		} else {
			err = verifyLinksTo(ctx, conf, *sourceID, *runID, os.Stdout)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *syncUplink || *verifyUplink {
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
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
		ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
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
	ctx, stop := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
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
