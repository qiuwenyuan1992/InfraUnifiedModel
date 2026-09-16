# MySQL and Nebula schema design

Status: historical snapshot-oriented proposal, not the current storage contract or executable migrations.

Superseded on 2026-09-16 for asset storage/publication: Nebula now holds current assets with stable VIDs, and MySQL holds control/identity metadata only. Per-generation asset rows and generation-qualified graph VIDs below are not the implemented model. Use [the current-graph plan](../plans/2026-09-15-current-graph.md), `docs/schema/current_graph.ngql` and `docs/migration/inventory.md` as the current references. Historical source queries belong to external CMDB ClickHouse; no history connector is implemented. The remaining proposal is retained as design history, not deployment instructions.

See [Architecture and lifecycle](2026-09-15-topology-migration-design.md) for publication invariants. Only MySQL and Nebula are target-owned stores.

## 1. Common conventions

- Public IDs: UUIDs encoded as lowercase 32-character hex strings; immutable and opaque. MySQL `CHAR(32)` uses ASCII binary collation. Never expose auto-increment IDs as JavaScript numbers.
- Time: UTC `DATETIME(6)` in SQL, RFC 3339 in HTTP. Unknown observed/valid time is SQL NULL, not ingestion time or the Unix epoch.
- Source tokens/namespaces are case-sensitive unless a particular adapter contract explicitly normalizes them. Preserve their original text separately.
- Canonical native-key encoding is a length-delimited tuple, hashed with SHA-256 into `BINARY(32)` for indexing. Store the original tuple and compare it after lookup; a digest collision is an error, not a match.
- JSON is for source evidence and bounded extension attributes, not a substitute for indexed IDs, parent references, status, or relation endpoints. Never store credentials in source metadata.
- Version rows use `(generation_id, entity_id)` primary keys. Child/endpoint references resolve within the same generation; validate polymorphic relation references before publication.
- No generic GORM soft-delete flag replaces explicit lifecycle or source retirement. Physical deletion is a separate retention operation.

## 2. Control, identity and evidence tables

| Table | Essential fields | Keys/constraints |
| --- | --- | --- |
| `topology_scopes` | `id`, `name`, `active_generation_id` nullable, `lease_owner` nullable, `lease_token BIGINT`, `lease_expires_at` nullable | PK `id`; unique name; token monotonic; active generation belongs to this scope |
| `sources` | `id`, `scope_id`, `name`, `adapter_kind`, `config_ref`, `enabled`, `policy_version`, fixed `coverage_definition`, `coverage_fingerprint`, timestamps | PK `id`; unique `(scope_id,name)`; config reference only, no DSN/secret; changing coverage requires explicit migration |
| `entities` | `id`, `scope_id`, `kind`, `created_at` | PK `id`; registry identity only, no mutable display facts; owning scope/kind immutable |
| `source_keys` | `id`, `source_id`, `key_hash`, `object_type`, `namespace`, `native_id`, `key_encoding_version` | PK `id`; unique `(source_id,key_hash)`; original tuple checked on reuse |
| `identity_bindings` | `id`, `source_key_id`, `incarnation`, `entity_id`, `first_generation_id`, `retired_generation_id` nullable | Unique `(source_key_id,incarnation)`; all changes finalized during publication; preserve upstream key reuse history |
| `identity_binding_heads` | `source_key_id`, `binding_id` | PK `source_key_id`; at most one accepted active binding per key; lock row while rebinding |
| `sync_runs` | `id`, `scope_id`, `status`, `mode`, `base_generation_id`, `generation_id`, `publication_profile`, `request_hash`, `idempotency_key`, `requested_by`, `lease_token`, `cancel_requested_at`, `created_at`, `started_at`, `finished_at`, `error_code`, bounded error detail | Unique `(scope_id,idempotency_key)`; index `(scope_id,status,created_at,id)`; hash caller request before resolving base; terminal rows immutable |
| `sync_run_sources` | `run_id`, `source_id`, filter/namespace, `coverage_fingerprint`, `enumeration_kind`, `status`, `pages`, `records`, `watermark_before`, `watermark_after`, `observed_from/to`, gap summary | PK `(run_id,source_id)` for fixed coverage per source; successful counts do not imply full enumeration |
| `source_records` | `id`, `run_id`, `source_id`, `source_key_id`, `observed_at`, `ingested_at`, `operation`, `payload_hash`, bounded sanitized `payload`, parse/resolution status | Unique `(run_id,source_key_id)` for snapshot adapters; duplicate differing payloads fail/quarantine rather than last-write-wins |
| `generations` | `id`, `scope_id`, `run_id`, `base_generation_id`, `state`, `publication_profile`, `inventory_ready`, `graph_ready`, `routing_ready`, `schema_version`, pinned `policy_snapshot`, `coverage`, `manifest_hash`, `created_at`, `published_at` | Unique `run_id`; index `(scope_id,published_at,id)`; state `building/validated/published/failed`; immutable content/readiness after validation |
| `generation_source_state` | `generation_id`, `source_id`, `accepted_run_id`, `checkpoint`, `coverage_fingerprint`, `completeness`, `policy_version` | PK `(generation_id,source_id)`; carry unselected source state from pinned base; reject incompatible checkpoint coverage; checkpoints become active with pointer |
| `generation_source_records` | `generation_id`, `source_key_id`, `source_record_id`, `assertion_state` (`active/retired`), `retirement_reason` nullable | PK `(generation_id,source_key_id)`; complete logical source snapshot; only active records participate in current canonical selection; keep retired record provenance |
| `generation_source_bindings` | `generation_id`, `source_key_id`, `entity_id`, `incarnation`, `status` (`resolved/unresolved/conflict`) | PK `(generation_id,source_key_id)`; `entity_id/incarnation` nullable unless resolved; candidate resolved mappings become accepted heads only during publication; assertion retirement is independent |
| `entity_evidence` | `generation_id`, `entity_id`, `source_record_id`, `assertion_kind`, `resolution_status`, `selected_fields` | Unique `(generation_id,entity_id,source_record_id)`; evidence can reference earlier retained records for unchanged facts; selected fields record field-level authority |

