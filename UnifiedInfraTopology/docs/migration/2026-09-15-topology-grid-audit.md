# topology_grid migration: audit and modeling inputs

Date: 2026-09-15

Status: static audit complete; candidate model and migration sequence await design approval. No application code or production data has been migrated.

## 1. Scope and evidence

- Source: `/Users/qiuwenyuan/Documents/workspace/JD/topology_grid`.
- Source commit: `7cf91811533b58a6b9e0ed89a02adfe50457e0df` (2026-09-09).
- Target: `/Users/qiuwenyuan/Documents/workspace/AIgen/InfraUnifiedModel/UnifiedInfraTopology`.
- Model references: sibling `UnifiedInfrastructureModel/model4web/` documents.
- Source worktree was clean during inspection. Existing untracked target files were preserved.
- This audit inspected implementation and documentation; it did not run either application, sync jobs, live database queries, external APIs, or legacy tests.
- Production schemas, data volumes, consumers, external schedules, and performance remain unverified. Source locations below are relative to the source root unless explicitly marked target.
- Future product UI must use Vue 3, TypeScript, and Vite, not standalone HTML. Swagger documentation is not the product UI.

## 2. Executive assessment

The source is more than a synchronization service. It combines inventory ingestion, physical and logical graph construction, route materialization, topology/path queries, plan-versus-real reconciliation, monitoring, and operational controls.

The target is an initialized Nunu Advanced scaffold. Its user/auth example is not an infrastructure-domain implementation. Migration should preserve validated business semantics while replacing global state, import-time initialization, and unsafe publication patterns.

The existing infrastructure documents already distinguish source evidence from canonical assets and derived relationships. The legacy graph's IP-based identities cannot become the target's global asset keys.

## 3. Implemented capability inventory

| Capability | Source evidence | Migration treatment |
| --- | --- | --- |
| Process bootstrap | `cmd/main.go:11`; `module/init/init_log.go:13`; `module/init/init.go:15` | Replace import-time config/client/job initialization with explicit Nunu composition and lifecycle. |
| CMDB inventory refresh | `module/manager/sync_cmdb_devices/sync_cmdb_dev.go:67`; `model/mysql/cmdb_devices.go:3` | Read-only source adapter; preserve native IDs and management/out-of-band aliases. |
| DCI, logical-business, display refresh | `module/init/cron.go:39` | Separate source contracts from business grouping and UI display metadata. Verify pagination and empty results. |
| LLDP cache | `module/manager/public/init_load_lldp.go:22` | Preserve observations and endpoint-resolution results; do not equate every observation to a confirmed cable. |
| Physical/logical topology build | `module/manager/standard_layer/topology/topology.go:47`; `prepare_data.go` in the same directory | Migrate builders by entity/relation family with fixture-based comparison. |
| Route materialization | `module/manager/standard_layer/topology/build_route_v2.go:812`; `route_processing.go:40` in the same directory | Independent projection build over routes, ports, ARP, MAC, aggregation, and LLDP. |
| Graph/path queries | `module/manager/standard_layer/topology/route_path_from_db.go:360`; `module/manager/business/link_path.go:35` | Preserve distinct physical, logical, and routing semantics; specify bounds and incomplete-result behavior. |
| Server/container endpoint resolution | `module/manager/standard_layer/topology/worm_arp_api.go:71` | Dedicated upstream adapters with explicit unresolved states. |
| Plan-real comparison/application | `module/manager/operation_diff/diff_plan_real.go`; `module/manager/operation_diff/make-real-plan.go` | Retain as a separately inventoried capability. Require audited mutation and recovery semantics. |
| GPU/monitoring enrichment | `module/init/cron.go:184`; `model/prometheus/prometheus.go:40` | Separate enrichment from topology truth; carry metric windows and freshness. |
| Administrative controls | `module/web/router/topology.go:121`; `test/httputil/route_handler.go:31` | Move production behavior out of test packages; authenticate/authorize state-changing operations. |

The inventory records capabilities, not a claim that all legacy implementations are correct or that every capability belongs in the first delivery.

### Legacy data access — not target storage requirements

- MySQL supplies CMDB devices, ports, interface addresses/groups, hardware units, ARP, LLDP, MAC data, and 16 route shards. It also stores some derived logical topology and version/diff records.
- Nebula holds graph projections and plan/real topology.
- ClickHouse holds materialized port-route/VLAN data and telemetry.
- MongoDB supplies or stores CMDB/business views, endpoint data, and comparison results.
- Redis holds caches, some job coordination, and active-table selection.
- HTTP integrations supply CMDB/DCI, Worm ARP, CMP, and monitoring data.

