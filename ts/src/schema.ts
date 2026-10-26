import {
  GraphQLError,
  buildASTSchema,
  buildClientSchema,
  getNamedType,
  isEnumType,
  isInputObjectType,
  isInterfaceType,
  isObjectType,
  isAbstractType,
  isUnionType,
  parse,
  printSchema,
  validate,
  type ExecutionResult,
  type GraphQLNamedType,
  type GraphQLObjectType,
  type GraphQLSchema,
  type IntrospectionQuery,
} from "graphql";
import { ExplorerRuntime } from "./explorer/runtime.ts";
import type { GraphQLClient, RequestOptions } from "./client.ts";
import {
  fieldScore,
  fieldSignature,
  formatTypeSDL,
  isBuiltInScalar,
  printTypeCompact,
  targetDistances,
  typeDef,
  viewField,
  type FieldView,
  type PrintOptions,
  type TypeDef,
} from "./typeprint.ts";
import type { GraphQLResponse } from "./types.ts";

export interface OperationInfo {
  name: string;
  /** `name(args): Return`; args only when requested. */
  signature: string;
  description?: string;
}

export interface OperationListOptions extends PrintOptions {
  /** Case-insensitive substring on the field name. */
  filter?: string;
  /** Also return SDL for the referenced arg/return types, this many levels deep. */
  depth?: number;
}

export interface TypeInfo {
  name: string;
  kind: string;
  description?: string;
  sdl?: string;
}

export interface TypeListOptions extends PrintOptions {
  /** Case-insensitive substring on the type name. */
  filter?: string;
  /** Include each type's compact SDL (implied by `args` or `desc`). */
  sdl?: boolean;
  /** OBJECT, ENUM, INPUT_OBJECT, SCALAR, INTERFACE or UNION. */
  kind?: string;
}

export interface DescribeOptions extends PrintOptions {
  /** 0: just the type. >=1: referenced types plus operations/fields that reference it, within this many hops. */
  depth?: number;
  /** Cap on appended referencing operations. Default 5; 0 = unlimited. */
  maxOpRefs?: number;
  /** Cap on appended referencing schema types. Default 5; 0 = unlimited. */
  maxFieldRefs?: number;
}

export interface ValidationIssue {
  message: string;
  line?: number;
  column?: number;
  /** Compact SDL of the type the message refers to. */
  schemaHint?: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationIssue[];
}

function kindOf(t: GraphQLNamedType): string {
  if (isObjectType(t)) return "OBJECT";
  if (isInterfaceType(t)) return "INTERFACE";
  if (isInputObjectType(t)) return "INPUT_OBJECT";
  if (isEnumType(t)) return "ENUM";
  if (isUnionType(t)) return "UNION";
  return "SCALAR";
}

/** Schema exploration over a built schema: list, describe, SDL, validate. Pure and offline once constructed. */
export class SchemaExplorer {
  readonly schema: GraphQLSchema;
  readonly roots: { query?: GraphQLObjectType | null; mutation?: GraphQLObjectType | null; subscription?: GraphQLObjectType | null };
  private explorerRuntime: ExplorerRuntime | undefined;

  constructor(schema: GraphQLSchema) {
    this.schema = schema;
    this.roots = { query: schema.getQueryType(), mutation: schema.getMutationType(), subscription: schema.getSubscriptionType() };
  }

  /** From an introspection envelope (`{data:{__schema}}`) or bare `{__schema}` data. */
  static fromIntrospection(result: GraphQLResponse<{ __schema: unknown }> | { __schema: unknown }): SchemaExplorer {
    const data = "__schema" in result ? result : result.data;
    if (!data?.__schema) {
      const detail = "errors" in result && result.errors?.length ? `: ${result.errors[0]?.message}` : "";
      throw new Error(`introspection result has no schema${detail}`);
    }
    // Go reconstructs a parsed schema from introspection SDL before indexing.
    // Materialize the same applied metadata (notably @deprecated) so graph
    // snapshot IDs and cursors agree across Go and TypeScript cache readers.
    return SchemaExplorer.fromSDL(printSchema(buildClientSchema(data as unknown as IntrospectionQuery)));
  }

  /** From SDL text, e.g. the output of {@link sdl} saved to disk, for offline work. */
  static fromSDL(sdl: string): SchemaExplorer {
    return new SchemaExplorer(buildASTSchema(parse(sdl), { assumeValidSDL: false }));
  }

  static async fromClient(client: GraphQLClient, o: RequestOptions = {}): Promise<SchemaExplorer> {
    return SchemaExplorer.fromIntrospection(await client.introspect(o));
  }

  /** Execute a GraphQL explorer document against this cached schema, entirely locally. */
  explore(document: string, variables?: Record<string, unknown>, operationName?: string): Promise<ExecutionResult> {
    this.explorerRuntime ??= new ExplorerRuntime(this.schema);
    return this.explorerRuntime.explore(document, variables, operationName);
  }

