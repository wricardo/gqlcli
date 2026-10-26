import assert from "node:assert/strict";
import { test } from "node:test";
import {
  GraphQLObjectType,
  GraphQLSchema,
  GraphQLString,
  buildSchema,
  isEnumType,
  isInputObjectType,
  isObjectType,
  parse,
  type GraphQLArgument,
  type GraphQLEnumValue,
  type GraphQLField,
  type GraphQLInputField,
  type GraphQLNamedType,
} from "graphql";
import { ExplorerGraph, type ExplorerElement } from "./graph.ts";
import { renderElement } from "./render.ts";

const schema = buildSchema(`
  directive @tag(label: String!) on SCALAR | OBJECT | UNION | ENUM | INPUT_OBJECT | FIELD_DEFINITION
  interface Node { id: ID! }
  "A product\\nwith details"
  type Product implements Node @tag(label: "catalog") {
    id: ID!
    "Look up a product"
    lookup(
      "Status to search"
      status: Status = READY @deprecated(reason: "changed")
      limit: Int = 5
    ): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")
  }
  "Statuses"
  enum Status @tag(label: "state") {
    "Ready now"
    READY @deprecated(reason: "retired")
    WAITING
  }
  input Filter @tag(label: "filter") {
    "Requested status"
    status: Status = READY @deprecated(reason: "use statuses")
    statuses: [Status!] = [READY, WAITING]
  }
  union Result @tag(label: "union") = Product
  scalar Date @specifiedBy(url: "https://example.test/date")
  type Edge {
    "quoted \\"description\\""
    simple(flag: Boolean = false, note: String = "ctrl\\u0001"): Int @deprecated
  }
  type Empty
`);

function element(kind: ExplorerElement["kind"], coordinate: string, value: ExplorerElement["value"]): ExplorerElement {
  return { kind, coordinate, name: coordinate.split(".").at(-1)!, description: "description" in value ? value.description ?? null : null, value };
}
function named(name: string): GraphQLNamedType {
  const type = schema.getType(name);
  assert.ok(type);
  return type;
}
function objectField(type: string, name: string): GraphQLField<unknown, unknown> {
  const parent = named(type);
  assert.ok(isObjectType(parent));
  const field = parent.getFields()[name];
  assert.ok(field);
  return field;
}

const lookup = objectField("Product", "lookup");
const status = named("Status");
assert.ok(isEnumType(status));
const filter = named("Filter");
assert.ok(isInputObjectType(filter));
const statusArg = lookup.args.find((arg) => arg.name === "status");
assert.ok(statusArg);
const statusValue = status.getValues().find((item) => item.name === "READY");
assert.ok(statusValue);
const filterStatus = filter.getFields()["status"];
assert.ok(filterStatus);

