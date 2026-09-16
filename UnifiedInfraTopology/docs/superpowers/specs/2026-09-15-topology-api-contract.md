# Versioned topology API contract

Status: inventory/control reads and queued sync-run APIs are implemented; evidence, collectors, publication, topology/path queries and UI remain design-only. Base path `/v1` follows the initialized Nunu server. No legacy compatibility endpoints.

Current-graph amendment (2026-09-16): asset reads use a dedicated Nebula current-state graph; MySQL keeps control/identity metadata. `generation_id` is current publication metadata, not an asset snapshot selector. Earlier retained-snapshot assumptions do not apply. Source history remains in external CMDB ClickHouse with no history adapter implemented.

## 1. Shared contract

- JSON envelope remains `{ "code": 0, "message": "ok", "data": ... }`; nonzero codes represent application errors and HTTP status remains meaningful.
- UUIDs and opaque cursors are strings. Counts are integers; 64-bit counters that may exceed JavaScript's safe range are decimal strings.
- UTC RFC 3339 timestamps. Unknown values are null; empty lists mean a successful empty result, not dependency failure.
- All domain endpoints require authentication. Permissions are `inventory:read`, `topology:read`, `sync:read`, `sync:write`, and later `management:write`, checked against the requested scope.
- All list endpoints use `limit` (default 50, maximum 200) and opaque `cursor`. Default deterministic order is by stable ID; run lists use `(created_at,id)` descending. No arbitrary SQL sort expressions.
- Asset reads require scope `projection_state=ready` and its active `state=published` generation with both inventory/graph readiness. A supplied non-current `generation_id` yields 409 `state_conflict`, never a historical snapshot. Missing readiness yields 409 `projection_not_ready`. Readers check state, monotonic projection epoch and active batch around graph access; concurrent changes reject the response. Responses carry batch ID, publication time and readiness; coverage/gaps are not implemented.
- Cursor format v2 binds caller, resource, current batch/epoch where applicable, scope, parent, filters and last key; sign it with a dedicated configured key. Malformed, mismatched or v1 cursors yield 400. A valid v2 cursor whose epoch/batch changed yields 409; an updating/failed/uninitialized scope yields `projection_not_ready`. Restart pagination after updates; there is no retained-asset 410 behavior.
- Lists return `items`, `next_cursor` (null at end), and metadata. Do not compute unbounded totals by default.
- Planned path/subgraph deadline is 10 seconds; list/read middleware uses a 5-second context deadline. The Nebula SDK cannot cancel an in-flight query with that context: socket timeouts and before/after context checks do not guarantee a 5-second wall-clock bound. List limits bound response rows, not graph scan/sort work; benchmark EXPLAIN/PROFILE before production.
- Every response carries `X-Request-ID`. Logs include that ID, scope, run/generation when present, never credentials or unsanitized raw upstream payloads.

### Errors

| HTTP | Application code/name | Meaning |
| --- | --- | --- |
| 400 | `40001 invalid_argument` | Invalid field/filter/cursor or unsupported request mode |
| 401 | `40101 unauthenticated` | Missing/invalid credentials |
| 403 | `40301 forbidden` | Caller lacks permission for scope/operation |
| 404 | `40401 not_found` | Requested resource absent within an authorized scope |
| 409 | `40901 identity_ambiguous` | Endpoint/source identity resolves to multiple candidates |
| 409 | `40902 projection_not_ready` | No published generation or required projection unavailable |
| 409 | `40903 state_conflict` | Mutation cannot apply to the current state/base generation |
| 409 | `40904 idempotency_conflict` | Same key reused with a different normalized request |
| 410 | `41001 generation_expired` | Known generation intentionally retired from retention |
| 429 | `42901 work_limit_exceeded` | Request exceeds admission/concurrency budget |
| 502 | `50201 upstream_failure` | Explicit upstream query failed; no fabricated empty data |
| 503 | `50301 storage_unavailable` | Required target store unavailable |
| 504 | `50401 query_deadline_exceeded` | Deadline without a safely reportable bounded result |
| 500 | `50001 internal_error` | Unexpected internal failure, sanitized message |

