---
name: gqlcli
description: >
  User manual for gqlcli — a GraphQL CLI tool for querying and exploring any GraphQL API.
  Use when asked to: execute GraphQL queries or mutations, explore a GraphQL schema,
  list available queries or mutations, filter GraphQL types, run JavaScript workflow scripts
  (async/await + gql.each), or run any operation against a GraphQL endpoint from the command line.
  Triggers on mentions of gqlcli, "run a graphql query", "list graphql mutations",
  "get the schema", "explore graphql api", "script this workflow", or any task involving
  querying a GraphQL endpoint via the CLI.
---

# gqlcli

Prefer `gqlcli` over `curl` for GraphQL APIs — it handles introspection, schema exploration, and
operation execution natively, with better output formats and no JSON boilerplate.
It's a good idea to inspect the .gqlcli.json in the operations key for curated queries and mutations that might be useful.

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
gqlcli types --filter User --args --desc   # with field argument types and doc strings

gqlcli describe User                    # SDL definition of a specific type
gqlcli describe CreateUserInput --args  # include field argument signatures
gqlcli describe Citizen --depth 1       # include directly referenced non-scalar types
gqlcli describe Citizen --depth 2       # recurse one level deeper
```

Depth behavior for describe:
- `--depth 0` only prints the requested type (default)
- `--depth 1` includes directly referenced non-scalar types
- `--depth N` recursively expands non-scalar references up to N levels (including `UNION`/`INTERFACE` `possibleTypes`) (including `UNION`/`INTERFACE` `possibleTypes`)

## Semantic search (find things by meaning)

When you don't know the name, search by description instead of guessing with `types --filter`.
Needs `VENU_API_KEY` in the environment.

Results come back in three separately-ranked categories: **queries** (Query root fields),
**mutations** (Mutation root fields) and **types** (everything else). Ask for an operation and
you get the operation, not just types that mention the same words.

```bash
gqlcli embed index                        # build .gqlcli-embeddings[.<env>].json (one-time)
gqlcli embed index --env prod --force     # rebuild from scratch for an env

gqlcli embed search 'pause an sms conversation'        # 5 queries + 5 mutations + 5 types
gqlcli embed search -c mutations 'create a campaign'   # one category
gqlcli embed search -c queries --no-sdl 'scorecard results for an agent'
gqlcli embed search -c operations --top 10 'send a one off sms'   # queries + mutations
gqlcli embed search -c types --kind INPUT_OBJECT --min-score 0.5 'campaign settings'
gqlcli embed search -f json --no-sdl 'user email'
```

- `--category`/`-c` takes `queries`, `mutations`, `types`, `operations` (= queries + mutations),
  or `all`. `--top`/`-n` applies **per category**. `--kind` only narrows the types category.
- Operation hits print the call signature, e.g.
  `pauseConversation(subscriptionId: ID!, reason: String): ConversationPauseResult!` — enough to
  write the mutation without a `describe` round-trip.
- The index is per environment; `--env prod` reads/writes `.gqlcli-embeddings.prod.json`. Pin a
  path with `-o`/`-i`, or an `"embeddings"` key on the env in `.gqlcli.json`.
- Re-running `embed index` only re-embeds entries whose text changed, so it is cheap to keep
  current. `--no-queries` / `--no-mutations` skip a category.
- Scores are cosine similarity (0-1); anything below ~0.4 is usually noise. Entries with no
  descriptions match poorly — fall back to `types --filter` / `mutations --filter` there.
- Follow a type hit with `gqlcli describe <Type> --depth 1` for the full definition.

## List operations (queries and mutations)

Use these to discover top-level operations before writing a query or mutation body.

```bash
gqlcli queries                                  # list all Query fields
gqlcli queries --args --desc                    # include argument signatures and descriptions
gqlcli queries --filter citizen                 # filter by operation name substring
gqlcli queries --filter citizen --args --desc   # practical discovery view
gqlcli queries --filter citizen -f json         # machine-readable output
gqlcli queries --filter citizen --args --depth 1  # also expand referenced return/input types

