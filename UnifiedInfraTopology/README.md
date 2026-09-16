# Nunu — A CLI tool for building Go applications.

Nunu is a scaffolding tool for building Go applications. Its name comes from a game character in League of Legends, a little boy riding on the shoulders of a Yeti. Just like Nunu, this project stands on the shoulders of giants, as it is built upon a combination of popular libraries from the Go ecosystem. This combination allows you to quickly build efficient and reliable applications.

[简体中文介绍](https://github.com/go-nunu/nunu/blob/main/README_zh.md)

![Nunu](https://github.com/go-nunu/nunu/blob/main/.github/assets/banner.png)

## Documentation
* [User Guide](https://github.com/go-nunu/nunu/blob/main/docs/en/guide.md)
* [Architecture](https://github.com/go-nunu/nunu/blob/main/docs/en/architecture.md)
* [Getting Started Tutorial](https://github.com/go-nunu/nunu/blob/main/docs/en/tutorial.md)
* [Unit Testing](https://github.com/go-nunu/nunu/blob/main/docs/en/unit_testing.md)


## UnifiedInfraTopology inventory foundation

The implemented `/v1/scopes` API provides authenticated scope, device, interface,
address, source and published-generation reads, plus durable sync-run
creation, listing, retrieval and cancellation. Existing user endpoints remain available.
Sync runs are **queued only**: no collection worker, publication pipeline, topology
queries or upstream writes are included. Evidence, source progress, checkpoints and
coverage summaries are not yet exposed. The generated Swagger documentation still
covers the scaffold endpoints, not this inventory slice.

### Configuration and startup

Use a deployment-owned configuration file, replacing the scaffold database and JWT
credentials. Inventory uses `data.db.main`; server startup does not migrate it.
Add the following configuration with real user/scope IDs and an independently
generated cursor-signing secret of at least 32 bytes (not the JWT secret):

```yaml
inventory:
  cursor_key: "REPLACE_WITH_DEPLOYMENT_MANAGED_SECRET"
  grants:
    - user_id: "authenticated-user-id"
      scope_id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      permissions: ["inventory:read", "sync:read", "sync:write"]
```

There are no default grants. Scope and source records are deployment-managed; no
seed data or configuration CRUD endpoints are provided. Missing cursor configuration
makes list operations unavailable. Cursors bind the caller, scope, resource, filters
and current publication batch plus projection epoch (cursor format v2). Old cursors,
non-current batch selectors and cursors invalidated by a graph update cannot read
historical assets; restart pagination against the current batch. Changing the signing
secret also invalidates existing cursors.
Inventory IDs are 32 lowercase hexadecimal characters.

Run these commands from this module directory **only after verifying the target
configuration and backing up the target database**:

```sh
go run ./cmd/migration -conf /absolute/path/to/deployment.yml
go run ./cmd/server -conf /absolute/path/to/deployment.yml
```

The migration command applies the inventory schema and existing user migration,
then exits. It is never invoked by the API process. Schema versions and checksums
are tracked in `topology_schema_migrations`. Never edit applied SQL or clear a dirty
ledger to force a retry. If migration fails, inspect the ledger and actual schema,
restore from backup or perform an operator-reviewed repair before rerunning. MySQL
DDL can commit partially; SQLite rollback tests do not establish MySQL compatibility.
For SQLite deployments/tests, enable foreign keys on **every pooled connection**, for
example with the supported driver DSN `file:inventory.db?_pragma=foreign_keys(1)`.

Enqueue requires `Content-Type: application/json`, authentication and an
`Idempotency-Key` header. It accepts `source_ids`, `mode: "full"` and nullable
`base_generation_id`, and returns HTTP 202 with `Location`. Replaying the same
normalized body/key returns the original run. Creating a run does not execute it.
Asset reads require a ready scope and its current published batch with both inventory
and graph readiness. Otherwise they report `projection_not_ready`. A `generation_id`
selects only that current batch; a non-current selector returns a conflict, not history.

### Current graph storage and initialization

Nebula stores current devices, interfaces, addresses and relationships using stable
scope/kind/entity VIDs. MySQL stores control and identity metadata, not asset copies
per batch. Existing `0000`/`0001` migrations remain checksummed and unchanged;
`0002` adds projection state/epoch. The old asset tables are unused, not auto-dropped.
Source history belongs to the external CMDB ClickHouse; no history adapter is implemented.
This section supersedes snapshot-read assumptions in the earlier foundation plan.

Review `docs/schema/current_graph.ngql` and execute its phases manually in a **new,
dedicated space**. Do not run it in legacy spaces. Review partition/replica settings,
wait for metadata and index availability between phases, then configure:

```yaml
inventory:
  # Merge with cursor_key/grants above; do not create a second inventory key.
  graph:
    hosts: ["127.0.0.1:9669"]
    space: "unified_inventory_current"
    username: "DEPLOYMENT_USER"
    password: "DEPLOYMENT_SECRET"
    timeout_seconds: 5
```

Startup never creates graph schema. Missing hosts or space leaves asset reads
unavailable without SQL fallback; control APIs can still start. An explicitly configured
but invalid/unreachable graph may fail startup. SDK socket timeouts bound individual
network operations, not the total HTTP deadline; cancellation is checked before/after
SDK execution and does not interrupt an in-flight call.

Scopes start `uninitialized`. A future publisher must durably mark the scope `updating`
and increment its monotonic epoch **before any graph mutation**, complete and validate
all writes, then publish the current batch and `ready` state atomically in MySQL.
Failure must leave the scope unavailable, never restore readiness over partial writes.
Readers recheck state, epoch and active batch after graph access. This is a write-fence
protocol, not a distributed transaction. No publisher or CMDB collection worker is
included; initializing schema does not import assets or make a scope ready.

Graph addresses are textual IPs, decoded into domain bytes; IPv4 uses mapped sixteen-byte
encoding internally and renders dotted-quad JSON. IPv6 remains IPv6. Graph list limits
bound response size only: validate scan/sort cost with EXPLAIN/PROFILE on realistic
volumes before production; million-interface performance is not established.

### Verification

```sh
go test ./... -count=1 -timeout=90s
go build ./...
go vet ./...
```

Tests use isolated SQLite fixtures and mocks, not the configured database. Live MySQL
DDL compatibility and production deployment remain unverified. See the inventory
foundation plan and API/storage contracts under `docs/superpowers/` for scope details.

## License

Nunu is released under the MIT License. For more information, see the [LICENSE](LICENSE) file.