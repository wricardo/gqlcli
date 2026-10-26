import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { buildSchema, getIntrospectionQuery, graphqlSync, parse, validate } from "graphql";
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

test("Go/TypeScript SDL description option across entity kinds and aliases", async () => {
  await equalExplorer(`query($docs: Boolean!) {
    operation(coordinate:"RootQuery.lead") { compact:sdl described:sdl(includeDescription:$docs) }
    type(name:"Person") { compact:sdl described:sdl(includeDescription:$docs) }
    arg: element(coordinate:"RootQuery.lead(id:)") { compact:sdl described:sdl(includeDescription:$docs) }
    value: element(coordinate:"LeadStatus.ARCHIVED") { sdl(includeDescription:$docs) }
  }`, { docs: true });
  await equalExplorer(`{
    operation(coordinate:"RootQuery.lead") { compact:sdl explicit:sdl(includeDescription:false) }
    type(name:"CreateLeadInput") { sdl }
  }`);
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

test("Go/TypeScript filtered reachability binds cursors to operation kind and all query parameters", async () => {
  const doc = `query($kind: OperationKind, $after: String) {
    reachableFields(matching:"email", kind:$kind, maxDepth:5, first:1, after:$after) {
      nodes { operation { coordinate kind } target { coordinate } paths(first:1) {
        nodes { coordinates edgeKinds suggestedDocument } pageInfo { hasNextPage endCursor }
      } }
      pageInfo { hasNextPage endCursor }
    }
  }`;
  for (const kind of ["QUERY", "MUTATION", "SUBSCRIPTION"] as const) {
    const first = record(record((await equalExplorer(doc, { kind })).data).reachableFields);
    const nodes = first.nodes as { operation: { kind: string } }[];
    assert.ok(nodes.length, `expected ${kind} reachability`);
    assert.ok(nodes.every((node) => node.operation.kind === kind));
    const cursor = cursorFrom(first);
    assert.ok(cursor);
    const decoded = JSON.parse(Buffer.from(cursor, "base64url").toString("utf8"));
    assert.equal(decoded.Kind, kind);
    assert.deepEqual(Object.keys(decoded), ["Snapshot", "From", "Matching", "Kind", "Depth", "Position", "First"]);
    await equalExplorer(doc, { kind, after: cursor });
    const wrongKind = kind === "QUERY" ? "MUTATION" : "QUERY";
    const mismatch = record(await equalExplorer(doc, { kind: wrongKind, after: cursor }));
    assert.match(JSON.stringify(mismatch.errors), /reachability cursor does not match this query/);
  }
  const unfiltered = record(record((await equalExplorer(doc)).data).reachableFields);
  const unfilteredCursor = cursorFrom(unfiltered);
  assert.ok(unfilteredCursor);
  assert.equal(JSON.parse(Buffer.from(unfilteredCursor, "base64url").toString("utf8")).Kind, "");
  const mismatch = record(await equalExplorer(doc, { kind: "QUERY", after: unfilteredCursor }));
  assert.match(JSON.stringify(mismatch.errors), /reachability cursor does not match this query/);
});

test("Go/TypeScript operation shapes include direct declarations in argument order without duplicates", async () => {
  const result = record(record(await equalExplorer(`{
    operations { coordinate sdl shape }
    fromType: type(name:"CreateLeadInput") { sdl }
    fromEnum: type(name:"LeadStatus") { sdl }
    fromOutput: type(name:"Lead") { sdl }
  }`)).data);
  const operations = result.operations as { coordinate: string; sdl: string; shape: string }[];
  const byCoordinate = Object.fromEntries(operations.map((operation) => [operation.coordinate, operation]));
  assert.equal(byCoordinate["RootMutation.createLead"]?.shape,
    [byCoordinate["RootMutation.createLead"]?.sdl, record(result.fromType).sdl, record(result.fromOutput).sdl].join("\n"));
  assert.equal(byCoordinate["RootQuery.leads"]?.shape,
    [byCoordinate["RootQuery.leads"]?.sdl, record(result.fromEnum).sdl, record(result.fromOutput).sdl].join("\n"));
  assert.equal(byCoordinate["RootQuery.lead"]?.shape,
    [byCoordinate["RootQuery.lead"]?.sdl, record(result.fromOutput).sdl].join("\n"));
});

test("Go/TypeScript compact paths and executable documents handle root types, fragments, terminals and unsupported edges", async () => {
  const cases = [
    { from: "RootQuery", to: "Lead.contactEmail", document: "query($v0_id: ID!) { lead(id: $v0_id) { contactEmail } }" },
    { from: "RootQuery.lead", to: "RootQuery.lead", document: "query($v0_id: ID!) { lead(id: $v0_id) { __typename } }" },
    { from: "RootQuery.search", to: "Agent.name", document: "query { search { ... on Agent { name } } }" },
    { from: "RootQuery.lead", to: "Lead.manager", document: "query($v0_id: ID!) { lead(id: $v0_id) { manager { __typename } } }" },
    { from: "RootQuery.leads", to: "Lead.status", document: "query { leads { status } }" },
    { from: "RootMutation.createLead", to: "Lead.contactEmail", document: "mutation($v0_input: CreateLeadInput!) { createLead(input: $v0_input) { contactEmail } }" },
    { from: "RootSubscription.leadUpdated", to: "Lead.contactEmail", document: "subscription($v0_id: ID!) { leadUpdated(id: $v0_id) { contactEmail } }" },
    { from: "RootQuery.lead", to: "RootQuery.lead(id:)", traversal: "INPUT", document: null },
    { from: "RootQuery.lead", to: "Lead", document: null },
    { from: "Lead.contactEmail", to: "Lead.contactEmail", document: null },
  ] as const;
  const target = buildSchema(sdl);
  for (const { from, to, document, ...options } of cases) {
    const traversal = "traversal" in options ? `, traversal:${options.traversal}` : "";
    const response = record(record(await equalExplorer(`{ paths(from:${JSON.stringify(from)}, to:${JSON.stringify(to)}${traversal}, maxDepth:8) {
      nodes { from { coordinate } to { coordinate } depth coordinates edgeKinds suggestedDocument
        segments { edge { kind source { coordinate } target { coordinate } } } }
      pageInfo { hasNextPage endCursor }
    } }`)).data);
    const paths = record(response.paths).nodes as {
      from: { coordinate: string }; to: { coordinate: string }; depth: number;
      coordinates: string[]; edgeKinds: string[]; suggestedDocument: string | null;
      segments: { edge: { kind: string; source: { coordinate: string }; target: { coordinate: string } } }[];
    }[];
    assert.ok(paths.length, `${from} -> ${to}`);
    for (const path of paths) {
      assert.deepEqual(path.coordinates, [from, ...path.segments.map((segment) => segment.edge.target.coordinate)]);
      assert.deepEqual(path.edgeKinds, path.segments.map((segment) => segment.edge.kind));
      assert.equal(path.coordinates.at(-1), to);
      assert.equal(path.depth, path.edgeKinds.length);
      if (path.suggestedDocument !== null) assert.deepEqual(validate(target, parse(path.suggestedDocument)), []);
    }
    if (from === "RootQuery") {
      assert.ok(paths.some((path) => path.suggestedDocument === document), "root query paths should include the lead selection");
    } else {
      assert.equal(paths[0]!.suggestedDocument, document);
    }
  }
});

test("Go/TypeScript custom-root suggestions skip defaulted args, select abstract members and deduplicate shape types", async () => {
  const customSDL = `schema { query: LookupQuery mutation: RootMutation subscription: RootSubscription }
    type LookupQuery {
      lead(id: ID!, optional: Int, page: Int! = 3): Lead!
      search(term: String!): Search!
      lookup: Result!
      multi(first: Filter!, second: Filter!): Agent!
    }
    type RootMutation { setLead(id: ID!): Lead! }
    type RootSubscription { watch: Lead! }
    input Filter { term: String! }
    type Lead { contactEmail: String! agent(id: ID!): Agent! }
    interface Result { name: String! }
    type Agent implements Result { name: String! rank: Int! }
    union Search = Agent
  `;
  const schemaFile = join(work, "suggestion.graphql");
  writeFileSync(schemaFile, customSDL);
  const document = `{
    lead: paths(from:"LookupQuery.lead", to:"Lead.contactEmail") { nodes { suggestedDocument coordinates edgeKinds } }
    root: paths(from:"LookupQuery", to:"Lead.contactEmail") { nodes { suggestedDocument } }
    nested: paths(from:"LookupQuery.lead", to:"Agent.rank") { nodes { suggestedDocument } }
    union: paths(from:"LookupQuery.search", to:"Agent.name") { nodes { suggestedDocument } }
    interface: paths(from:"LookupQuery.lookup", to:"Agent.rank") { nodes { suggestedDocument } }
    mutation: paths(from:"RootMutation.setLead", to:"Lead.contactEmail") { nodes { suggestedDocument } }
    subscription: paths(from:"RootSubscription.watch", to:"Lead.contactEmail") { nodes { suggestedDocument } }
    filterShape: operation(coordinate:"LookupQuery.multi") { sdl shape }
    multi: paths(from:"LookupQuery.multi", to:"Agent.rank") { nodes { suggestedDocument } }
  }`;
  const fromGo: unknown = JSON.parse(execFileSync(go!, ["explore", "--schema-file", schemaFile, "--format", "json", "--strict=false", document], { cwd: root, encoding: "utf8" }));
  const fromTS: unknown = JSON.parse(JSON.stringify(await SchemaExplorer.fromSDL(customSDL).explore(document)));
  assert.deepEqual(fromTS, fromGo);
  const result = record(record(fromGo).data);
  const suggestion = (key: string) => (record(result[key]).nodes as { suggestedDocument: string | null }[])[0]?.suggestedDocument;
  assert.equal(suggestion("lead"), "query($v0_id: ID!) { lead(id: $v0_id) { contactEmail } }");
  assert.equal(suggestion("root"), suggestion("lead"));
  assert.equal(suggestion("nested"), "query($v0_id: ID!, $v1_id: ID!) { lead(id: $v0_id) { agent(id: $v1_id) { rank } } }");
  assert.equal(suggestion("union"), "query($v0_term: String!) { search(term: $v0_term) { ... on Agent { name } } }");
  assert.equal(suggestion("interface"), "query { lookup { ... on Agent { rank } } }");
  assert.equal(suggestion("mutation"), "mutation($v0_id: ID!) { setLead(id: $v0_id) { contactEmail } }");
  assert.equal(suggestion("subscription"), "subscription { watch { contactEmail } }");
  assert.equal(suggestion("multi"), "query($v0_first: Filter!, $v0_second: Filter!) { multi(first: $v0_first, second: $v0_second) { rank } }");
  const shape = record(result.filterShape);
  assert.equal(shape.shape, `${shape.sdl}\ninput Filter {\n  term: String!\n}\ntype Agent implements Result {\n  name: String!\n  rank: Int!\n}`);
});
