#!/bin/sh

set -eu

start_path=${1:-.}

if [ ! -d "$start_path" ]; then
  printf 'error: not a directory: %s\n' "$start_path" >&2
  exit 2
fi

project_root=$(cd "$start_path" && pwd -P)
search_root=$project_root

while [ ! -f "$search_root/go.mod" ]; do
  parent=$(dirname "$search_root")
  if [ "$parent" = "$search_root" ]; then
    printf 'error: no go.mod found at or above %s\n' "$project_root" >&2
    exit 1
  fi
  search_root=$parent
done

project_root=$search_root
module_name=$(sed -n 's/^module[[:space:]][[:space:]]*//p' "$project_root/go.mod" | sed -n '1p')
go_version=$(sed -n 's/^go[[:space:]][[:space:]]*//p' "$project_root/go.mod" | sed -n '1p')

variant=unknown
reason='no distinctive Nunu layout signal found'

if [ "$module_name" = "github.com/go-nunu/nunu" ] && [ -d "$project_root/internal/command" ]; then
  variant=nunu-cli
  reason='go-nunu module with internal/command'
elif [ -d "$project_root/app" ] && find "$project_root/app" -path '*/cmd/*/main.go' -type f -print -quit 2>/dev/null | grep -q .; then
  variant=monorepo
  reason='multiple app-scoped command entry points'
elif grep -q 'mcp-go' "$project_root/go.mod" 2>/dev/null || [ -f "$project_root/internal/server/mcp.go" ]; then
  variant=mcp
  reason='mcp-go dependency or MCP server composition'
elif [ -d "$project_root/internal/router" ] && [ -d "$project_root/api/v1" ]; then
  variant=advanced
  reason='api/v1 contracts with a dedicated router layer'
elif [ -d "$project_root/internal/handler" ] && [ -d "$project_root/internal/service" ] && [ -d "$project_root/internal/repository" ]; then
  variant=base-or-custom
  reason='core Nunu layered directories'
fi

printf '# Nunu project inspection\n\n'
printf -- '- Root: `%s`\n' "$project_root"
printf -- '- Module: `%s`\n' "${module_name:-unknown}"
printf -- '- Go directive: `%s`\n' "${go_version:-unknown}"
printf -- '- Detected variant: `%s`\n' "$variant"
printf -- '- Evidence: %s\n' "$reason"

print_matches() {
  heading=$1
  pattern=$2
  printf '\n## %s\n\n' "$heading"
  matches=$(find "$project_root" -path "$project_root/.git" -prune -o -path "$project_root/vendor" -prune -o -path "$project_root/node_modules" -prune -o -type f -path "$pattern" -print 2>/dev/null | sed "s#^$project_root/##" | LC_ALL=C sort)
  if [ -n "$matches" ]; then
    printf '%s\n' "$matches" | sed 's/^/- `/' | sed 's/$/`/'
  else
    printf '%s\n' '- none found'
  fi
}

printf '\n## Entrypoints\n\n'
entrypoints=$(find "$project_root" -path "$project_root/.git" -prune -o -path "$project_root/vendor" -prune -o -path "$project_root/node_modules" -prune -o -type f -name main.go -print 2>/dev/null | sed "s#^$project_root/##" | grep -E '^(main\.go|cmd/[^/]+/main\.go|app/[^/]+/cmd/[^/]+/main\.go)$' | LC_ALL=C sort || true)
if [ -n "$entrypoints" ]; then
  printf '%s\n' "$entrypoints" | sed 's/^/- `/' | sed 's/$/`/'
else
  printf '%s\n' '- none found'
fi

printf '\n## Wire injector sources\n\n'
wire_sources=$(find "$project_root" -path "$project_root/.git" -prune -o -path "$project_root/vendor" -prune -o -type f -name wire.go -print 2>/dev/null | while IFS= read -r file; do
  if grep -q 'wireinject' "$file"; then
    printf '%s\n' "$file"
  fi
done | sed "s#^$project_root/##" | LC_ALL=C sort)
if [ -n "$wire_sources" ]; then
  printf '%s\n' "$wire_sources" | sed 's/^/- `/' | sed 's/$/`/'
else
  printf '%s\n' '- none found'
fi

print_matches 'Wire generated files' '*/wire/wire_gen.go'

printf '\n## Present layers\n\n'
found_layer=false
for layer in api/v1 internal/model internal/repository internal/service internal/handler internal/router internal/server internal/job internal/task config test; do
  if [ -d "$project_root/$layer" ]; then
    printf -- '- `%s`\n' "$layer"
    found_layer=true
  fi
done
if [ "$found_layer" = false ]; then
  printf '%s\n' '- none of the standard root-level layers found; inspect app subdirectories'
fi

printf '\n## Repository guidance\n\n'
guidance=$(find "$project_root" -path "$project_root/.git" -prune -o -type f \( -name AGENTS.md -o -name CLAUDE.md -o -name GEMINI.md \) -print 2>/dev/null | sed "s#^$project_root/##" | LC_ALL=C sort)
if [ -n "$guidance" ]; then
  printf '%s\n' "$guidance" | sed 's/^/- `/' | sed 's/$/`/'
else
  printf '%s\n' '- none found'
fi

printf '\n## Suggested next reads\n\n'
case "$variant" in
  nunu-cli)
    printf '%s\n' '- `cmd/nunu/root.go`' '- `internal/command/`' '- `tpl/create/`'
    ;;
  monorepo)
    printf '%s\n' '- target `app/<name>/internal/`' '- target `app/<name>/cmd/*/wire/wire.go`' '- root shared packages only as needed'
    ;;
  mcp)
    printf '%s\n' '- `internal/server/mcp.go`' '- neighboring handler/service/repository feature' '- `cmd/server/wire/wire.go`'
    ;;
  advanced)
    printf '%s\n' '- one complete `api/v1` to router feature' '- transaction and error helpers' '- affected `cmd/*/wire/wire.go`' '- matching tests and Makefile targets'
    ;;
  base-or-custom)
    printf '%s\n' '- one neighboring feature across handler/service/repository/model' '- `internal/server/http.go`' '- `cmd/server/wire/wire.go`'
    ;;
  *)
    printf '%s\n' '- `go.mod` and entry points' '- application composition and neighboring features'
    ;;
esac