Error data may include safe field errors, conflicting public IDs, or expected/current generation, but not DSNs, raw SQL/nGQL, tokens or upstream payloads. These proposed codes do not collide with the currently inspected `api/v1/errors.go` codes (0, 400, 401, 404, 500, 1001). Existing success helper always emits HTTP 200; extend it with an explicit-status helper for 202 rather than returning success with the wrong status.

### Permission mapping

| Endpoint family | Required permission |
| --- | --- |
| Scope listing | Any configured grant; return only granted scopes |
| Device, interface, address and entity evidence reads | `inventory:read` in requested scope |
| Source configuration summaries and sync-run reads | `sync:read` in requested scope |
| Enqueue/cancel sync run | Both `sync:read` and `sync:write` in requested scope |
| Published generation listing | `inventory:read` or `topology:read` in requested scope |
| Network/node/link/subgraph/path queries | `topology:read` in requested scope |

Initial grants map authenticated user IDs to scope/permission sets in deployment-managed server configuration; deny by default. Do not trust client-supplied roles or build a general RBAC editor for the first slice. Evidence reads are limited to normalized allowlisted fields; arbitrary raw payload download is not exposed. Validate resource ownership after permission checks so a valid scope grant cannot access another scope's resource ID.

## 2. Inventory and provenance — initial slices

All routes below are relative to `/v1`; scope is explicit in the path.

| Method and route | Request/filters | Result |
| --- | --- | --- |
| `GET /scopes` | Pagination; only caller-authorized scopes | Configured scope summaries |
| `GET /scopes/{scope_id}/devices` | `device_kind`, `name` exact match, `lifecycle`, current `generation_id` | Current device summaries |
| `GET /scopes/{scope_id}/devices/{device_id}` | Optional current batch | Current device fields and publication metadata; no evidence summary |
| `GET /scopes/{scope_id}/devices/{device_id}/interfaces` | `interface_kind`, generation, pagination | Physical/logical interfaces without flattening aggregation |
| `GET /scopes/{scope_id}/devices/{device_id}/addresses` | `address_family`, generation, pagination | Device/interface address assignments with scope and purpose |
| `GET /scopes/{scope_id}/entities/{entity_id}/evidence` | Generation, pagination | Sanitized normalized evidence, source-native reference and resolution status |
| `GET /scopes/{scope_id}/sources` | Pagination | Configured adapters, enabled state and published checkpoint summary; no credentials |
| `GET /scopes/{scope_id}/generations` | Pagination | Published generation metadata and readiness |

Do not implement unrestricted device CRUD: synchronized facts have an authority/source contract. Later manual changes become explicit audited assertions rather than overwriting imported facts. Scope/source configuration is deployment-managed initially; avoid creating an unneeded secret-editing API.

## 3. Synchronization — initial slices

| Method and route | Behavior |
| --- | --- |
| `POST /scopes/{scope_id}/sync-runs` | Validate enabled sources, mode and base; require `Idempotency-Key`; persist queued run; return 202 with `Location` |
| `GET /scopes/{scope_id}/sync-runs` | Filter by status/source; bounded cursor pagination |
| `GET /scopes/{scope_id}/sync-runs/{run_id}` | Run status, per-source progress, checkpoints, completeness, safe errors, generation and timestamps |
| `POST /scopes/{scope_id}/sync-runs/{run_id}/cancel` | Persist cancellation; return 202 if accepted, 200 if already canceled, 409 for succeeded/failed |

Example enqueue body:

```json
{
  "source_ids": ["11111111111111111111111111111111"],
  "mode": "full",
  "base_generation_id": null
}
```

`full` means complete enumeration of the selected sources' fixed coverage, not implicit deletion of every omitted source. Incremental mode is accepted only by adapters with verified checkpoint semantics. Arbitrary filter changes are not accepted. For a new run, the server resolves a null base to the active generation at enqueue; null remains valid for first publication when none exists.

Canonicalize the caller body with null preserved and source IDs sorted/deduplicated; compute its hash before resolving any dynamic base. Look up the idempotency key before assigning the base for a new run. Same key and caller-request hash returns the original run even if active generation has changed; a changed body yields 409. Persist the resolved base separately. Keep deduplication records with the run; do not introduce a Redis cache for this. A queued run whose captured base is no longer active fails with `base_generation_changed`; retry is explicit against the new base, not an unnoticed rebase.

