import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { buildSchema, getIntrospectionQuery, graphqlSync } from "graphql";
import { SchemaExplorer } from "../schema.ts";

const root = join(import.meta.dirname, "../../..");
const fixture = join(root, "testdata/explorer/schema.graphql");
const sdl = readFileSync(fixture, "utf8");
const work = mkdtempSync(join(tmpdir(), "gqlcli-explorer-parity-"));
const bin = join(work, "gqlcli");
let go: string | undefined;
before(() => {
  if (process.env["GQLCLI_GO_BIN"]) {
    go = process.env["GQLCLI_GO_BIN"];
  } else {
    execFileSync("go", ["build", "-o", bin, "./cmd/gqlcli"], { cwd: root });
    go = bin;
  }
});
after(() => {
  if (!process.env["GQLCLI_GO_BIN"]) rmSync(work, { recursive: true, force: true });
});

async function equalExplorer(document: string, variables?: Record<string, unknown>): Promise<unknown> {
  const args = ["explore", "--schema-file", fixture, "--format", "json", "--strict=false"];
  if (variables) args.push("--variables", JSON.stringify(variables));
  args.push(document);
  const fromGo: unknown = JSON.parse(execFileSync(go!, args, { cwd: root, encoding: "utf8" }));
  const fromTS = await SchemaExplorer.fromSDL(sdl).explore(document, variables);
  const fromTSJSON: unknown = JSON.parse(JSON.stringify(fromTS));
  assert.deepEqual(fromTSJSON, fromGo);
  return fromGo;
}

function record(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("expected GraphQL object");
  return value as Record<string, unknown>;
}

function cursorFrom(page: unknown): string | null {
  const cursor = record(record(page).pageInfo).endCursor;
  if (cursor !== null && typeof cursor !== "string") throw new Error("expected GraphQL endCursor");
  return cursor;
}

test("Go/TypeScript explorer entity identity, wrappers, roots and lazy SDL", async () => {
  await equalExplorer(`query($c: String!) {
    overview { snapshot queryCount mutationCount subscriptionCount queryRoot { name } }
    element(coordinate: $c) { __typename coordinate sdl ... on SchemaInputValue { required parentField { coordinate } type { signature ofType { signature } } } }
    type(name:"Lead") { kind sdl fields { coordinate sdl isDeprecated } interfaces { name } }
    operation(coordinate:"RootMutation.createLead") { coordinate kind sdl rootField { coordinate } returnType { signature ofType { signature } } }
  }`, { c: "RootQuery.lead(id:)" });
});

test("Go/TypeScript graph edges and paginated paths", async () => {
  const doc = `query($after: String) {
    type(name:"Lead") { outgoing { id kind source { coordinate } target { coordinate } field { coordinate } } incoming { id kind } }
    paths(from:"RootQuery", to:"Lead.email", maxDepth:5, first:1, after:$after) {
      nodes { depth fieldDepth segments { index edge { id kind source { coordinate } target { coordinate } } } }
      pageInfo { hasNextPage endCursor }
    }
  }`;
  const first = await equalExplorer(doc);
  const cursor = cursorFrom(record(record(first).data).paths);
  assert.ok(cursor);
  await equalExplorer(doc, { after: cursor });
});

test("Go/TypeScript independent reachability pages and target validation", async () => {
  const doc = `query($after: String, $inner: String) {
    reachableFields(matching:"email", maxDepth:5, first:1, after:$after) {
      nodes { operation { coordinate } target { coordinate } paths(first:1, after:$inner) { nodes { depth fieldDepth } pageInfo { hasNextPage endCursor } } }
      pageInfo { hasNextPage endCursor }
    }
    validate(document:"{ lead(id: \\"1\\") { contactEmail } }") { valid errors { message } }
  }`;
  const first = await equalExplorer(doc);
  const outer = record(record(first).data).reachableFields;
  const outerCursor = cursorFrom(outer);
  if (outerCursor) await equalExplorer(doc, { after: outerCursor });
  const nodes = record(outer).nodes;
  if (!Array.isArray(nodes)) throw new Error("expected reachability nodes");
  const firstNode = nodes[0];
  if (firstNode) {
    const innerCursor = cursorFrom(record(firstNode).paths);
    if (innerCursor) await equalExplorer(doc, { inner: innerCursor });
  }
});

test("Go/TypeScript explorer validation and resolver errors", async () => {
  await equalExplorer(`{ missing }`);
  await equalExplorer(`{ element(coordinate: }`);
  await equalExplorer(`{ paths(from: "RootQuery", to: "Lead.email", after: "bad") { nodes { depth } } }`);
});

test("Go/TypeScript abstract and input traversal, enum metadata and recursive paths", async () => {
  await equalExplorer(`{
    operations(kind: SUBSCRIPTION) { coordinate kind }
    returnsLead: operations(returns: "Lead") { coordinate }
    wrapperIsNotNamedType: operations(returns: "Lead!") { coordinate }
    acceptsInput: operations(accepts: "CreateLeadInput") { coordinate }
    enumType: type(name: "LeadStatus") { enumValues { coordinate sdl isDeprecated deprecationReason } }
    input: element(coordinate: "CreateLeadInput.email") { __typename coordinate sdl ... on SchemaInputValue { required parentInputType { name } type { signature } } }
    note: element(coordinate: "CreateLeadInput.note") { ... on SchemaInputValue { defaultValue sdl } }
    statuses: element(coordinate: "CreateLeadInput.statuses") { ... on SchemaInputValue { defaultValue sdl } }
    unionPaths: paths(from: "RootQuery.search", to: "Agent.name", maxDepth: 4) {
      nodes { depth fieldDepth segments { edge { kind source { coordinate } target { coordinate } } } }
      pageInfo { hasNextPage endCursor }
    }
    inputPaths: paths(from: "RootMutation.createLead", to: "CreateLeadInput.email", traversal: INPUT, maxDepth: 4) {
      nodes { depth fieldDepth segments { edge { kind } } }
      pageInfo { hasNextPage endCursor }
    }
    recursive: paths(from: "Lead", to: "Lead", maxDepth: 4) {
      nodes { depth fieldDepth }
      pageInfo { hasNextPage endCursor }
    }
  }`);
});

test("introspection-built explorer preserves argument descriptions and Go snapshot", async () => {
  const introspection = graphqlSync({ schema: buildSchema(sdl), source: getIntrospectionQuery({
    descriptions: true, specifiedByUrl: true, directiveIsRepeatable: true,
    schemaDescription: true, inputValueDeprecation: true, oneOf: true,
  }) });
  assert.equal(introspection.errors, undefined);
  assert.ok(introspection.data?.__schema);
  const document = `{ overview { snapshot queryCount typeCount }
    operation(coordinate: "RootQuery.lead") { sdl arguments { coordinate description } }
    type(name: "LeadStatus") { enumValues { coordinate isDeprecated deprecationReason } }
  }`;
  const goResult: unknown = JSON.parse(execFileSync(go!, ["explore", "--schema-file", fixture, document], { cwd: root, encoding: "utf8" }));
  const tsResult: unknown = JSON.parse(JSON.stringify(await SchemaExplorer.fromIntrospection({ __schema: introspection.data.__schema }).explore(document)));
  assert.deepEqual(tsResult, goResult);
});
