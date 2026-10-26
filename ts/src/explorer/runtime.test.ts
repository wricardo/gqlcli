import assert from "node:assert/strict";
import { test } from "node:test";
import { buildSchema, graphql } from "graphql";
import { Gqlcli, SchemaExplorer } from "../index.ts";

const schema = buildSchema(`
  type Query { people(limit: Int! = 5): People! }
  type People { direct: Target indirect: Wrapper }
  type Wrapper { child: Target ids: [String!]! }
  type Target { name: String! label: String }
  input Choice { enabled: Boolean = true }
  enum Role { ADMIN MEMBER }
`);
const explorer = new SchemaExplorer(schema);

interface PathPage {
  paths: { nodes: { depth: number; segments: { index: number }[] }[]; pageInfo: { hasNextPage: boolean; endCursor: string | null } };
}
interface ReachPage {
  reachableFields: {
    nodes: { target: { coordinate: string }; paths: { nodes: { depth: number }[]; pageInfo: { hasNextPage: boolean; endCursor: string | null } } }[];
    pageInfo: { hasNextPage: boolean; endCursor: string | null };
  };
}
interface ValidationPage {
  validate: {
    valid: boolean;
    errors: { locations: { line: number; column: number }[]; suggestions: { replacementField: { coordinate: string } | null; replacementInputValue: { coordinate: string } | null }[] }[];
  };
}

test("explorer supports variables, fragments, operation selection and introspection", async () => {
  const result = await explorer.explore(`
    query Other { overview { mutationCount } }
    query Find($coord: String!) {
      element(coordinate: $coord) { __typename coordinate ... on SchemaField { parentType { name } type { signature ofType { signature } } } }
      operation(coordinate: "Query.people") { ...OperationFields }
      __schema { queryType { name } }
    }
    fragment OperationFields on Operation { kind rootField { name } arguments { name required defaultValue } }
  `, { coord: "Query.people" }, "Find");
  assert.equal(result.errors, undefined);
  assert.deepEqual(JSON.parse(JSON.stringify(result.data)), {
    element: { __typename: "SchemaField", coordinate: "Query.people", parentType: { name: "Query" }, type: { signature: "People!", ofType: { signature: "People" } } },
    operation: { kind: "QUERY", rootField: { name: "people" }, arguments: [{ name: "limit", required: false, defaultValue: "5" }] },
    __schema: { queryType: { name: "Query" } },
  });
});

test("discover operations, kinds, edges, and lazy SDL", async () => {
  const result = await explorer.explore(`{
    overview { queryCount typeCount fieldCount snapshot queryRoot { coordinate } }
    operations(returns: "People") { coordinate sdl reachableTypes(maxDepth: 4) { name } }
    elements(filter: "Target.name", kinds: [FIELD]) { __typename coordinate ... on SchemaField { parentType { name } } }
    type(name: "People") { fields { name } outgoing(kind: DECLARES_FIELD) { id source { coordinate } target { coordinate } field { coordinate } } }
  }`);
  assert.equal(result.errors, undefined);
  const data = result.data as unknown as {
    overview: { queryCount: number; typeCount: number; snapshot: string };
    operations: { coordinate: string; sdl: string; reachableTypes: { name: string }[] }[];
    elements: { coordinate: string }[];
    type: { outgoing: { source: { coordinate: string }; target: { coordinate: string }; field: { coordinate: string } }[] };
  };
  assert.equal(data.overview.queryCount, 1);
  assert.ok(data.overview.typeCount > 4);
  assert.match(data.overview.snapshot, /^[a-f0-9]{64}$/);
  assert.equal(data.operations[0].coordinate, "Query.people");
  assert.match(data.operations[0].sdl, /people\(limit: Int! = 5\): People!/);
  assert.ok(data.operations[0].reachableTypes.some((t: { name: string }) => t.name === "Target"));
  assert.equal(data.elements[0].coordinate, "Target.name");
  assert.equal(data.type.outgoing[0].source.coordinate, "People");
  assert.equal(data.type.outgoing[0].field.coordinate, data.type.outgoing[0].target.coordinate);
});

