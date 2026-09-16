# Inventory schema migration

## Invocation and scope

Call `migration.Apply(ctx, targetDB)` from the explicit migration command, not API startup. The caller supplies an existing GORM target connection. This package does not read source configuration, open upstream connections, or create Redis, MongoDB, ClickHouse, or Nebula schemas.

The MySQL target requires InnoDB, session `autocommit=1`, and MySQL 8.0.16 or newer for enforced CHECK constraints. The runner rejects non-autocommit sessions before schema writes so ledger states are durable independently of DDL. SQLite is an isolated test equivalent, not a production compatibility claim. Do not use domain `AutoMigrate`: the embedded `mysql/0000_ledger.sql`, `mysql/0001_inventory.sql`, `mysql/0002_current_graph.sql`, and corresponding `sqlite/` scripts are authoritative.

Version 0 bootstraps `topology_schema_migrations`; version 1 creates the initial inventory, identity, and queued-run tables. Version 2 (`0002_current_graph.sql`) adds scope `projection_state` and `projection_epoch`, with checks for the supported states and a nonnegative epoch. The original version 0 and 1 scripts remain byte-for-byte unchanged. No worker, graph publication, identity-head promotion, or source ingestion is implemented by these migrations.

Every scope starts `uninitialized` at epoch 0, including scopes with an existing active generation. Migration never infers graph readiness from historical SQL rows. Versions 0/1 asset tables remain dormant: the application neither reads nor writes them and the migration does not drop, copy, or backfill asset data. Nebula stores only current graph assets; MySQL generations retain lightweight batch metadata, not asset snapshots.

Future publishers must durably change the scope to `updating` and monotonically increment its epoch before the first graph mutation. After graph writes finish, atomically publish ready generation metadata and the scope's active generation/state. Failed or incomplete graph updates leave the scope `failed` or `updating`, never `ready`. Epochs must never be reset or reused, even when retrying the same generation. The SQL check enforces nonnegativity; monotonic increments are a publisher transaction obligation. No publisher is implemented here.

Readers require `ready`, an active published generation, and both inventory/graph readiness. They check scope state, active generation and epoch before and after graph operations. A non-current selector or stale v2 cursor returns a conflict; old cursor versions are rejected. These fences reject observed concurrent updates, but do not create a cross-database transaction.

## Safety and repeated execution

- MySQL obtains a database-specific `GET_LOCK` on one pinned physical connection before any schema change or ledger inspection. It releases that same session lock even when the caller cancels. An uncertain lock acquisition or failed release discards the connection rather than returning a potentially locked session to the pool.
- SQLite uses `BEGIN IMMEDIATE` before ledger inspection. Version DDL executes inside a savepoint. On failure, it rolls back the version's DDL and commits the failed ledger marker while still holding the write lock.
- Every existing version is checked against the SHA-256 of the exact embedded dialect script, including whitespace. Modified checksums, unknown versions, missing predecessors, `dirty`, and `failed` states stop execution. Applied migrations are never edited in place.
- MySQL writes `dirty` before version DDL. DDL may commit independently and cannot be assumed rollbackable. Successful completion changes the row to `applied`; errors attempt to persist `failed` with the statement number. A crash or lost connection may leave `dirty`, which is equally blocking.
- There are no automatic down migrations, destructive repairs, table drops, or retry/reset switches.

## Manual recovery

1. Stop competing migration attempts and application schema writes. Capture the error, release version, ledger rows, and a target backup using the deployment's approved procedure. Never modify upstream data.
2. Compare the exact release SQL and recorded checksum. A mismatch normally means the wrong application release or an edited historical migration; restore the correct release rather than replacing the stored checksum.
3. For a MySQL `dirty`/`failed` version, inspect every table, column, index, foreign key, and check constraint against each numbered script statement. Some earlier statements may already be committed. Do not rerun the entire script or mark the version applied merely because its tables exist.
4. An operator must approve and execute a concrete repair plan. Prefer completing missing non-destructive statements after reviewing actual schema state. Only after verifying the complete version may the operator reconcile its ledger row to `applied` and record the recovery in the operational audit trail. This package does not perform that reconciliation.
5. SQLite normally rolls back version DDL while preserving `failed`. Before a deliberate ledger repair and retry, verify that rollback completed and that the original conflicting objects remain understood. Never blindly remove a ledger row on MySQL. If bootstrap itself failed, inspect the ledger structure before any repair.
6. Re-run the explicit migration command only after the reviewed repair. A normal rerun must perform checksum checks without altering existing inventory data.

## Constraints and remaining boundaries

IDs are MySQL `CHAR(32) CHARACTER SET ascii COLLATE ascii_bin`. Active/base/run-generation references enforce scope membership. The dormant version-1 asset tables retain their original `(generation_id, entity_id)` keys, same-generation device/interface references, and nullable properties for migration compatibility only. Current graph domain models have no SQL asset table mapping.

The minimal model contracts do not duplicate scope/kind columns on every child row. Entity-kind and entity/generation scope agreement, run/source scope agreement, source-key/entity binding agreement, canonical hash collision checks, and published-generation immutability still require application validation before use or publication. Foreign keys are not substitutes for those policies. For SQLite application tests, enable foreign keys on every pooled connection via `?_pragma=foreign_keys(1)` (the migration also enables them on its pinned connection).

## Isolated checks

```sh
go test ./internal/migration ./internal/model -count=1
go test -race ./internal/migration -count=1
go build ./internal/migration ./internal/model
go vet ./internal/migration ./internal/model
```

The migration tests use temporary SQLite databases and MySQL protocol mocks. They do not connect to a real MySQL server. Passing them does not verify MySQL server-version compatibility; that requires a separately approved isolated MySQL integration environment.
