# Inventory foundation implementation plan

**Goal:** Ship an executable target schema plus authenticated inventory reads and durable synchronization-run controls, not another design-only delivery.

**Architecture:** Existing Nunu layers; MySQL production persistence, isolated SQLite tests. Published generation is pinned for reads. No upstream or Nebula writes in this slice.

**Spec:** `../specs/2026-09-15-topology-migration-design.md`, storage schema and API contract alongside it.

## Scope

Implement scopes, sources, generations, source keys/bindings, devices, interfaces, addresses, and queued sync runs. Preserve the existing user feature. Do not pretend queued tasks have been executed: this slice provides enqueue/read/cancel, not a source worker or publication pipeline. Authentication and deployment-configured scope grants are required. No real database connection is used during development checks.

## Tasks

- [x] Add model contracts and explicit versioned migration SQL, checksum ledger and isolated tests. Migration supports MySQL and SQLite tests only; no automatic schema mutation during server startup. Existing user migration remains supported.
- [x] Add transaction-aware inventory repository and service, bounded signed-cursor lists, generation eligibility checks and source validation. Test duplicate IPs, unpublished/cross-scope generations, empty inventories, immutable terminal runs and null-base idempotency replay.
- [x] Add `/v1/scopes` and scope-scoped device/interface/address/source/generation/run routes. Test real router registration, authentication, permission denial, 202/Location and invalid payloads.
- [x] Wire constructors into server injector and regenerate generated code. Remove the example background loop from API composition, without deleting unrelated sample packages.
- [x] Run focused tests then all tests, build and vet; review all touched files and document how to run migrations and configure grants. Do not run migrations against the user's database.

## Implementation boundaries

- `internal/model/inventory.go`: persistence structs only, stable string IDs and nullable unknown times.
- `internal/migration/`: embedded dialect-specific versioned SQL and checksum-verified application with explicit failure handling.
- `internal/repository/inventory.go`: target reads and atomic run operations using `Repository.DB(ctx)`.
- `internal/service/inventory.go`: UUID validation, signed pagination, scope grants, caller-request idempotency hashing and run policy.
- `api/v1/inventory.go`, `internal/handler/inventory.go`, `internal/router/inventory.go`: typed transport and error mapping; no SQL or upstream DTOs.
- Existing router/Wire/migration composition: minimal constructor and registration changes.

## Acceptance commands

`go test ./internal/migration ./internal/repository ./internal/service ./internal/router`

`go test ./... && go build ./... && go vet ./...`

## Verification record — 2026-09-15

- `go test ./... -count=1 -timeout=90s`: passed.
- `go build ./...` and `go vet ./...`: passed.
- `go test -race ./internal/repository ./internal/service ./internal/router ./internal/server -count=1 -timeout=90s`: passed.
- Final review identified IPv4-mapped address formatting and Gin default query logging; both now have regression tests that failed before the fixes and passed afterward.
- Deployment configuration, explicit migration invocation, dirty-ledger recovery precautions and limitations are documented in `README.md`.
- No configured/live database migration, upstream access or production deployment was performed. Generated Swagger documentation for inventory routes remains outstanding; workers, publication, evidence and per-source progress remain outside this slice.

All application-layer behavior must be exercised with isolated data. SQLite passing is not proof of MySQL DDL/server compatibility; real MySQL integration remains explicitly unverified until an approved test instance exists.
