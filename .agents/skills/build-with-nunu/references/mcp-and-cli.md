# MCP layout and Nunu CLI work

Load only the section relevant to the task.

## MCP server changes

Trace the local callback flow before editing:

```text
MCP client -> internal/server/mcp.go -> handler -> service -> repository/integration
```

- Define concise, action-oriented tool names and descriptions with constrained input schemas.
- Bind and validate arguments in the handler using the installed `mcp-go` API; do not invent methods from another SDK version.
- Return protocol-appropriate tool results. Distinguish a tool-reported failure from a transport/server error according to neighboring handlers.
- Register tools, resources, prompts, notifications, and capabilities in `internal/server/mcp.go` or the repository's equivalent.
- Keep external I/O and durable state behind services/repositories so handlers remain testable.
- Propagate cancellation and set timeouts for outbound I/O.
- Annotate or document destructive/open-world behavior when the installed SDK supports it.

If stdio transport is enabled, keep stdout reserved for MCP protocol frames. Configure application logs for files or stderr exactly as supported by the local server; console logging to stdout can corrupt the session.

Verify the built binary with the MCP Inspector when Node is available and interactive protocol testing is in scope:

```bash
go build -o ./bin/server ./cmd/server
npx -y @modelcontextprotocol/inspector ./bin/server
```

Do not leave the Inspector or server running after verification.

## Nunu CLI changes

The CLI is a Cobra application. Trace the command from registration to implementation before editing:

```text
cmd/nunu/root.go -> internal/command/<name> -> helper/config/template code
```

- Preserve non-interactive argument behavior as well as survey prompts.
- Validate paths and inputs before file creation, removal, cloning, or process execution.
- Keep errors actionable and wrap the operation being attempted.
- Check Unix and Windows implementations when changing `nunu run`, process handling, signals, paths, or argument splitting.
- Update `config/config.go` when adding layout/tool defaults, then verify help text and examples.
- Edit embedded source templates under `tpl/create`; test both default templates and `--tpl-path` overrides when affected.
- Treat changes to `nunu new` as potentially destructive because it can replace an existing project directory. Preserve confirmation and failure behavior.
- Test command help, exact argument counts, nested component paths, existing-target handling, and external command failures.

When changing generation behavior, validate output in a temporary module rather than the skills repository. Compile and format the generated code, then inspect its Wire and import behavior.