These entries describe legacy code access, including legacy writes; they do not establish target ownership or deployment requirements. The user clarified that target-owned storage is MySQL and Nebula only. Other systems are upstream query/data sources, integrated only where needed. Do not migrate legacy ClickHouse/MongoDB writes or Redis coordination into additional target stores. Separate upstream read access from target-owned persistence, even when both use MySQL or Nebula.

## 4. Important existing workflows

### Inventory and topology

1. Startup loads clients and inventory-dependent caches.
2. Periodic refreshes replace device, DCI, business, and display maps. Production configuration specifies six-hour intervals for these refreshes.
3. LLDP is loaded at startup and through a manual reload; periodic LLDP refresh was not established by this audit.
4. A requested topology build combines inventory, interfaces, units, ARP, LLDP, and grouping data.
5. Builders write graph vertices/edges and selected SQL projections/version records.

The route-relation save phase inside topology preparation is commented out; do not assume a topology build also refreshes route projections.

### Routes and queries

1. A separate route build loads route shards and supporting interface/neighbor indexes.
2. It resolves routes into local/remote physical-port alternatives and writes route/VLAN projections.
3. Full and selected-device builds have different destructive/append behavior.
4. Queries resolve endpoint aliases, load route data, apply longest-prefix match, traverse candidate connectivity, expand skipped LLDP devices, and attach host/container endpoints.
5. Some query variants supplement missing connectivity using LLDP paths; that inference must remain distinguishable from route evidence.

The main HTTP API also dispatches by request `type`; matching URL paths alone does not establish compatibility (`module/manager/business/getter.go:111`).

## 5. Candidate domain mapping — not an approved schema

| Legacy concept | Candidate target concept | Required distinction |
| --- | --- | --- |
| Device graph vertex keyed by IP | `Device` + source identity + address aliases | Stable asset identity survives IP/name changes; IP reuse must not merge unrelated assets. |
| Device within a topology | `NetworkNode` scoped to a network/view | Network membership is not another copy of the physical asset. |
| Device-IP/interface-name port key | `Interface` / `TerminationPoint` + scoped source identity | Physical ports, logical interfaces, and aggregation membership are distinct. |
| Server vertex, ARP attachment | Shared `Device` + observed endpoint/attachment | IP/MAC observations alone may be insufficient to resolve an asset. |
| Hardware unit keyed by serial | `Component` owned by a device | Serial scope and missing/duplicate values require source rules. |
| Logical MD5 grouping node | `LogicalGroup` and versioned projection membership | Mutable grouping attributes cannot redefine physical asset identity. |
| LLDP/ARP/MAC rows | `Observation` with resolved/unresolved endpoints | Observed adjacency is not proof of physical cabling. |
| Raw route shard row | `RouteEntry` in an explicit routing scope | Prefix, next hop, protocol, address family, device and VRF scope belong together. Unknown VRF remains unknown. |
| ClickHouse port-route row | `RouteProjection` tied to a build generation | Derived forwarding candidates are not raw routes or guaranteed packet paths. |
| Plan/real graph and diff | `TopologyView`, `Comparison`, audited change request | Planned, observed, and manually confirmed assertions must not overwrite each other. |
| Global timestamp/table switch | `SyncRun`, source snapshot, `ProjectionGeneration` | Execution state, source completeness, and published read version are separate concerns. |
| Metric-enriched graph | Topology result plus telemetry annotations | Metrics carry their own time window and cannot silently change topology identity. |

### Shared model rules

Reuse `Device`, `Cabinet`, and `Room` from the cross-domain model baseline. Preserve `Module` as logical grouping rather than inserting it into a physical containment tree. The migration does not imply implementing power or all spatial capabilities immediately.

Keep stable internal entity IDs separate from `(source_system, source_object_type, source_id, scope)`. Store original identifiers and evidence references. Identity equivalence is an explicit resolution outcome, not a string-coincidence rule.

Keep source assertions separate from resolved canonical facts and derived projections. Capture observed/ingested times, evidence and resolution status, schema version, and derivation inputs where applicable. Unknown valid time stays unknown. Full historical reconstruction requires retained entity-property versions as well as relationship history.

Only a complete, successful snapshot or authoritative deletion event can authorize retirement. Missing pages, failed shards, authorization filtering, and source outages are not deletions. Full and incremental runs require distinct completeness and invalidation rules.

