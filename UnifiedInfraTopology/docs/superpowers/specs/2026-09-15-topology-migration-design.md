# UnifiedInfraTopology domain and migration design

Date: 2026-09-15

Status: historical design draft; its snapshot storage/publication strategy is superseded by [the current-graph plan](../plans/2026-09-15-current-graph.md) as of 2026-09-16.

The implemented inventory slice reads stable current assets from a dedicated Nebula space and control/identity metadata from MySQL. It does not maintain per-generation asset copies or retained graph snapshots. Scope state/epoch fencing replaces snapshot-selection assumptions for current reads. External CMDB ClickHouse owns source history; no history adapter, collector or publisher is implemented. Later topology/routing sections remain proposals, not delivered behavior. No live migration or cutover has run.

## 1. Confirmed constraints

- Use the existing Nunu Advanced Go module, with independently runnable HTTP API and sync worker.
- Target-owned persistence is **MySQL and Nebula only**. Other databases/APIs are upstream query sources, not stores to deploy or administer for this project.
- Redesign versioned APIs; do not reproduce legacy URLs, request-type dispatch, or response shapes.
- The future product frontend uses Vue 3 + TypeScript + Vite. Do not build plain-HTML management pages.
- Proceed in order: characterize the legacy behavior, model the domain, migrate vertical slices, then verify and cut over.
- Do not copy production credentials, write to upstream stores, or execute a production cutover as part of local implementation.

## 2. Design choices and assumptions

Choose a layered modular monolith rather than copying legacy managers or splitting into microservices. It fits the initialized Nunu layout and permits separate API/worker deployment without duplicated models.

MySQL is authoritative for entity identity, versioned facts, source evidence, run status, and published-generation selection. Nebula is a rebuildable graph projection, not a competing identity authority. No distributed MySQL/Nebula transaction is assumed.

Target SQL design assumes MySQL 8.0/InnoDB, UTC timestamps and enforced transactions. The graph design targets NebulaGraph 3.x semantics. Exact server/client versions must be pinned and exercised in isolated integration tests before deployment; these assumptions do not claim the installed infrastructure supports them.

The first slice is single-organization. A `scope` is an ingestion/publication boundary, not a claim of tenant isolation. Source identifier namespaces and network/VRF scopes remain explicit even in this deployment. Device access across organizations is not exposed until an actual authorization contract exists.

An inventory entity has one owning publication scope; network views may represent it several times inside that scope. Cross-scope topology is initially rejected rather than joined implicitly. Start with one configured scope; do not create one scope per source, network layer, or UI view.

## 3. Domain boundaries

| Domain | Owned concepts | Must not own |
| --- | --- | --- |
| Inventory | Device, component, interface, address assignment, logical group; shared spatial references where evidenced | Raw source protocols or graph traversal |
| Source integration | Source-native records, normalization, paging/checkpoints, evidence and completeness | Canonical identity decisions or target schema writes |
| Identity resolution | Source bindings, scoped address resolution, explicit ambiguity/conflict | Global IP-as-ID rules or automatic serial/name merges |
| Synchronization | Durable runs, leases, staging, validation and publication | HTTP request lifecycle or scheduler-specific business logic |
| Topology | Network views, nodes, termination points, typed relations, graph projections | Treating LLDP/ARP as confirmed physical cabling |
| Routing | Route facts, forwarding alternatives and bounded path calculation | Claims that connectivity proves packet delivery |
| Management | Run controls, provenance inspection, later plan comparison and audited change requests | Direct database/table switching endpoints |

Reuse shared `Device`, `Cabinet` and `Room` semantics from the sibling infrastructure model. Space/power expansion is not part of the first migration slice; retain unresolved source references instead of creating fabricated parents/components.

### Identity and evidence

1. Generate opaque, immutable entity IDs. Names, management IPs, MACs, serials and source UUIDs are attributes or source identifiers, never the universal primary key.
2. A source key comprises source instance, object type, normalized source namespace and native ID. The adapter must declare the native-key rule and whether upstream IDs may be reused.
3. Without a verified cross-source equivalence rule, preserve an unresolved record or independent candidate. Do not merge on IP/name alone.
4. IP resolution requires publication generation and routing/address scope. Multiple candidates produce ambiguity, not the first map entry.
5. Observation time and ingestion time are distinct. Unknown observation/effective times remain null.
6. Keep inventory, planned state, observed adjacency and derived paths distinguishable in persistence and API responses.
7. An incomplete or failed snapshot cannot retire missing entities. An authoritative tombstone or a successful complete snapshot may retire that source's assertion, not automatically every other source's assertion.

