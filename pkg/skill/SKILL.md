---
name: gqlcli
description: >
  User manual for gqlcli — a GraphQL CLI tool for querying and exploring any GraphQL API.
  Use when asked to: execute GraphQL queries or mutations, explore a GraphQL schema,
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

gqlcli types                            # all types
gqlcli types --filter User
gqlcli types --kind ENUM                # OBJECT | ENUM | INPUT_OBJECT | SCALAR | INTERFACE | UNION

gqlcli describe User                    # SDL definition of a specific type
gqlcli describe CreateUserInput --args  # include field argument signatures
```

## Execute queries

```bash
gqlcli query '{ users { id name email } }'

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

## Batch operations

Send multiple operations in one request. Each line is a JSON object with `"query"` (required),
optional `"variables"`, `"operationName"`, and `"jq"` fields.

```bash
# Multiple queries via NDJSON (default transport)
printf '{"query":"{ users { id name } }"}\n{"query":"{ posts { id title } }"}\n' | gqlcli batch

# Server-side jq: "jq" field is applied by the server before returning the response.
# jq receives the full {"data":...,"errors":...} envelope — always start paths from .data.
printf '{"query":"{ smsCampaigns { campaigns { id name } } }","jq":".data.smsCampaigns.campaigns[].name"}\n' | gqlcli batch
printf '{"query":"{ users { id name } }","jq":".data.users[] | select(.active)"}\n' | gqlcli batch
printf '{"query":"{ users { id } }","jq":".data.users | length"}\n' | gqlcli batch

# Client-side jq: --jq flag applies to every response after the server returns
printf '{"query":"{ users { id name } }"}\n' | gqlcli batch --jq '.data.users[].name'

# JSON array transport (single POST, returns a JSON array)
gqlcli batch --array --file operations.json

# Pipeline: extract IDs from a query, pipe into batch mutations
gqlcli query '{ users { id status } }' --format json \
  | jq -c '.data.users[] | select(.status == "inactive") | {query: "mutation($id:ID!){archive(id:$id){ok}}", variables: {id: .id}}' \
  | gqlcli batch
```

## jq filtering on single queries

```bash
# Pipe --format json to jq for client-side filtering
gqlcli query '{ users { id name } }' --format json | jq '.data.users[].name'
gqlcli query '{ users { id } }' --format json | jq '.data.users | length'
```

## Output formats

```bash
gqlcli queries -f toon          # default, token-optimized
gqlcli queries -f llm           # compact SDL-like, best for feeding to an LLM
gqlcli queries -f table         # aligned columns
gqlcli query ... -f json        # full JSON response
gqlcli query ... -f json-pretty # indented JSON
gqlcli query ... --output out.json
```

## Project config (.gqlcli.json)

Named environments with URLs and headers. Loaded from the current directory.

```bash
gqlcli config init                          # create .gqlcli.json with a sample local env
gqlcli config add-env --name prod --url https://api.example.com/graphql
gqlcli config add-env --name prod --url https://api.example.com/graphql \
  --header "Authorization=Bearer tok" --header "X-Api-Key=secret"
gqlcli config set-default --name prod       # use prod when --env is omitted
gqlcli config remove-env --name prod
gqlcli config list                          # show all envs, * marks the default
```

`.gqlcli.json` structure:

```json
{
  "default": "local",
  "environments": {
    "local": {
      "url": "http://localhost:8080/graphql",
      "headers": { "Authorization": "Bearer dev-token" }
    },
    "prod": {
      "url": "https://api.example.com/graphql",
      "headers": { "Authorization": "Bearer prod-token" }
    }
  }
}
```

## Authentication

```bash
# First login — mutation and token-path are saved for future re-authentication
gqlcli login --env prod \
  --mutation 'mutation Login($email: String!, $password: String!) { login(email: $email, password: $password) { token } }' \
  --variables '{"email":"you@example.com","password":"secret"}' \
  --token-path login.token

# Subsequent logins — mutation and token-path already stored in .gqlcli.json
gqlcli login --env prod --variables '{"email":"you@example.com","password":"secret"}'

# Clear the token
gqlcli logout --env prod
```

`login` writes the token as `Authorization: Bearer <token>` in the env's headers and stores
the mutation + token-path under `environments.<name>.login` for future use.
Custom header name or prefix: `--header X-Auth-Token --prefix ""`.

## Other flags

```bash
--debug     # log HTTP request/response
--env NAME  # select environment from .gqlcli.json (e.g. local, prod)
```
