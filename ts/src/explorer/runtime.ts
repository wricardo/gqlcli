import { readFileSync } from "node:fs";
import {
  GraphQLError, GraphQLList, GraphQLNonNull, buildSchema, getNamedType, graphql, isEnumType,
  isInputObjectType, isInterfaceType, isObjectType, isUnionType, parse,
  validate, type DocumentNode, type ExecutionResult, type GraphQLArgument, type GraphQLEnumValue,
  type GraphQLField, type GraphQLInputField, type GraphQLInputType, type GraphQLNamedType,
  type GraphQLOutputType, type GraphQLSchema, type SelectionNode,
} from "graphql";
import { ExplorerGraph, type ExplorerEdge, type ExplorerElement, type ExplorerPath } from "./graph.ts";
import { defaultValueLiteral, renderElement } from "./render.ts";

const explorationSchema = buildSchema(readFileSync(new URL("./schema.graphql", import.meta.url), "utf8"));
const contains = (text: string, term: string) => text.toLowerCase().includes(term.toLowerCase());
const positive = (value: number): number => { if (value <= 0) throw new Error("first must be positive"); return value; };
const typeKind = (type: GraphQLNamedType): string =>
  isObjectType(type) ? "OBJECT" : isInterfaceType(type) ? "INTERFACE" :
  isInputObjectType(type) ? "INPUT_OBJECT" : isUnionType(type) ? "UNION" : isEnumType(type) ? "ENUM" : "SCALAR";
const named = (type: GraphQLOutputType | GraphQLInputType) => getNamedType(type);
const typeMatches = (type: GraphQLOutputType | GraphQLInputType, term: string) => named(type).name.toLowerCase() === term.toLowerCase();
const ELEMENT_TYPE_NAMES: Record<ExplorerElement["kind"], string> = {
  TYPE: "SchemaType", FIELD: "SchemaField", INPUT_VALUE: "SchemaInputValue", ENUM_VALUE: "EnumValue",
};

type Source = Record<string, unknown>;
type ExplorerArgs = {
  coordinate: string; name: string; from: string; to: string; traversal: "OUTPUT" | "INPUT" | "ALL";
  first: number; maxDepth: number; matching: string; document: string;
  filter: string; kind?: string; kinds?: string[]; returns?: string; accepts?: string;
  parentType?: string; returnType?: string; after?: string; includeDescription?: boolean;
};
type Operation = { __typename: "Operation"; name: string; coordinate: string; kind: string; description: string | null };
type Reachable = { operation: Operation; target: ExplorerElement; depth: number };

/** Executes explorer documents entirely against a cached target schema; never sends them to the endpoint. */
export class ExplorerRuntime {
  readonly graph: ExplorerGraph;
  private readonly ordered: ExplorerElement[];
  private readonly operationsList: Operation[];
  private readonly edgesById: Map<string, ExplorerEdge>;

  readonly schema: GraphQLSchema;
  constructor(schema: GraphQLSchema) {
    this.schema = schema;
    this.graph = new ExplorerGraph(schema);
    this.ordered = [...this.graph.elements.values()].sort((a, b) => a.coordinate < b.coordinate ? -1 : a.coordinate > b.coordinate ? 1 : 0);
    this.edgesById = new Map([...this.graph.outgoing.values()].flat().map((edge) => [edge.id, edge]));
    this.operationsList = [];
    for (const [kind, root] of [["QUERY", schema.getQueryType()], ["MUTATION", schema.getMutationType()], ["SUBSCRIPTION", schema.getSubscriptionType()]] as const) {
      for (const field of Object.values(root?.getFields() ?? {})) {
        const e = this.graph.elements.get(`${root?.name}.${field.name}`);
        if (e) this.operationsList.push({ __typename: "Operation", name: e.name, coordinate: e.coordinate, kind, description: e.description || null });
      }
    }
  }

