# gqlcli - Reusable GraphQL CLI Library

A battle-tested, modular GraphQL CLI library written in Go. Execute queries, mutations, and schema exploration against any GraphQL server with flexible output formats and configuration.

## Features

✨ **Multiple Output Formats**
- `json`: Full JSON (prettified or compact)
- `table`: Human-readable table format
- `compact`: Minimal JSON (strips nulls, no whitespace)
- `toon`: Token-optimized format (40-60% smaller than JSON)
- `llm`: Markdown-friendly format for LLMs

🚀 **Flexible Input Methods**
- Inline queries/mutations: `--query "query { ... }"`
- From files: `--query-file query.graphql`
- As arguments: `query "{ users { id } }"`
- Variables: `--variables '{"id":"123"}'` or `--variables-file vars.json`

⚙️ **Smart Configuration**
- Default to `http://localhost:8080/graphql`
- Override via `--url` flag or `GRAPHQL_URL` env var
- Support for named operations in multi-operation files
- Auto-wrap input objects with `--input` flag

🎯 **Core Commands**
- `query`: Execute GraphQL queries
- `mutation`: Execute GraphQL mutations
- `introspect`: Explore GraphQL schema
- `types`: List all available GraphQL types

## Installation

```bash
go get github.com/wricardo/gqlcli
```

## Quick Start

### As a Library

```go
package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
	"github.com/wricardo/gqlcli/pkg"
)

func main() {
	cfg := &gqlcli.Config{
		URL:     "http://localhost:8080/graphql",
		Format:  "json",
		Pretty:  false,
		Timeout: 30,
	}

	builder := gqlcli.NewCLIBuilder(cfg)
	app := &cli.App{
		Name:  "gql",
		Usage: "GraphQL CLI",
	}

	builder.RegisterCommands(app)

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
```

### Build & Run

```bash
go build -o gql ./example/main.go

# Execute a query
gql query --query "{ users { id name } }"

# With variables
gql query \
  --query-file queries/getUser.graphql \
  --variables '{"id":"123"}'

# Different output format
gql query --query "{ users { id } }" --format table

# Override endpoint
gql -u http://api.example.com/graphql query --query "..."
```

## Usage Examples

### Basic Query

```bash
gql query --query "{ users { id name email } }"
```

### Query from File

```bash
gql query --query-file ./queries/getUser.graphql
```

### With Variables

```bash
gql query \
  --query "query GetUser($id: ID!) { user(id: $id) { id name } }" \
  --variables '{"id":"123"}'
```

### Variables from File

```bash
gql query \
  --query-file ./queries/getUser.graphql \
  --variables-file ./variables.json
```

### Mutation with Input

```bash
gql mutation \
  --mutation "mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }" \
  --input '{"name":"Alice","email":"alice@example.com"}'
```

### Different Output Formats

```bash
# Pretty JSON (default)
gql query --query "..." --format json --pretty

# Table format for readability
gql query --query "..." --format table

# Compact format (minimal size)
gql query --query "..." --format compact

# LLM-friendly markdown
gql query --query "..." --format llm

# Token-optimized (smallest)
gql query --query "..." --format toon
```

### Schema Exploration

```bash
# Full schema introspection
gql introspect --format llm

# Just list all types
gql types

# Filter types by name
gql types --filter "User"

# Filter by kind
gql types --kind "OBJECT"

# Save schema to file
gql introspect --output schema.json --format json
```

### Environment Configuration

```bash
# Set default endpoint via environment variable
export GRAPHQL_URL="http://staging-api.example.com/graphql"

gql query --query "{ users { id } }"  # Uses staging endpoint

# Override with flag
gql -u http://prod-api.example.com/graphql query --query "..."
```

## Flag Reference

### Global Flags
- `-u, --url` - GraphQL endpoint (default: `http://localhost:8080/graphql`, env: `GRAPHQL_URL`)
- `-f, --format` - Output format: json, table, compact, toon, llm (default: json)
- `-p, --pretty` - Pretty print JSON output

### Query/Mutation Flags
- `-q, --query` - GraphQL query string
- `--query-file, --file` - Read query from file
- `-m, --mutation` - GraphQL mutation string
- `--mutation-file` - Read mutation from file
- `-v, --variables` - Variables as JSON
- `--variables-file, --var-file` - Read variables from file
- `-o, --operation` - Named operation to execute
- `--input` - Input object (auto-wrapped as `{"input":{...}}`)
- `--output` - Write result to file
- `-o, --output` - Write to file

### Introspect/Types Flags
- `--filter` - Filter by name (substring match)
- `--kind` - Filter by type kind (OBJECT, ENUM, INPUT_OBJECT, SCALAR, INTERFACE, UNION)
- `--output` - Write schema to file
- `--pretty` - Pretty print JSON (for json format)

## Architecture

### Core Components

**Types** (`pkg/types.go`)
- `Config`: Configuration holder
- `Client`: Interface for GraphQL operations
- `Formatter`: Interface for output formatting
- `FormatterRegistry`: Manages available formatters

**Client** (`pkg/client.go`)
- `HTTPClient`: Executes GraphQL operations via HTTP
- Error formatting with detailed messages
- Query validation and field path tracing

**Formatters** (`pkg/formatter.go`)
- `JSONFormatter`: JSON output
- `TableFormatter`: Table format
- `CompactFormatter`: Minimal JSON
- `TOONFormatter`: Token-optimized
- `LLMFormatter`: Markdown format
- `DefaultFormatterRegistry`: Manages formatters

**CLI** (`pkg/cli.go`)
- `CLIBuilder`: Builds all CLI commands
- Automatic flag handling
- Multiple input methods support

## Design Patterns

### 1. Dependency Injection
Config is passed through to all components:
```go
cfg := &gqlcli.Config{URL: "...", Format: "json"}
builder := gqlcli.NewCLIBuilder(cfg)
```

### 2. Interface-Based Design
- `Client` interface allows custom implementations
- `Formatter` interface for custom formatters
- `FormatterRegistry` for extensibility

### 3. Flexible Input
Multiple ways to provide the same data:
- Flags: `--query "..."`
- Files: `--query-file path.graphql`
- Arguments: `query "..."`
- Environment: `GRAPHQL_URL`

### 4. Format Negotiation
Choose output format that fits your workflow:
- `json` for programmatic use
- `table` for human reading
- `toon` for token-constrained environments
- `llm` for AI consumption

## Error Handling

Detailed error messages with context:

```
🚨 GraphQL Validation/Execution Errors:

  ❌ 1. Cannot query field "unknown" on type "Query"
     📂 Path: unknown
     🏷️  Code: GRAPHQL_VALIDATION_FAILED
     📍 Position: Line 1, Column 3

📝 Query that caused the error:
   1 | { unknown }
```

## Extending the Library

### Add Custom Formatter

```go
type MyFormatter struct{}

func (f *MyFormatter) Format(data map[string]interface{}) (string, error) {
    // Your formatting logic
    return formatted, nil
}

func (f *MyFormatter) Name() string {
    return "myformat"
}

// Register it
registry := gqlcli.NewFormatterRegistry()
registry.Register("myformat", &MyFormatter{})
```

### Add Custom Client

```go
type MyClient struct{}

func (c *MyClient) Execute(ctx context.Context, mode gqlcli.ExecutionMode, opts gqlcli.QueryOptions) (map[string]interface{}, error) {
    // Custom execution logic
    return result, nil
}
```

## License

MIT

## Contributing

Contributions welcome! This library is designed to be extensible and reusable across projects.