Source-key reuse requires an explicit adapter incarnation/retirement signal or operator resolution. Never infer reuse solely from a changed name/IP. Provisional IDs left by failed runs are harmless unreachable registry entries; do not expose them through asset reads.

This initial `source_records` contract is snapshot-oriented. A future CDC/event adapter needs an event ID/sequence key and ordering rules; do not feed multiple events for one source key into the snapshot uniqueness constraint.

## 3. Versioned inventory tables

All rows below have `generation_id`, `entity_id`, `lifecycle` (`active/retired`), `resolution_status` (`resolved/unresolved/conflict`), and nullable `valid_from/valid_to` where the source provides effective time. Each entity references the immutable registry and matching owning scope. Effective intervals are half-open; ingestion does not manufacture valid time.

| Table | Typed fields | Required constraints/indexes |
| --- | --- | --- |
| `device_versions` | `name`, `device_kind`, `role`, `serial_number` nullable, `vendor`, `model`, `service_status`, optional evidenced `room_id/cabinet_id` | PK `(generation_id,entity_id)`; index `(generation_id,device_kind,entity_id)` and `(generation_id,name,entity_id)`; serial/name not unique |
| `component_versions` | `device_id`, `component_kind`, `slot`, `serial_number`, `model` | Same-generation parent device; index `(generation_id,device_id,entity_id)`; slot does not establish replacement identity |
| `interface_versions` | `device_id`, `component_id` nullable, `namespace`, `source_name`, `normalized_name`, `interface_kind`, `admin_state`, `oper_state`, `speed_bps BIGINT` nullable, `mac` nullable | Index `(generation_id,device_id,namespace,normalized_name)`; physical/logical/aggregation/unknown kind; name normalization adapter-specific; ambiguity is retained |
| `address_versions` | `device_id`, `interface_id` nullable, `address_family`, `address BINARY(16)`, `prefix_length` nullable, `address_scope_key`, `scope_status`, `purpose` | Index `(generation_id,address_scope_key,address_family,address)`; canonical IPv4 encoding distinguished by family; no global IP uniqueness |
| `logical_group_versions` | `group_kind`, `name`, `source_code`, `plane`, `description` | Index `(generation_id,group_kind,entity_id)`; ComputePod, Module and logical IDC are not physical containers |

State enums include `unknown`. Missing operational status is not `up`; configured speed is not observed throughput. Device-level management addresses remain device-level until the source proves an interface association.

Room/cabinet references are populated only when the corresponding shared spatial entity exists in the same snapshot. Otherwise retain original external references in evidence. Creating full spatial tables is a later slice, not required to import devices.

## 4. Topology, observations and routes