These rules follow `UnifiedInfrastructureModel/model4web/空间归属域.md:19–70` and `计算与主机接入域.md`. Target storage is MySQL and Nebula; schema allocation, indexing, retention, and performance commitments remain design decisions.

## 6. Migration risks that must not be copied

| Risk | Evidence | Required acceptance gate |
| --- | --- | --- |
| Explicit route destination ignored | `model/mysql/port_route_info.go:47` accepts `table` but writes to the global current table; route build separately truncates the requested destination. | A run writes only its immutable destination; readers cannot switch it mid-build. |
| Partial writes can precede stale-data deletion | Topology builder/publication code logs some write errors without propagating failure. | Failed or incomplete runs never publish or authorize stale-record retirement. |
| Overlapping mutable builds | Shared topology handlers/global build timestamp; route HTTP lock is process-local. | Specify cross-process ownership, cancellation and fencing before multi-instance sync. |
| Inconsistent projection switching | Route/VLAN selections switch independently; route caches lack generation identity. | A reader pins a coherent published generation; every cache key includes its relevant version. |
| Route workers can outlive failed savers | Bounded worker/saver channels and delayed error collection. | Failure cancels producers/consumers; no deadlock, unbounded goroutine, or false success. |
| Unbounded path behavior | Path-count argument is unused in inspected enumeration; constants alone do not enforce limits. | Test hop/path/work budgets, cancellation, and explicit truncation indicators. |
| Identity drift | IP/composite keys and mutable logical hashes in graph builders. | Fixtures for alias changes, IP reuse, duplicate identifiers, and unresolved joins. |
| Unprotected mutation/control endpoints | Main middleware lacks demonstrated authentication; some GET endpoints mutate state. | Read/write/admin authorization and audited mutation contracts. |
| Credential-bearing tracked configuration | Credential fields found in legacy configuration; token-bearing URL logging found in DCI adapter. | Never copy secrets or production endpoints into defaults/fixtures; use injected secret configuration and redact logs. Assess rotation separately. |
| Live side effects in tests/initialization | Source initialization opens clients; some tests invoke operational code. | Hermetic characterization tests with sanitized fixtures and fake adapters. |

Do not repair the old production system as an incidental part of migration. Confirm defects against controlled reproductions and implement the intended invariant in the target.

## 7. Target structure and boundaries

Prefer one Nunu Go module with separately deployable API and worker entrypoints. Split by business capability within the established layers rather than transplanting a generic `manager` package or introducing microservices immediately.

| Nunu location | Responsibility |
| --- | --- |
| `api/v1/` | Versioned contracts for assets, topology, sync runs and eventual comparison controls; no source database structs in responses. |
| `internal/model/` | Persistent identities, typed assets, assertions, run records and publication metadata after schema approval. |
| `internal/service/` | Identity resolution, validation, topology/query policies and synchronization orchestration; pure algorithms isolated from I/O. |
| `internal/repository/` | Target-owned MySQL persistence and Nebula graph access through injected interfaces. |
| `internal/integration/` (proposed) | Read-only source adapters and source-native DTOs; add only as sources migrate. |
| `internal/handler/`, `internal/router/` | Thin validated HTTP boundaries and authorization-aware routing. |
| `internal/job/`, `internal/task/` | Job invocation, scheduling and cancellation; not embedded business algorithms. |
| `cmd/server/`, `cmd/task/`, `cmd/migration/` | Independent composition, API service, sync worker, explicit schema migration. |
| `web/` (future) | Vue 3 + TypeScript + Vite product frontend, consuming versioned APIs. |

The current server Wire injector includes an example background job (`cmd/server/wire/wire.go:41–58`, target). That example must not become the real sync runner. Target-owned migrations must not alter upstream source schemas. Use versioned schema changes when actual domain persistence is introduced; do not treat the sample `AutoMigrate(User)` as a production migration strategy.

## 8. Approach options

1. **Copy/repackage the legacy modules.** Fastest mechanical transfer, but carries global state, implicit initialization, mixed storage ownership, unsafe publication and identity defects. Not recommended.
2. **Staged capability migration with characterized behavior. Recommended.** Establish identities and run/publication boundaries, migrate one vertical slice at a time, compare against sanitized legacy fixtures, and query upstream systems through read-only adapters. Persist target-owned data only in MySQL and Nebula. More explicit acceptance work, less cutover risk.
3. **Full storage and service redesign before migration.** Maximum freedom but changes data contracts, infrastructure and algorithms simultaneously. Defer until workload evidence demonstrates a need.

