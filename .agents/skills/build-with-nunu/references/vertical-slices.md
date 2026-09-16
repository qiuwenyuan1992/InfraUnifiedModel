# Vertical feature slices

Use this reference when adding or reshaping application behavior.

## Map behavior to artifacts

Start from a user-visible contract, then identify the smallest complete slice.

| Concern | Common home | Keep out |
| --- | --- | --- |
| Request/response DTOs, documented errors | `api/v1` or adjacent transport package | GORM queries, business orchestration |
| Persistence shape | `internal/model` plus the repository's migration mechanism | HTTP binding and response formatting |
| Storage/integration access | repository interface and implementation | Gin contexts, status codes |
| Business policy and transactions | service interface and implementation | Route registration, raw response writes |
| Input binding and error translation | handler | Durable business rules |
| Auth grouping and route paths | router or HTTP server | Data access |
| Object graph | `wire.go` provider sets | Hand-authored `wire_gen.go` |

## Follow the local contract style

- Preserve the project's JSON naming, validation tags, response envelope, business error codes, pagination, and Swagger annotation style.
- Return stable domain/application errors from lower layers and translate them consistently at the transport edge.
- Do not leak database models in public responses when the repository already separates API types.
- Pass request contexts down through services and repositories. Do not replace them with `context.Background()` in a request flow.
- Reuse request-scoped logging and correlation metadata where the project exposes them.

## Preserve repository boundaries

- Define repository interfaces small enough for the service use case and straightforward to mock.
- Use parameterized GORM queries and the existing `DB(ctx)` or equivalent so transactions propagate.
- Keep external clients behind repositories or dedicated adapters when that is the local convention.
- Orchestrate multi-repository atomic work through the existing transaction abstraction. Do not open independent transactions inside each repository call.
- Map `gorm.ErrRecordNotFound` and unique/conflict failures to the project's established errors.

## Preserve service boundaries

- Put validation that requires domain state in the service, while syntax/shape validation stays at the transport edge.
- Make authorization decisions close to the business action when they go beyond route-level authentication.
- Hash and compare secrets using the repository's established security helpers; never log credentials, tokens, DSNs, or raw secrets.
- Keep time, IDs, and external side effects injectable or otherwise controllable when tests need determinism.

## Register the feature completely

For an Advanced HTTP layout, a new feature often requires all of:

```text
api/v1/<feature>.go
internal/model/<feature>.go
internal/repository/<feature>.go
internal/service/<feature>.go
internal/handler/<feature>.go
internal/router/<feature>.go
internal/router/router.go          dependency field, when RouterDeps is used
internal/server/http.go            router initializer
cmd/server/wire/wire.go            constructors in provider sets
tests and generated mocks
```

Base layouts may register the handler directly in `internal/server/http.go` and inject it directly into the HTTP constructor. Match that simpler graph.

## Use generation safely

Before `nunu create`:

1. Confirm the current directory contains the intended module's `go.mod`.
2. Confirm target files do not already exist.
3. Check whether the repository uses custom templates or a newer layout style.

After generation:

1. Inspect every generated file before extending it.
2. Replace placeholder CRUD behavior with the actual contract.
3. Add missing API/router/Wire/migration/test artifacts.
4. Format and compile before building further behavior on top.

Generation accelerates naming and placement; it does not establish that the slice is complete.