| Table | Essential fields | Constraints |
| --- | --- | --- |
| `network_versions` | Common version key, `name`, `layer`, `plane`, `routing_scope_key`, `routing_scope_status` | Network identity stable across grouping-label edits; unknown VRF distinct from default |
| `network_node_versions` | Common version key, `network_id`, `device_id`, `role` | Same-generation network/device; unique `(generation_id,network_id,device_id)` in initial one-node-per-device-per-network model |
| `termination_point_versions` | Common version key, `network_node_id`, `interface_id` nullable, `local_key`, `kind` | Same-generation node/interface; unique `(generation_id,network_node_id,local_key)`; cannot assign interface from another device |
| `relation_identities` | `id BIGINT`, `public_id`, `scope_id`, canonical `identity_hash`, `identity_tuple` | PK `id`; unique `public_id`; unique `(scope_id,identity_hash)` with tuple collision check; numeric ID fits signed Nebula edge rank |
| `relation_versions` | `generation_id`, `relation_id`, `relation_kind`, `src_entity_id`, `dst_entity_id`, `network_id` nullable, `assertion_kind`, `resolution_status`, nullable effective times, bounded typed attributes | PK `(generation_id,relation_id)`; source/destination type and same-generation references validated; indexes by kind/src/dst/network |
| `relation_evidence` | `generation_id`, `relation_id`, `source_record_id`, `derivation_rule_version` nullable | Unique triple; derived facts preserve their input evidence and rule version |
| `access_observations` | `id`, `generation_id`, `source_record_id`, local device/interface nullable, raw remote identifiers, resolved remote device/interface nullable, `protocol`, `direction`, `observed_at`, `resolution_status` | Incomplete endpoints allowed here; never fabricate interface/node IDs to satisfy graph references |
| `route_versions` | Common version key, `device_id`, `routing_scope_key`, `scope_status`, `address_family`, normalized `prefix BINARY(16)`, `prefix_length`, next-hop address nullable, `egress_interface_id` nullable, `protocol`, optional metric/preference, `source_record_id` | Index `(generation_id,device_id,routing_scope_key,address_family,prefix_length)`; source IDs include route shard; unresolved egress retained |
| `route_forwarding_versions` | `generation_id`, `route_id`, `alternative_id`, local/remote termination point nullable, next-hop device nullable, `resolution_status`, `derivation_rule_version`, bounded evidence/skipped-node references | Unique `(generation_id,route_id,alternative_id)`; preserve alternatives; no arbitrary first next hop; projected topology references same generation |

These tables are added only in their implementation slice. Route facts/projections reside in MySQL initially, replacing legacy target-side ClickHouse materialization. Route access loads bounded per-device/scope candidate sets and uses a tested in-process prefix index; benchmark before enabling large routing workloads. Querying an upstream ClickHouse source does not make it the target route store.

### Typed relation rules

| Relation | Source -> destination | Meaning |
| --- | --- | --- |
| `HAS_COMPONENT` | Device -> Component | Asset ownership |
| `HAS_INTERFACE` | Device -> Interface | Interface ownership |
| `AGGREGATES` | Aggregation interface -> Interface | Evidenced membership, not proof of LACP health |
| `MEMBER_OF` | Device -> LogicalGroup | Logical membership, not physical placement |
| `REPRESENTS` | NetworkNode -> Device | Network-scoped view of shared asset |
| `HAS_TERMINATION_POINT` | NetworkNode -> TerminationPoint | Network endpoint ownership |
| `MAPS_TO` | TerminationPoint -> Interface | Binding when evidenced; not always physical |
| `PHYSICALLY_CONNECTED_TO` | Physical Interface <-> Physical Interface | Confirmed connection only; canonical unordered endpoint tuple |
| `OBSERVED_ADJACENCY` | TerminationPoint -> TerminationPoint | Directed source observation; network IDs must match |
| `DERIVED_CONNECTIVITY` | NetworkNode -> NetworkNode | Derived network-view relation with provenance and rule version |

One physical point-to-point port cannot have two simultaneously confirmed cable endpoints; conflicts block confirmation, not evidence ingestion. Network-layer constraints do not apply to asset ownership relations. A relation identity includes kind, view/network, canonical endpoints and a source discriminator when distinct assertions must coexist.