Worker progress uses phase plus counts, not fabricated percentages. A failed run reports source-level gaps and a safe failure code. Retry is another POST with a new idempotency key; no terminal-run reset endpoint.

## 4. Topology and paths — later slices

| Method and route | Behavior |
| --- | --- |
| `GET /scopes/{scope_id}/networks` | Published network views, layer/plane and readiness |
| `GET /scopes/{scope_id}/networks/{network_id}/nodes` | Paginated view-specific nodes with shared asset references |
| `GET /scopes/{scope_id}/networks/{network_id}/links` | Typed observed/derived links; filter kinds without erasing provenance |
| `POST /scopes/{scope_id}/topology/subgraph-queries` | Bounded expansion from explicit seed entities in a network and generation |
| `POST /scopes/{scope_id}/topology/path-queries` | Bounded synchronous graph/routing query; HTTP 200 result, not a background job |

Path body contains `network_id`, optional `generation_id`, `source`, `destination`, `mode`, `allow_inferred` (default false), and limits. A selector contains exactly one of `entity_id` or `{address, address_scope_key}`; accepted selectors depend on mode below. Address ambiguity yields 409. Unknown network scope is not coerced into the default VRF.

| Mode | Source / destination | Resolution and traversal |
| --- | --- | --- |
| `observed` | Device or NetworkNode entity IDs; scoped addresses allowed only if uniquely resolving to a device | Map to exactly one node in the selected network; traverse directed observed-node connectivity derived from LLDP; no ownership edges or inferred missing hops |
| `physical` | Physical Interface entity IDs only | Both interfaces must be mapped into the selected network; traverse confirmed cable edges only. Device-internal forwarding is not a cable and is not invented. No multi-device physical transit promise without modeled intermediate connections |
| `routing` | Source Device/NetworkNode ID or uniquely resolving scoped address; destination must be explicit address + address scope | Select source routing context and lookup destination prefix; reject destination entity ID without address. Require resolved routing scope; unknown-VRF sources remain inspectable facts but cannot produce asserted forwarding paths |

Reject incompatible entity kinds, missing network mappings and unsupported inference combinations with 400. `allow_inferred=true` is rejected until a characterized gap-filling rule is implemented; exact projection of observed port adjacency to network nodes is not gap filling.

Initial hard maxima: `max_hops=32`, `max_paths=100`, `max_visited_states=10000`. Defaults: 16, 20 and 2000 respectively. Subgraphs default to 2 hops/200 nodes and allow at most 4 hops/1000 nodes/4000 edges. Server-side work and response-byte budgets also cap upstream graph fan-out; a bounded output size alone is insufficient.

For path queries, the first algorithm enumerates simple paths only: a vertex cannot repeat within one path. Do not use a single global visited set that incorrectly drops valid alternative paths. Routing search state also includes routing scope and destination lookup context. Longest-prefix selection and ECMP expansion are explicit algorithm stages, not generic shortest-path traversal.

Results include `paths` or graph data, `generation_id`, `complete`, `truncated`, `truncation_reason`, `coverage`, `gaps`, `inference_used` and per-edge evidence/derivation references. `complete=true` requires a finished bounded search and complete required source coverage; zero paths with incomplete coverage means unknown, not unreachable. Deadline results are returned as partial only if the algorithm has an internally consistent result and can complete the response; otherwise return 504.

No promise of IPv6 routing query support until parity fixtures and the algorithm support it. Store IPv6 facts losslessly; unsupported path families fail explicitly with 400 instead of silently ignoring those routes.

### Concrete path contract examples

All identifiers and documentation-range IPs below are synthetic. Responses shown are the `data` inside the shared success envelope. A path has stable `node_ids`, ordered `edges` carrying relation/evidence IDs, and a named `mode`; it never exposes generation-qualified storage VIDs. Examples omit optional query limits, which use the defaults above.

Observed topology request:

