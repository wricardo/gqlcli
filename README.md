# gqlcli — Powerful GraphQL CLI & Library

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.20-lightblue)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](#license)
[![Go Report Card](https://goreportcard.com/badge/github.com/wricardo/gqlcli)](https://goreportcard.com/report/github.com/wricardo/gqlcli)

A battle-tested, production-ready GraphQL CLI library and tool written in Go. Execute queries, mutations, explore schemas, and list operations against any GraphQL server with flexible output formats, extensible architecture, and zero external dependencies.

**Perfect for:**
- 🔧 DevOps & DevTools teams building internal GraphQL CLI tools
- 🤖 AI/LLM workflows that need token-efficient output
- 🧪 GraphQL API testing and debugging
- 📊 Schema exploration and documentation
- 🔌 Custom GraphQL client applications

---

## ✨ Features

### 🎯 Powerful Commands
- **`query`** — Execute GraphQL queries with variables and multiple input methods
- **`mutation`** — Execute mutations with auto-wrapped input objects
- **`introspect`** — Download and explore full GraphQL schema
- **`types`** — List all schema types with filtering
- **`queries`** — Discover available Query fields instantly (new!)
- **`mutations`** — Discover available Mutation fields instantly (new!)

### 📊 Multiple Output Formats
- **`json`** — Full JSON (prettified or compact) for programmatic use
- **`json-pretty`** — Indented JSON for readability
- **`table`** — Aligned columns for terminal viewing
- **`compact`** — Minimal JSON output (strips nulls, no whitespace)
- **`toon`** — Token-optimized format (40-60% smaller than JSON) — **default**
- **`llm`** — Markdown-friendly for AI/LLM consumption

### 🔐 Flexible Configuration
- Default endpoint: `http://localhost:8080/graphql`
- Override via `--url` flag or `GRAPHQL_URL` environment variable
- Support for bearer token authentication
- Custom HTTP headers and timeouts
- Debug mode for request/response logging

### 📝 Multiple Input Methods
- Inline queries: `--query "query { ... }"`
- From files: `--query-file query.graphql`
- As arguments: `query "{ users { id } }"`
- Variables inline: `--variables '{"id":"123"}'`
- Variables from files: `--variables-file vars.json`
- Named operations in multi-operation files

### 🏗️ Extensible Architecture
- Clean interface-based design
- Custom formatter support
- Custom client implementations
- Reusable as a Go library
- Zero external binary dependencies (single small binary)

---

## 📦 Installation

### As a CLI Tool

```bash
go install github.com/wricardo/gqlcli/cmd/gql@latest
```

Or clone and build:
```bash
git clone https://github.com/wricardo/gqlcli.git
cd gqlcli
make build
./bin/gql --help
```

### As a Go Library

```bash
go get github.com/wricardo/gqlcli
```

---

## 🚀 Quick Start

### CLI Usage

```bash
# List available queries
gql queries

# Discover mutations
gql mutations --filter sms --desc

# Execute a query
gql query --query "{ users { id name } }"

# With environment variable
export GRAPHQL_URL=https://api.example.com/graphql
gql queries --filter campaign
```

### As a Library

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

---

## 📚 Usage Examples

### Discovering Operations

```bash
# List all queries (TOON format — token-efficient)
gql queries

# List with descriptions
gql queries --desc

# Show arguments and types
gql queries --args

# Filter by name
gql queries --filter user
gql mutations --filter campaign

# Different formats
gql queries -f json-pretty
gql mutations -f table
```

### Executing Queries

```bash
# Simple query
gql query --query "{ users { id name email } }"

# Query from file
gql query --query-file ./queries/getUser.graphql

# With variables
gql query \
  --query "query GetUser(\$id: ID!) { user(id: \$id) { id name } }" \
  --variables '{"id":"123"}'

# Variables from file
gql query \
  --query-file ./queries/getUser.graphql \
  --variables-file ./variables.json

# Named operation (from multi-operation file)
gql query \
  --query-file ./queries/operations.graphql \
  --operation "GetUser"
```

### Mutations

```bash
# Basic mutation
gql mutation \
  --mutation "mutation { createUser(name: \"Alice\") { id } }"

# With auto-wrapped input
gql mutation \
  --mutation "mutation CreateUser(\$input: CreateUserInput!) { createUser(input: \$input) { id } }" \
  --input '{"name":"Alice","email":"alice@example.com"}'

# Alternative: explicit variables
gql mutation \
  --mutation-file ./mutations/createUser.graphql \
  --variables '{"input":{"name":"Alice"}}'
```

### Schema Exploration

```bash
# Full schema introspection
gql introspect --format json-pretty > schema.json

# LLM-friendly schema
gql introspect --format llm

# List all types
gql types

# Filter types by name
gql types --filter User

# Filter by kind
gql types --kind OBJECT
gql types --kind ENUM
gql types --kind INPUT_OBJECT

# Compact output (good for piping)
gql types -f compact
```

### Environment Configuration

```bash
# Configure default endpoint
export GRAPHQL_URL="http://staging-api.example.com/graphql"

# Commands will use staging endpoint
gql queries
gql query --query "{ users { id } }"

# Override with flag
gql -u http://prod-api.example.com/graphql queries

# Use default
gql queries  # Uses GRAPHQL_URL or localhost:8080
```

### Output Formats

```bash
# JSON (compact)
gql queries -f json

# JSON pretty-printed
gql queries -f json-pretty

# Table (human-readable)
gql queries -f table

# TOON (token-optimized, ~40-60% smaller than JSON)
gql queries -f toon

# Markdown (for LLMs/documentation)
gql queries -f llm

# Minimal compact format
gql queries -f compact
```

### Advanced: Save Results to File

```bash
# Query result to file
gql query --query "{ users { id } }" --output results.json

# Schema to file
gql introspect --format json --output schema.json

# Types list to file
gql types --output types.json
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

## 🏗️ Architecture

### Core Components

| Component | Purpose |
|-----------|---------|
| **Config** | Configuration holder (URL, format, timeout) |
| **Client** | GraphQL operation executor (HTTP-based) |
| **Formatter** | Output format converter (JSON, table, TOON, etc.) |
| **CLIBuilder** | Command-line interface generator |
| **FormatterRegistry** | Manages available formatters |

### Package Structure

```
pkg/
├── cli.go           # CLI command builders
├── client.go        # HTTP GraphQL client
├── formatter.go     # Output formatters
└── types.go         # Type definitions and interfaces
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
gql queries
gql mutations

# Test a mutation
gql mutation \
  --mutation-file ./test/mutations/createUser.graphql \
  --variables-file ./test/variables.json
```

### Schema Documentation
```bash
# Generate schema documentation
gql introspect --format llm > SCHEMA.md

# List all types
gql types --format json-pretty > types.json
```

### CI/CD Pipelines
```bash
# Verify schema changes
gql introspect --format json > current-schema.json
git diff previous-schema.json current-schema.json
```

### AI/LLM Integration
```bash
# Get schema in token-efficient format
gql introspect --format toon

# Discover operations for LLM context
gql queries --desc --format toon
gql mutations --desc --args --format toon
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
- ✅ Query and Mutation operation discovery (`queries`, `mutations` commands)
- ✅ Token-optimized TOON format (default)
- ✅ Environment variable support (`GRAPHQL_URL`)
- ✅ Multiple output formats
- ✅ Extensible architecture

---

**Made with ❤️ for the GraphQL community**
