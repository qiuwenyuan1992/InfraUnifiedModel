# Current graph implementation plan

## Accepted direction

Nebula owns current devices, interfaces, addresses and topology relations in a dedicated space. MySQL owns scopes, source registration, stable identities, runs and lightweight publication metadata. External CMDB ClickHouse owns source history; no history connector is invented here. No collector or publication worker is included in this implementation slice.

Asset IDs are stable 32-character lowercase hexadecimal IDs, not IPs, names or generation-qualified IDs. Vertex IDs combine scope, kind and asset ID. Graph fields include scope and entity IDs; traversal relationships include device/interface ownership, addressing, physical links and aggregation membership. No per-generation asset copies are written.

The existing `generation_id` API field describes the current published batch, not a historical snapshot. A non-current selector or cursor returns a conflict. Cursor format is bumped. Scope projection state and monotonic epoch guard reads before and after graph access; updating/failed/uninitialized scopes are unavailable. Future writers MUST durably fence reads before their first graph mutation, finish graph writes before publishing and never restore readiness after an incomplete write. This does not provide a distributed transaction.

## Tasks

- [x] Introduce current domain structs and separate graph inventory reader; remove SQL asset reads. Preserve MySQL control operations and authorization/idempotency behavior.
- [x] Add Nebula session lifecycle, parameterized bounded queries, strict decoding, stable VID helpers and explicit graph DDL. No automatic graph creation or legacy-space access.
- [x] Change service reads to current-only with before/after projection checks; add stale cursor, authorization, graph failure and update-race regressions.
- [x] Add non-destructive control-state migration; preserve checksummed 0000/0001 scripts. Existing empty asset tables are dormant rather than a runtime dependency; no SQL asset writes or automated table drops.
- [x] Wire graph dependency into HTTP only, update DTOs/config/docs, then run focused tests, full suite, build, vet and targeted race tests.

## Verification and boundaries

No configured MySQL, Nebula or legacy submodule is modified. Initializers are explicit. Unit tests use isolated SQLite and fake graph executors. Live Nebula execution and production-scale performance require later integration validation; do not describe mocks as live validation.

2026-09-16 local verification:

- `go generate ./cmd/server/wire` regenerated graph injection and session-pool cleanup.
- `go test ./... -count=1 -timeout=90s` passed after updating the migration-server assertion for the third ledger entry and checking both projection columns.
- `go build ./...` and `go vet ./...` passed.
- `go test -race ./pkg/nebula ./internal/repository ./internal/service ./internal/router ./internal/server ./internal/migration -count=1 -timeout=90s` passed.
- Strengthened the unready-active-batch regression: an unpublished active batch now returns projection-not-ready rather than resource-not-found. The test failed before the service fix and passed afterward.
- Graph query, decoder and client lifecycle received a local code review; the independent review session could not be retrieved and is not counted as completed verification.
- No migration command, configured server or external database connection was run. Collectors, publishing, history adapters and topology query APIs remain out of scope.