## 4. Storage and publication model

Detailed tables and graph types are specified in [Storage schema](2026-09-15-topology-storage-schema.md).

Each publication scope has one active generation pointer in MySQL. A generation contains a complete, immutable logical snapshot of its entity revisions, selected facts and relations; routing is included only when its readiness flag is satisfied. Input deltas are allowed, but are applied to a pinned base to materialize a complete new snapshot before publication.

The scope's deployment-configured publication profile declares required datasets and is pinned into each run/generation: `inventory`, `inventory+graph`, or `inventory+graph+routing`. Slice A uses inventory only. A later graph/routing-enabled scope cannot silently downgrade readiness after a projection failure. Content and readiness become immutable at validation; enabling a new projection requires another generation, never in-place enrichment of a published one.

The first implementation favors complete snapshot copies for correctness. Do not introduce content-addressed manifests or shared mutable graph vertices prematurely. Measure copy/storage cost with real volumes before enabling production retention schedules or route workloads.

### Build and publish

1. Create a queued run with explicit scope, source selection, mode, base generation and idempotency key.
2. A worker claims it with a MySQL lease and monotonically increasing fencing token for that scope.
3. Read source pages into run-owned staging records and record each source's completeness. No upstream writes.
4. Normalize and resolve identities; materialize the candidate SQL snapshot. Failed candidates never appear in active inventory APIs.
5. If the pinned profile requires graph data, persist candidate vertices/edges under generation-qualified VIDs, never under a mutable global device/IP VID. Inventory-only publication does not require Nebula writes.
6. Join all run-owned writers and stop admitting writes before validation. SQL candidate writes require `state=building` and the current run token; no graph write may remain in flight at validation. Validate source coverage, required references, counts/checksums, applicable graph visibility and every dataset required by the profile. Record failures and stop before publication.
7. In one MySQL transaction, lock the scope/run rows, verify lease owner/token/expiry, cancellation state and base pointer, mark the generation published, update the active pointer, finalize accepted identity bindings and mark the run succeeded.
8. Retain the previous published generation for historical reads and controlled recovery. Graph cleanup is a separate bounded operation, never part of the publication transaction.

No Nebula write occurs inside the pointer transaction. For graph-enabled profiles, successful graph writes are a prerequisite, not an atomic participant. If MySQL publication fails, the candidate graph remains unreachable through public APIs. A crash after commit is resolved from the durable succeeded state rather than replaying publication blindly.

### Reader consistency

- Resolve every generation, explicit or default, to `state=published` within the authorized scope. Building/validated/failed candidates are never available through asset/topology APIs; sync endpoints expose their metadata only. Read the active pointer once at request start and carry the resolved generation through every MySQL/Nebula query.
- Graph responses get display attributes from the same immutable SQL revision or graph generation, not current mutable inventory records.
- Asset inventory endpoints may serve a generation with graph readiness false; graph/path endpoints return `409 projection_not_ready` for missing required projections. They never mix an older graph with newer attributes silently.
- APIs return generation ID, publication time, coverage/gaps and capability readiness. A consumer can explicitly query an older retained generation.
- Cache only with scope + generation + request parameters; no Redis dependency or process-local active-generation override.
- For the initial implementation, do not automatically purge published generations. Later cleanup requires reader leases or a measured grace period longer than every enforced request deadline and an explicit retention policy.

### Run state machine

`queued -> running -> validating -> publishing -> succeeded`

Before success, failures lead to `failed`; an accepted cancellation leads to `canceled`. Terminal runs are immutable. A retry creates a new run and generation; it does not recycle a possibly partial graph generation.

- Cancellation is a durable flag, checked by adapters and before publication. If publication already committed, cancellation returns a state conflict.
- Cancellation and publication lock the same run/scope records; cancellation accepted before that transaction prevents publication.
- Expired leases mark interrupted work failed. A fresh attempt gets a new run, generation and fencing token.
- Stale workers may finish isolated graph writes but cannot publish. Cleanup waits for old attempts to be quiescent; never repurpose their VIDs.
- Acquisition, renewal and publication use database time. Renewals require the matching owner/token and a still-valid lease; an expired lease cannot be revived by a delayed heartbeat.
- Candidate identity assignments are provisional. Stable IDs may be allocated before publication, but public APIs only expose published revisions; retiring/rebinding accepted source keys requires successful publication.