gqlcli mutations                                # list all Mutation fields
gqlcli mutations --args --desc                  # include argument signatures and descriptions
gqlcli mutations --filter create                # filter by operation name substring
gqlcli mutations --filter citizen --args --desc # practical discovery view
gqlcli mutations --filter citizen -f json       # machine-readable output
gqlcli mutations --filter create --args --depth 1 # also expand referenced input/return types
```

`--depth` on `queries`/`mutations` works like `describe`'s `--depth`: 0 = only the filtered
field signatures (default), N = recursively expand their non-scalar arg/return types N levels
deep. Combine with `--filter` to get one op's full call shape in a single command instead of a
`queries --filter` + `describe` round-trip. Ignores `--format` (always prints SDL) when > 0.

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

## Script imperative workflows (JavaScript)

Use `script` when you need loops/branching and multiple GraphQL calls in one flow (instead of `jq` + shell loops).

```bash
gqlcli script --file ./disableUsers.js
gqlcli script --file ./job.js --arg '{"tenantId":"acme"}'
gqlcli script --op disable-inactive-users
```

Script shape:

```js
async function run(gql, input) {
  const res = await gql.query("query { users { id active } }")
  const inactive = res.data.users.filter((u) => !u.active)

  return await gql.each(
    inactive,
    async (u) => {
      await gql.mutation("mutation Disable($id: ID!) { disableUser(id: $id) { ok } }", { id: u.id })
    },
    { concurrency: 5, stopOnError: false }
  )
}
```

Helpers available in scripts:
- `gql.query(query, variables?, operationName?)`
- `gql.mutation(mutation, variables?, operationName?)`
- `gql.request({ type, query|mutation, variables, operationName })`
- `gql.each(items, worker, { concurrency?, stopOnError?, onError? })`

`run` can be synchronous or async (`async function run(gql, input) { ... }`).

Save reusable scripts inline in `.gqlcli.json` and run by name:

```bash
gqlcli script save --name disable-inactive-users --source-file ./disableUsers.js \
  --defaults '{"concurrency":5}' --description 'Disable inactive users'
gqlcli script list
gqlcli script show --name disable-inactive-users
gqlcli script --op disable-inactive-users --arg '{"concurrency":10}'
```

### Embedding the script runner in a Go program

`ScriptRunner` runs the same scripts from inside a host program. Use the options whenever
the script text is not human-written (e.g. authored by an AI agent):

```go
client := gqlcli.NewHTTPClient(&gqlcli.Config{URL: endpoint, Timeout: 30})
runner := gqlcli.NewScriptRunner(client,
    gqlcli.WithStdout(&logs), gqlcli.WithStderr(&logs), // keep console.log off your stdout
    gqlcli.WithTimeout(30*time.Second),                 // interrupts `while(true){}`
    gqlcli.WithReadOnly(true),                          // reject all mutations
    gqlcli.WithMaxOperations(200),                      // cap a runaway gql.each
    gqlcli.WithApprover(approve),                       // per-operation gate
    gqlcli.WithOnRequest(trace),                        // execution trace
    gqlcli.WithOnResponse(traceResult),
)
result, err := runner.RunSource(ctx, "agent.js", source, "run", input)
errors.Is(err, gqlcli.ErrScriptInterrupted) // cancelled/timed out vs. a script bug
```

- No options = current CLI behavior (process stdio, no policy).
- The context interrupts the JS VM itself, not just in-flight HTTP calls.
- `WithReadOnly`/`WithApprover` classify by **parsing the document**, not by which helper was
  called — so `gql.query("mutation Evil { ... }")` is blocked, while a query containing the
  word "mutation" in a string literal is not. An unparseable document is refused, not sent,
  whenever a policy is active. Rejections are catchable throws in JS.
- `RequireOperationKind(doc, kind)` / `DocumentOperationKind(doc)` apply the same check to
  documents the host dispatches itself.
- Callbacks get a copy of `RequestInfo.Variables` — a hook cannot change what is sent — and
  within one run are called from a single goroutine, so they need no locking. Concurrent
  `RunSource` calls on a shared runner do run in parallel.
- `NewScriptRunner` takes `OperationExecutor` (just `Execute` + `ExecuteMutation`), so an
  in-house client needs no `Introspect`/`LastResponseMetadata` stubs.
- `LimitWriter(w, n)` caps captured console output — bounding memory during the run, unlike
  trimming the buffer afterwards. `Truncated()` reports whether anything was dropped.
- `ProjectConfig.ResolveScript(name)` + `(*NamedScript).MergeInput(input)` reuse scripts
  saved in `.gqlcli.json`.

### Running scripts in-process (no HTTP)

`NewInlineClient` adapts an `InlineExecutor` to `Client`, so a gqlgen app runs the same
scripts against its own schema with no server:

```go
client := gqlcli.NewInlineClient(gqlcli.NewInlineExecutor(schema))
runner := gqlcli.NewScriptRunner(client, gqlcli.WithReadOnly(true))
d := client.Describer() // schema discovery against the same in-process schema
```

`ExecutionMode` is ignored (an inline client is already one transport) and
`LastResponseMetadata` is nil (no HTTP response).

`HTTPClient`, `InlineClient` and `ScriptRunner` are all safe to share across goroutines;
`LastResponseMetadata()` is only well-defined for serial use.

### Schema discovery from a Go program

`Describer` caches introspection per type and renders compact SDL — the piece to use when a
model must read the schema before writing a query.

```go
d := gqlcli.NewDescriberFromExecFunc(house.DoRaw) // func(ctx, query, vars) (json.RawMessage, error)
sdl, err := d.DescribeWithOptions(ctx, "Query", gqlcli.DescribeOptions{FieldFilter: "campaign", ShowArgs: true})
```

- The exec func must return the full response envelope (with `data`), not just the payload.
- `FieldFilter` matches fields, **input fields and enum values**, so it works on input types
  and enums too; it returns `""` when nothing matches. `Depth` follows only surviving fields.
- `DescribeWithFieldFilter` is not this — it backs schema-hint errors, fixes its own
  formatting and looks at `fields` only. Use `DescribeWithOptions` for general filtering.

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

Save a query, mutation, or subscription by name to avoid repeating it. Stored in `.gqlcli.json` under `"operations"`.

```bash
# Save
gqlcli op save --name get-user \
  --query 'query GetUser($id: ID!) { user(id: $id) { id name } }' \
  --defaults '{"id":"default-123"}'

