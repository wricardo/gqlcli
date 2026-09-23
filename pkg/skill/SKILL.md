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

## Keep GraphQL workflows inside gqlcli

For GraphQL work, use gqlcli's native features before building Bash loops, `xargs` pipelines,
external `jq` pipelines, or raw `curl` requests. Most workflows fit one of these forms:

| Need | Use |
|---|---|
| Run one operation | `query` or `mutation` |
| Select, filter, or aggregate a response | built-in `--jq` |
| Run many operations already known up front | `batch`, optionally with `--batch-size` |
| Discover data and then loop, branch, or make dependent calls | `script` with `gql.each` |
| Reuse a known workflow | a named operation or saved script in `.gqlcli.json` |

Before writing a shell `for` loop, check whether `batch` or `script` expresses the workflow.
Before piping gqlcli output to external `jq`, check whether `--jq` can produce the final output
directly. Use shell orchestration only when the workflow must coordinate GraphQL with unrelated
programs or operating-system tasks. Use `curl` only when diagnosing raw HTTP behavior or using a
transport gqlcli does not support.

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
gqlcli describe Citizen --depth 1       # include directly referenced non-scalar types + reverse references
gqlcli describe Citizen --depth 2       # recurse one level deeper + widen reverse-lookup
gqlcli describe Citizen --depth 1 --max-op-refs 0 --max-field-refs 0  # remove default caps
# truncated reverse-reference sections print "(showing X of N)"
```

Depth behavior for describe:
- `--depth 0` only prints the requested type (default)
- `--depth 1` includes directly referenced non-scalar types **and appends reverse-reference sections**:
  - `# Referenced by top-level operations` — Query/Mutation fields whose args or return types reach the requested type
  - `# Referenced by fields` — non-root schema types/fields that point at the requested type
- `--depth N` recursively expands non-scalar references up to N levels (including `UNION`/`INTERFACE` `possibleTypes`) and widens that reverse lookup to the same depth
- Top-level Query/Mutation matches are ranked: arg matches first, then shallower return-type matches before deeper wrapper/pagination matches
- Reverse-reference sections default to 5 top-level operation refs and 5 referencing schema types; pass `--max-op-refs 0 --max-field-refs 0` for no limit
- When a reverse-reference section is capped, its header shows `(showing X of N)`

This is especially useful when you already know the right **type** but not yet the right **operation**. For example, `describe --depth 1 XmlPathCampaign` shows not just the campaign fields, but also top-level operations like `xmlPathCampaign(...)` / `xmlPathCampaigns` and related types like `XmlPathSession` and `XmlPathSimulation`. Likewise, `describe --depth 1 ScorecardRunLog` surfaces related entry points such as scorecard result queries and fields like `PhoneCall.scorecardResults`.

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

## Validate without executing

Check a document against the schema before running it. Nothing is sent to the server — only the schema is introspected, so no resolver runs and no data changes. Exits 0 when valid, 1 when not, with `line:column: message` per problem.

```bash
gqlcli validate '{ users { id name } }'
gqlcli validate --query-file ./getUser.graphql
gqlcli validate --mutation 'mutation { deleteUser(id:"1") { ok } }'

# machine-readable: {"valid":false,"errors":[{"line":1,"column":8,"message":...,"rule":...}]}
gqlcli validate '{ users { nope } }' --format json
gqlcli validate '{ users { nope } }' --jq '.errors[].message'

# each error carries compact SDL for the type it refers to, at the same
# extensions.schemaHint path the executed path uses
gqlcli validate '{ users { nope } }' --jq '.errors[].extensions.schemaHint'

# same check from the operation commands, which stop before sending
gqlcli query --validate-only '{ users { id } }'
gqlcli mutation --validate-only 'mutation { ... }'
```

Validate offline — no network, no credentials — by dumping the schema once with `sdl`:

```bash
gqlcli sdl > schema.graphql
gqlcli validate --schema-file schema.graphql '{ users { id } }'
```

`sdl` prints the whole schema as a loadable SDL document (builtin scalars and directives omitted, since every parser supplies them). Use `describe` instead when you just want to read a few types.

Variable *values* are not part of the document and are not checked: a document declaring required variables validates on its own.

## Script imperative workflows (JavaScript)

Use `script` when later GraphQL calls depend on earlier results, or when you need loops,
branching, aggregation, or controlled concurrency. Prefer it over shell loops that repeatedly
invoke gqlcli. A script keeps variables as JavaScript values, avoids JSON re-encoding between
steps, and reports one workflow result.

```bash
gqlcli script --file ./disableUsers.js
gqlcli script --file ./job.js --arg '{"tenantId":"acme"}'
gqlcli script --file ./job.js --arg-file ./input.json
gqlcli script --source 'async function run(gql) { return gql.query("query { viewer { id } }") }'
gqlcli script --op disable-inactive-users
```

The source must define a function named `run` by default. It receives the gqlcli helper and an
optional input object. Normal JavaScript declarations and data structures work, including
`const`, `let`, arrays, objects, loops, conditionals, and `async`/`await`.

This example loads scorecard details for an array of IDs without a Bash loop:

