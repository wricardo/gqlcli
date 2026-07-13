# gqlcli — GraphQL Client CLI & Library

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.20-lightblue)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](#license)
[![Go Report Card](https://goreportcard.com/badge/github.com/wricardo/gqlcli)](https://goreportcard.com/report/github.com/wricardo/gqlcli)

Two tools in one:

1. **`gqlcli` CLI** — A GraphQL client for querying any GraphQL API. Discover fields, execute queries and mutations, explore schemas—all from the command line.

2. **`gqlcli` library** — Build GraphQL-backed CLI applications in Go. Write CLIs where GraphQL is the interface language, not subcommands and flags. Perfect for AI agents that can introspect schemas and construct queries.

---

## 🚀 Quick Start — Using the CLI

### Installation

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/wricardo/gqlcli/main/install.sh | bash

# or with Go
go install github.com/wricardo/gqlcli/cmd/gqlcli@latest
```

See [Installation](#-installation) for more options.

### Basic Usage

```bash
# Discover what queries are available
gqlcli queries

# Find mutations related to "campaign"
gqlcli mutations --filter campaign

# Execute a query
gqlcli query --query "{ users { id name } }"

# Try against a different server
export GRAPHQL_URL=https://api.example.com/graphql
gqlcli queries --filter user
```

### Real Examples

```bash
# List all Query fields with descriptions
gqlcli queries --desc

# Show mutation arguments and types
gqlcli mutations --args

# Explore all types
gqlcli types

# Inspect a specific type
gqlcli describe User --args

# Execute a mutation with variables
gqlcli mutation \
  --mutation "mutation CreateUser(\$input: CreateUserInput!) { createUser(input: \$input) { id } }" \
  --input '{"name":"Alice","email":"alice@example.com"}'

# Use a query from a file
gqlcli query --query-file ./queries/getUser.graphql --variables '{"id":"123"}'
```

### Different Output Formats

```bash
gqlcli queries --filter user -f json-pretty    # Pretty JSON
gqlcli queries --filter user -f table           # Aligned columns
gqlcli queries --filter user -f toon            # Token-optimized (default)
gqlcli queries --filter user -f llm             # Markdown for LLMs
gqlcli queries --filter user -f compact         # Minimal JSON
```

---

## ✨ CLI Features

### 🎯 Commands
- **`query`** — Execute GraphQL queries with variables and multiple input methods
- **`mutation`** — Execute mutations with auto-wrapped input objects
- **`subscribe`** — Stream GraphQL subscription events over WebSocket (`graphql-transport-ws`)
- **`batch`** — Execute multiple operations in one request (NDJSON or JSON array) with jq filtering
- **`op`** — Save, list, show, and delete named operations in `.gqlcli.json`
- **`types`** — List all schema types with filtering
- **`describe`** — Print SDL definition of a named type
- **`queries`** — Discover available Query fields instantly
- **`mutations`** — Discover available Mutation fields instantly

### 📊 Output Formats
- **`json` / `json-pretty`** — Pretty or compact JSON
- **`table`** — Aligned columns for terminal viewing
- **`toon`** — Token-optimized format (40-60% smaller) — **default**
- **`llm`** — Markdown-friendly for AI/LLM consumption
- **`compact`** — Minimal JSON (strips nulls)

### 🔐 Configuration
- Default endpoint: `http://localhost:8080/graphql`
- Override via `--url` flag or `GRAPHQL_URL` environment variable
- Per-directory config file: `.gqlcli.json` with named environments (`local`, `prod`, `qa`, …)
- Switch environments at runtime with `--env prod`
- Bearer token authentication support
- Custom HTTP headers per environment
- Debug mode for request/response logging
- Per-request `--header/-H` overrides for one-off auth, tenant, trace, or preview headers
- Curl-style HTTP controls: `--timeout`, `--retry`, `--retry-delay`, `--fail-on-graphql-errors`, and `--insecure`
- Response metadata inspection with `--include-headers`, `--dump-headers`, and repeatable `--metadata` selectors

### 📝 Input Methods
- Inline: `--query "{ users { id } }"`
- From files: `--query-file queries/getUser.graphql` or `--subscription-file subscriptions/events.graphql`
- As arguments: `query "{ ... }"` or `subscribe "subscription { ... }"`
- Variables inline: `--variables '{"id":"123"}'`
- Variables from files: `--variables-file vars.json`
- Named operations in multi-operation files
- Saved named operations in `.gqlcli.json` via `gqlcli op`

---

## 📚 Complete Usage Examples

### Discovering Operations

```bash
# List all queries (TOON format — token-efficient)
gqlcli queries

# List with descriptions
gqlcli queries --desc

# Show arguments and types
gqlcli queries --args

# Filter by name
gqlcli queries --filter user
gqlcli mutations --filter campaign

# Different formats
gqlcli queries -f json-pretty
gqlcli mutations -f table
```

### Executing Queries

```bash
# Simple query
gqlcli query --query "{ users { id name email } }"

# Query from file
gqlcli query --query-file ./queries/getUser.graphql

# With variables
gqlcli query \
  --query "query GetUser(\$id: ID!) { user(id: \$id) { id name } }" \
  --variables '{"id":"123"}'

# Variables from file
gqlcli query \
  --query-file ./queries/getUser.graphql \
  --variables-file ./variables.json

# Named operation (from multi-operation file)
gqlcli query \
  --query-file ./queries/operations.graphql \
  --operation "GetUser"

# Saved named operation (from .gqlcli.json)
gqlcli query --op get-user --variables '{"id":"123"}'
```

### Mutations

```bash
# Basic mutation
gqlcli mutation \
  --mutation "mutation { createUser(name: \"Alice\") { id } }"

# With auto-wrapped input
gqlcli mutation \
  --mutation "mutation CreateUser(\$input: CreateUserInput!) { createUser(input: \$input) { id } }" \
  --input '{"name":"Alice","email":"alice@example.com"}'

# Alternative: explicit variables
gqlcli mutation \
  --mutation-file ./mutations/createUser.graphql \
  --variables '{"input":{"name":"Alice"}}'

# Saved named mutation (from .gqlcli.json)
gqlcli mutation --op create-user --input '{"name":"Alice","email":"alice@example.com"}'
```

### HTTP Controls

Use curl-style transport flags when scripts need one-off request customization or CI-friendly failure behavior. These flags work on HTTP-backed commands such as `query`, `mutation`, `subscribe`, `batch`, `queries`, `mutations`, `types`, and `describe`.

```bash
# Per-request headers override headers from the selected .gqlcli.json environment
gqlcli query '{ viewer { id } }' \
  --env prod \
  -H 'Authorization=Bearer temporary-token' \
  -H 'X-Tenant=acme'

# Bound latency and retry transient failures
gqlcli query '{ health }' --timeout 10 --retry 3 --retry-delay 500ms

# CI: fail non-zero when the GraphQL response includes an errors array
gqlcli query --query-file ./checks/schema.graphql --fail-on-graphql-errors

# Internal/self-signed TLS endpoints
gqlcli queries --url https://localhost:8443/graphql --insecure
```

Response metadata flags are opt-in so normal JSON output stays parseable by default:

```bash
# Include status line + headers before the body, like curl -i
gqlcli query '{ viewer { id } }' --include-headers

# Dump headers without changing stdout
gqlcli query '{ viewer { id } }' --dump-headers headers.txt -f json | jq .data

# Print selected metadata after the response
gqlcli query '{ viewer { id } }' --metadata status-code --metadata header:X-Request-Id
```

### Subscriptions

```bash
# Stream subscription events as NDJSON envelopes
gqlcli subscribe 'subscription { messageAdded { id text } }'

# From a file, with variables
gqlcli subscribe \
  --subscription-file ./subscriptions/messages.graphql \
  --variables-file ./variables.json

# Named operation in a multi-operation document
gqlcli subscribe \
  --subscription 'subscription WatchRoom($room: ID!) { messageAdded(room: $room) { id text } }' \
  --variables '{"room":"general"}' \
  --operation WatchRoom
```

`subscribe` writes one JSON object per line to stdout:

```jsonl
{"type":"next","payload":{"data":{"messageAdded":{"id":"1","text":"hello"}}}}
{"type":"error","payload":[{"message":"..."}]}
{"type":"complete"}
```

Transport coverage: subscriptions use the standard GraphQL over WebSocket protocol (`graphql-transport-ws`). HTTP and HTTPS endpoint URLs are automatically mapped to `ws://` and `wss://`; explicit `ws://` or `wss://` URLs are also accepted. Server-Sent Events (SSE) is another common subscription transport, but is not implemented yet; it should be added as an explicit `--transport sse` mode if needed.

Press Ctrl-C to cancel; gqlcli sends a WebSocket `complete` message and closes the connection cleanly.

### Batch Operations

Execute multiple GraphQL operations in a single request. Supports two wire formats:
- **NDJSON** (default) — one JSON object per line, `Content-Type: application/x-ndjson`
- **JSON array** — standard batch format, `Content-Type: application/json`

Each operation can include an optional `"jq"` field for server-side response filtering.

```bash
# Multiple queries via NDJSON (pipe from stdin)
printf '{"query":"{ users { id name } }"}\n{"query":"{ posts { id title } }"}\n' \
  | gqlcli batch

# Server-side jq filtering (per operation)
printf '{"query":"{ users { id name status } }","jq":".data.users[] | select(.status == \"active\") | .name"}\n' \
  | gqlcli batch

# JSON array batch mode
printf '{"query":"{ users { id } }"}\n{"query":"{ posts { id } }"}\n' \
  | gqlcli batch --array

# Client-side jq (applied to all responses)
printf '{"query":"{ users { id name } }"}\n' \
  | gqlcli batch --jq '.data'

# From a file
gqlcli batch --file operations.ndjson

# Pipeline: query -> filter -> feed into mutations
gqlcli query -q '{ users { id status } }' -f json \
  | jq -c '.data.users[] | select(.status == "inactive") | {query: "mutation($id:ID!){archive(id:$id){ok}}", variables: {id: .id}}' \
  | gqlcli batch
```

**jq examples for the `"jq"` field:**
| Expression | Effect |
|-----------|--------|
| `.data` | Strip the GraphQL envelope |
| `.data.users[].name` | Extract a field from each array element |
| `.data.users \| length` | Count results |
| `.data.users[] \| select(.active)` | Filter array items |
| `.data.logs[] \| select(.message \| test("error"))` | Regex match |
| `.data \| {count: (.users \| length), first: .users[0]}` | Transform/reshape |

### Schema Exploration

```bash
# List all types
gqlcli types

# Filter types by name
gqlcli types --filter User

# Filter by kind
gqlcli types --kind OBJECT
gqlcli types --kind ENUM
gqlcli types --kind INPUT_OBJECT

# Compact output (good for piping)
gqlcli types -f compact
```

### Environment Configuration

#### Option 1 — Environment variable

```bash
export GRAPHQL_URL="http://staging-api.example.com/graphql"
gqlcli queries
```

#### Option 2 — `.gqlcli.json` (per-directory config)

Create `.gqlcli.json` in your project directory to define named environments:

```json
{
  "default": "local",
  "environments": {
    "local": {
      "url": "http://localhost:8080/graphql",
      "headers": {
        "Authorization": "Bearer dev-token"
      }
    },
    "staging": {
      "url": "http://staging-api.example.com/graphql",
      "headers": {
        "Authorization": "Bearer staging-token",
        "X-Tenant": "acme"
      }
    },
    "prod": {
      "url": "https://api.example.com/graphql",
      "headers": {
        "Authorization": "Bearer prod-token"
      }
    }
  }
}
```

```bash
# Uses "local" (the default)
gqlcli queries

# Switch to prod
gqlcli queries --env prod
gqlcli query --query "{ users { id } }" --env prod

# Override URL on top of a named env
gqlcli queries --env staging --url http://other-host/graphql
```

**Priority** (lowest → highest): hardcoded default → `.gqlcli.json` env → `GRAPHQL_URL` → `--url` flag

### Saved Named Operations

Save frequently used queries and mutations in `.gqlcli.json`, then execute them by name with `--op`.

```bash
# Save a query with default variables
gqlcli op save \
  --name get-user \
  --query 'query GetUser($id: ID!) { user(id: $id) { id name email } }' \
  --defaults '{"id":"123"}'

# Run it; explicit variables override saved defaults
gqlcli query --op get-user --variables '{"id":"456"}'

# Save and run a mutation
gqlcli op save \
  --name create-user \
  --mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }'

gqlcli mutation --op create-user --input '{"name":"Alice","email":"alice@example.com"}'

# Manage saved operations
gqlcli op list
gqlcli op show --name get-user
gqlcli op delete --name get-user
```

Saved operations are stored under the `operations` key:

```json
{
  "operations": {
    "get-user": {
      "type": "query",
      "query": "query GetUser($id: ID!) { user(id: $id) { id name email } }",
      "defaults": { "id": "123" }
    }
  }
}
```

Use `--operation` when selecting a GraphQL operation from a multi-operation document. Use `--op` when running a saved operation from `.gqlcli.json`.

### Advanced: Save Results to File

```bash
# Query result to file
gqlcli query --query "{ users { id } }" --output results.json

# Types list to file
gqlcli types --output types.json
```

---

## 🔧 Command Reference

### Global Flags
```
-u, --url VALUE       GraphQL endpoint (default: http://localhost:8080/graphql, env: GRAPHQL_URL)
--env VALUE           Environment to use from .gqlcli.json (e.g. local, prod)
-f, --format VALUE    Output format: json, json-pretty, table, compact, toon, llm (default: toon)
-p, --pretty          Pretty print JSON output
-h, --help            Show help
```

### `query` Command
```
-q, --query STRING           GraphQL query
--query-file PATH            Read query from file
-v, --variables JSON         Query variables as JSON
--variables-file PATH        Read variables from file
-o, --operation STRING       Named operation to execute from a multi-operation document
--op NAME                    Saved operation from .gqlcli.json
-f, --format FORMAT          Output format
--output FILE                Write to file
-H, --header KEY=VALUE       Per-request HTTP header (repeatable; overrides env headers)
--include-headers, -i        Include response status line and headers before body
--dump-headers FILE          Write response status line and headers to file
--metadata SELECTOR          Print selected metadata (status, status-code, headers, header:Name)
--timeout SECONDS            Request timeout (default: 30)
--retry N                    Retry transient failures
--retry-delay DURATION       Delay between retries (e.g. 500ms, 2s)
--fail-on-graphql-errors     Exit non-zero when response.errors is present
--insecure                   Skip TLS certificate verification
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable HTTP debug logging
```

### `mutation` Command
```
-m, --mutation STRING        GraphQL mutation
--mutation-file PATH         Read mutation from file
--input JSON                 Input object (auto-wrapped as {"input":{...}})
-v, --variables JSON         Variables as JSON
--variables-file PATH        Read variables from file
-o, --operation STRING       Named operation to execute from a multi-operation document
--op NAME                    Saved operation from .gqlcli.json
-f, --format FORMAT          Output format
--output FILE                Write to file
-H, --header KEY=VALUE       Per-request HTTP header (repeatable; overrides env headers)
--include-headers, -i        Include response status line and headers before body
--dump-headers FILE          Write response status line and headers to file
--metadata SELECTOR          Print selected metadata (status, status-code, headers, header:Name)
--timeout SECONDS            Request timeout (default: 30)
--retry N                    Retry transient failures
--retry-delay DURATION       Delay between retries (e.g. 500ms, 2s)
--fail-on-graphql-errors     Exit non-zero when response.errors is present
--insecure                   Skip TLS certificate verification
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable HTTP debug logging
```

### `subscribe` Command
```
-s, --subscription STRING    GraphQL subscription
--subscription-file PATH     Read subscription from file
-v, --variables JSON         Variables as JSON
--variables-file PATH        Read variables from file
-o, --operation STRING       Named operation to execute from a multi-operation document
--op NAME                    Saved subscription from .gqlcli.json (type: "subscription")
-H, --header KEY=VALUE       Per-request HTTP/WebSocket header (repeatable)
--timeout SECONDS            Connection/read timeout (default: 30)
--insecure                   Skip TLS certificate verification for wss:// endpoints
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL); http(s) maps to ws(s)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable debug logging
```

Output is NDJSON subscription envelopes: `next`, `error`, and `complete`.

### `op` Command
```
gqlcli op save --name NAME (--query QUERY | --mutation MUTATION) [--defaults JSON]
gqlcli op list
gqlcli op show --name NAME
gqlcli op delete --name NAME
```

Saved operations live in `.gqlcli.json` and run with `gqlcli query --op NAME` or `gqlcli mutation --op NAME`.

### `batch` Command
```
--ndjson                     Use NDJSON transport (default)
--array                      Use JSON array batch transport
--file PATH                  Read operations from file instead of stdin
--jq EXPR                    Apply jq expression to each response (client-side)
-H, --header KEY=VALUE       Per-request HTTP header (repeatable)
--timeout SECONDS            Request timeout (default: 30)
--retry N                    Retry transient failures
--retry-delay DURATION       Delay between retries
--fail-on-graphql-errors     Exit non-zero when any response.errors is present
--insecure                   Skip TLS certificate verification
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable HTTP debug logging
```

### `queries` Command
```
--desc                       Include field descriptions
--args                       Include field arguments with types
--filter PATTERN             Filter by name (case-insensitive)
-f, --format FORMAT          Output format (default: toon)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable debug logging
```

### `mutations` Command
```
--desc                       Include field descriptions
--args                       Include field arguments with types
--filter PATTERN             Filter by name (case-insensitive)
-f, --format FORMAT          Output format (default: toon)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable debug logging
```

### `describe` Command
```
TYPE_NAME                    Name of the type to describe (required)
--args, -a                   Expand field argument signatures
--descriptions               Include field/type descriptions
--depth N                    Recursively include referenced non-scalar types (including UNION/INTERFACE possibleTypes)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable debug logging
```

### `types` Command
```
--filter PATTERN             Filter by name (substring match)
--kind KIND                  Filter by kind (OBJECT, ENUM, INPUT_OBJECT, SCALAR, INTERFACE, UNION)
-f, --format FORMAT          Output format (default: compact)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
--env VALUE                  Environment from .gqlcli.json
-d, --debug                  Enable debug logging
```

---

## 📚 Using as a Library

The `gqlcli` package provides two ways to build CLI tools:

| Mode | Use Case |
|------|----------|
| **HTTP Mode** | Build a CLI that queries external GraphQL APIs over HTTP |
| **Inline Mode** | Build a GraphQL-backed CLI with inline execution (using gqlgen) — perfect for AI agents and schema-driven CLIs |

See sections below for detailed examples of each mode.

---

## 📦 Installation

### Download a Binary (no Go required)

**macOS / Linux — one-liner (auto-detects OS and architecture):**

```bash
curl -fsSL https://raw.githubusercontent.com/wricardo/gqlcli/main/install.sh | bash
```

Installs to `/usr/local/bin/gqlcli`. Override with `INSTALL_DIR`:

```bash
INSTALL_DIR=~/.local/bin curl -fsSL https://raw.githubusercontent.com/wricardo/gqlcli/main/install.sh | bash
```

**Windows:** Download `gqlcli_windows_amd64.zip` from the [Releases page](https://github.com/wricardo/gqlcli/releases/latest) and extract `gqlcli.exe` to a directory in your `PATH`.

### Install with Go

```bash
go install github.com/wricardo/gqlcli/cmd/gqlcli@latest
```

### Build from Source

```bash
git clone https://github.com/wricardo/gqlcli.git
cd gqlcli
make install
gqlcli --help
```

### As a Go Library

```bash
go get github.com/wricardo/gqlcli
```

### HTTP Mode — Query External GraphQL APIs

Build a CLI that connects to external GraphQL servers over HTTP. Useful for API testing, schema exploration, and CI/CD pipelines:

```go
package main

import (
	"os"
	"log"
	"github.com/urfave/cli/v2"
	"github.com/wricardo/gqlcli/pkg"
)

func main() {
	cfg := &gqlcli.Config{
		URL:     "http://localhost:8080/graphql",
		Format:  "toon",
		Timeout: 30,
	}

	builder := gqlcli.NewCLIBuilder(cfg)
	app := &cli.App{
		Name: "gql",
		Usage: "GraphQL CLI",
	}

	builder.RegisterCommands(app)
	// Use gqlcli.RunApp, not app.Run directly — it reorders flags placed
	// after a command's positional argument so they aren't dropped.
	if err := gqlcli.RunApp(app, os.Args); err != nil {
		log.Fatal(err)
	}
}
```

### Inline Mode — GraphQL-Backed CLI Applications

Build GraphQL-native CLI applications where GraphQL is the interface language, not subcommands and flags. This is especially powerful for AI agents that can introspect schemas and construct queries dynamically.

**Why GraphQL for CLIs:**

| Traditional CLI | GraphQL-Native CLI |
|---|---|
| `myapp --user-type=active --limit 10 --format json` | `myapp query '{ users(type: "active", limit: 10) { id name } }'` |
| Multiple commands for different operations | One unified query language |
| AI must learn your CLI's custom flags | AI naturally understands GraphQL |
| Hard to combine operations | Execute multiple queries in parallel |
| Schema is implicit | **Schema is explicit and queryable** |

If you have a [gqlgen](https://github.com/99designs/gqlgen) schema, you can run operations in-process without an HTTP server. This is useful for building a CLI that ships alongside your application binary.

```go
package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
	gqlcli "github.com/wricardo/gqlcli/pkg"

	"github.com/myorg/myapp/graph" // your gqlgen package
)

func main() {
	// 1. Create your gqlgen ExecutableSchema.
	r := graph.NewResolver()
	execSchema := graph.NewExecutableSchema(graph.Config{Resolvers: r})

	// 2. Inline executor — runs operations directly in-process.
	//    WithSchemaHints attaches compact type SDL to validation errors.
	exec := gqlcli.NewInlineExecutor(execSchema,
		gqlcli.WithSchemaHints(),
	)

	// 3. Command set — adds query, mutation, describe, types commands.
	commands := gqlcli.NewInlineCommandSet(exec)

	// 4. Mount onto any urfave/cli app.
	app := &cli.App{Name: "myapp", Usage: "CLI for my GraphQL API"}
	commands.Mount(app)

	// Use gqlcli.RunApp, not app.Run directly — it reorders flags placed
	// after a command's positional argument so they aren't dropped.
	if err := gqlcli.RunApp(app, os.Args); err != nil {
		log.Fatal(err)
	}
}
```

This adds the following subcommands:

| Command | Description |
|---------|-------------|
| `query` | Execute a query (TOON format by default) |
| `mutation` | Execute a mutation (JSON format by default) |
| `batch` | Execute multiple operations from stdin (NDJSON) with jq filtering |
| `op` | Manage saved named operations in `.gqlcli.json` |
| `describe TYPE` | Print SDL definition of a type |
| `types` | List all types in the schema |

**Schema hints** — when `WithSchemaHints()` is enabled, validation errors include a compact SDL description of the referenced type:

```
Error: Cannot query field "titl" on type "Book".
Schema hint:
type Book {
  id: ID!
  title: String!
  author: Author!
}
```

### `describe` Command (Inline-Only)

Available only in inline execution mode. Print the SDL definition of a type:

```bash
# Describe a type
./myapp describe Query
./myapp describe Book
./myapp describe AddBookInput

# Output shows field signatures and relationships
type Book {
  id: ID!
  title: String!
  author: Author!
}
```

Useful for AI agents to discover schema structure before constructing queries.

---

### Complete Example

See [example/README.md](example/README.md) for a complete working example of a **GraphQL-native CLI** — no subcommands, no flags, just GraphQL queries and mutations. The example demonstrates:

- **GraphQL as the interface** — Execute queries like `./myapp query '{ books { id title author { name } } }'`
- **Schema introspection** — AI agents can discover capabilities with `./myapp describe Book`
- **Parallel execution** — Multiple top-level queries in one command
- **Inline execution** — No HTTP server needed, runs in-process against a gqlgen schema
- **File-based persistence** — Data stored in `store.json`
- **Forced resolvers** — Using `@goField(forceResolver: true)` for lazy-loading
- **Split schema files** — Organized with follow-schema layout

This is the ideal paradigm for:
- **AI agents** — Introspect schema, construct queries, explore data
- **CLI automation** — Write complex queries instead of chaining commands
- **Consistent interfaces** — GraphQL works everywhere, agents already understand it

See [example/README.md](example/README.md) for detailed setup and usage.

---

## 🏗️ Architecture

### Core Components

| Component | Purpose |
|-----------|---------|
| **Config** | Configuration holder (URL, format, timeout) |
| **CLIBuilder** | HTTP-based CLI command generator |
| **InlineExecutor** | In-process executor for gqlgen schemas |
| **InlineCommandSet** | CLI commands backed by an InlineExecutor |
| **TokenStore** | JWT persistence at `~/.{appName}/token` |
| **Describer** | Introspects a schema and returns SDL for a type |
| **Formatter** | Output format converter (JSON, table, TOON, etc.) |
| **FormatterRegistry** | Manages available formatters |

### Package Structure

```
pkg/
├── cli.go              # HTTP-based CLI command builders (CLIBuilder)
├── client.go           # HTTP GraphQL client
├── batch.go            # Batch/NDJSON execution + jq filtering
├── inline.go           # InlineExecutor — in-process execution
├── inline_commands.go  # InlineCommandSet — query/mutation/describe/login commands
├── projectconfig.go    # .gqlcli.json loader and environment resolution
├── token.go            # TokenStore — JWT persistence and parsing
├── describe.go         # Describer — schema introspection and SDL formatting
├── formatter.go        # Output formatters
└── types.go            # Type definitions and interfaces
```

---

## 🔌 Extending the Library

### Add a Custom Formatter

```go
package main

import "github.com/wricardo/gqlcli/pkg"

type CSVFormatter struct{}

func (f *CSVFormatter) Format(data map[string]interface{}) (string, error) {
	// Your CSV formatting logic
	return csvOutput, nil
}

func (f *CSVFormatter) Name() string {
	return "csv"
}

// Usage:
registry := gqlcli.NewFormatterRegistry()
registry.Register("csv", &CSVFormatter{})
```

### Custom Client Implementation

```go
type CachedClient struct {
	cache map[string]interface{}
}

func (c *CachedClient) Execute(ctx context.Context, mode gqlcli.ExecutionMode, opts gqlcli.QueryOptions) (map[string]interface{}, error) {
	// Check cache first
	// Fall back to HTTP if not found
	return result, nil
}
```

---

## 📊 Use Cases

### API Development & Testing
```bash
# Discover available operations
gqlcli queries
gqlcli mutations

# Test a mutation
gqlcli mutation \
  --mutation-file ./test/mutations/createUser.graphql \
  --variables-file ./test/variables.json
```

### Schema Documentation
```bash
# Export all types as JSON
gqlcli types --format json-pretty > types.json

# Describe specific types
gqlcli describe User --args --descriptions
gqlcli describe CreateUserInput --args
```

### CI/CD Pipelines
```bash
# Capture types for schema drift detection
gqlcli types --format json > current-types.json
git diff previous-types.json current-types.json
```

### AI/LLM Integration
```bash
# Discover operations for LLM context
gqlcli queries --desc --format toon
gqlcli mutations --desc --args --format toon

# Inspect a type before writing a query
gqlcli describe User --args
gqlcli types --kind INPUT_OBJECT
```

---

## 🧪 Testing

```bash
# Run all tests
make test

# Test with coverage
make test-coverage

# Run linter
make lint

# Format code
make fmt
```

---

## ⚙️ Development

```bash
# Build
make build

# Build and test
make dev

# Install locally
make install

# Clean artifacts
make clean

# View all available commands
make help
```

---

## 🔒 Error Handling

Rich error messages with context:

```
🚨 GraphQL Validation/Execution Errors:

  ❌ 1. Cannot query field "unknown" on type "Query"
     📂 Path: unknown
     🏷️  Code: GRAPHQL_VALIDATION_FAILED
     📍 Position: Line 1, Column 3

📝 Query that caused the error:
   1 | { unknown }
```

---

## 🌟 Why gqlcli?

- **Zero Dependencies** — Single binary, no runtime dependencies
- **Production-Ready** — Extensively tested and battle-hardened
- **Token-Efficient** — TOON format reduces tokens by 40-60%
- **Extensible** — Clean interfaces for custom formatters and clients
- **Flexible Input** — Multiple ways to specify queries and variables
- **DevOps Friendly** — Perfect for scripts, CI/CD, and automation
- **Open Source** — MIT licensed, community-driven

---

## 🤝 Contributing

We welcome contributions! Whether it's bug fixes, features, documentation, or examples.

### Getting Started
1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Make your changes
4. Run tests: `make test`
5. Run linter: `make lint`
6. Commit: `git commit -m 'Add amazing feature'`
7. Push: `git push origin feature/amazing-feature`
8. Open a Pull Request

### Guidelines
- Keep commits focused and descriptive
- Add tests for new functionality
- Update documentation as needed
- Follow Go conventions and style
- Run `make fmt` before committing

---

## 📝 License

MIT License — see [LICENSE](LICENSE) file for details.

---

## 📞 Support & Community

- **Issues** — [GitHub Issues](https://github.com/wricardo/gqlcli/issues) for bugs and features
- **Discussions** — [GitHub Discussions](https://github.com/wricardo/gqlcli/discussions) for questions
- **Email** — Open an issue with the `question` label

---

## 🙏 Acknowledgments

Built with:
- [urfave/cli](https://github.com/urfave/cli) — CLI framework
- [go-resty/resty](https://github.com/go-resty/resty) — HTTP client
- [toon-format/toon-go](https://github.com/toon-format/toon-go) — Token-optimized format
- [itchyny/gojq](https://github.com/itchyny/gojq) — Pure Go jq implementation

---

## 📈 Project Status

**Active Development** — Maintained and open to contributions.

Latest features:
- ✅ **GraphQL subscriptions** — `subscribe` streams `graphql-transport-ws` events as NDJSON
- ✅ **Curl-style HTTP controls** — per-request `--header/-H`, timeout/retry/fail behavior, `--insecure`, and response metadata flags
- ✅ **Batch operations** — execute multiple queries/mutations in one request (NDJSON + JSON array)
- ✅ **Server-side jq filtering** — per-operation `"jq"` field for response transformation
- ✅ **Client-side jq** — `--jq` flag applies jq to all batch responses
- ✅ `.gqlcli.json` project config — named environments with URL and custom headers, `--env` flag
- ✅ Inline execution — run operations in-process against a gqlgen schema (no HTTP server)
- ✅ Schema hints — attach type SDL to GraphQL validation errors
- ✅ Token store — JWT persistence and parsing for login/logout/whoami
- ✅ Query and Mutation operation discovery (`queries`, `mutations` commands)
- ✅ Token-optimized TOON format (default)
- ✅ Environment variable support (`GRAPHQL_URL`)
- ✅ Multiple output formats
- ✅ Extensible architecture

---

**Made with ❤️ for the GraphQL community**