Operational graph vertices require active, resolved identities; unknown non-identity attributes can remain unknown. Traverse only active, resolved relations whose endpoints are eligible. Keep unresolved/conflicting observations and facts in SQL for inspection, not as traversable placeholder vertices. Both observed adjacency and derived connectivity require endpoints in the declared network. Ownership/mapping edges must agree with the typed parent fields; cycle-check component ownership and aggregation before publication.

`DERIVED_CONNECTIVITY` may collapse an evidenced directed termination-point adjacency to its two network nodes without claiming cabling or packet delivery. That exact view transformation is distinct from heuristic gap filling. Keep originating relation/evidence IDs; inference across a missing hop is excluded unless the API explicitly allows it and the relevant inference rule is implemented.

## 5. Nebula graph schema

One dedicated target graph space with `VID_TYPE=FIXED_STRING(128)`. Partition/replica counts are deployment parameters, not inferred from development. Tags/edge types below are explicit schema migration steps, not generated from arbitrary API strings.

VID format: `g:<generation-32hex>:e:<entity-32hex>` (69 ASCII characters). Node identity exposed to clients remains the stable entity ID, not this storage VID. Both endpoints of every edge must use the same generation prefix.

| Tag | Properties |
| --- | --- |
| `device` | `entity_id`, `generation_id`, `scope_id`, `name`, `device_kind`, `role` |
| `component` | Common identity fields, `component_kind` |
| `interface` | Common identity fields, `interface_kind`, `name`, `oper_state` |
| `logical_group` | Common identity fields, `group_kind`, `name` |
| `network` | Common identity fields, `layer`, `plane` |
| `network_node` | Common identity fields, `network_id`, `device_id`, `role` |
| `termination_point` | Common identity fields, `network_id`, `network_node_id`, `kind` |

Graph edges use lower-case equivalents of the typed relations above. Common edge properties: `relation_id` (public UUID), `generation_id`, `network_id` where applicable, `assertion_kind`, `resolution_status`, `derivation_rule_version`. Use the signed MySQL relation identity as edge rank; never collapse parallel observations into rank zero.

Physical undirected connections are stored as two directed edges with the same relation identity/rank. Public responses deduplicate by relation ID. Observed directed adjacency is not automatically mirrored. Algorithm edge allowlists depend on the requested mode; generic traversal across ownership/membership edges would produce invalid paths.

Do not put full source payloads or routing prefixes into generic graph vertices. Fetch evidence through MySQL and route alternatives through the routing repository. Address lookup is performed in MySQL before graph traversal.

### Query and write discipline

- Resolve scope, generation, requested network and authorization before constructing a query.
- Resolve known VIDs from MySQL IDs and use indexed/direct vertex access; no global scan filtered only after traversal.
- Graph queries always include allowed edge types and network filters. For physical mode, derive the allowed interface set from the selected network mappings before traversing asset connections.
- Validate fixed-format internal identifiers. Bind data values using the chosen client's supported parameter mechanism; identifiers/type names come only from server allowlists. Do not interpolate names or arbitrary nGQL supplied by clients.
- Insert vertices before edges, inspect every batch result, propagate all errors, and validate completeness plus read visibility before pointer publication.
- Maintain a candidate manifest of expected vertex/edge keys and payload digests. Verification must establish the expected records are visible, not merely compare counts that could mask wrong records.
- A schema/index change uses an explicit version; rebuild a candidate generation before publishing reads that require it.

## 6. History, retention and migration safety

Retained generation snapshots answer “what did the system publish at this time,” not arbitrary valid-time reconstruction. Nullable source effective times and retained evidence allow later historical analysis without promising a complete bitemporal system in the first slice.

Published generations are immutable. Keep referenced evidence, relation identities and entity registry rows while any retained generation references them. Failed candidates can be removed only after ownership/lease and worker-quiescence checks. No automatic published-generation deletion in the first implementation.

Versioned migrations use a target-only schema ledger with version, checksum, state and applied time. MySQL DDL is not assumed transactionally rollbackable; each step needs an explicit recovery procedure. Nebula DDL requires readiness checks before graph writes. Non-destructive schema evolution precedes application rollout; destructive changes require a separately approved maintenance operation.

## 7. Verified legacy field mapping

Paths below are relative to `/Users/qiuwenyuan/Documents/workspace/JD/topology_grid`. Native IDs are exposed by Go models, not verified database uniqueness constraints. Initial adapters must validate duplicates; live schema guarantees remain unverified.