```js
async function run(gql, input) {
  return await gql.each(
    input.scorecardIds,
    async (id) => {
      const response = await gql.query(
        `query Scorecard($id: ID!) {
          scorecard(id: $id) {
            id
            name
            sections { id name }
          }
        }`,
        { id }
      )
      return response.data.scorecard
    },
    { concurrency: 5, stopOnError: false }
  )
}
```

```json
{"scorecardIds":["sc_101","sc_102","sc_103"]}
```

```bash
gqlcli script --file ./scorecards.js --arg-file ./input.json
```

Helpers available in scripts:
- `gql.query(query, variables?, operationName?)`
- `gql.mutation(mutation, variables?, operationName?)`
- `gql.request({ type, query|mutation, variables, operationName })`
- `gql.each(items, worker, { concurrency?, stopOnError?, onError? })`

Use `gql.each` for independent operations instead of hand-building concurrency with `xargs` or
background shell jobs. Set `concurrency: 1` for sequential execution. Use `stopOnError: true`
when no later item should run after a failure; use `false` when all items should be attempted.

`run` can be synchronous or async (`async function run(gql, input) { ... }`). Its returned value
is serialized as JSON. Use `--output result.json` to write that value to a file.

For short scripts, `--source` accepts inline JavaScript. For multiline scripts, prefer `--file`.
On Unix-like systems, a quoted heredoc can provide source through stdin without shell-escaping
the JavaScript:

```bash
gqlcli script --file /dev/stdin --arg-file ./input.json <<'EOF'
async function run(gql, input) {
  let results = []
  for (const id of input.scorecardIds) {
    const response = await gql.query(
      `query Scorecard($id: ID!) { scorecard(id: $id) { id name } }`,
      { id }
    )
    results.push(response.data.scorecard)
  }
  return results
}
EOF
```

The CLI has no overall script deadline. By default, GraphQL HTTP calls also have no timeout.
`--timeout 60` gives each individual GraphQL request a 60-second deadline; it does not limit
the duration of the entire script.

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
client := gqlcli.NewHTTPClient(&gqlcli.Config{URL: endpoint})
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

Use `batch` when all operations are known up front and do not depend on earlier responses. This
replaces shell loops that invoke `gqlcli query` or `gqlcli mutation` once per item. Each input
operation has `"query"` (required) and optional `"variables"`, `"operationName"`, and `"jq"`
fields.

The default transport is NDJSON. Put one operation on each line:

```json
{"query":"query User($id: ID!) { user(id: $id) { id name } }","variables":{"id":"u_101"}}
{"query":"query User($id: ID!) { user(id: $id) { id name } }","variables":{"id":"u_102"}}
{"query":"query User($id: ID!) { user(id: $id) { id name } }","variables":{"id":"u_103"}}
```

Run the file directly:

```bash
gqlcli batch --file ./operations.ndjson
```

By default, all input operations are sent in one HTTP request. Use `--batch-size` for large or
slow workloads. Chunks are sent sequentially and output remains in input order:

```bash
gqlcli batch --file ./operations.ndjson --batch-size 10
```

`--batch-size 10` sends at most ten operations per HTTP request. `--batch-size 1` sends one
operation per request without requiring a shell loop. `--batch-size 0` is the default and sends
all operations in one request. By default there is no HTTP timeout; add `--timeout 120` only when
the request should have a deadline.

Use JSON-array transport when the server expects the conventional GraphQL batch shape:

```json
[
  {"query":"{ users { id name } }"},
  {"query":"{ posts { id title } }"}
]
```

```bash
gqlcli batch --array --file ./operations.json --batch-size 10
```

Each operation may contain a server-side `"jq"` expression. This reduces the response before it
returns to gqlcli:

```json
{"query":"{ users { id name active } }","jq":".data.users[] | select(.active) | {id, name}"}
{"query":"{ posts { id } }","jq":".data.posts | length"}
```

Apply one client-side expression to every returned response with `--jq`:

```bash
gqlcli batch --file ./operations.ndjson --jq '.data'
```

If one operation must discover the IDs used by later operations, switch to `script`; do not
assemble a query-to-external-jq-to-batch shell pipeline unless another non-GraphQL program must
participate.

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

Prefer built-in `--jq` when the filtered value is the desired command output. It avoids a shell
pipeline, does not require `--format json`, and preserves gqlcli's GraphQL error handling.
Useful patterns include:

```bash
gqlcli query '{ users { id name active } }' --jq '.data.users[] | select(.active) | {id, name}'
gqlcli query '{ users { id } }' --jq '[.data.users[].id]'
gqlcli query '{ users { status } }' --jq '.data.users | group_by(.status) | map({status: .[0].status, count: length})'
```

Use external `jq` only when its output must feed a non-gqlcli program, when processing files that
are not gqlcli responses, or when an expression needs a jq feature not supported by the built-in
engine. External jq runs independently and therefore filters error responses too; built-in
`--jq` leaves GraphQL and transport errors visible.

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

`--header/-H`, `--timeout`, `--retry`, `--retry-delay`, `--strict` (default true), and `--insecure` apply to HTTP-backed commands (`query`, `mutation`, `subscribe`, `validate`, `sdl`, `batch`, `script`, `queries`, `mutations`, `types`, `describe`).

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
