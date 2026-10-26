import { createHash } from "node:crypto";
import {
  GraphQLBoolean,
  GraphQLFloat,
  GraphQLID,
  GraphQLInt,
  GraphQLString,
  getNamedType,
  isEnumType,
  isInputObjectType,
  isInterfaceType,
  isObjectType,
  isScalarType,
  isUnionType,
  valueToLiteral,
  type GraphQLArgument,
  type GraphQLEnumValue,
  type GraphQLField,
  type GraphQLInputField,
  type GraphQLNamedType,
  type GraphQLSchema,
  type GraphQLType,
  type ValueNode,
} from "graphql";

export interface ExplorerElement {
  coordinate: string;
  name: string;
  description: string | null;
  kind: "TYPE" | "FIELD" | "INPUT_VALUE" | "ENUM_VALUE";
  value: GraphQLNamedType | GraphQLField<unknown, unknown> | GraphQLArgument | GraphQLInputField | GraphQLEnumValue;
  parent?: GraphQLNamedType;
  parentField?: GraphQLField<unknown, unknown>;
}

export interface ExplorerEdge {
  id: string;
  kind: string;
  source: ExplorerElement;
  target: ExplorerElement;
  field?: ExplorerElement;
  inputValue?: ExplorerElement;
}

export interface ExplorerPath {
  from: ExplorerElement;
  to: ExplorerElement;
  edges: ExplorerEdge[];
  depth: number;
  fieldDepth: number;
}

export interface PathConnection {
  nodes: ExplorerPath[];
  pageInfo: { hasNextPage: boolean; endCursor: string | null };
}

type Traversal = "OUTPUT" | "INPUT" | "ALL";
type Directive = { readonly name: { readonly value: string }; readonly arguments?: ReadonlyArray<{ readonly name: { readonly value: string }; readonly value: ValueNode }> };

const MAX_EXPLORED_STATES = 1_000_000;
const MAX_ENUMERATED_PATHS = 100_000;
const SCALARS = [GraphQLString, GraphQLInt, GraphQLFloat, GraphQLBoolean, GraphQLID];
// gqlparser's prelude is included by LoadSchema even when the SDL doesn't refer to these scalars.
const PRELUDE_DESCRIPTIONS: Record<string, string> = {
  Int: "The `Int` scalar type represents non-fractional signed whole numeric values. Int can represent values between -(2^31) and 2^31 - 1.",
  Float: "The `Float` scalar type represents signed double-precision fractional values as specified by [IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point).",
  String: "The `String`scalar type represents textual data, represented as UTF-8 character sequences. The String type is most often used by GraphQL to represent free-form human-readable text.",
  Boolean: "The `Boolean` scalar type represents `true` or `false`.",
  ID: 'The `ID` scalar type represents a unique identifier, often used to refetch an object or as key for a cache. The ID type appears in a JSON response as a String; however, it is not intended to be human-readable. When expected as an input type, any string (such as "4") or integer (such as 4) input value will be accepted as an ID.',
};

// Go's strconv.Quote (also used by fmt %q) differs from JSON.stringify for control runes.
function goQuote(text: string): string {
  return `"${text.replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029"\\]/g, (char) => {
    switch (char) {
      case '"': return '\\"';
      case "\\": return "\\\\";
      case "\u0007": return "\\a";
      case "\b": return "\\b";
      case "\f": return "\\f";
      case "\n": return "\\n";
      case "\r": return "\\r";
      case "\t": return "\\t";
      case "\v": return "\\v";
    }
    const rune = char.charCodeAt(0);
    return rune < 128 ? `\\x${rune.toString(16).padStart(2, "0")}` : `\\u${rune.toString(16).padStart(4, "0")}`;
  })}"`;
}

// ast.Value.String() omits spaces inside lists/objects and quotes both normal and block strings.
function valueString(value: ValueNode | undefined): string {
  if (!value) return "";
  switch (value.kind) {
    case "StringValue": return goQuote(value.value);
    case "ListValue": return `[${value.values.map(valueString).join(",")}]`;
    case "ObjectValue": return `{${value.fields.map((field) => `${field.name.value}:${valueString(field.value)}`).join(",")}}`;
    case "Variable": return `$${value.name.value}`;
    case "NullValue": return "null";
    case "BooleanValue": return value.value ? "true" : "false";
    default: return value.value;
  }
}

function directives(nodes: ReadonlyArray<Directive> | undefined): string {
  return (nodes ?? []).map((directive) =>
    goQuote(directive.name.value) + (directive.arguments ?? []).map((arg) => goQuote(arg.name.value) + goQuote(valueString(arg.value))).join(""),
  ).join("");
}