  async explore(document: string, variables?: Record<string, unknown>, operationName?: string): Promise<ExecutionResult> {
    const result = await graphql({ schema: explorationSchema, source: document, rootValue: {}, variableValues: variables, operationName,
      fieldResolver: (source: unknown, args: ExplorerArgs, _context, info) => this.resolve(source, args, info.parentType.name, info.fieldName),
      typeResolver: (source: ExplorerElement) => ELEMENT_TYPE_NAMES[source.kind],
    });
    if (!result.errors) return result;
    return {
      data: result.data ?? null,
      errors: result.errors.map((error) => error.path
        ? new GraphQLError(error.message, { path: error.path })
        : new GraphQLError(error.message.replace(/^Syntax Error: (?:Unexpected|Expected [^,]+, found) "([^"]+)"\.$/, "Unexpected $1"), {
          nodes: error.nodes, source: error.source, positions: error.positions,
          extensions: { code: error.message.startsWith("Syntax Error:") ? "GRAPHQL_PARSE_FAILED" : "GRAPHQL_VALIDATION_FAILED" },
        })),
    };
  }

  private element(coord: string, kind?: string): ExplorerElement | null {
    const e = this.graph.elements.get(coord);
    return e && (!kind || e.kind === kind) ? e : null;
  }

  private ref(type: GraphQLOutputType | GraphQLInputType): Source {
    const t = named(type);
    const inner = type instanceof GraphQLNonNull ? type.ofType : type instanceof GraphQLList ? type.ofType : null;
    return { signature: String(type), name: t.name, kind: typeKind(t), namedType: this.element(t.name, "TYPE"),
      isList: type instanceof GraphQLList, isNonNull: type instanceof GraphQLNonNull,
      ofType: inner && this.ref(inner) };
  }

  private field(e: ExplorerElement) { return e.value as GraphQLField<unknown, unknown>; }
  private input(e: ExplorerElement) { return e.value as GraphQLArgument | GraphQLInputField; }
  private op(coord: string): Operation | null { return this.operationsList.find((o) => o.coordinate === coord) ?? null; }
  private args(e: ExplorerElement): ExplorerElement[] {
    return this.field(e).args.map((a) => this.element(`${e.coordinate}(${a.name}:)`, "INPUT_VALUE")).filter((v): v is ExplorerElement => !!v);
  }
  private edges(coord: string, outgoing: boolean, kind?: string): ExplorerEdge[] {
    return (outgoing ? this.graph.outgoing : this.graph.incoming).get(coord)?.filter((e) => !kind || e.kind === kind) ?? [];
  }
  private shape(operation: Operation): string {
    const element = this.element(operation.coordinate, "FIELD")!;
    const field = this.field(element);
    const declarations = [renderElement(element)];
    const seen = new Set<string>();
    for (const type of [...field.args.map((arg) => named(arg.type)), named(field.type)]) {
      if (seen.has(type.name)) continue;
      seen.add(type.name);
      if (!isInputObjectType(type) && !isEnumType(type) && !isObjectType(type) && !isInterfaceType(type) && !isUnionType(type)) continue;
      const definition = this.element(type.name, "TYPE");
      if (definition) declarations.push(renderElement(definition));
    }
    return declarations.join("\n");
  }

  private reachableTypes(coord: string, maxDepth: number): ExplorerElement[] {
    if (maxDepth < 0) throw new Error("maxDepth must not be negative");
    const seen = new Set([coord]);
    const queue = [{ coord, depth: 0 }];
    const result: ExplorerElement[] = [];
    if (!this.element(coord)) return result;
    for (let i = 0; i < queue.length; i++) {
      const current = queue[i]!;
      if (current.depth >= maxDepth) continue;
      for (const edge of this.edges(current.coord, true)) {
        if (!["DECLARES_FIELD", "DECLARES_ENUM_VALUE", "RETURNS", "UNION_MEMBER", "POSSIBLE_TYPE"].includes(edge.kind) || seen.has(edge.target.coordinate)) continue;
        seen.add(edge.target.coordinate);
        if (edge.target.kind === "TYPE") result.push(edge.target);
        queue.push({ coord: edge.target.coordinate, depth: current.depth + 1 });
      }
    }
    return result.sort((a, b) => a.coordinate < b.coordinate ? -1 : a.coordinate > b.coordinate ? 1 : 0);
  }

  private paths(from: string, to: string, traversal: "OUTPUT" | "INPUT" | "ALL", depth: number, first: number, after?: string) {
    return this.graph.paths(from, to, traversal, depth, first, after ?? "");
  }
  private suggestedDocument(path: ExplorerPath): string | null {
    let current = path.from;
    let start = 0;
    if (current.kind === "TYPE") {
      const edge = path.edges[0];
      if (!edge || edge.source !== current || edge.kind !== "DECLARES_FIELD" || !this.op(edge.target.coordinate)) return null;
      current = edge.target;
      start = 1;
    }
    const operation = this.op(current.coordinate);
    if (!operation || current.kind !== "FIELD") return null;
    const selected: { field: ExplorerElement; fragment: string }[] = [{ field: current, fragment: "" }];
    let fragment = "";
    for (let index = start; index < path.edges.length; index++) {
      const edge = path.edges[index]!;
      if (edge.source !== current) return null;
      const target = edge.target;
      switch (edge.kind) {
        case "RETURNS":
          if (current.kind !== "FIELD" || target.kind !== "TYPE" || named(this.field(current).type).name !== target.name) return null;
          break;
        case "UNION_MEMBER":
        case "POSSIBLE_TYPE":
          if (current.kind !== "TYPE" || target.kind !== "TYPE" || !isObjectType(target.value as GraphQLNamedType) || fragment ||
            (edge.kind === "UNION_MEMBER" ? !isUnionType(current.value as GraphQLNamedType) : !isInterfaceType(current.value as GraphQLNamedType))) return null;
          fragment = target.name;
          break;
        case "DECLARES_FIELD":
          if (current.kind !== "TYPE" || target.kind !== "FIELD" || target.parent !== current.value) return null;
          selected.push({ field: target, fragment });
          fragment = "";
          break;
        default: return null;
      }
      current = target;
    }
    if (current.kind !== "FIELD" || current.coordinate !== path.to.coordinate || fragment) return null;
    const variables: string[] = [];
    let fields = "";
    for (const [index, step] of selected.entries()) {
      if (index !== 0) fields += ` { ${step.fragment ? `... on ${step.fragment} { ` : ""}`;
      fields += step.field.name;
      const argumentsText: string[] = [];
      for (const arg of this.field(step.field).args) {
        if (!(arg.type instanceof GraphQLNonNull) || arg.defaultValue !== undefined || arg.default !== undefined) continue;
        const variable = `v${index}_${arg.name}`;
        argumentsText.push(`${arg.name}: $${variable}`);
        variables.push(`$${variable}: ${arg.type}`);
      }
      if (argumentsText.length) fields += `(${argumentsText.join(", ")})`;
    }
    const terminal = named(this.field(current).type);
    if (isObjectType(terminal) || isInterfaceType(terminal) || isUnionType(terminal)) fields += " { __typename }";
    else if (!isEnumType(terminal) && typeKind(terminal) !== "SCALAR") return null;
    for (let i = selected.length - 1; i > 0; i--) {
      if (selected[i]!.fragment) fields += " }";
      fields += " }";
    }
    const document = `${operation.kind.toLowerCase()}${variables.length ? `(${variables.join(", ")})` : ""} { ${fields} }`;
    return validate(this.schema, parse(document)).length ? null : document;
  }

  private reachable(matching: string, kind: string, from: string, depth: number, first: number, after?: string) {
    positive(first);
    if (depth < 0) throw new Error("maxDepth must not be negative");
    if (from && !this.op(from)) throw new Error(`unknown operation ${JSON.stringify(from)}`);
    let position = 0;
    if (after) {
      let cursor: { Snapshot: string; From: string; Matching: string; Kind: string; Depth: number; Position: number; First: number };
      try {
        if (!/^[A-Za-z0-9_-]+$/.test(after) || Buffer.from(after, "base64url").toString("base64url") !== after) throw new Error("invalid base64url");
        cursor = JSON.parse(Buffer.from(after, "base64url").toString("utf8"));
      } catch { throw new Error("invalid reachability cursor"); }
      if (cursor.Snapshot !== this.graph.snapshot) throw new Error("stale reachability cursor: schema changed");
      if (cursor.From !== from || cursor.Matching !== matching || cursor.Kind !== kind || cursor.Depth !== depth || cursor.First !== first || !Number.isInteger(cursor.Position) || cursor.Position <= 0)
        throw new Error("reachability cursor does not match this query");
      position = cursor.Position;
    }
    const fields = this.ordered.filter((e) => e.kind === "FIELD" && contains(`${e.coordinate} ${e.description ?? ""}`, matching));
    const nodes: Reachable[] = [];
    let found = 0;
    let hasNextPage = false;
    outer: for (const op of this.operationsList) {
      if (from && op.coordinate !== from) continue;
      if (kind && op.kind !== kind) continue;
      for (const target of fields) {
        if (op.coordinate !== target.coordinate && !this.paths(op.coordinate, target.coordinate, "OUTPUT", depth, 1).nodes.length) continue;
        found++;
        if (found <= position) continue;
        if (nodes.length === first) { hasNextPage = true; break outer; }
        nodes.push({ operation: op, target, depth });
      }
    }
    if (!hasNextPage && found < position) throw new Error("reachability cursor is past the end");
    const endCursor = nodes.length ? Buffer.from(JSON.stringify({ Snapshot: this.graph.snapshot, From: from, Matching: matching, Kind: kind, Depth: depth, Position: position + nodes.length, First: first })
      .replace(/[<>&\u2028\u2029]/g, (char) => ({ "<": "\\u003c", ">": "\\u003e", "&": "\\u0026", "\u2028": "\\u2028", "\u2029": "\\u2029" })[char]!)).toString("base64url") : null;
    return { nodes, pageInfo: { hasNextPage, endCursor } };
  }

  private validation(document: string) {
    let ast: DocumentNode;
    try { ast = parse(document); }
    catch (error) { if (!(error instanceof GraphQLError)) throw error; return { valid: false, errors: [this.diagnostic(error, [])] }; }
    const sites: { line: number; column: number; coordinate: string; input?: string; type?: GraphQLOutputType | GraphQLInputType }[] = [];
    const fragments = new Map(ast.definitions.filter((d) => d.kind === "FragmentDefinition").map((d) => [d.name.value, d]));
    const visited = new Set<string>();
    const walk = (selections: readonly SelectionNode[], parent: GraphQLNamedType | undefined) => {
      for (const item of selections) {
        if (item.kind === "Field") {
          if (!parent) continue;
          const coord = `${parent.name}.${item.name.value}`;
          const def = (isObjectType(parent) || isInterfaceType(parent)) ? parent.getFields()[item.name.value] : undefined;
          const loc = item.name.loc?.startToken;
          if (loc) sites.push({ line: loc.line, column: loc.column, coordinate: coord, type: def?.type });
          for (const arg of item.arguments ?? []) {
            const token = arg.name.loc?.startToken;
            if (token) sites.push({ line: token.line, column: token.column, coordinate: coord, input: `${coord}(${arg.name.value}:)`, type: def?.args.find((a) => a.name === arg.name.value)?.type });
          }
          if (def) walk(item.selectionSet?.selections ?? [], named(def.type));
        } else if (item.kind === "InlineFragment") {
          walk(item.selectionSet.selections, item.typeCondition ? this.schema.getType(item.typeCondition.name.value) : parent);
        } else if (item.kind === "FragmentSpread" && !visited.has(item.name.value)) {
          visited.add(item.name.value);
          const fragment = fragments.get(item.name.value);
          if (fragment) walk(fragment.selectionSet.selections, this.schema.getType(fragment.typeCondition.name.value));
        }
      }
    };
    for (const definition of ast.definitions) {
      if (definition.kind === "OperationDefinition") walk(definition.selectionSet.selections, definition.operation === "query" ? this.schema.getQueryType() ?? undefined : definition.operation === "mutation" ? this.schema.getMutationType() ?? undefined : this.schema.getSubscriptionType() ?? undefined);
    }
    for (const fragment of fragments.values()) if (!visited.has(fragment.name.value)) { visited.add(fragment.name.value); walk(fragment.selectionSet.selections, this.schema.getType(fragment.typeCondition.name.value)); }
    const errors = validate(this.schema, ast).map((e) => this.diagnostic(e, sites));
    return { valid: errors.length === 0, errors };
  }

  private diagnostic(error: GraphQLError, sites: { line: number; column: number; coordinate: string; input?: string; type?: GraphQLOutputType | GraphQLInputType }[]) {
    const loc = error.locations?.[0];
    // AST traversal is not necessarily source order (fragments are visited through spreads).
    const site = loc ? sites.filter((s) => s.line < loc.line || s.line === loc.line && s.column <= loc.column)
      .sort((a, b) => b.line - a.line || b.column - a.column)[0] : undefined;
    let coordinate: string | null = site?.input ?? site?.coordinate ?? null;
    let field = site ? this.element(site.coordinate, "FIELD") : null;
    let inputValue = site?.input ? this.element(site.input, "INPUT_VALUE") : null;
    let expectedType = site?.type ? this.ref(site.type) : null;
    const message = error.message;
    const unknownArgument = /Unknown argument "([^"]+)" on field "([^"]+)"/.exec(message);
    if (unknownArgument) {
      const fieldName = unknownArgument[2]!;
      const fieldCoord = fieldName.includes(".") ? fieldName : (site?.coordinate ?? fieldName);
      coordinate = `${fieldCoord}(${unknownArgument[1]}:)`;
      field = this.element(fieldCoord, "FIELD"); inputValue = null; expectedType = null;
    }
    const required = /[Aa]rgument "([^"]+)" of type "[^"]+" is required, but it was not provided/.exec(message);
    if (required && site) {
      const coord = `${site.coordinate}(${required[1]}:)`;
      const value = this.element(coord, "INPUT_VALUE");
      if (value) { coordinate = coord; inputValue = value; expectedType = this.ref(this.input(value).type); }
    }
    const unknownInput = /Field "([^"]+)" is not defined by type "([^"]+)"/.exec(message);
    if (unknownInput) { coordinate = `${unknownInput[2]}.${unknownInput[1]}`; field = null; inputValue = this.element(coordinate, "INPUT_VALUE"); expectedType = null; }
    const suggestions: Source[] = [];
    const marker = message.indexOf("Did you mean");
    if (coordinate && marker >= 0) {
      for (const match of message.slice(marker).matchAll(/"([_A-Za-z][_0-9A-Za-z]*)"/g)) {
        const candidate = coordinate.includes("(") ? `${coordinate.split("(")[0]}(${match[1]}:)` : `${coordinate.split(".")[0]}.${match[1]}`;
        const replacementField = this.element(candidate, "FIELD");
        const replacementInputValue = this.element(candidate, "INPUT_VALUE");
        if (replacementField || replacementInputValue) suggestions.push({ message: `Use ${match[1]}`, replacementField, replacementInputValue });
      }
    }
    const rule = unknownArgument ? "KnownArgumentNames" :
      required ? "ProvidedRequiredArguments" :
      unknownInput ? "ValuesOfCorrectType" :
      message.startsWith("Cannot query field ") ? "FieldsOnCorrectType" :
      message.startsWith("Unknown type ") ? "KnownTypeNames" :
      message.startsWith("Unknown directive ") ? "KnownDirectives" :
      message.startsWith("Variable ") && message.includes("is not defined") ? "NoUndefinedVariables" :
      message.startsWith("Variable ") && message.includes("never used") ? "NoUnusedVariables" : null;
    return { message, coordinate, rule, locations: error.locations ?? [], field, inputValue, expectedType, suggestions };
  }

  private resolve(source: unknown, args: ExplorerArgs, parent: string, field: string): unknown {
    if (parent === "Query") {
      switch (field) {
        case "overview": {
          const types = this.ordered.filter((e) => e.kind === "TYPE");
          return { snapshot: this.graph.snapshot, queryCount: this.operationsList.filter((o) => o.kind === "QUERY").length,
            mutationCount: this.operationsList.filter((o) => o.kind === "MUTATION").length, subscriptionCount: this.operationsList.filter((o) => o.kind === "SUBSCRIPTION").length,
            typeCount: types.length, fieldCount: this.ordered.filter((e) => e.kind === "FIELD").length,
            inputTypeCount: types.filter((e) => typeKind(e.value as GraphQLNamedType) === "INPUT_OBJECT").length,
            enumCount: types.filter((e) => typeKind(e.value as GraphQLNamedType) === "ENUM").length,
            scalarCount: types.filter((e) => typeKind(e.value as GraphQLNamedType) === "SCALAR").length,
            queryRoot: this.schema.getQueryType() ? this.element(this.schema.getQueryType()!.name) : null,
            mutationRoot: this.schema.getMutationType() ? this.element(this.schema.getMutationType()!.name) : null,
            subscriptionRoot: this.schema.getSubscriptionType() ? this.element(this.schema.getSubscriptionType()!.name) : null };
        }
        case "operations": return this.operationsList.filter((o) => (!args.kind || args.kind === o.kind) && (!args.filter || contains(`${o.coordinate} ${o.description ?? ""}`, args.filter)) &&
          (!args.returns || typeMatches(this.field(this.element(o.coordinate)!).type, args.returns)) &&
          (!args.accepts || this.field(this.element(o.coordinate)!).args.some((a) => typeMatches(a.type, args.accepts ?? "")))).slice(0, positive(args.first));
        case "operation": return this.op(args.coordinate);
        case "types": return this.ordered.filter((e) => e.kind === "TYPE" && (!args.kind || args.kind === typeKind(e.value as GraphQLNamedType)) && (!args.filter || contains(`${e.name} ${e.description ?? ""}`, args.filter))).slice(0, positive(args.first));
        case "type": return this.element(args.name, "TYPE");
        case "fields": return this.ordered.filter((e) => e.kind === "FIELD" && contains(`${e.coordinate} ${e.description ?? ""}`, args.filter) &&
          (!args.parentType || e.parent?.name === args.parentType) && (!args.returnType || typeMatches(this.field(e).type, args.returnType))).slice(0, positive(args.first));
        case "field": return this.element(args.coordinate, "FIELD");
        case "element": return this.element(args.coordinate);
        case "elements": return this.ordered.filter((e) => contains(`${e.coordinate} ${e.description ?? ""}`, args.filter) && (!args.kinds?.length || args.kinds.includes(e.kind))).slice(0, positive(args.first));
        case "paths": return this.paths(args.from, args.to, args.traversal, args.maxDepth, args.first, args.after);
        case "reachableFields": return this.reachable(args.matching, args.kind ?? "", args.from ?? "", args.maxDepth, args.first, args.after);
        case "validate": return this.validation(args.document);
      }
    }
    if (field === "__typename") return parent;
    if (["SchemaType", "SchemaField", "SchemaInputValue", "EnumValue", "Operation"].includes(parent) && field === "sdl") {
      const value = source as ExplorerElement | Operation;
      const e = this.element(value.coordinate);
      if (!e) throw new Error(`unknown element ${value.coordinate}`);
      return renderElement(e, args.includeDescription ?? false);
    }
    if (parent === "Operation") {
      const e = this.element((source as Operation).coordinate)!;
      switch (field) {
        case "shape": return this.shape(source as Operation);
        case "rootField": return e;
        case "arguments": return this.args(e);
        case "returnType": return this.ref(this.field(e).type);
        case "reachableTypes": return this.reachableTypes(e.coordinate, args.maxDepth);
      }
    }
    if (parent === "SchemaType") {
      const e = source as ExplorerElement, type = e.value as GraphQLNamedType;
      switch (field) {
        case "kind": return typeKind(type);
        case "fields": return (isObjectType(type) || isInterfaceType(type) ? Object.values(type.getFields()) : [])
          .filter((f) => !args.filter || contains(`${f.name} ${f.description ?? ""}`, args.filter))
          .map((f) => this.element(`${e.name}.${f.name}`, "FIELD")).filter(Boolean);
        case "inputFields": return (isInputObjectType(type) ? Object.values(type.getFields()) : []).map((f) => this.element(`${e.name}.${f.name}`, "INPUT_VALUE")).filter(Boolean);
        case "enumValues": return (isEnumType(type) ? type.getValues() : []).map((v) => this.element(`${e.name}.${v.name}`, "ENUM_VALUE")).filter(Boolean);
        case "possibleTypes": return this.edges(e.coordinate, true).filter((edge) => edge.kind === "UNION_MEMBER" || edge.kind === "POSSIBLE_TYPE").map((edge) => edge.target);
        case "interfaces": return (isObjectType(type) || isInterfaceType(type) ? type.getInterfaces() : []).map((t) => this.element(t.name, "TYPE"));
        case "outgoing": return this.edges(e.coordinate, true, args.kind);
        case "incoming": return this.edges(e.coordinate, false, args.kind);
        case "operations": return this.operationsList.filter((o) => (!args.kind || args.kind === o.kind) && (() => {
          const opField = this.element(o.coordinate)!;
          return opField.parent?.name === e.name || named(this.field(opField).type).name === e.name || this.field(opField).args.some((a) => named(a.type).name === e.name);
        })());
      }
    }
    if (parent === "SchemaField") {
      const e = source as ExplorerElement;
      switch (field) {
        case "parentType": return this.element(e.parent!.name, "TYPE");
        case "type": return this.ref(this.field(e).type);
        case "arguments": return this.args(e);
        case "isDeprecated": return this.field(e).deprecationReason != null;
        case "deprecationReason": return this.field(e).deprecationReason ?? null;
      }
    }
    if (parent === "SchemaInputValue") {
      const e = source as ExplorerElement, input = this.input(e);
      switch (field) {
        case "parentField": return e.coordinate.includes("(") ? this.element(e.coordinate.split("(")[0]!, "FIELD") : null;
        case "parentInputType": return e.parent && isInputObjectType(e.parent) ? this.element(e.parent.name, "TYPE") : null;
        case "type": return this.ref(input.type);
        case "defaultValue": return defaultValueLiteral(input);
        case "required": return input.type instanceof GraphQLNonNull && input.defaultValue === undefined && input.default === undefined;
        case "isDeprecated": return input.deprecationReason != null;
        case "deprecationReason": return input.deprecationReason ?? null;
      }
    }
    if (parent === "EnumValue") {
      const e = source as ExplorerElement;
      switch (field) {
        case "parentType": return this.element(e.parent!.name, "TYPE");
        case "isDeprecated": return (e.value as GraphQLEnumValue).deprecationReason != null;
        case "deprecationReason": return (e.value as GraphQLEnumValue).deprecationReason ?? null;
      }
    }
    if (parent === "SchemaEdge") {
      const value = source as ExplorerEdge;
      const edge = this.edgesById.get(value.id);
      if (!edge) throw new Error(`unknown edge ${value.id}`);
      if (field === "source") return edge.source;
      if (field === "target") return edge.target;
      if (field === "field") return edge.field ?? null;
      if (field === "inputValue") return edge.inputValue ?? null;
    }
    if (parent === "ReachableField" && field === "paths") {
      const value = source as Reachable;
      return this.paths(value.operation.coordinate, value.target.coordinate, "OUTPUT", value.depth, args.first, args.after);
    }
    if (parent === "SchemaPath") {
      const value = source as ExplorerPath;
      switch (field) {
        case "segments": return value.edges.map((edge, index) => ({ index, edge }));
        case "coordinates": return [value.from.coordinate, ...value.edges.map((edge) => edge.target.coordinate)];
        case "edgeKinds": return value.edges.map((edge) => edge.kind);
        case "suggestedDocument": return this.suggestedDocument(value);
      }
    }
    const value = source as Source;
    return value[field];
  }
}
