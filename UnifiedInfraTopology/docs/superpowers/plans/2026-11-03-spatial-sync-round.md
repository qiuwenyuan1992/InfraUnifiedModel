# Spatial Relationships and Unified Sync Implementation Plan

> For implementers: execute with test-driven development and independent final review. No commits or live writes without approval.

**Goal:** Publish the three approved spatial relationships and provide a sequential, independently verified sync round.
**Architecture:** Adapter collects and resolves existing references; service prechecks endpoints and writes edges only. CLI reuses standalone tasks with a shared snapshot propagated through context while lease clocks remain real-time.
**Tech Stack:** Go, Viper, GORM/MySQL, NebulaGraph.
**Spec:** `docs/superpowers/specs/2026-11-02-spatial-sync-round-design.md`, approved by user.

## Global Constraints
- Existing identities, 10 Tags, 4 edge types; no DDL, inferred relations, placeholder nodes or automatic cleanup.
- No numeric cabinet identity fallback, singular cabinet pod_id fallback or guessed pod_ids encodings.
- Same UTC microsecond timestamp across round writes/checkpoints/verification; task/lease timestamps remain real-time.
- Existing dirty files belong to prior work and must be preserved. Do not commit.

## Review Focus
- Missing directory entries and duplicate UUID/ID mappings must be diagnosed, never arbitrarily resolved.
- Scope conditions must remain compatible and must not silently widen collection.
- Directory/ref changes between independent passes must prevent success.
- Shared timestamp must not leak into lease renewal or mutate StartedAt.
- Failed stages must stop the round and report not-executed stages, preserving earlier writes.

## Task 1: Spatial adapter and edge worker
Files: new `internal/adapter/cmdb_spatial.go`, `cmdb_spatial_test.go`; new `internal/service/spatial_sync.go`, `spatial_sync_test.go`.
Interfaces: `(*CMDB).CollectSpatial(context.Context, model.Source) (SpatialCollection,error)`; collection fields `Relations []model.Relation`, `Objects,Skipped int`, `Reasons map[string]int`, `SourceDigest string`.
Worker: `NewSpatialSyncWorker(*adapter.CMDB, repository.TopologyGraphRepository) *SpatialSyncWorker`; `Run(context.Context,model.Source,time.Time) (SpatialSyncResult,error)`; result `Edges,Objects,Skipped,MissingEndpoints int; Reasons map[string]int`.
- [x] Write failing tests for all 3 combinations, independent resolution, absent/duplicate/malformed references, pagination and scope checks.
- [x] Implement complete collect-first pass with deterministic identity resolution/digest and no graph writes.
- [x] Write failing worker tests: precheck-before-write, no nodes, missing endpoints, errors and changed reread.
- [x] Implement normalized/deduplicated edges, cached endpoint checks, separate source reread; run adapter/service tests.

## Task 2: Shared round snapshot without fake lease time
Files: new `internal/service/sync_snapshot.go/tests`; modify `internal/service/sync_worker.go`, `cmd/worker/server_uplink.go` and tests.
Interfaces: `WithSyncSnapshot(context.Context,time.Time) context.Context`; `SyncSnapshot(context.Context,time.Time) time.Time` returns injected T or normalized fallback.
- [x] Test injected UTC microsecond T and legacy fallback plus real claim/lease behavior.
- [x] Replace graph/checkpoint snapshot selection only; preserve StartedAt and real-time renewal/transitions.
- [x] Run worker and CLI execution regressions.

## Task 3: Spatial CLI and graph verification
Files: new `cmd/worker/spatial_sync.go/tests`; modify `cmd/worker/pod_membership.go`, `internal/repository/inventory_checkpoints.go/tests`, `cmd/worker/main.go`.
Interfaces: `runSpatialSync(ctx,conf,logger,sourceID,output) error` and `verifySpatialSync(ctx,conf,sourceID,runID,output) error`, matching the existing separate sync/verify entrypoints. Exact typed signatures are in `cmd/worker/spatial_sync.go`.
- [x] Add failing resource/argument/execution/verifier tests.
- [x] Add `spatial_relations` checkpoint mapping and standalone adapter using edge count.
- [x] Verify all 3 current-snapshot combinations by source, kind, VID, rank, unique endpoints and timestamps; compare per-kind counts.
- [x] Wire mutually exclusive `-sync-spatial`, `-verify-spatial` flags.

## Task 4: Unified entrypoint
Files: new `cmd/worker/sync_round.go/tests`; modify `cmd/worker/main.go`, `docs/cmdb-sync/README.md`.
- [x] Test exact phase ordering, isolated Viper configuration, shared T, stop-on-error and not-run reporting with injected callbacks (no live dependencies).
- [x] Execute data_center,cabinet,device,transformer,ups_group,ups,rpp,pod,interface then power_relations,pod_membership,spatial_relations,gpu_sync,server_uplinks,links_to; each standalone stage followed by existing independent verification.
- [x] Add `-sync-round` exclusive mode with source validation and structured phase report. Do not freeze clocks or share mutable stage overrides.

## Task 5: Verification and live evidence
- [x] Re-read changed runtime files, independent review, full tests/race/vet/three builds/diff checks.
- [x] Explicitly approved spatial write + independent verification; report missing/skipped source references.
- [x] Explicitly approved unified round, per-stage checks and final graph counts; report exclusions and partial-write semantics.
- [x] Update design/plan and evidence; no commit.

## Execution Ledger

- Task1 RED: missing collector/worker APIs; targeted and race GREEN. Review found unchecked device response filters; four cross-IDC/category regression tests reproduced RED, then requested authoritative idc_id and enforced configured filters, GREEN including live double reread.
- Task2 RED: missing snapshot helpers and resumed checkpoint mismatches. GREEN covers old T with real StartedAt/lease renewals and strict standalone/explicit verification; checkpoints reject wrong T before writes.
- Task3 RED→GREEN: three typed paths, malformed/missing/ambiguous endpoint checks, resource mapping including zero/nonzero checkpoints. Independent source/cabinet/POD identities and rank verification maintained.
- Task4 RED→GREEN: ordered 15 phases, config isolation, failure stop and not-run report; failed stage without emitted JSON retains only safe run ID from sanitized error, never full error text.
- Independent task and final reviews PASS. Full tests, affected-package race, vet, three entrypoint builds and diff checks PASS after final code changes.
- Approved spatial publish `64b6cb148c7f9fe29791b64a8465f0eb` produced 5636 verified edges: device→cabinet2407, cabinet→IDC2573, cabinet→POD656; missing endpoints0. Empty references1994 diagnosed.
- Approved unified round completed15stages with T=`2026-11-03T03:00:24.269220Z`, saved to `reports/idc238-sync-round-2026-11-03.json`; separate read-only process verified all15runIDs/checkpoints/paths/sharedT and released leases. Public `-verify-spatial -snapshot-at` CLI also PASS.
- Current counts: devicePOD1445, GPU1289/contains1289/uplink455, serveruplink1661, power5438, physicallinks3026. Physicallinks retains60endpoint-mismatch candidates; missing-SN/conflicting-SN/PODplane/invalidpower references remain explicit diagnostics. No cleanup, DDL, rollback, commits or inferred relationships introduced.
