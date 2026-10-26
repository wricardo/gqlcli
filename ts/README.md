# @wricardo/gqlcli (TypeScript)

Native TypeScript port of the gqlcli features an agent needs, with **no subprocess**: GraphQL client, schema
exploration, validation, jq, batch, subscriptions, JavaScript scripts, and ready-made
[Pi Durable](https://earendil.com/posts/pi-durable/) tools.

It reads the same `.gqlcli.json` (environments, headers, saved operations and scripts) and shares the Go CLI's
on-disk schema cache, so both tools agree on config and don't re-introspect.

## Pi Durable

```ts
import { createRegistry } from "@earendil-works/pi-durable";
import { gqlcliExtension } from "@wricardo/gqlcli/pi-durable";

const registry = createRegistry();
const gql = gqlcliExtension({ gqlcli: { env: "prod" } }); // reads .gqlcli.json / GRAPHQL_URL
registry.install(gql);

const root = await harness.root(context, { agent: { model, extensions: [gql] } });
```

| Tool | Replay after crash | What it does |
|---|---|---|
| `gql_queries`, `gql_mutations` | safe | list root fields (`filter`, `args`, `desc`, `depth`) |
| `gql_types` | safe | list types (`filter`, `kind`) |
| `gql_describe` | safe | compact SDL for `Type` or `Type.field`, `depth`, referencing operations/types |
| `gql_sdl` | safe | whole schema as SDL |
| `gql_validate` | safe | validate without executing; `line:col` + SDL hint |
| `gql_query` | safe | run a query (rejects documents declaring a mutation); built-in `jq` |
| `gql_batch` | safe | independent queries, ndjson/array, `batchSize`, `jq` |
| `gql_subscribe` | safe | collect events up to `maxEvents`/`timeoutMs` |
| `gql_script` | safe (read-only) / unsafe | JS workflow: `gql.query`, `gql.each`; limits on operations and time |
| `gql_mutation` | **unsafe** | only with `mutations: true` |

**Read-only by default.** Without `mutations: true`, no mutation tool exists, `gql_script` rejects mutations, and every
tool is `replay: "safe"`. With `mutations: true`, `gql_mutation` and `gql_script` keep Pi Durable's default (no replay), so a
write interrupted by a crash is reported to the model instead of repeated. Failed GraphQL responses come back as
`isError` with the body plus schema hints for the types involved.

Pick tools individually with `createGqlcliTools(opts)`; options: `prefix`, `format` (`toon` default), `scriptMaxOperations`,
`scriptTimeoutMs`.

## Library

```ts
import { Gqlcli } from "@wricardo/gqlcli";

const g = new Gqlcli({ env: "prod", retry: 2, timeoutMs: 10_000 });
const r = await g.query({ query: "{ users { id name active } }", jq: ".data.users[] | select(.active) | .name" });

const schema = await g.schema();
schema.describe("Query.user", { args: true, depth: 1 });
schema.validate("{ users { nope } }"); // { valid: false, errors: [{ line, column, message, schemaHint }] }

await g.script({ source: `async function run(gql, input) { /* … */ }` }, { readOnly: true, maxOperations: 20, timeoutMs: 30_000 });
await g.validate(doc, { schemaFile: "schema.graphql" }); // offline
```

Also exported: `GraphQLClient`, `SchemaExplorer` (from introspection or SDL), `executeBatch`, `runScript`, `subscribe`,
`jq` (real jq compiled to WASM), `formatOutput` (`toon|json|json-pretty|compact|table|llm`),
`.gqlcli.json` helpers.

### Local GraphQL schema explorer

The explorer accepts a GraphQL document against the **explorer schema**, not
the target API. Print its contract with `gqlcli explore --print-schema`; the
canonical source is `pkg/explorer/schema.graphqls` in this repository, copied
byte-for-byte into the npm package at build time.

```ts
import { Gqlcli, SchemaExplorer } from "@wricardo/gqlcli";

const gql = new Gqlcli({ env: "prod" });
const result = await gql.explore(`
  query Find($coord: String!) {
    element(coordinate: $coord) {
      __typename coordinate sdl
      ... on SchemaField { parentType { name } type { signature namedType { name } } }
    }
  }
`, { coord: "Lead.email" });

// No endpoint, credentials, or network calls needed.
const offline = SchemaExplorer.fromSDL(savedTargetSDL);
const paths = await offline.explore(`{
  paths(from: "RootQuery", to: "Lead.email", maxDepth: 6, first: 10) {
    nodes { depth fieldDepth segments { edge { kind source { coordinate } target { coordinate } } } }
    pageInfo { hasNextPage endCursor }
  }
}`);
```

`Gqlcli.explore(document, variables?, operationName?)` introspects the selected
target once, using the same per-URL-and-credentials on-disk cache as Go;
`refreshSchema()` clears the in-memory and on-disk schema so the next call
fetches it again. `SchemaExplorer.fromSDL(...).explore(...)` stays offline.
`gqlcli explore --schema-file schema.graphql` and `--offline` offer equivalent
CLI workflows. Explorer documents and `validate(document:)` run locally;
`query`, `mutation`, and `subscribe` send operations to the target.
Only the *schema introspection* on a cache miss or refresh may contact it.

Coordinates identify canonical declarations: `Lead`, `Lead.email`,
`RootQuery.lead`, `RootQuery.lead(id:)`, `CreateLeadInput.email`, and
`LeadStatus.NEW`. Use `element` with `__typename` / inline fragments to
navigate actual objects, rather than parsing strings. An operation projects
its root field and shares its coordinate. Built-in scalars are indexed;
`__` introspection types and directive coordinates are not.

Paths follow directed edges. `OUTPUT` allows field declarations, returns,
interface-to-concrete possible types, union members, and enum declarations;
`INPUT` allows argument/input-field declarations and input type references;
`ALL` also includes object-to-interface implementation edges. Output
selections through interface/union members require inline fragments in an
executable target query. `depth` counts graph edges; `fieldDepth` counts
GraphQL output field selections, including a terminal field. All unique
simple paths within `maxDepth` are ordered by depth then edge identity.
They may be exponentially numerous: paginate with `first`/`after`, use
practical depth bounds, and handle explicit resource-limit errors.

`reachableFields` paginates distinct (operation, field) pairs. Every node's
`paths(first:, after:)` is **independent**: finishing the outer page does
not fetch every nested path. Cursors bind the schema snapshot and query
parameters; an inner cursor cannot be reused for another pair, and cursors
from a refreshed schema are rejected. `sdl` is rendered on selection as
canonical standalone SDL from metadata available in the schema. Descriptions
are omitted by default, including descriptions of nested members; select
`sdl(includeDescription: true)` to include them. Defaults, deprecations, and
available directives are retained either way. An introspection cache cannot
restore original whitespace or applied directives that introspection did not
report.

## Parity with the Go CLI

`src/parity.test.ts` runs the Go binary and this library against the same GraphQL server and compares them. It builds
`cmd/gqlcli` with `go build`, or uses `$GQLCLI_GO_BIN`; without either it is skipped.

`src/explorer/parity.test.ts` compares local explorer results against
`gqlcli explore --schema-file` using the same fixture, including actual
cursor strings and lazy SDL. The copied explorer SDL has a separate
byte-for-byte parity test.

| Area | Compared | Result |
|---|---|---|
| `describe` (types, `Type.field`, `--args`, `--desc`, `--depth`, reference caps, errors) | output bytes | identical |
| `types`, `queries`, `mutations` (filters, kinds, `--args`, `--depth`) | same types/signatures | identical |
| `sdl` | schema after sorting | identical |
| `query`/`mutation` responses, variables, `--operation`, `--input`, errors | JSON | identical |
| `--jq`, `batch` (ndjson, array, `--batch-size`, `--jq`) | values | identical |
| `script` results, `gql.each` and `strict` defaults | JSON, abort behavior | identical |
| `validate` | verdict and `line:column` | identical |
| `explore` (custom roots, entities, edges, SDL, paths, independent nested pagination) | complete JSON and cursors | identical on shared fixture |

## Known differences

- **Validation wording.** The Go CLI validates with gqlparser, this library with graphql-js. Verdicts and positions match, but a few
  messages differ (for example a missing required argument, or an unsupported operation type).
- **Validation hints** show compact SDL for the type involved, not the Go CLI's fuller SDL (no `implements`, defaults).
- **Executed-error hints.** The Go CLI adds `extensions.schemaHint` to GraphQL errors; here the Pi Durable tools append the same
  hints to the error text, while `Gqlcli.query` returns the server response unchanged.
- **Listings are structured.** `types`/`queries`/`mutations` return objects (names, signatures, optional SDL), not the CLI's text.
  `types` hides `__` introspection types.
- Scripts run in `node:vm` with only `console`, `gql` and JS built-ins. This is not a security boundary against hostile code. The
  `timeoutMs` interrupts synchronous busy loops in the entry function, but a busy loop after an `await` cannot be interrupted;
  use `readOnly`, `maxOperations` and `approve` as guardrails.
- No `--insecure` flag: pass a custom `fetch` (undici dispatcher) for self-signed TLS. `subscribe` accepts `insecure`.
- `login`/auto-relogin, `embed` (semantic search) and `install-skill` are not ported.

## Develop

```bash
npm install
npm run typecheck && npm test && npm run build
```