  /** `gqlcli queries` / `mutations` / subscriptions. */
  operations(kind: "query" | "mutation" | "subscription", o: OperationListOptions = {}): { operations: OperationInfo[]; types?: string } {
    const root = this.roots[kind];
    if (!root) return { operations: [] };
    const needle = o.filter?.toLowerCase();
    const fields = Object.values(root.getFields()).filter((f) => !needle || f.name.toLowerCase().includes(needle));
    const operations = fields.map((f): OperationInfo => ({
      name: f.name,
      signature: fieldSignature(f, o.args),
      ...(o.desc && f.description ? { description: f.description } : {}),
    }));
    const depth = o.depth ?? 0;
    if (depth < 1) return { operations };
    const seen = new Set<string>();
    let sdl = "";
    const names = new Set(fields.flatMap((f) => [getNamedType(f.type).name, ...f.args.map((a) => getNamedType(a.type).name)]));
    for (const name of [...names].sort()) {
      if (isBuiltInScalar(name)) continue;
      sdl += this.appendTypeRecursive(name, { args: o.args ?? false, desc: o.desc ?? false }, depth - 1, seen);
    }
    return { operations, ...(sdl ? { types: sdl } : {}) };
  }

  /** `gqlcli types`. Includes deprecated members, like the Go listing. */
  types(o: TypeListOptions = {}): TypeInfo[] {
    const needle = o.filter?.toLowerCase();
    const kind = o.kind?.toUpperCase();
    return Object.values(this.schema.getTypeMap())
      .filter((t) => !t.name.startsWith("__") && (!needle || t.name.toLowerCase().includes(needle)) && (!kind || kindOf(t) === kind))
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((t): TypeInfo => ({
        name: t.name,
        kind: kindOf(t),
        ...(t.description ? { description: t.description } : {}),
        ...(o.sdl || o.args || o.desc ? { sdl: printTypeCompact(t, o, true) } : {}),
      }));
  }

  /**
   * `gqlcli describe Type` or `Type.field`, in the Go CLI's compact format: fields sorted by type then name,
   * same-typed fields collapsed, `depth` expanding referenced types and appending the operations and schema fields
   * that reference the type.
   */
  describe(name: string, o: DescribeOptions = {}): string {
    const depth = Math.max(0, o.depth ?? 0);
    const maxOps = Math.max(0, o.maxOpRefs ?? 5);
    const maxFields = Math.max(0, o.maxFieldRefs ?? 5);
    const p: PrintOptions = { args: o.args ?? false, desc: o.desc ?? false };

    const dot = name.indexOf(".");
    const selector = dot > 0 && dot < name.length - 1 && !name.includes(".", dot + 1) ? { type: name.slice(0, dot), field: name.slice(dot + 1) } : undefined;
    const rootName = selector?.type ?? name;
    const type = this.requireType(rootName);

    const seen = new Set<string>();
    let out: string;
    if (selector) {
      if (!(isObjectType(type) || isInterfaceType(type))) throw new Error(`field "${selector.field}" not found on type "${type.name}"`);
      const field = Object.values(type.getFields()).find((f) => f.name === selector.field && !f.deprecationReason);
      if (!field) throw new Error(`field "${selector.field}" not found on type "${type.name}"`);
      out = this.appendDef({ ...typeDef(type), fields: [viewField(field)] }, p, depth, seen);
    } else {
      out = this.appendTypeRecursive(type.name, p, depth, seen);
    }
    if (depth >= 1) out += this.referencingOperations(rootName, depth, maxOps, p) + this.referencingFields(rootName, depth, maxFields, p);
    return out;
  }

  /** The whole schema as SDL; builtin scalars/directives omitted, loadable by {@link fromSDL}. */
  sdl(): string {
    return printSchema(this.schema);
  }

  /** Validates a document against the schema without executing it. Variable values are not checked. */
  validate(document: string): ValidationResult {
    let doc;
    try {
      doc = parse(document);
    } catch (err) {
      if (err instanceof GraphQLError) return { valid: false, errors: [this.issue(err)] };
      throw err;
    }
    const errors = validate(this.schema, doc).map((e) => this.issue(e));
    return { valid: errors.length === 0, errors };
  }

  private issue(e: GraphQLError): ValidationIssue {
    const loc = e.locations?.[0];
    const named = /\b(?:on type|of type|type|Type) "([A-Za-z_]\w*)"/.exec(e.message)?.[1];
    const hintType = named ? this.schema.getType(named) : undefined;
    return {
      message: e.message,
      ...(loc ? { line: loc.line, column: loc.column } : {}),
      ...(hintType && !hintType.name.startsWith("__") ? { schemaHint: printTypeCompact(hintType, { args: true }) } : {}),
    };
  }