// Go's encoding/json escapes HTML characters and the two Unicode line separators.
function goJSON(values: string[]): string {
  return JSON.stringify(values).replace(/[<>&\u2028\u2029]/g, (char) => {
    switch (char) {
      case "<": return "\\u003c";
      case ">": return "\\u003e";
      case "&": return "\\u0026";
      case "\u2028": return "\\u2028";
      default: return "\\u2029";
    }
  }) + "\n";
}

function kindOf(type: GraphQLNamedType): string {
  if (isObjectType(type)) return "OBJECT";
  if (isInterfaceType(type)) return "INTERFACE";
  if (isInputObjectType(type)) return "INPUT_OBJECT";
  if (isUnionType(type)) return "UNION";
  if (isEnumType(type)) return "ENUM";
  if (isScalarType(type)) return "SCALAR";
  throw new Error("unsupported GraphQL type kind");
}

function snapshot(schema: GraphQLSchema, types: GraphQLNamedType[]): string {
  const hash = createHash("sha256");
  const encode = (parts: string[]) => hash.update(goJSON(parts));
  const schemaNodes = [schema.astNode, ...(schema.extensionASTNodes ?? [])];
  encode([schema.getQueryType()?.name ?? "", schema.getMutationType()?.name ?? "", schema.getSubscriptionType()?.name ?? "", schema.description ?? "", directives(schemaNodes.flatMap((node) => node?.directives ?? []))]);
  for (const type of types) {
    const astNodes = [type.astNode, ...(type.extensionASTNodes ?? [])];
    encode([type.name, kindOf(type), PRELUDE_DESCRIPTIONS[type.name] ?? type.description ?? "", directives(astNodes.flatMap((node) => node?.directives ?? []))]);
    if (isObjectType(type) || isInterfaceType(type)) {
      for (const iface of type.getInterfaces()) encode(["interface", iface.name]);
    }
    if (isUnionType(type)) {
      for (const member of type.getTypes()) encode(["member", member.name]);
    }
    if (isObjectType(type) || isInterfaceType(type) || isInputObjectType(type)) {
      for (const field of Object.values(type.getFields())) {
        if (field.name.startsWith("__")) continue;
        const node = field.astNode;
        const inputField = isInputObjectType(type) ? field as GraphQLInputField : undefined;
        const defaultValue = inputField?.astNode?.defaultValue ?? (inputField?.defaultValue === undefined ? undefined : valueToLiteral(inputField.defaultValue, inputField.type));
        encode(["field", field.name, field.description ?? "", String(field.type), valueString(defaultValue), directives(node?.directives)]);
        if (isObjectType(type) || isInterfaceType(type)) {
          for (const arg of (field as GraphQLField<unknown, unknown>).args) {
            const argNode = arg.astNode;
            encode(["arg", arg.name, arg.description ?? "", String(arg.type), valueString(argNode?.defaultValue ?? (arg.defaultValue === undefined ? undefined : valueToLiteral(arg.defaultValue, arg.type))), directives(argNode?.directives)]);
          }
        }
      }
    }
    if (isEnumType(type)) {
      for (const value of type.getValues()) encode(["enum", value.name, value.description ?? "", directives(value.astNode?.directives)]);
    }
  }
  return hash.digest("hex");
}

interface PathCursor {
  v: number;
  s: string;
  f: string;
  t: string;
  r: Traversal;
  d: number;
  n: number;
  e: string[];
}

function compareEdges(a: readonly ExplorerEdge[], b: readonly string[]): number {
  for (let i = 0; i < a.length; i++) {
    const id = a[i]!.id;
    if (id < b[i]!) return -1;
    if (id > b[i]!) return 1;
  }
  return 0;
}

function isAllowed(kind: string, traversal: Traversal): boolean {
  if (traversal === "ALL") return true;
  if (traversal === "INPUT") return kind === "DECLARES_INPUT_VALUE" || kind === "ACCEPTS";
  // IMPLEMENTS must not let a concrete object turn into another implementor.
  return kind === "DECLARES_FIELD" || kind === "DECLARES_ENUM_VALUE" || kind === "RETURNS" || kind === "UNION_MEMBER" || kind === "POSSIBLE_TYPE";
}

export class ExplorerGraph {
  readonly snapshot: string;
  readonly elements = new Map<string, ExplorerElement>();
  readonly outgoing = new Map<string, ExplorerEdge[]>();
  readonly incoming = new Map<string, ExplorerEdge[]>();
  private readonly edgeIDs = new Set<string>();