The target storage boundary is settled: MySQL and Nebula. Legacy use of other databases does not justify adding them to target-owned storage. Placement of individual models and projections within the two approved stores remains part of detailed modeling.

## 9. Proposed sequence and acceptance gates

| Stage | Deliverable | Gate before proceeding |
| --- | --- | --- |
| 1. Audit | This inventory, source evidence and unresolved decisions | Confirm compatibility scope and capability priorities. |
| 2. Model/design | Entity/relationship dictionary, identity resolution, source contracts, job state machine, projection publication, storage decision and API boundaries | User approves a concrete schema and implementation plan; no placeholders for required invariants. |
| 3. Foundation slice | Identity, source/run records, schema migrations, configuration and authenticated API conventions | Unit/repository tests, repeatable local migration, no source-side writes or embedded credentials. |
| 4. Inventory sync | Device/interface/unit/LLDP adapters and durable run status using controlled fixtures | Replay is idempotent; incomplete runs cannot retire records; cancellation/retry tests pass. |
| 5. Topology | Physical connectivity and logical grouping projections plus read APIs | Golden graph fixtures, provenance, dangling-reference handling, generation consistency and parity report. |
| 6. Routes | Route projection and path algorithms | Longest-prefix, ECMP alternatives, aggregation, loops, missing neighbors, dual-stack policy, bounded work and regression tests. |
| 7. Management capabilities | Plan-real comparison, controlled application, monitoring and Vue frontend | Explicit authorization, change auditing, freshness and partial-result UI states. |
| 8. Cutover | Shadow comparison, workload tests, operational runbooks and rollback | Approved parity/performance thresholds, credentials and environment confirmation; no automatic production cutover. |

No capability in later stages is silently discarded. The stages establish dependency order, not permission to run production migration or change infrastructure.

## 10. Decisions and remaining constraints

### Approved: redesign versioned APIs

The user approved clean, versioned APIs rather than preserving legacy endpoint contracts. Preserve validated business capabilities, not old URLs, request-type dispatch, or response shapes. Do not build a legacy compatibility layer by default. Existing consumers will need to adopt the new contracts.

Candidate API groups are assets/interfaces, topology views and paths, synchronization runs, and later plan comparisons and controlled changes. Detailed resources, version prefix, authorization, pagination, errors, and asynchronous-operation contracts remain part of the upcoming design; approving redesign does not approve those details or production cutover.

### Approved: MySQL and Nebula target storage

The user clarified that MySQL and Nebula are the target stores; the other systems are upstream places to query data, not a five-store target architecture. The earlier storage question conflated legacy dependencies with target persistence requirements.

- Proposed MySQL responsibility: canonical asset records, source identities and evidence metadata, synchronization runs/checkpoints, management records, and publication metadata.
- Proposed Nebula responsibility: topology vertices/edges, physical and logical graph views, and graph traversal.
- Upstream adapters: query external databases/APIs as required; do not own their schemas or write to them. Legacy write-side functions that remain required must be redesigned within MySQL/Nebula, not copied unchanged.
- No target-owned ClickHouse, MongoDB, or Redis dependency. Detailed route-projection placement and job coordination must respect this boundary.
- Keep source and target client configuration and permissions separate. Sharing a database technology does not mean sharing database ownership.

The responsibility split above is a modeling proposal, not an implemented schema. Remaining inputs are source access contracts and authoritative identifiers, supported database versions, source scale, topology/path workloads, and history requirements. These determine schemas and indexes within the approved stores, not whether to add more stores.

## 11. Detailed design follow-up

The following drafts turn the audit into concrete model and contract proposals, using the confirmed MySQL/Nebula storage boundary and redesigned APIs:

- [Domain, synchronization lifecycle and migration slices](../superpowers/specs/2026-09-15-topology-migration-design.md)
- [MySQL tables and Nebula tags/edges](../superpowers/specs/2026-09-15-topology-storage-schema.md)
- [Versioned API resources and acceptance rules](../superpowers/specs/2026-09-15-topology-api-contract.md)

These are design deliverables, not migrated application code or tested database migrations.

## 12. Verification status

- Completed: static source/target inventory, direct inspection of graph identities and the route writer target mismatch, comparison with the current cross-domain identity/provenance rules, and target Wire integration review.
- Not executed: legacy tests, runtime comparisons, API calls, synchronization, database migration, performance benchmarks, or target application tests for this documentation-only change.
- Not delivered yet: approved domain schema, implemented migration, Vue application, or production cutover.
