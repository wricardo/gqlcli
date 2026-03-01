# gqlcli Example — GraphQL-Native CLI

A demonstration of a new CLI paradigm: **GraphQL as the interface language** instead of subcommands and flags. This example shows how to build CLIs that AI agents can naturally interact with using GraphQL queries.

Why GraphQL for CLIs?
- **Structured exploration** — AI agents can introspect the schema to understand what operations are available
- **Complex queries** — Ask for exactly what you need with GraphQL's query language
- **Parallel execution** — Multiple top-level queries execute together (like running multiple CLI commands in one shot)
- **No argument parsing** — Humans and AI write natural GraphQL, not cryptic CLI flags
- **Self-documenting** — Schema is the documentation

Example: Instead of:
```bash
# Traditional CLI
myapp --type books --filter author=Kernighan --limit 10
myapp --type authors --filter name=Donovan --include-books
```

With gqlcli:
```bash
# GraphQL-native CLI — AI agents can construct these naturally
myapp query '{
  books { id title author { name } }
  authors { id name }
}'
```

## Quick Start

### Build

```bash
make build
```

This runs `gqlgen generate` then `go build -o myapp`.

### Run Commands

```bash
# Query all books
./myapp query '{ books { id title author { id name } } }'

# Add a book
./myapp mutation 'mutation {
  addBook(input: {title: "Clean Code", authorName: "Robert C. Martin"}) {
    id title author { id name }
  }
}'

# Fetch a specific book
./myapp query '{ book(id: "1") { id title author { name } } }'

# Describe a type
./myapp describe Book
./myapp describe Author

# List all types
./myapp types
```

## Why This Matters for AI Agents

### Schema as Capability Discovery

AI agents can introspect the schema to discover what the CLI can do:

```bash
# Agent asks: "What can I do with this CLI?"
./myapp types
# Response: Author, Book, Mutation, Query, AddBookInput

# Agent asks: "What queries are available?"
./myapp describe Query
# Response: books, book(id: ID!)

# Agent asks: "What can I mutate?"
./myapp describe Mutation
# Response: addBook(input: AddBookInput!)
```

### Sophisticated Queries

Agents can construct complex queries without CLI flag parsing:

```graphql
# Multi-query execution — fetch books AND authors in one command
query {
  books { id title author { id name } }
  authors { id name }
}

# With filtering logic (queryable structure)
query {
  booksByAuthor: books {
    id
    title
    author { name }
  }
  allAuthors: authors { id name }
}
```

### No More "What's the flag for..."

Traditional CLIs force agents to memorize flag syntax:
- `--filter` vs `--where` vs `--search`?
- `--output json` vs `--format json` vs `-f json`?
- Can I combine flags? Which go where?

**GraphQL has one universal syntax.** Agents understand it, don't need to learn custom flags.

## Architecture

### Schema Structure

The schema is split into logical files with `follow-schema` layout (separate resolver files per schema file):

- **`book.graphqls`** — Query, Mutation, Book type with forced resolver directive
- **`author.graphqls`** — Author type
- **`book.resolvers.go`** — All Book-related resolvers (auto-generated template + implementations)

### Data Model

```graphql
type Query {
  books: [Book!]!        # List all books
  book(id: ID!): Book    # Get a book by ID
}

type Mutation {
  addBook(input: AddBookInput!): Book!  # Create a book
}

type Book {
  id: ID!
  title: String!
  author: Author! @goField(forceResolver: true)  # Requires explicit resolver
}

type Author {
  id: ID!
  name: String!
}

input AddBookInput {
  title: String!
  authorName: String!
}
```

### Persistence

Data is stored in `store.json`:

```json
{
  "books": [
    {
      "id": "1",
      "title": "The Go Programming Language",
      "author": {
        "id": "a1",
        "name": "Donovan & Kernighan"
      }
    }
  ],
  "authors": [
    {
      "id": "a1",
      "name": "Donovan & Kernighan"
    }
  ],
  "nextId": 2
}
```

## Code Organization

### `main.go`