| Source and evidence | Source-key input | Target normalization and limits |
| --- | --- | --- |
| `device_cmdb`, `model/mysql/cmdb_devices.go:3` | String `id` in source/table namespace | `name/type/sn/manufacturer/role/service_status` become device facts; preserve `dev_ip`, `management_ip`, `outofband_ip` as separate original address assertions. No source observation/deletion timestamp exists in this model. |
| `dev_port`, `model/mysql/dev_port.go:5` | Integer `id` | `dev_ip` resolves device; name/alias and string `if_index` remain distinct. `admin_status/operation_status/speed` are raw integer values until enum/unit contracts are verified. Port kind is not provided by the source. |
| `dev_interface_ipv4`, `model/mysql/dev_interface_ipv4.go:5` | Table + int64 `id` | Join `(dev_ip,interface)` with ambiguity checking; normalize `ipv4/ipv4_mask`; retain `role/update_time`. No VRF field. |
| `dev_interface_ipv6`, `model/mysql/dev_interface_ipv6.go:5` | Table + int64 `id` | Parse `ipv6/ipv6_mask` losslessly; keep family and prefix length; do not invent VRF/zone identity or IPv6 path support. |
| `dev_unit_cmdb`, `model/mysql/unit.go:5` | Table + integer `id` | Preserve `dev_id/dev_ip`, `stack_no/slot/unit_type/sn`; resolve parent explicitly. Serial and slot are not global identity. |
| `if_group`, `model/mysql/if_group.go:5` | Int64 row `id` for membership | `(dev_ip,if_group_name,member_if)` describes a relation. Resolve group/member interfaces; if group absent from port inventory, retain an unresolved membership rather than invent physical ports. |
| `dev_lldp_cmdb_table`, `model/mysql/lldp.go:5` | `lldp_id` uint64, serialized without precision loss | Preserve directed local/remote IP, interface names/indexes and source device IDs. `create_time` is a source record time, not proven observation freshness. Previous-neighbor columns stay historical evidence, not current links. |
| CMDB LLDP API, `model/mysql/lldp_connection.go:3` | Native ID not retained by legacy DTO | The available directed endpoint tuple is only an observation signature. Adapter must retain a real source ID if available; otherwise declare signature identity and keep incomplete endpoints as observations. |
| `route_0` through `route_15`, `model/mysql/route.go:5` | Shard table + int64 `id` | Normalize prefix and next hop; retain both management IP fields, protocol, interface names, `delete_status`, create/update times. No VRF, metric/preference or explicit family field; family is parsed from address syntax. |
| `dev_arp_table`, `model/mysql/dev_arp_table.go:5` | Int64 `id` | Device/interface/host IP/MAC and timestamps become observations; neither IP nor MAC implies globally unique asset identity. |
| `dev_mac_table`, `model/mysql/dev_mac_table.go:5` | Int64 `mac_item_id` | Preserve device/interface context; VLAN/bridge domain absent, so do not merge observations across those unknown domains. |
| `arp_host`, `model/mysql/arp_host.go:5` | Int64 `arp_host_id` | Preserve interface references, IP/MAC, `is_bond`, source string timestamps. `flag` semantics are unknown; it is not automatically a tombstone. |

Legacy runtime behavior that needs explicit characterization, not automatic transplantation:

- Inventory preparation overwrites `dev_ip` with management IP (otherwise out-of-band), skips rows with neither, and filters offline devices. The new inventory preserves original aliases and lifecycle facts; topology eligibility is a separate policy. Evidence: `module/manager/standard_layer/topology/prepare_data.go:2465`.
- Port classification is name-derived, including dotted subinterfaces; preserve the rule/version as inference rather than claiming the source supplied a physical/logical type. Evidence: `module/manager/standard_layer/topology/prepare_data.go:608`.
- Route loading filters `delete_status=0` and protocols `static/local`. That filtered view is not a complete routing table or a deletion feed. Initial parity fixtures must name this coverage; broader protocol ingestion requires explicit adapter support. Evidence: `module/manager/standard_layer/topology/route_processing.go:94`.
- LLDP graph link status and reverse aggregate edges are generated; they do not prove observed link health or reciprocal discovery. Evidence: `module/manager/standard_layer/topology/prepare_data.go:1686`.

No source-native ID is accepted as globally unique across tables, source instances or route shards. Unknown address/routing scope remains a source-qualified unknown namespace; it cannot be used to join separate sources or assert default-VRF routing.