  constructor(schema: GraphQLSchema) {
    const types = [...Object.values(schema.getTypeMap()).filter((type) => !type.name.startsWith("__"))];
    for (const scalar of SCALARS) {
      if (!types.some((type) => type.name === scalar.name)) types.push(scalar);
    }
    types.sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
    for (const type of types) {
      this.add({ coordinate: type.name, name: type.name, kind: "TYPE", description: PRELUDE_DESCRIPTIONS[type.name] ?? type.description ?? null, value: type });
    }
    for (const type of types) {
      const owner = this.elements.get(type.name)!;
      if (isObjectType(type) || isInterfaceType(type)) {
        for (const field of Object.values(type.getFields())) {
          if (field.name.startsWith("__")) continue;
          const element = this.add({ coordinate: `${type.name}.${field.name}`, name: field.name, description: field.description ?? null, kind: "FIELD", value: field, parent: type });
          this.link("DECLARES_FIELD", owner, element, element);
          this.reference("RETURNS", element, field.type, element);
          for (const arg of field.args) {
            const input = this.add({ coordinate: `${element.coordinate}(${arg.name}:)`, name: arg.name, description: arg.description ?? null, kind: "INPUT_VALUE", value: arg, parent: type, parentField: field });
            this.link("DECLARES_INPUT_VALUE", element, input, element, input);
            this.reference("ACCEPTS", input, arg.type, element, input);
          }
        }
      } else if (isInputObjectType(type)) {
        for (const field of Object.values(type.getFields())) {
          const input = this.add({ coordinate: `${type.name}.${field.name}`, name: field.name, description: field.description ?? null, kind: "INPUT_VALUE", value: field, parent: type });
          this.link("DECLARES_INPUT_VALUE", owner, input, undefined, input);
          this.reference("ACCEPTS", input, field.type, undefined, input);
        }
      } else if (isEnumType(type)) {
        for (const value of type.getValues()) {
          const element = this.add({ coordinate: `${type.name}.${value.name}`, name: value.name, description: value.description ?? null, kind: "ENUM_VALUE", value, parent: type });
          this.link("DECLARES_ENUM_VALUE", owner, element);
        }
      }
    }
    for (const type of types) {
      const source = this.elements.get(type.name)!;
      if (isObjectType(type) || isInterfaceType(type)) {
        for (const iface of type.getInterfaces()) this.link("IMPLEMENTS", source, this.element(iface.name));
      }
      if (isUnionType(type)) {
        for (const member of type.getTypes()) this.link("UNION_MEMBER", source, this.element(member.name));
      }
    }
    // Include interface inheritance regardless of whether possible types are precomputed.
    for (const type of types) {
      if (!isObjectType(type)) continue;
      const seen = new Set<string>();
      const visit = (current: GraphQLNamedType): void => {
        if (!isObjectType(current) && !isInterfaceType(current)) return;
        for (const iface of current.getInterfaces()) {
          if (seen.has(iface.name)) continue;
          seen.add(iface.name);
          this.link("POSSIBLE_TYPE", this.element(iface.name), this.element(type.name));
          visit(iface);
        }
      };
      visit(type);
    }
    for (const edges of this.outgoing.values()) edges.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
    for (const edges of this.incoming.values()) edges.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
    this.snapshot = snapshot(schema, types);
  }

  private add(element: ExplorerElement): ExplorerElement {
    if (this.elements.has(element.coordinate)) throw new Error(`duplicate schema coordinate ${JSON.stringify(element.coordinate)}`);
    this.elements.set(element.coordinate, element);
    return element;
  }

  private element(coordinate: string): ExplorerElement {
    const element = this.elements.get(coordinate);
    if (!element) throw new Error(`unknown type ${coordinate}`);
    return element;
  }

  private reference(kind: string, source: ExplorerElement, type: GraphQLType, field?: ExplorerElement, inputValue?: ExplorerElement): void {
    const named = getNamedType(type);
    if (named.name.startsWith("__")) return;
    this.link(kind, source, this.element(named.name), field, inputValue);
  }

  private link(kind: string, source: ExplorerElement, target: ExplorerElement, field?: ExplorerElement, inputValue?: ExplorerElement): void {
    const id = [kind, source.coordinate, target.coordinate, field?.coordinate ?? "", inputValue?.coordinate ?? ""].join("|");
    if (this.edgeIDs.has(id)) return;
    this.edgeIDs.add(id);
    const edge: ExplorerEdge = { id, kind, source, target, field, inputValue };
    const outgoing = this.outgoing.get(source.coordinate);
    if (outgoing) outgoing.push(edge);
    else this.outgoing.set(source.coordinate, [edge]);
    const incoming = this.incoming.get(target.coordinate);
    if (incoming) incoming.push(edge);
    else this.incoming.set(target.coordinate, [edge]);
  }

