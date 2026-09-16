# Testing and verification

Use the repository's own targets when present. The commands below are fallbacks, not a mandate to add tools or change dependencies.

## Test in increasing scope

1. Run the nearest changed package or named test:

   ```bash
   go test ./internal/service -run 'TestFeature'
   go test ./test/server/service -run 'TestFeature'
   ```

2. Run the affected layer or app:

   ```bash
   go test ./internal/...
   go test ./test/server/...
   go test ./app/admin/...
   ```

3. Run the module suite and compile entry points:

   ```bash
   go test ./...
   go build ./...
   go vet ./...
   ```

4. Use `go test -race ./...` when concurrency, jobs, caches, transports, or shared state changed and the environment supports it.

## Test each boundary

- Handler: bind/validation failures, auth context, service error mapping, status and response envelope.
- Service: business invariants, authorization, transaction behavior, error propagation, side-effect ordering.
- Repository: query filters, not-found/conflict mapping, transaction context, write behavior. Use the project's sqlmock/redismock/test database convention.
- Router/server: route path, method, middleware group, and provider availability.
- Job/task: cancellation, retries or idempotency where relevant, and no leaked goroutines.
- MCP: valid and invalid arguments, result content type, protocol errors, cancellation, and each enabled transport that changed.

Prefer table-driven tests when several cases share setup. Match the repository's existing assertion and mocking libraries instead of introducing another test stack.

## Regenerate deterministically

- Prefer tool versions pinned by `go.mod`, tool directives, scripts, or existing automation. Avoid `@latest` when the repository tracks generated artifacts.
- Add constructors to `wire.go`, then run the repository's Wire target or `nunu wire <injector-dir>`.
- Generate mocks only from the final interface. Use the Makefile target when one exists.
- Regenerate Swagger only when annotations or public contracts changed and the repository tracks generated docs.
- Review generated diffs; large unrelated churn usually indicates a tool-version or scope mismatch.

## Diagnose common failures

| Symptom | Likely check |
| --- | --- |
| Wire reports no provider | Constructor missing from the correct entry point's provider set, or interface binding/return type mismatch |
| Generated app compiles but route is absent | Router initializer or server registration missing |
| Transaction test bypasses the transaction | Repository used raw `db` instead of the context-aware accessor |
| Handler test returns the wrong envelope | Bypassed `api/v1` response/error helpers |
| Config key is empty | Wrong config namespace, wrong app config, or `APP_CONF`/`-conf` mismatch |
| Tests need live MySQL/Redis unexpectedly | Integration leaked below a mockable repository boundary or test config selected a live backend |
| `nunu run` passes flags incorrectly | Separate program args with `--`, for example `nunu run ./cmd/server -- --conf=...` |
| Compiler reports inconsistent Go versions | Compare `go version`, `go env GOROOT`, and the repository's `go`/`toolchain` directives before changing code |

If a required external service is unavailable, still run formatting, compilation, unit tests, and static checks that do not require it. Report the precise skipped command and dependency.