Minimal setup using `gqlcli.NewInlineExecutor` and `gqlcli.NewInlineCommandSet`:

```go
// 1. Create resolver with gqlgen ExecutableSchema
r := graph.NewResolver()
execSchema := graph.NewExecutableSchema(graph.Config{Resolvers: r})

// 2. Create inline executor (no HTTP server)
exec := gqlcli.NewInlineExecutor(execSchema,
  gqlcli.WithSchemaHints(),  // Add type SDL to validation errors
)

// 3. Create CLI commands
commands := gqlcli.NewInlineCommandSet(exec)

// 4. Mount onto urfave/cli app
app := &cli.App{Name: "myapp", Usage: "Book management CLI"}
commands.Mount(app)
```

### `graph/resolver.go`

Root resolver with file-based persistence:

- `NewResolver()` — Creates resolver, loads `store.json` if it exists
- `load()` — Reads books and authors from `store.json`
- `save()` — Writes books and authors to `store.json`
- `getOrCreateAuthor(name)` — Deduplicates authors by name

### `graph/book.resolvers.go`

Implements all resolvers:

| Resolver | Purpose |
|----------|---------|
| `Query.Books` | Return all books |
| `Query.Book` | Get a book by ID |
| `Mutation.AddBook` | Create a book (auto-creates author if needed) |
| `Book.Author` | Forced resolver for lazy-loading author |

## Key Features

### 1. Forced Resolvers

The `Book.author` field uses `@goField(forceResolver: true)`:

```graphql
type Book {
  author: Author! @goField(forceResolver: true)
}
```

This tells gqlgen to generate a dedicated resolver stub instead of auto-binding to a struct field. Useful for lazy-loading relationships or computed fields.

### 2. File-Based Persistence

No database needed. Data is stored as JSON:

```go
func (r *Resolver) save() error {
  data := storeData{Books: r.books, Authors: r.authors, NextID: r.nextID}
  bytes, _ := json.MarshalIndent(data, "", "  ")
  return os.WriteFile(r.storePath, bytes, 0644)
}
```

Each mutation calls `r.save()` to persist changes.

### 3. Author Deduplication

When adding a book, authors are reused by name:

```go
func (r *Resolver) getOrCreateAuthor(name string) *model.Author {
  // Check if author exists
  for _, a := range r.authors {
    if a.Name == name {
      return a
    }
  }
  // Create new author
  author := &model.Author{ID: fmt.Sprintf("a%d", r.nextID), Name: name}
  r.authors = append(r.authors, author)
  return author
}
```

### 4. Inline Execution (No HTTP)

The `gqlcli` library executes queries directly against the gqlgen schema in-process:

```bash
./myapp query '{ books { id title } }'  # No HTTP, runs locally
```

This is perfect for:
- CLIs shipped with your app
- Embedded GraphQL interfaces
- Local development tools
- Testing without a running server

## Development

### Regenerate After Schema Changes

```bash
make generate
# or
gqlgen generate
```

The Makefile auto-installs gqlgen if not present.

### File Structure After Generation

```
graph/
├── book.graphqls              # Schema definitions
├── author.graphqls
├── book.resolvers.go          # ← Edit resolver bodies here
├── generated.go               # NEVER EDIT
├── model/
│   └── models_gen.go          # NEVER EDIT
└── resolver.go                # Shared DI root, edit freely
```

### Resolver Development Cycle

1. **Update schema** (`*.graphqls`)
2. **Run `make generate`** — gqlgen creates stubs, preserves bodies
3. **Implement resolver bodies** in `*.resolvers.go`
4. **Test** with `./myapp query` or `./myapp mutation`

## Usage Examples

### For AI Agents

#### Discover Capabilities
```bash
# Agent learns what's available
./myapp types
./myapp describe Query
./myapp describe Book
```

#### Multi-Query Execution
```bash
# Execute multiple queries at once (parallel-like)
./myapp query '{
  books { id title author { name } }
  authors { id name }
  specificBook: book(id: "1") { id title }
}'
```