  paths(from: string, to: string, traversal: Traversal, maxDepth: number, first: number, after = ""): PathConnection {
    if (traversal !== "OUTPUT" && traversal !== "INPUT" && traversal !== "ALL") throw new Error(`invalid path traversal ${JSON.stringify(traversal)}`);
    if (!Number.isSafeInteger(maxDepth) || !Number.isSafeInteger(first) || maxDepth < 0 || first <= 0) throw new Error("maxDepth must be nonnegative and first must be positive");
    const start = this.elements.get(from);
    const end = this.elements.get(to);
    if (!start || !end) throw new Error(`unknown path endpoint ${JSON.stringify(from)} or ${JSON.stringify(to)}`);
    let cursor: PathCursor | undefined;
    if (after) {
      try {
        if (!/^[A-Za-z0-9_-]+$/.test(after)) throw new Error("bad base64url");
        const bytes = Buffer.from(after, "base64url");
        if (bytes.toString("base64url") !== after) throw new Error("bad base64url");
        cursor = JSON.parse(bytes.toString("utf8")) as PathCursor;
        if (!cursor || cursor.v !== 1 || typeof cursor.s !== "string") throw new Error("bad cursor");
      } catch {
        throw new Error("invalid path cursor");
      }
      if (cursor.s !== this.snapshot) throw new Error("stale path cursor: schema changed");
      if (cursor.f !== from || cursor.t !== to || cursor.r !== traversal || cursor.d !== maxDepth || cursor.n !== first || !Array.isArray(cursor.e) || !cursor.e.every((id) => typeof id === "string")) {
        throw new Error("path cursor does not match query parameters");
      }
    }
    const requestedDepth = maxDepth;
    maxDepth = Math.min(maxDepth, this.elements.size - 1);
    const paths: ExplorerPath[] = [];
    const visited = new Set([from]);
    const stack: ExplorerEdge[] = [];
    let states = 0;
    const walk = (node: ExplorerElement, selected: number): void => {
      if (++states > MAX_EXPLORED_STATES) throw new Error("schema path exploration resource limit exceeded");
      if (node === end) {
        if (paths.length === MAX_ENUMERATED_PATHS) throw new Error("schema path exploration resource limit exceeded");
        paths.push({ from: start, to: end, edges: stack.slice(), depth: stack.length, fieldDepth: traversal === "INPUT" ? 0 : selected });
        return;
      }
      if (stack.length >= maxDepth) return;
      for (const edge of this.outgoing.get(node.coordinate) ?? []) {
        if (!isAllowed(edge.kind, traversal) || visited.has(edge.target.coordinate)) continue;
        visited.add(edge.target.coordinate);
        stack.push(edge);
        walk(edge.target, selected + (traversal !== "INPUT" && ((stack.length === 1 && start.kind === "FIELD") || edge.kind === "DECLARES_FIELD") ? 1 : 0));
        stack.pop();
        visited.delete(edge.target.coordinate);
      }
    };
    walk(start, 0);
    paths.sort((a, b) => {
      if (a.depth !== b.depth) return a.depth - b.depth;
      for (let i = 0; i < a.edges.length; i++) {
        const left = a.edges[i]!.id;
        const right = b.edges[i]!.id;
        if (left < right) return -1;
        if (left > right) return 1;
      }
      return 0;
    });
    let pos = 0;
    if (cursor) {
      let low = 0;
      let high = paths.length;
      while (low < high) {
        const mid = (low + high) >>> 1;
        const path = paths[mid]!;
        if (path.depth < cursor.e.length || (path.depth === cursor.e.length && compareEdges(path.edges, cursor.e) < 0)) low = mid + 1;
        else high = mid;
      }
      if (low === paths.length || paths[low]!.depth !== cursor.e.length || compareEdges(paths[low]!.edges, cursor.e) !== 0) throw new Error("invalid path cursor: path not found");
      pos = low + 1;
    }
    const stop = Math.min(paths.length, pos + first);
    const nodes = paths.slice(pos, stop);
    const endCursor = nodes.length ? Buffer.from(JSON.stringify({ v: 1, s: this.snapshot, f: from, t: to, r: traversal, d: requestedDepth, n: first, e: nodes[nodes.length - 1]!.edges.map((edge) => edge.id) } satisfies PathCursor)).toString("base64url") : null;
    return { nodes, pageInfo: { hasNextPage: stop < paths.length, endCursor } };
  }
}