```json
{
  "network_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "mode": "observed",
  "source": {"entity_id": "11111111111111111111111111111111"},
  "destination": {"entity_id": "22222222222222222222222222222222"}
}
```

Example observed response (selectors in this example are network-node IDs):

```json
{
  "generation_id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "published_at": "2026-09-15T00:00:00Z",
  "readiness": {"inventory": true, "graph": true, "routing": true},
  "paths": [{
    "mode": "observed",
    "node_ids": ["11111111111111111111111111111111", "22222222222222222222222222222222"],
    "edges": [{"relation_id": "cccccccccccccccccccccccccccccccc", "evidence_ids": ["dddddddddddddddddddddddddddddddd"], "derivation_rule_version": "lldp-node-view-v1"}]
  }],
  "complete": true, "truncated": false, "truncation_reason": null,
  "coverage": {"required_sources_complete": true}, "gaps": [], "inference_used": false
}
```

Physical connection request:

```json
{
  "network_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "mode": "physical",
  "source": {"entity_id": "33333333333333333333333333333333"},
  "destination": {"entity_id": "44444444444444444444444444444444"}
}
```

Example physical response uses interface IDs and a confirmed cable relation, not an LLDP inference:

```json
{
  "generation_id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "published_at": "2026-09-15T00:00:00Z",
  "readiness": {"inventory": true, "graph": true, "routing": true},
  "paths": [{
    "mode": "physical",
    "node_ids": ["33333333333333333333333333333333", "44444444444444444444444444444444"],
    "edges": [{"relation_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "evidence_ids": ["ffffffffffffffffffffffffffffffff"], "derivation_rule_version": null}]
  }],
  "complete": true, "truncated": false, "truncation_reason": null,
  "coverage": {"required_sources_complete": true}, "gaps": [], "inference_used": false
}
```

Routing request, with explicitly resolved address/routing scope:

```json
{
  "network_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "mode": "routing",
  "source": {"entity_id": "11111111111111111111111111111111"},
  "destination": {"address": "192.0.2.10", "address_scope_key": "lab:vrf-blue"}
}
```

Example routing response when the configured static/local source view has no candidate but does not cover all forwarding protocols:

```json
{
  "generation_id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "published_at": "2026-09-15T00:00:00Z",
  "readiness": {"inventory": true, "graph": true, "routing": true},
  "paths": [], "complete": false, "truncated": false, "truncation_reason": null,
  "coverage": {"required_sources_complete": true, "route_protocols": ["static", "local"]},
  "gaps": [{"code": "routing_protocol_coverage_limited"}], "inference_used": false
}
```

Source enumeration completeness and query-semantic coverage are different: a fully read filtered source can still be insufficient to decide routing reachability. Every query evaluates both. Successful routing paths use ordered forwarding alternatives with route/evidence IDs rather than pretending all route hops are confirmed cable relations; finalize the alternative payload with route characterization in slice D.

## 5. Deferred management and UI

Plan comparison/application, manual asset assertions, source policy editing, metric/alert enrichment and the Vue management interface are inventoried but not initial endpoint contracts. Design their mutation/audit and permission rules in their slice; do not register placeholder handlers or expose legacy database switch controls.

The UI must distinguish source-query errors, failed syncs, no active generation, unresolved evidence, stale generation and empty valid results. Model management operates through typed, audited service APIs rather than arbitrary table/graph editors.

## 6. Contract acceptance

- Router tests mount the real `/v1` groups, not only handlers in isolation.
- Validate authentication and scope permissions on reads and writes; GET endpoints never mutate state.
- Idempotency concurrency tests prove one run per key and rejection of changed bodies.
- Pagination tests prove generation/filter binding and no page drift after a new publication; explicit candidate/cross-scope generation IDs remain inaccessible.
- Idempotency tests replay a null-base request after an intervening publication and receive the original run; retirement tests preserve another source's active assertions and reject checkpoint coverage drift.
- Contract tests cover 202/Location, safe errors, null unknowns, stable public IDs and JavaScript-safe numeric output.
- Query tests cover directed observations, aggregation versus physical links, ECMP, ambiguity, loops, truncation and explicit inference.
- OpenAPI generation is updated with each implemented slice; do not claim these design routes are already registered.