#### Complex Nested Queries
```bash
# Agent constructs exactly what it needs
./myapp query '{
  books {
    id
    title
    author {
      id
      name
    }
  }
}'
```

#### Mutations with Exploration
```bash
# Agent first explores to understand input requirements
./myapp describe AddBookInput
# Response shows: title (String!), authorName (String!)

# Then executes mutation
./myapp mutation 'mutation {
  addBook(input: {
    title: "The Pragmatic Programmer"
    authorName: "Hunt & Thomas"
  }) {
    id
    title
    author { id name }
  }
}'
```

### For Human Users

Same syntax, just more natural to write:

```bash
# Get all books
./myapp query '{ books { id title author { name } } }'

# Add a book
./myapp mutation 'mutation {
  addBook(input: {title: "Clean Code", authorName: "Robert C. Martin"}) {
    id title author { name }
  }
}'

# Get one book
./myapp query '{ book(id: "1") { title author { name } } }'

# Explore schema
./myapp types
./myapp describe Book
```

## Configuration

### Building with gqlcli

The example uses these gqlcli features:

- **InlineExecutor** — Executes queries directly (no HTTP)
- **InlineCommandSet** — Provides query/mutation/describe/types commands
- **Schema hints** — Attaches type SDL to validation errors

See the [gqlcli README](../README.md) for more details on the library features.

## Building Your Own GraphQL CLI

This example shows the pattern. To build a GraphQL-native CLI for your application:

1. **Define your schema** (`*.graphqls`) — This becomes your CLI interface
2. **Implement resolvers** — Each resolver is a "command"
3. **Inline execution** — Use gqlcli to run queries without an HTTP server
4. **Expose via CLI** — Agents explore schema and execute queries

No subcommands. No flags. Just GraphQL.

## Learn More

### GraphQL for CLIs — The New Paradigm

This approach is particularly powerful for:
- **AI agents** — Can introspect and construct queries dynamically
- **CLI automation** — Write complex queries instead of chaining commands
- **Schema-driven development** — Schema is the source of truth for CLI capabilities
- **Complex workflows** — Multiple operations in one query

### Technical References

- [gqlgen](https://gqlgen.com) — Schema-first GraphQL code generation
- [gqlcli README](../README.md) — Complete gqlcli library documentation
- Inline execution: [gqlcli README](../README.md#inline-execution-no-http-server-required)

## What's Not Included (Intentionally)

This is a minimal MVP to demonstrate inline execution. For production:

- ✗ Real database (use your ORM)
- ✗ Authentication/authorization (implement with middleware)
- ✗ Pagination (add `first`, `after` args)
- ✗ Subscriptions (use gqlgen's subscription support)
- ✗ Error handling (extend `*.resolvers.go`)
- ✗ Logging (add context and structured logging)

Extend this example by:

1. Replacing `store.json` with a real database
2. Adding middleware for auth in `main.go`'s context enricher
3. Adding more types and resolvers to the schema
4. Implementing DataLoaders for N+1 prevention

## Testing

```bash
# Clean and rebuild
make clean && make build

# Run a query
./myapp query '{ books { id title } }'

# Run a mutation
./myapp mutation 'mutation { addBook(input: {title: "Test", authorName: "Author"}) { id } }'

# Check persisted data
cat store.json | jq
```

## Troubleshooting

### `gqlgen generate` fails

```bash
# Make sure gqlgen is installed (Makefile does this)
go install github.com/99designs/gqlgen@latest

# Regenerate
make generate
```

### Changes not persisting

```bash
# Check store.json exists and is writable
ls -l store.json

# Clear and retry
rm store.json
./myapp mutation 'mutation { addBook(input: {title: "Test", authorName: "Author"}) { id } }'
cat store.json
```

### Compilation errors in resolvers

```bash
# Ensure schema is up-to-date
make generate

# Then rebuild
go build -o myapp
```

---

**Next Steps:**

- Modify the schema in `graph/*.graphqls`
- Implement custom resolvers in `graph/*.resolvers.go`
- Connect to a real database in `graph/resolver.go`
- Add authentication middleware in `main.go`