### Completeness and retirement

Every adapter reports `full`, `incremental` or `partial`, its filter/namespace, page counts, record counts and optional watermark. Only an adapter with a verified complete-enumeration contract may report `full`. Each configured source has one fixed, versioned coverage definition; persist its fingerprint in runs and published source state. Initial APIs cannot change source filters. Reject fingerprint changes/checkpoint reuse until an explicit adapter-configuration migration reconciles old and new coverage.

Materialize the candidate active assertion set from `generation_source_records`: copy the pinned base; replace only the selected full-enumeration coverage; apply incremental upserts/tombstones; preserve unselected coverage. Recompute canonical field selection, relations and lifecycle from all surviving active records under the pinned policy snapshot. `entity_evidence` records the selected field provenance, not the complete set of still-active assertions. Retiring an assertion does not itself retire its stable source identity binding; a new incarnation/rebinding requires an explicit reuse decision finalized on publication.

- A failed page, shard or parse makes the required source incomplete; fail the run rather than publishing an unmarked partial replacement.
- An empty full response requires a verified authoritative-empty contract; otherwise record `unexpected_empty` and refuse retirement/publication for that required source.
- Sources not selected in a partial-scope refresh retain their pinned-base facts. Sources selected but failed are not silently replaced by cached data.
- Incremental runs carry forward unchanged records, process explicit tombstones and advance checkpoints only at publication.
- Expiration applies to source assertions. An entity retires only when no authoritative active assertions remain under the configured source policy. Authority is explicit per adapter/entity family; equal-priority disagreements are surfaced as conflicts.

### Recovery boundary

Historical reads are available immediately; pointer-only rollback is not. Publication also advances accepted identity heads, so restoring only the active pointer would leave inconsistent identity state. Before cutover, implement and test controlled recovery by republishing the selected historical assertion snapshot as a new validated generation, reconciling binding heads and source checkpoints in its publication transaction. No rollback endpoint exists in the initial slices.

## 5. Topology and routing semantics

Physical assets and network views are separate. `NetworkNode` represents a device in a particular `Network`; `TerminationPoint` belongs to that node and may reference an evidenced physical or logical interface. An asset can participate in several networks without duplication.

Keep ownership, aggregation, membership, confirmed cable connection, directed observed adjacency and derived connectivity as separate relation types. A one-sided LLDP record can create an observed adjacency; it cannot create a confirmed cable. ARP identifies an address/neighbor observation, not a physical port attachment by itself.

Route facts preserve prefix, family, device, routing scope, next hop, protocol and selected egress alternatives. Unknown VRF remains explicitly unknown. Do not place unknown-scope routes in the default VRF implicitly. Legacy IPv4 algorithms require characterization; IPv6 routes can be stored before IPv6 path traversal is offered.

Path queries name a mode: observed topology, confirmed physical connectivity, or routing. Results include derivation/evidence IDs and coverage warnings. Route candidates use longest-prefix matching and preserve equal-cost alternatives; unresolved forwarding is reported, not filled with LLDP unless the caller explicitly selects an inference option.

Bound requests by hop count, returned paths, visited states, query work and context deadline. Output records truncation; a partial enumeration must not report itself complete. Never call a returned path proof of end-to-end business reachability.

## 6. Versioned API and frontend boundary

Use `/v1`, matching the existing Nunu router/Swagger base. Keep the `{code, message, data}` envelope and real HTTP status codes. Resource endpoints replace legacy payload dispatch. Detailed routes and errors are in [API contract](2026-09-15-topology-api-contract.md).

First deliver inventory/provenance reads and synchronization controls, then topology and path reads. Plan editing/application and monitoring follow after parity fixtures exist. Do not register future placeholder endpoints that falsely return successful empty data.

Authenticate all domain endpoints. Authorize read, synchronization-write and management operations separately. The sample JWT middleware is an authentication starting point, not sufficient domain authorization. Do not add an unrestricted production registration flow as a side effect of this migration.

The future Vue frontend consumes the same API and renders run progress, generation/freshness, unresolved/conflict states, and incomplete query results explicitly. No separate privileged browser-only data path.

## 7. Nunu artifact map