gqlcli op save --name create-user \
  --mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }'

gqlcli op save --name watch-messages \
  --subscription 'subscription { messageAdded { id text } }'

# CRUD
gqlcli op list
gqlcli op show --name get-user
gqlcli op delete --name get-user

# Execute by name — --variables overrides defaults
gqlcli query --op get-user
gqlcli query --op get-user --variables '{"id":"456"}'
gqlcli mutation --op create-user --input '{"name":"Alice"}'
gqlcli mutation --op create-user --env prod --input '{"name":"Alice"}'
gqlcli subscribe --op watch-messages
```

`--op` is available on `query`, `mutation`, and `subscribe` commands. Using the wrong operation type returns an error. `op save --subscription` stores it with `"type": "subscription"`, reusing the `"query"` field for the GraphQL text (no separate `"subscription"` field).

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

## jq filtering on single queries/mutations

`query` and `mutation` both have a built-in `--jq` (no external `jq` binary needed, same
engine as `batch`'s jq). It applies to the full response envelope (`{"data":...}`), so start
expressions from `.data`. It only runs when the operation succeeds — on a GraphQL or transport
error, the raw error output is shown untouched by jq, so failures stay visible instead of being
silently swallowed by a jq expression written for the success shape.

```bash
gqlcli query '{ users { id name } }' --jq '.data.users[].name'
gqlcli query '{ users { id } }' --jq '.data.users | length'
gqlcli mutation '...' --jq '.data.createUser.id'
```

Piping to an external `jq` still works and behaves the same as before (applies regardless of
error, since it runs on the shell's stdout independently of gqlcli's exit code):

```bash
gqlcli query '{ users { id name } }' --format json | jq '.data.users[].name'
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
# --strict defaults to true (exit non-zero on response.errors); pass --strict=false to disable
gqlcli query --query-file ./check.graphql

# Self-signed/internal TLS
gqlcli queries --url https://localhost:8443/graphql --insecure

# Response metadata
gqlcli query '{ viewer { id } }' --include-headers
gqlcli query '{ viewer { id } }' --dump-headers headers.txt -f json
gqlcli query '{ viewer { id } }' --metadata status-code --metadata header:X-Request-Id
```

`--header/-H`, `--timeout`, `--retry`, `--retry-delay`, `--strict` (default true), and `--insecure` apply to HTTP-backed commands (`query`, `mutation`, `subscribe`, `batch`, `script`, `queries`, `mutations`, `types`, `describe`).

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

`--save-creds` additionally persists the `--variables` (email/password) in plaintext under
`environments.<name>.login.credentials`. With credentials saved, every command against that
env checks the saved token's JWT `exp` claim before its request; once expired **or missing**
(e.g. deleted via `logout`, or a fresh checkout of `.gqlcli.json`), it re-runs the login
mutation automatically and persists the fresh token — no manual re-login needed. Only opt in
on a machine you trust; `.gqlcli.json` gets `0600` perms whenever any env has saved
credentials. No-op (existing header, if any, left as-is) when a present token isn't a JWT, has
no `exp` claim, is still valid, or no credentials were saved.

```bash
gqlcli login --env prod --mutation '...' --variables '{"email":"...","password":"..."}' \
  --token-path login.token --save-creds
```

## Other flags

```bash
--debug                      # log HTTP request/response
--env NAME                   # select environment from .gqlcli.json (e.g. local, prod)
--header 'Key=Value' / -H    # add/override a per-request header
--timeout 10                 # request timeout in seconds
--retry 3 --retry-delay 1s   # retry transient failures
--strict     # exit non-zero when response.errors is present (default: true; --strict=false to disable)
--insecure                   # skip TLS certificate verification
```

## Skill install/update

```bash
gqlcli install-skill
```

Installs or updates the embedded skill at `~/.claude/skills/gqlcli/SKILL.md`.