test("path pages and nested reachability cursors stay independent", async () => {
  const path = `query($after: String) { paths(from: "Query.people", to: "Target.name", maxDepth: 6, first: 1, after: $after) { nodes { depth fieldDepth segments { index edge { kind target { coordinate } } } } pageInfo { hasNextPage endCursor } } }`;
  const first = (await explorer.explore(path)).data as unknown as PathPage;
  assert.equal(first.paths.nodes.length, 1);
  assert.equal(first.paths.pageInfo.hasNextPage, true);
  assert.equal(first.paths.nodes[0].segments[0].index, 0);
  const second = (await explorer.explore(path, { after: first.paths.pageInfo.endCursor })).data as unknown as PathPage;
  assert.notEqual(second.paths.nodes[0].depth, first.paths.nodes[0].depth);
  const reach = `query($outer: String, $inner: String) {
    reachableFields(matching: "Target.", from: "Query.people", maxDepth: 6, first: 1, after: $outer) {
      nodes { target { coordinate } paths(first: 1, after: $inner) { nodes { depth } pageInfo { hasNextPage endCursor } } }
      pageInfo { hasNextPage endCursor }
    }
  }`;
  const page = (await explorer.explore(reach)).data as unknown as ReachPage;
  assert.equal(page.reachableFields.pageInfo.hasNextPage, true);
  const inner = page.reachableFields.nodes[0].paths;
  assert.equal(inner.pageInfo.hasNextPage, true);
  const innerPage = (await explorer.explore(reach, { inner: inner.pageInfo.endCursor })).data as unknown as ReachPage;
  assert.notEqual(innerPage.reachableFields.nodes[0].paths.nodes[0].depth, inner.nodes[0].depth);
  const next = (await explorer.explore(reach, { outer: page.reachableFields.pageInfo.endCursor })).data as unknown as ReachPage;
  assert.notEqual(next.reachableFields.nodes[0].target.coordinate, page.reachableFields.nodes[0].target.coordinate);
  const mismatch = await explorer.explore(`{ reachableFields(matching: "Other", after: "${page.reachableFields.pageInfo.endCursor}") { nodes { target { coordinate } } } }`);
  assert.match(mismatch.errors?.[0]?.message ?? "", /cursor does not match/);
  const changedFirst = await explorer.explore(`{ reachableFields(matching: "Target.", from: "Query.people", maxDepth: 6, first: 2, after: "${page.reachableFields.pageInfo.endCursor}") { nodes { target { coordinate } } } }`);
  assert.match(changedFirst.errors?.[0]?.message ?? "", /cursor does not match/);
  const changedSchema = new SchemaExplorer(buildSchema(`type Query { people: Target } type Target { name: String label: String additional: String }`));
  const stale = await changedSchema.explore(`{ reachableFields(matching: "Target.", maxDepth: 6, first: 1, after: "${page.reachableFields.pageInfo.endCursor}") { nodes { target { coordinate } } } }`);
  assert.match(stale.errors?.[0]?.message ?? "", /stale/);
});

test("validation enriches references and suggestions without executing the target", async () => {
  const response = await explorer.explore(`query($doc: String!) { validate(document: $doc) { valid errors { message coordinate rule locations { line column } field { coordinate } inputValue { coordinate } expectedType { signature } suggestions { replacementField { coordinate } replacementInputValue { coordinate } } } } }`,
    { doc: "{ people(limt: 3) { direct { nmae } } }" });
  assert.equal(response.errors, undefined);
  const responseData = response.data as unknown as ValidationPage;
  const validation = responseData.validate;
  assert.equal(validation.valid, false);
  assert.ok(validation.errors.some((d) => d.suggestions.some((s) => s.replacementField?.coordinate === "Target.name")));
  assert.ok(validation.errors.some((d) => d.suggestions.some((s) => s.replacementInputValue?.coordinate === "Query.people(limit:)")));
  assert.ok(validation.errors.every((d) => d.locations.length));
  const valid = (await explorer.explore(`{ validate(document: "{ people { direct { name } } }") { valid errors { message } } }`)).data as unknown as ValidationPage;
  assert.deepEqual(JSON.parse(JSON.stringify(valid.validate)), { valid: true, errors: [] });
});

test("Gqlcli explorer only fetches introspection, never the explorer document", async () => {
  const queries: string[] = [];
  const gqlcli = new Gqlcli({ url: "http://example.invalid/graphql", config: {}, schemaCache: { ttlMs: 0 }, fetch: async (_url, init) => {
    const query = JSON.parse(String(init?.body)).query as string;
    queries.push(query);
    assert.match(query, /__schema/);
    const result = await graphql({ schema, source: query });
    return new Response(JSON.stringify(result), { headers: { "content-type": "application/json" } });
  } });
  const result = await gqlcli.explore("{ operations { coordinate } }");
  assert.equal(result.errors, undefined);
  const data = result.data as unknown as { operations: { coordinate: string }[] };
  assert.equal(data.operations[0]?.coordinate, "Query.people");
  assert.equal(queries.length, 1);
});

test("refreshSchema rebuilds the explorer graph from the new target schema", async () => {
  let current = buildSchema("type Query { oldField: String }");
  let introspections = 0;
  const gqlcli = new Gqlcli({ url: "http://example.invalid/graphql", config: {}, schemaCache: { ttlMs: 0 }, fetch: async (_url, init) => {
    introspections++;
    const query = JSON.parse(String(init?.body)).query as string;
    const result = await graphql({ schema: current, source: query });
    return new Response(JSON.stringify(result), { headers: { "content-type": "application/json" } });
  } });
  const before = await gqlcli.explore(`{ field(coordinate: "Query.newField") { coordinate } }`);
  assert.equal(JSON.parse(JSON.stringify(before.data)).field, null);
  current = buildSchema("type Query { oldField: String newField: String }");
  gqlcli.refreshSchema();
  const after = await gqlcli.explore(`{ field(coordinate: "Query.newField") { coordinate } }`);
  assert.equal(JSON.parse(JSON.stringify(after.data)).field.coordinate, "Query.newField");
  assert.equal(introspections, 2);
});
