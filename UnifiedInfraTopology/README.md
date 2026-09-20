# UnifiedInfraTopology

A Go infrastructure inventory API using the Nunu layered layout, MySQL for control
metadata and NebulaGraph for current assets. [简体中文（推荐）](README_zh.md)

## Layout

| Path | Responsibility |
| --- | --- |
| `cmd/server` | HTTP API entry point and dependency wiring |
| `cmd/migration` | Explicit, operator-run SQL migration entry point |
| `api/v1` | Versioned request/response DTOs and API contracts |
| `internal/handler` | HTTP transport, binding and response mapping |
| `internal/service` | Business rules, authorization and orchestration |
| `internal/repository` | MySQL control/identity persistence and Nebula current-asset access |
| `internal/model` | Domain and persistence models |
| `internal/migration` | Versioned SQL schema and migration ledger logic |
| `internal/server` | HTTP server construction and routes |
| `pkg` | Shared infrastructure: configuration, logging, JWT and graph clients |
| `*_test.go`, `test` | Colocated tests, existing server tests and `test/mocks` |

The old task/job samples are replaced with an explicit foundation:

- `cmd/worker`: independent entry point and Wire wiring, configuration loading and graceful SIGINT/SIGTERM shutdown.
- `internal/adapter`: external-source boundary. `SourceAdapter.Validate` defines preflight checks; the CMDB placeholder returns `ErrNotImplemented`.
- `internal/service/sync_worker.go`: orchestration boundary. `Run` waits in standby; `Execute` returns an explicit unimplemented error, never a false success.

Run the foundation without database credentials:

```sh
make worker CONF=config/local.example.yml
# Or run the entry point directly; Ctrl+C stops it.
go run ./cmd/worker -conf config/local.example.yml
```

`APP_CONF` overrides `CONF` / `-conf`. `make build` and `make wire` include the worker.
The worker does not start HTTP, connect to MySQL/Nebula, migrate, claim jobs or mutate queued runs.
Collection, publication, source configuration resolution and job leases remain unimplemented.
Collection result contracts will be defined with real integrations; no fake assets or successful empty syncs are provided.

## Implemented API

The implemented `/v1/scopes` API provides authenticated scope, device, interface,
address, source and published-generation reads, plus durable sync-run
creation, listing, retrieval and cancellation. Existing user endpoints remain available.
Sync runs are **queued only**: no collection worker, publication pipeline, topology
queries or upstream writes are included. Evidence, source progress, checkpoints and
coverage summaries are not yet exposed. Generated Swagger covers only existing user
routes, not the full inventory API.

## Configuration and startup

Run commands from this module directory. For a new local setup:

```sh
cp config/local.example.yml config/local.yml
```

Existing users must keep their configuration files intact; do not overwrite them with
examples. `config/local.example.yml` and `config/prod.example.yml` contain sanitized
placeholders. Fill in database credentials and a strong JWT secret. Local `config/local.yml`,
`config/prod.yml`, `storage/nunu-test.db` and runtime outputs are ignored, not deployment artifacts;
untracking existing files does not delete them from disk. Previously committed defaults
and secrets remain in Git history: rotate exposed credentials and never use defaults
in production.

Inventory uses `data.db.main`; server startup does not migrate it. Configure real
user/scope IDs and an independently generated cursor-signing secret of at least
32 bytes (not the JWT secret):

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
go run ./cmd/migration -conf config/local.yml
go run ./cmd/server -conf config/local.yml
```

`APP_CONF`, when set, overrides `-conf`; verify or unset it before either command.
There is no automatic bootstrap or DDL execution during API startup.

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

## Model baseline

The previous executable NebulaGraph schema and current-graph initialization instructions
were retired during the 2026-09-20 development baseline reset. Do not initialize a graph
space from historical code or documentation. The next schema must be generated and
reviewed from the authoritative entity and relationship definitions under `docs/model/`.

## Development and verification

```sh
make help
make test   # 完整测试范围：./...
make build
make vet
make race
make wire
make swag   # 仅生成已有用户路由的 Swagger 元数据
```

`make test` includes colocated tests and existing server tests. `make wire` regenerates
dependency wiring; `make swag` does not add inventory endpoint coverage. Ordinary tests
use isolated SQLite fixtures and mocks, not the configured database. Live graph checks
are skipped unless explicitly enabled against a dedicated development space:

```sh
INVENTORY_GRAPH_TEST_CONFIG=/absolute/path/to/development.yml \
  go test ./internal/repository -run '^TestGraphInventoryLiveReadOnly$' -count=1
```

This opt-in test checks schema and queries read-only in `unified_inventory_current`.
Initialize the dedicated schema before running this check; default tests do not verify it.
`make test`, `make coverage` and `make race` explicitly disable live checks; use the direct
command above to opt in. Do not target production or perform production writes for verification. Live MySQL DDL compatibility, large-scale graph performance and production
deployment remain unverified. The current entity and relationship model is defined
under `docs/model/`; legacy implementation plans and specifications are no longer authoritative.

## License

See [LICENSE](LICENSE) for the MIT license.