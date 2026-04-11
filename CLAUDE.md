# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make build          # Build binary to ./bin/gqlcli
make test           # Run all tests
make test-coverage  # Run tests with coverage report (generates coverage.html)
make lint           # Run golangci-lint
make fmt            # Format with go fmt (uses gofmt -s)
make vet            # Run go vet
make all            # Run all checks and build
```

Run a single test:
```bash
go test ./pkg/... -run TestFunctionName -v
```

## Architecture

**gqlcli** is both a CLI tool and a Go library for building GraphQL-backed CLI applications.

### Execution Modes

Two independent execution models share the same command set and output formatters:

1. **HTTP Mode** (`CLIBuilder` + `HTTPClient`) — calls an external GraphQL endpoint over HTTP via `go-resty`. Entry point: `cmd/gqlcli/main.go`.
2. **Inline Mode** (`InlineExecutor` + `InlineCommandSet`) — runs operations in-process against a `gqlgen.ExecutableSchema` with no HTTP server required. Intended for library users embedding gqlcli in their own Go apps.

### Package Layout (`pkg/`)

| File | Purpose |
|------|---------|
| `types.go` | Core interfaces: `Client`, `Formatter`, `Config`, `QueryOptions` |
| `cli.go` | `CLIBuilder` — registers HTTP-mode CLI commands via `urfave/cli` |
| `client.go` | `HTTPClient` — introspection, schema hints, error extraction |
| `inline.go` | `InlineExecutor` — in-process execution against a gqlgen schema |
| `inline_commands.go` | `InlineCommandSet` — CLI commands backed by `InlineExecutor` (adds login/logout/whoami) |
| `formatter.go` | Output formatters: JSON, Table, Compact, TOON (token-optimized), LLM |
| `batch.go` | Batch operations with NDJSON and JSON array transports + jq filtering |
| `describe.go` | `Describer` — schema introspection with SDL output, cached via `sync.Map` |
| `projectconfig.go` | `ProjectConfig` — loads `.gqlcli.json` with named environments |
| `token.go` | `TokenStore` — JWT persistence at `~/.{appName}/token` |

### CLI Commands

`query`, `mutation`, `batch`, `queries`, `mutations`, `describe`, `types` — available in both modes. `login`/`logout`/`whoami` only in inline mode.

### Configuration Precedence

Built-in default → `.gqlcli.json` environment → `GRAPHQL_URL` env var → `--url` flag

### Output Formatters

`json` (default), `table`, `compact`, `toon` (token-optimized, ~40-60% smaller), `llm` (markdown-friendly)

## Wiki

- GitHub wiki is a separate git repo cloned into `gqlcli.wiki/` (gitignored). Push with `cd gqlcli.wiki && git push`.
- Wiki remote can accumulate changes (e.g. from GitHub UI) — always `git pull --rebase` before pushing to avoid rejection.
- `Home.md` is the wiki landing page. Inter-page links use `[[Page-Name]]` syntax (no `.md` extension).

## Releases

- GoReleaser config: `main: ./cmd/gqlcli` is required — binary is not at repo root.
- Publish a release: `git tag vX.Y.Z && git push origin vX.Y.Z` — the GitHub Actions workflow handles the rest.
- Release artifacts: `gqlcli_{os}_{arch}.tar.gz` (`.zip` for Windows) + `checksums.txt`.


<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
