# Nunu layout recognition

Use this reference after running `scripts/inspect-nunu-project.sh`. The working repository is authoritative; Nunu layouts evolve independently and may be customized.

## Recognition table

| Variant | Strong signals | Typical composition points |
| --- | --- | --- |
| Nunu CLI | Module `github.com/go-nunu/nunu`, `internal/command`, `tpl/create` | `cmd/nunu/root.go`, `internal/command/*`, `tpl/create/*.tpl` |
| Base / legacy layered API | `internal/{handler,service,repository,model}`, no `api/v1` or `internal/router` | `internal/server/http.go`, `cmd/server/wire/wire.go` |
| Advanced API | `api/v1`, `internal/router`, transaction abstraction, jobs/tasks | `internal/router/*.go`, `internal/server/*.go`, each `cmd/*/wire/wire.go` |
| MCP server | `mcp-go` dependency, `internal/server/mcp.go` | MCP definitions in server, callbacks in handler, providers in server Wire set |
| Monorepo | One root `go.mod`, apps under `app/<name>`, shared root packages | `app/<name>/cmd/*/wire/wire.go`, app-local internals, root shared packages |
| Admin / chat | Advanced-style layers plus UI/RBAC or WebSocket/Pitaya code | Inspect the feature's owning app and transport-specific server/router |

Names alone are weak signals. Confirm imports, constructor signatures, provider sets, and a complete neighboring feature.

## Layout selection for a new project

- Choose Base for a small service that needs only the core layered API structure.
- Choose Advanced for a production API or learning project that benefits from examples for persistence, auth, transactions, migrations, background work, documentation, and tests.
- Choose Admin for an admin product that needs the supplied UI, JWT, and Casbin RBAC conventions.
- Choose MCP Server for tools, resources, or prompts exposed over stdio, SSE, or Streamable HTTP.
- Choose Monorepo when multiple deployable applications intentionally share packages or models.
- Choose Chat for long-lived WebSocket/TCP or real-time service patterns.

When the choice affects long-term boundaries, state the tradeoff and ask only if the product requirements do not make the choice clear.

## Canonical discovery paths

Inspect these paths rather than assuming they all exist:

```text
api/v1/                         transport contracts and response errors
cmd/<entry>/main.go             executable entry point
cmd/<entry>/wire/wire.go        Wire injector source
cmd/<entry>/wire/wire_gen.go    generated dependency graph
config/                         environment configuration
internal/handler/               transport adapters
internal/router/                route grouping and middleware policy
internal/service/               business use cases
internal/repository/            persistence and integrations
internal/model/                 persistence/domain data structures
internal/server/                HTTP, migration, task, job, MCP servers
internal/job/, internal/task/   background behavior
pkg/                            deliberately reusable infrastructure
test/ or *_test.go              test conventions and generated mocks
```

For a monorepo, prefix app-owned paths with `app/<name>/`. Do not move app-specific behavior into a shared root package merely to reduce duplication.

## Evidence order

Use this order when examples disagree:

1. Instructions and tests in the target repository
2. A complete neighboring feature in the same app
3. Current constructors, interfaces, router/server composition, and Makefile targets
4. Current go-nunu layout repositories and documentation
5. Generic Go or remembered Nunu patterns

This order matters because the Nunu CLI's embedded `create` templates and independently updated layout repositories can temporarily represent different generations of the architecture.
