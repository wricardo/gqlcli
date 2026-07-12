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
gqlcli types --filter Citizen --kind ENUM  # combine name + kind filters

gqlcli describe User                    # SDL definition of a specific type
gqlcli describe CreateUserInput --args  # include field argument signatures
gqlcli describe Citizen --depth 1       # include directly referenced non-scalar types
gqlcli describe Citizen --depth 2       # recurse one level deeper
```

Depth behavior for describe:
- `--depth 0` only prints the requested type (default)
- `--depth 1` includes directly referenced non-scalar types
- `--depth N` recursively expands non-scalar references up to N levels (including `UNION`/`INTERFACE` `possibleTypes`) (including `UNION`/`INTERFACE` `possibleTypes`)

## List operations (queries and mutations)

Use these to discover top-level operations before writing a query or mutation body.

```bash
gqlcli queries                                  # list all Query fields
gqlcli queries --args --desc                    # include argument signatures and descriptions
gqlcli queries --filter citizen                 # filter by operation name substring
gqlcli queries --filter citizen --args --desc   # practical discovery view
gqlcli queries --filter citizen -f json         # machine-readable output

gqlcli mutations                                # list all Mutation fields
gqlcli mutations --args --desc                  # include argument signatures and descriptions
gqlcli mutations --filter create                # filter by operation name substring
gqlcli mutations --filter citizen --args --desc # practical discovery view
gqlcli mutations --filter citizen -f json       # machine-readable output
```

Suggested flow:
- Run `queries` or `mutations` to discover operation names and arguments
- Run `describe` on related input/output types
- Execute with `query` or `mutation`

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

## Subscribe to events

Subscriptions use the GraphQL over WebSocket protocol (`graphql-transport-ws`) and stream NDJSON envelopes. HTTP(S) endpoint URLs are automatically mapped to WS(S).

```bash
gqlcli subscribe 'subscription { messageAdded { id text } }'

gqlcli subscribe \
  --subscription-file ./messages.graphql \
  --variables-file ./vars.json

gqlcli subscribe \
  --subscription 'subscription Watch($room:ID!){ messageAdded(room:$room){ id text } }' \
  --variables '{"room":"general"}' \
  --operation Watch
```

Output is one JSON object per line: `next`, `error`, and `complete`. Press Ctrl-C to cancel cleanly.

## Named operations (save and reuse)

Save a query or mutation by name to avoid repeating it. Stored in `.gqlcli.json` under `"operations"`.

```bash
# Save
gqlcli op save --name get-user \
  --query 'query GetUser($id: ID!) { user(id: $id) { id name } }' \
  --defaults '{"id":"default-123"}'

gqlcli op save --name create-user \
  --mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }'

# CRUD
gqlcli op list
gqlcli op show --name get-user
gqlcli op delete --name get-user

# Execute by name — --variables overrides defaults
gqlcli query --op get-user
gqlcli query --op get-user --variables '{"id":"456"}'
gqlcli mutation --op create-user --input '{"name":"Alice"}'
gqlcli mutation --op create-user --env prod --input '{"name":"Alice"}'
```

`--op` is available on `query`, `mutation`, and `subscribe` commands. Using the wrong operation type returns an error. Subscription operations can also be stored manually in `.gqlcli.json` with `"type": "subscription"`.

`op save` currently supports `--query` and `--mutation` flags only. To use `--op` with `subscribe`, add the subscription operation manually under `.gqlcli.json -> operations` with `"type": "subscription"` and the GraphQL text in `"query"`.

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

## HTTP controls

Use these instead of dropping to curl when you need transport-level control:

```bash
# One-off headers; CLI headers override .gqlcli.json env headers
gqlcli query '{ viewer { id } }' --env prod \
  -H 'Authorization=Bearer temp-token' \
  -H 'X-Tenant=acme'

# Reliability / CI
gqlcli query '{ health }' --timeout 10 --retry 3 --retry-delay 500ms
gqlcli query --query-file ./check.graphql --fail-on-graphql-errors

# Self-signed/internal TLS
gqlcli queries --url https://localhost:8443/graphql --insecure

# Response metadata
gqlcli query '{ viewer { id } }' --include-headers
gqlcli query '{ viewer { id } }' --dump-headers headers.txt -f json
gqlcli query '{ viewer { id } }' --metadata status-code --metadata header:X-Request-Id
```

`--header/-H`, `--timeout`, `--retry`, `--retry-delay`, `--fail-on-graphql-errors`, and `--insecure` apply to HTTP-backed commands (`query`, `mutation`, `subscribe`, `batch`, `queries`, `mutations`, `types`, `describe`).

Metadata flags (`--include-headers`, `--dump-headers`, `--metadata`) apply to operation commands that return a single response envelope (`query`, `mutation`, `subscribe`), not schema listing commands (`queries`, `mutations`, `types`, `describe`).

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
--debug                      # log HTTP request/response
--env NAME                   # select environment from .gqlcli.json (e.g. local, prod)
--header 'Key=Value' / -H    # add/override a per-request header
--timeout 10                 # request timeout in seconds
--retry 3 --retry-delay 1s   # retry transient failures
--fail-on-graphql-errors     # exit non-zero when response.errors is present
--insecure                   # skip TLS certificate verification
```

## Skill install/update

```bash
gqlcli install-skill
```

Installs or updates the embedded skill at `~/.claude/skills/gqlcli/SKILL.md`.