`api/v1 contracts -> model + migrations -> repository -> service -> handler -> router -> Wire -> tests`

- Keep feature-named files in existing layers initially: `device`, `source`, `sync_run`, `topology`, `route`. Do not create a generic mega-manager or pre-create empty domain packages.
- `internal/integration/` contains source adapters/DTOs. Repositories own target MySQL/Nebula access; source adapters cannot call target repositories.
- Service interfaces express canonical inputs and results, not upstream database rows or Nebula query strings.
- Pure normalization/identity/path logic is exercised without external clients. Extract focused internal packages only when a migrated algorithm actually requires them.
- `cmd/server` starts HTTP only after removal of the sample job from its composition; `cmd/task` composes the sync worker and scheduler. Business orchestration remains in services.
- `cmd/migration` applies explicit, versioned MySQL/Nebula schema steps to target connections only. Do not expand `AutoMigrate(User)` into production domain migration.
- Edit all affected `wire.go` provider sets, regenerate `wire_gen.go`, and refresh mocks after interfaces stabilize.

## 8. Migration slices and acceptance

| Slice | Concrete result | Required proof |
| --- | --- | --- |
| A: foundation/inventory | Versioned schema, source identities, device/interface reads, durable run records and fake adapter | Identity stability, ambiguous aliases, schema up/down strategy, HTTP/auth contract, full vs partial snapshot behavior |
| B: legacy source adapters | CMDB, ports, interface addresses, units and LLDP query adapters | Sanitized fixtures; pagination, source-native keys, cancellation, no source writes; restart/idempotency |
| C: topology publication | Networks/nodes/termination points, typed relations, candidate graph and active pointer | Cross-generation isolation, failed graph-write rejection, crash before/after commit, conflict and missing endpoint handling |
| D: route behavior | Route facts/projections and bounded path query | Prefix match, ECMP, loop/aggregation/missing-neighbor fixtures; IPv6 explicitly supported or rejected; workload benchmark |
| E: management/UI | Plan comparisons, audited local changes, enrichment adapters and Vue application | Explicit approval of mutation semantics, permission tests, provenance/freshness rendering; no upstream writes |
| F: cutover | Shadow comparison and operator runbook | Sanitized parity report, measured workload thresholds, verified restore/rollback; separately approved production operations |

No storage service beyond MySQL/Nebula is introduced by these slices. Legacy ClickHouse route materialization and Mongo comparison writes are behavior to redesign, not target writes to transplant.

## 9. Test strategy and release gates

- Unit: source normalization, source-key collisions, IP reuse, conflict resolution, relation type rules and state transitions.
- Repository: MySQL transactions/constraints, pointer compare-and-swap, idempotency races, lease expiry/fencing and unchanged checkpoints on failure.
- Graph integration: isolated Nebula space; same-generation edge endpoints, view filters, property indexes/read visibility, candidate failure and generation-specific queries. Mocks alone cannot prove these semantics.
- Handler/router: status codes, validation, scope checks, pagination, omitted/unknown values, response generation and asynchronous cancellation.
- Algorithm: legacy characterization datasets with explicitly justified fixes, not unconditional byte-for-byte preservation of defects.
- Lifecycle: worker shutdown, context deadlines, bounded queues and no goroutine leaks after adapter/saver failures.
- Security: no credential values in fixtures/logs, parameterized SQL, controlled graph identifiers, bounded read endpoints, denied mutations without permission.
- Verification at each implemented slice: focused tests, `go test ./...`, `go build ./...`, `go vet ./...`; race/integration tests on affected concurrency and storage paths.

Database schema details are design artifacts, not a claim of tested DDL. Exact source contracts and workload measurements are integration prerequisites rather than reasons to block offline implementation.

## 10. Review and current delivery status

- An independent design review identified gaps in publication profiles, explicit generation access, active assertion materialization, coverage-bound checkpoints, idempotency ordering, graph eligibility, API permissions/selectors, and rollback. This draft incorporates corrections for all eight areas.
- Documentation checks validated local links, seven JSON examples, sixteen source evidence path/line references, and the 69-character Nebula VID convention.
- No Go application code, executable migrations, upstream adapters, or Vue components changed in this modeling step. No service, database query, runtime test, or production migration was executed.
- Next delivery is slice A: inventory/sync foundation with a fake source adapter and tests. Review this design before writing the implementation plan and domain code; later slices remain ordered by the gates above.
