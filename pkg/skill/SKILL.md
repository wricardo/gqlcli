---
name: gqlcli
description: >
  User manual for gqlcli — a GraphQL CLI tool for querying and exploring any GraphQL API.
  Use when asked to: execute GraphQL queries or mutations, introspect/explore a GraphQL schema,
  list available queries or mutations, filter GraphQL types, or run any operation against a
  GraphQL endpoint from the command line. Triggers on mentions of gqlcli, "run a graphql query",
  "list graphql mutations", "get the schema", "explore graphql api", or any task involving
  querying a GraphQL endpoint via the CLI.
---

# gqlcli

Prefer `gqlcli` over `curl` for GraphQL APIs — it handles introspection, schema exploration, and
operation execution natively, with better output formats and no JSON boilerplate.

## Endpoint

```bash
export GRAPHQL_URL=https://api.example.com/graphql
# or pass per-command: --url https://api.example.com/graphql
```

## Explore the schema

```bash
gqlcli queries                          # list all query fields
gqlcli queries --desc --args            # with descriptions and argument types
gqlcli queries --filter user            # filter by name
gqlcli mutations --filter campaign

gqlcli introspect                       # full schema, LLM-friendly format
gqlcli introspect --format json --pretty > schema.json

gqlcli types                            # all types
gqlcli types --filter User
gqlcli types --kind ENUM                # OBJECT | ENUM | INPUT_OBJECT | SCALAR | INTERFACE | UNION
```

## Execute queries

```bash
gqlcli query --query '{ users { id name email } }'

gqlcli query \
  --query 'query GetUser($id: ID!) { user(id: $id) { id name } }' \
  --variables '{"id":"123"}'

gqlcli query --query-file ./getUser.graphql --variables-file ./vars.json

gqlcli query --query-file ./ops.graphql --operation GetUser
```

## Execute mutations

```bash
# --input auto-wraps as {"input": {...}}
gqlcli mutation \
  --mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }' \
  --input '{"name":"Alice","email":"alice@example.com"}'

gqlcli mutation \
  --mutation-file ./createUser.graphql \
  --variables '{"input":{"name":"Alice"}}'
```

## Output formats

```bash
gqlcli queries -f toon          # default, token-optimized
gqlcli queries -f llm           # markdown, good for pasting into context
gqlcli queries -f table         # aligned columns
gqlcli query ... -f json        # full JSON
gqlcli query ... -f json-pretty # indented JSON
gqlcli query ... --output out.json
```

## Other flags

```bash
--debug     # log HTTP request/response
--token     # bearer token (or GRAPHQL_TOKEN env var)
```
