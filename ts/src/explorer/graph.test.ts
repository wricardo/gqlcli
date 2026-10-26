import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { buildSchema, type GraphQLInputField } from "graphql";
import { ExplorerGraph, type ExplorerPath } from "./graph.ts";

// Shared with pkg/explorer/graph/graph_test.go.
const fixture = `
schema { query: Root }
type Root {
  search(id: ID!, filter: Filter): Node
  list: [Node!]!
  direct: Item
  lookup: Search
}
interface Node { id: ID! }
type Item implements Node { id: ID! child: Item status: Status }
type Other implements Node { id: ID! }
union Search = Item | Other
input Filter { next: Filter tags: [String!] }
enum Status { ACTIVE INACTIVE }
`;

const graph = new ExplorerGraph(buildSchema(fixture));
const ids = (path: ExplorerPath): string[] => path.edges.map((edge) => edge.id);

describe("canonical schema graph", () => {
  test("includes declarations, built-ins, stable edges, and an order-sensitive snapshot", () => {
    assert.equal(graph.snapshot, new ExplorerGraph(buildSchema(fixture)).snapshot);
    for (const coordinate of ["Root", "Root.search", "Root.search(id:)", "Root.search(filter:)", "Filter.next", "Status.ACTIVE", "String", "Int", "ID", "Boolean", "Float"]) {
      assert.ok(graph.elements.has(coordinate), coordinate);
    }
    assert.ok([...graph.elements.keys()].every((coordinate) => !coordinate.startsWith("__") && !coordinate.includes(".__")));
    const input = graph.elements.get("Filter.next")!;
    assert.equal(input.kind, "INPUT_VALUE");
    assert.equal(input.parent?.name, "Filter");
    assert.equal((input.value as GraphQLInputField).type.toString(), "Filter");
    assert.equal(graph.elements.get("Root.search(id:)")?.parentField?.name, "search");

    const edges: Array<[string, string, string, string]> = [
      ["Root", "Root.search", "DECLARES_FIELD", "Root.search|"],
      ["Root.search", "Node", "RETURNS", "Root.search|"],
      ["Root.search", "Root.search(filter:)", "DECLARES_INPUT_VALUE", "Root.search|Root.search(filter:)"],
      ["Root.search(filter:)", "Filter", "ACCEPTS", "Root.search|Root.search(filter:)"],
      ["Filter", "Filter.next", "DECLARES_INPUT_VALUE", "|Filter.next"],
      ["Filter.next", "Filter", "ACCEPTS", "|Filter.next"],
      ["Status", "Status.ACTIVE", "DECLARES_ENUM_VALUE", "|"],
      ["Item", "Node", "IMPLEMENTS", "|"],
      ["Node", "Item", "POSSIBLE_TYPE", "|"],
      ["Search", "Item", "UNION_MEMBER", "|"],
    ];
    for (const [from, to, kind, tail] of edges) {
      const expectedID = `${kind}|${from}|${to}|${tail}`;
      const edge = graph.outgoing.get(from)?.find((candidate) => candidate.id === expectedID);
      assert.ok(edge, expectedID);
      assert.equal(edge.target, graph.elements.get(to));
      assert.ok(graph.incoming.get(to)?.includes(edge));
    }
    for (const outgoing of graph.outgoing.values()) {
      const sorted = outgoing.map((edge) => edge.id).sort();
      assert.deepEqual(outgoing.map((edge) => edge.id), sorted);
      assert.equal(new Set(sorted).size, sorted.length);
    }
    assert.notEqual(graph.snapshot, new ExplorerGraph(buildSchema(fixture.replace("status: Status", "status: Status!"))).snapshot);
    assert.notEqual(graph.snapshot, new ExplorerGraph(buildSchema(fixture.replace("ACTIVE INACTIVE", "INACTIVE ACTIVE"))).snapshot);
    assert.notEqual(graph.snapshot, new ExplorerGraph(buildSchema(fixture.replace("status: Status", 'status: Status @deprecated(reason: "old")'))).snapshot);
    assert.notEqual(graph.snapshot, new ExplorerGraph(buildSchema(fixture.replace("filter: Filter", "filter: Filter = {tags: [\"x\"]}"))).snapshot);
    assert.notEqual(graph.snapshot, new ExplorerGraph(buildSchema(fixture.replace("schema { query: Root }", '\"A schema\" schema { query: Root }'))).snapshot);
  });

  test("enumerates exact simple paths through abstract outputs, inputs, and self", () => {
    const output = graph.paths("Root.search", "Item.status", "OUTPUT", 4, 100).nodes;
    assert.equal(output.length, 1);
    assert.equal(output[0]?.depth, 3);
    assert.equal(output[0]?.fieldDepth, 2);
    assert.deepEqual(output[0]?.edges.map((edge) => edge.kind), ["RETURNS", "POSSIBLE_TYPE", "DECLARES_FIELD"]);
    const union = graph.paths("Root.lookup", "Item.status", "OUTPUT", 4, 100).nodes;
    assert.equal(union.length, 1);
    assert.deepEqual(union[0]?.edges.map((edge) => edge.kind), ["RETURNS", "UNION_MEMBER", "DECLARES_FIELD"]);
    assert.deepEqual(graph.paths("Root.search", "Root.search(id:)", "OUTPUT", 4, 100).nodes, []);
    const input = graph.paths("Root.search", "Filter.next", "INPUT", 4, 100).nodes;
    assert.equal(input.length, 1);
    assert.equal(input[0]?.depth, 3);
    assert.equal(input[0]?.fieldDepth, 0);
    assert.deepEqual(graph.paths("Root.search", "Filter.next", "OUTPUT", 4, 100).nodes, []);
    assert.equal(graph.paths("Root.search", "Filter.next", "ALL", 4, 100).nodes.length, 1);
    assert.deepEqual(graph.paths("Root.search", "Item.status", "OUTPUT", 2, 100).nodes, []);
    assert.deepEqual(graph.paths("Root.direct", "Other", "OUTPUT", 5, 100).nodes, []);
    assert.equal(graph.paths("Item", "Node", "ALL", 1, 100).nodes[0]?.edges[0]?.kind, "IMPLEMENTS");
    const self = graph.paths("Root.search", "Root.search", "OUTPUT", 0, 100);
    assert.equal(self.nodes[0]?.depth, 0);
    assert.equal(self.nodes[0]?.fieldDepth, 0);
    assert.deepEqual(self.nodes[0]?.edges, []);
    assert.equal(self.pageInfo.hasNextPage, false);
  });

  test("indexes inherited interfaces as selectable abstract outputs once", () => {
    const inherited = new ExplorerGraph(buildSchema(`
      interface Node { id: ID! }
      interface Named implements Node { id: ID! name: String }
      type Item implements Named & Node { id: ID! name: String }
      type Query { item: Named }
    `));
    assert.equal(inherited.outgoing.get("Node")?.filter((edge) => edge.kind === "POSSIBLE_TYPE" && edge.target.coordinate === "Item").length, 1);
    assert.equal(inherited.paths("Query.item", "Item.name", "OUTPUT", 4, 100).nodes.length, 1);
  });

  test("paginates every sorted path with Go-compatible bound cursors", () => {
    const all = graph.paths("Root", "Item", "OUTPUT", 9, 100).nodes;
    assert.equal(all.length, 4);
    const ordered = all.map((path) => [path.depth, ids(path)]);
    assert.deepEqual(ordered, [...ordered].sort((a, b) => {
      if (a[0] !== b[0]) return (a[0] as number) - (b[0] as number);
      const left = a[1] as string[];
      const right = b[1] as string[];
      for (let i = 0; i < left.length; i++) {
        if (left[i]! < right[i]!) return -1;
        if (left[i]! > right[i]!) return 1;
      }
      return 0;
    }));
    const paged: ExplorerPath[] = [];
    let after: string | undefined;
    for (;;) {
      const page = graph.paths("Root", "Item", "OUTPUT", 9, 1, after);
      assert.equal(page.nodes.length, 1);
      paged.push(page.nodes[0]!);
      assert.ok(page.pageInfo.endCursor);
      const data = JSON.parse(Buffer.from(page.pageInfo.endCursor!, "base64url").toString("utf8")) as Record<string, unknown>;
      assert.deepEqual(Object.keys(data), ["v", "s", "f", "t", "r", "d", "n", "e"]);
      assert.deepEqual(data, { v: 1, s: graph.snapshot, f: "Root", t: "Item", r: "OUTPUT", d: 9, n: 1, e: ids(page.nodes[0]!) });
      if (!page.pageInfo.hasNextPage) break;
      after = page.pageInfo.endCursor;
    }
    assert.deepEqual(paged.map(ids), all.map(ids));
    const firstCursor = graph.paths("Root", "Item", "OUTPUT", 9, 1).pageInfo.endCursor!;
    assert.throws(() => graph.paths("Root.search", "Item", "OUTPUT", 9, 1, firstCursor), /query parameters/);
    assert.throws(() => graph.paths("Root", "Other", "OUTPUT", 9, 1, firstCursor), /query parameters/);
    assert.throws(() => graph.paths("Root", "Item", "ALL", 9, 1, firstCursor), /query parameters/);
    assert.throws(() => graph.paths("Root", "Item", "OUTPUT", 8, 1, firstCursor), /query parameters/);
    assert.throws(() => graph.paths("Root", "Item", "OUTPUT", 9, 2, firstCursor), /query parameters/);
    assert.throws(() => new ExplorerGraph(buildSchema(fixture.replace("status: Status", "status: Status!"))).paths("Root", "Item", "OUTPUT", 9, 1, firstCursor), /stale/);
    assert.throws(() => graph.paths("Root", "Item", "OUTPUT", 9, 1, "invalid"), /invalid path cursor/);
    const fake = Buffer.from(JSON.stringify({ ...JSON.parse(Buffer.from(firstCursor, "base64url").toString("utf8")), e: ["nonexistent"] })).toString("base64url");
    assert.throws(() => graph.paths("Root", "Item", "OUTPUT", 9, 1, fake), /path not found/);
    assert.throws(() => graph.paths("Root", "Item", "INVALID" as "OUTPUT", 9, 1), /invalid path traversal/);
    assert.throws(() => graph.paths("Root", "Item", "OUTPUT", -1, 1), /maxDepth/);
    assert.throws(() => graph.paths("Root", "Item", "OUTPUT", 9, 0), /first/);
    assert.throws(() => graph.paths("missing", "Item", "OUTPUT", 9, 1), /unknown path endpoint/);
  });

  test("returns an error rather than truncating over 100,000 simple paths", () => {
    // Each layer has two choices, and all 2^17 routes eventually converge on End.
    const layers = Array.from({ length: 17 }, (_, layer) => `type A${layer} { a: A${layer + 1} b: B${layer + 1} } type B${layer} { a: A${layer + 1} b: B${layer + 1} }`).join("\n");
    const schema = buildSchema(`type Query { start: A0 } ${layers} type A17 { end: End } type B17 { end: End } type End { id: ID }`);
    const dense = new ExplorerGraph(schema);
    assert.throws(() => dense.paths("Query.start", "End", "OUTPUT", 60, 1), /resource limit exceeded/);
  });
});