const cases: ReadonlyArray<[string, ExplorerElement, string]> = [
  ["type", element("TYPE", "Product", named("Product")), `"A product\\nwith details"
type Product implements Node @tag(label: "catalog") {
  id: ID!
  "Look up a product"
  lookup(
    "Status to search"
    status: Status = READY @deprecated(reason: "changed")
    limit: Int = 5
  ): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")
}`],
  ["field", element("FIELD", "Product.lookup", lookup), `"Look up a product"
lookup(
  "Status to search"
  status: Status = READY @deprecated(reason: "changed")
  limit: Int = 5
): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")`],
  ["argument", element("INPUT_VALUE", "Product.lookup(status:)", statusArg as GraphQLArgument), `"Status to search"
status: Status = READY @deprecated(reason: "changed")`],
  ["input field", element("INPUT_VALUE", "Filter.status", filterStatus as GraphQLInputField), `"Requested status"
status: Status = READY @deprecated(reason: "use statuses")`],
  ["input type", element("TYPE", "Filter", filter), `input Filter @tag(label: "filter") {
  "Requested status"
  status: Status = READY @deprecated(reason: "use statuses")
  statuses: [Status!] = [READY, WAITING]
}`],
  ["enum", element("TYPE", "Status", status), `"Statuses"
enum Status @tag(label: "state") {
  "Ready now"
  READY @deprecated(reason: "retired")
  WAITING
}`],
  ["enum value", element("ENUM_VALUE", "Status.READY", statusValue as GraphQLEnumValue), `"Ready now"
READY @deprecated(reason: "retired")`],
  ["union", element("TYPE", "Result", named("Result")), `union Result @tag(label: "union") = Product`],
  ["scalar", element("TYPE", "Date", named("Date")), `scalar Date @specifiedBy(url: "https://example.test/date")`],
  ["built-in scalar", new ExplorerGraph(schema).elements.get("String")!, `"The \`String\`scalar type represents textual data, represented as UTF-8 character sequences. The String type is most often used by GraphQL to represent free-form human-readable text."\nscalar String`],
  ["empty object", element("TYPE", "Empty", named("Empty")), "type Empty"],
  ["escaped default literals", element("FIELD", "Edge.simple", objectField("Edge", "simple")), `"quoted \\"description\\""
simple(flag: Boolean = false, note: String = "ctrl\\u0001"): Int @deprecated`],
];

for (const [name, target, expected] of cases) {
  test(`Go renderer parity: ${name}`, () => {
    assert.equal(renderElement(target, true), expected);
    assert.equal(renderElement(target, true), expected);
    let document = expected;
    if (target.kind === "FIELD") document = `type Holder {\n${expected}\n}`;
    if (target.kind === "INPUT_VALUE") document = target.coordinate.endsWith(":)") ? `type Holder { f(${expected}): Boolean }` : `input Holder {\n${expected}\n}`;
    if (target.kind === "ENUM_VALUE") document = `enum Holder {\n${expected}\n}`;
    assert.doesNotThrow(() => parse(document));
  });
}

test("SDL omits nested descriptions by default without dropping metadata", () => {
  assert.equal(renderElement(element("TYPE", "Product", named("Product"))), `type Product implements Node @tag(label: "catalog") {
  id: ID!
  lookup(status: Status = READY @deprecated(reason: "changed"), limit: Int = 5): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")
}`);
  assert.equal(renderElement(element("FIELD", "Product.lookup", lookup)), `lookup(status: Status = READY @deprecated(reason: "changed"), limit: Int = 5): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")`);
  assert.equal(renderElement(element("INPUT_VALUE", "Product.lookup(status:)", statusArg as GraphQLArgument)), `status: Status = READY @deprecated(reason: "changed")`);
  assert.equal(renderElement(element("ENUM_VALUE", "Status.READY", statusValue as GraphQLEnumValue)), `READY @deprecated(reason: "retired")`);
});

test("SDL preserves nested literal formatting and Go JSON escapes", () => {
  const type = buildSchema(`type Query { f(a: String = "<>&\\u2028", obj: Settings = {enabled: true, tags: ["one", "two"]}): Boolean } input Settings { enabled: Boolean tags: [String!] }`).getQueryType()!;
  const value = renderElement(element("FIELD", "Query.f", type.getFields()["f"]!), true);
  assert.equal(value, `f(a: String = "\\u003c\\u003e\\u0026\\u2028", obj: Settings = {enabled: true, tags: ["one", "two"]}): Boolean`);
});

test("deprecation and specifiedBy render even when no source AST is available", () => {
  const query = new GraphQLObjectType({ name: "Query", fields: { retired: { type: GraphQLString, deprecationReason: "No longer supported" }, replaced: { type: GraphQLString, deprecationReason: "use other" } } });
  const runtime = new GraphQLSchema({ query });
  assert.equal(renderElement(element("TYPE", "Query", runtime.getQueryType()!)), `type Query {\n  retired: String @deprecated\n  replaced: String @deprecated(reason: "use other")\n}`);
});