  private requireType(name: string): GraphQLNamedType {
    const t = this.schema.getType(name);
    if (t) return t;
    const needle = name.toLowerCase();
    const near = Object.keys(this.schema.getTypeMap()).filter((n) => !n.startsWith("__") && n.toLowerCase().includes(needle)).slice(0, 10);
    throw new Error(`type "${name}" not found in schema${near.length ? `; similar: ${near.join(", ")}` : ""}`);
  }

  /** Prints a definition, then (depth-first, names sorted) the types it references with one less level of depth. */
  private appendDef(def: TypeDef, p: PrintOptions, depth: number, seen: Set<string>): string {
    if (!def.name || seen.has(def.name) || def.name.startsWith("__")) return "";
    seen.add(def.name);
    let out = formatTypeSDL(def, p.args ?? false, !(p.desc ?? false));
    if (depth === 0) return out;
    const names = new Set<string>();
    for (const f of def.fields ?? []) {
      names.add(f.type.replaceAll(/[[\]!]/g, ""));
      for (const a of f.args) names.add(a.type.replaceAll(/[[\]!]/g, ""));
    }
    const type = this.schema.getType(def.name);
    const possible = type && isAbstractType(type) ? this.schema.getPossibleTypes(type).map((t) => t.name) : [];
    for (const n of [...names, ...possible].sort()) {
      if (seen.has(n) || isBuiltInScalar(n) || n.startsWith("__")) continue;
      out += this.appendTypeRecursive(n, p, depth - 1, seen);
    }
    return out;
  }

  private appendTypeRecursive(name: string, p: PrintOptions, depth: number, seen: Set<string>): string {
    const t = this.schema.getType(name);
    return t ? this.appendDef(typeDef(t), p, depth, seen) : "";
  }

  /** `# Referenced by top-level operations`: query then mutation fields that use the type, args first then nearest. */
  private referencingOperations(target: string, depth: number, maxRefs: number, p: PrintOptions): string {
    const { query, mutation, subscription } = this.roots;
    if (target.startsWith("__") || [query, mutation, subscription].some((r) => r?.name === target)) return "";
    const dist = targetDistances(this.schema, target, depth);
    const sections: string[] = [];
    let remaining = maxRefs;
    let total = 0;
    let shown = 0;
    for (const root of [query, mutation]) {
      if (!root) continue;
      const matches = Object.values(root.getFields())
        .filter((f) => !f.deprecationReason)
        .map((f) => ({ view: viewField(f), score: fieldScore(viewField(f), target, dist) }))
        .filter((m): m is { view: FieldView; score: NonNullable<typeof m.score> } => m.score !== undefined)
        .sort((a, b) => a.score.locationRank - b.score.locationRank || a.score.depth - b.score.depth || (a.view.name < b.view.name ? -1 : a.view.name > b.view.name ? 1 : 0));
      total += matches.length;
      const limited = maxRefs > 0 && matches.length > remaining ? matches.slice(0, remaining) : matches;
      if (limited.length === 0) continue;
      shown += limited.length;
      if (remaining > 0) remaining -= limited.length;
      sections.push(formatTypeSDL({ name: root.name, kind: "OBJECT", fields: limited.map((m) => m.view), preserveFieldOrder: true }, true, !(p.desc ?? false)));
      if (remaining === 0 && maxRefs > 0) break;
    }
    if (sections.length === 0) return "";
    return `\n# Referenced by top-level operations${total > shown ? ` (showing ${shown} of ${total})` : ""}\n${sections.join("")}`;
  }

  /** `# Referenced by fields`: non-root object/interface types having a field whose args or type reach the target. */
  private referencingFields(target: string, depth: number, maxRefs: number, p: PrintOptions): string {
    if (target.startsWith("__")) return "";
    const rootNames = Object.values(this.roots).filter((r) => r).map((r) => r!.name);
    const dist = targetDistances(this.schema, target, depth);
    let sections: { name: string; sdl: string }[] = [];
    for (const t of Object.values(this.schema.getTypeMap())) {
      if (t.name.startsWith("__") || rootNames.includes(t.name) || !(isObjectType(t) || isInterfaceType(t))) continue;
      const def = typeDef(t);
      const fields = (def.fields ?? []).filter((f) => fieldScore(f, target, dist) !== undefined);
      if (fields.length > 0) sections.push({ name: t.name, sdl: formatTypeSDL({ ...def, fields }, true, !(p.desc ?? false)) });
    }
    if (sections.length === 0) return "";
    const total = sections.length;
    sections = sections.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    if (maxRefs > 0 && sections.length > maxRefs) sections = sections.slice(0, maxRefs);
    return `\n# Referenced by fields${total > sections.length ? ` (showing ${sections.length} of ${total})` : ""}\n${sections.map((s) => s.sdl).join("")}`;
  }
}
