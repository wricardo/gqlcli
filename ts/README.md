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

## Parity with the Go CLI

`src/parity.test.ts` runs the Go binary and this library against the same GraphQL server and compares them. It builds
`cmd/gqlcli` with `go build`, or uses `$GQLCLI_GO_BIN`; without either it is skipped.

| Area | Compared | Result |
|---|---|---|
| `describe` (types, `Type.field`, `--args`, `--desc`, `--depth`, reference caps, errors) | output bytes | identical |
| `types`, `queries`, `mutations` (filters, kinds, `--args`, `--depth`) | same types/signatures | identical |
| `sdl` | schema after sorting | identical |
| `query`/`mutation` responses, variables, `--operation`, `--input`, errors | JSON | identical |
| `--jq`, `batch` (ndjson, array, `--batch-size`, `--jq`) | values | identical |
| `script` results, `gql.each` and `strict` defaults | JSON, abort behavior | identical |
| `validate` | verdict and `line:column` | identical |

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
