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
go install github.com/wricardo/gqlcli/cmd/gqlcli@latest
# or clone and build
git clone https://github.com/wricardo/gqlcli.git
cd gqlcli && make install
```

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

# Explore schema in readable format
gqlcli introspect --format table

# Export schema as JSON
gqlcli introspect --format json > schema.json

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
- **`introspect`** — Download and explore full GraphQL schema
- **`types`** — List all schema types with filtering
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
- Bearer token authentication support
- Custom HTTP headers and timeouts
- Debug mode for request/response logging

### 📝 Input Methods
- Inline: `--query "{ users { id } }"`
- From files: `--query-file queries/getUser.graphql`
- As arguments: `query "{ ... }"`
- Variables inline: `--variables '{"id":"123"}'`
- Variables from files: `--variables-file vars.json`
- Named operations in multi-operation files

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
```

### Schema Exploration

```bash
# Full schema introspection
gqlcli introspect --format json-pretty > schema.json

# LLM-friendly schema
gqlcli introspect --format llm

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

```bash
# Configure default endpoint
export GRAPHQL_URL="http://staging-api.example.com/graphql"

# Commands will use staging endpoint
gqlcli queries
gqlcli query --query "{ users { id } }"

# Override with flag
gqlcli -u http://prod-api.example.com/graphql queries

# Use default
gqlcli queries  # Uses GRAPHQL_URL or localhost:8080
```

### Advanced: Save Results to File

```bash
# Query result to file
gqlcli query --query "{ users { id } }" --output results.json

# Schema to file
gqlcli introspect --format json --output schema.json

# Types list to file
gqlcli types --output types.json
```

---

## 🔧 Command Reference

### Global Flags
```
-u, --url VALUE       GraphQL endpoint (default: http://localhost:8080/graphql, env: GRAPHQL_URL)
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
-o, --operation STRING       Named operation to execute
-f, --format FORMAT          Output format
--output FILE                Write to file
-d, --debug                  Enable HTTP debug logging
```

### `mutation` Command
```
-m, --mutation STRING        GraphQL mutation
--mutation-file PATH         Read mutation from file
--input JSON                 Input object (auto-wrapped as {"input":{...}})
-v, --variables JSON         Variables as JSON
--variables-file PATH        Read variables from file
-o, --operation STRING       Named operation
-f, --format FORMAT          Output format
--output FILE                Write to file
-d, --debug                  Enable HTTP debug logging
```

### `queries` Command
```
--desc                       Include field descriptions
--args                       Include field arguments with types
--filter PATTERN             Filter by name (case-insensitive)
-f, --format FORMAT          Output format (default: toon)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
-d, --debug                  Enable debug logging
```

### `mutations` Command
```
--desc                       Include field descriptions
--args                       Include field arguments with types
--filter PATTERN             Filter by name (case-insensitive)
-f, --format FORMAT          Output format (default: toon)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
-d, --debug                  Enable debug logging
```

### `introspect` Command
```
-f, --format FORMAT          Output format (default: llm)
-o, --output FILE            Write schema to file
-p, --pretty                 Pretty print JSON
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
-d, --debug                  Enable debug logging
```

### `types` Command
```
--filter PATTERN             Filter by name (substring match)
--kind KIND                  Filter by kind (OBJECT, ENUM, INPUT_OBJECT, SCALAR, INTERFACE, UNION)
-f, --format FORMAT          Output format (default: compact)
-u, --url URL                GraphQL endpoint (env: GRAPHQL_URL)
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

### CLI Tool

```bash
go install github.com/wricardo/gqlcli/cmd/gqlcli@latest
```

Or clone and build:
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
	if err := app.Run(os.Args); err != nil {
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

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
```

This adds the following subcommands:

| Command | Description |
|---------|-------------|
| `query` | Execute a query (TOON format by default) |
| `mutation` | Execute a mutation (JSON format by default) |
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
├── inline.go           # InlineExecutor — in-process execution
├── inline_commands.go  # InlineCommandSet — query/mutation/describe/login commands
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
# Generate schema documentation
gqlcli introspect --format llm > SCHEMA.md

# List all types
gqlcli types --format json-pretty > types.json
```

### CI/CD Pipelines
```bash
# Verify schema changes
gqlcli introspect --format json > current-schema.json
git diff previous-schema.json current-schema.json
```

### AI/LLM Integration
```bash
# Get schema in token-efficient format
gqlcli introspect --format toon

# Discover operations for LLM context
gqlcli queries --desc --format toon
gqlcli mutations --desc --args --format toon
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

---

## 📈 Project Status

**Active Development** — Maintained and open to contributions.

Latest features:
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
