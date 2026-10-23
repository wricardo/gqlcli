import {
  GraphQLError,
  buildASTSchema,
  buildClientSchema,
  getNamedType,
  isEnumType,
  isInputObjectType,
  isInterfaceType,
  isObjectType,
  isScalarType,
  isUnionType,
  parse,
  printSchema,
  validate,
  type GraphQLField,
  type GraphQLNamedType,
  type GraphQLSchema,
  type IntrospectionQuery,
} from "graphql";
import type { GraphQLClient, RequestOptions } from "./client.ts";
import {
  distancesTo,
  fieldSignature,
  fieldsReferencing,
  operationsReferencing,
  printTypeCompact,
  referencedTypeNames,
  type PrintOptions,
  type RootFields,
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
  /** Also return SDL for the referenced arg/return types, this many hops deep. */
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
  readonly roots: RootFields;

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
    return new SchemaExplorer(buildClientSchema(data as unknown as IntrospectionQuery));
  }

  /** From SDL text, e.g. the output of {@link sdl} saved to disk, for offline work. */
  static fromSDL(sdl: string): SchemaExplorer {
    return new SchemaExplorer(buildASTSchema(parse(sdl), { assumeValidSDL: false }));
  }

  static async fromClient(client: GraphQLClient, o: RequestOptions = {}): Promise<SchemaExplorer> {
    return SchemaExplorer.fromIntrospection(await client.introspect(o));
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
    const blocks: string[] = [];
    for (const f of fields) {
      this.collectReferenced([getNamedType(f.type)!.name, ...f.args.map((a) => getNamedType(a.type)!.name)], depth, seen, blocks, { args: o.args ?? false, desc: o.desc ?? false });
    }
    return { operations, ...(blocks.length ? { types: blocks.join("\n\n") } : {}) };
  }

  /** `gqlcli types`. */
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
        ...(o.args || o.desc ? { sdl: printTypeCompact(t, o) } : {}),
      }));
  }

  /** `gqlcli describe Type` or `Type.field`. */
  describe(name: string, o: DescribeOptions = {}): string {
    const depth = o.depth ?? 0;
    const pOpts: PrintOptions = { args: o.args ?? false, desc: o.desc ?? false };
    const dot = name.indexOf(".");
    if (dot > 0) return this.describeField(name.slice(0, dot), name.slice(dot + 1), depth, pOpts);

    const type = this.requireType(name);
    const blocks = [printTypeCompact(type, pOpts)];
    const seen = new Set<string>([type.name]);
    this.collectReferenced(referencedTypeNames(type), depth, seen, blocks, pOpts);
    if (depth < 1) return blocks.join("\n\n");

    const rootNames = [this.roots.query, this.roots.mutation, this.roots.subscription].filter(Boolean).map((t) => t!.name);
    if (rootNames.includes(type.name)) return blocks.join("\n\n");

    const ops = operationsReferencing(this.schema, this.roots, type.name, depth);
    const maxOps = o.maxOpRefs ?? 5;
    const shownOps = maxOps > 0 ? ops.slice(0, maxOps) : ops;
    if (shownOps.length) {
      const count = shownOps.length < ops.length ? ` (showing ${shownOps.length} of ${ops.length})` : "";
      const byRoot: Record<string, string[]> = {};
      for (const m of shownOps) (byRoot[m.root] ??= []).push(`  ${fieldSignature(m.field, true)}`);
      blocks.push(`# Operations referencing ${type.name}${count}`, ...Object.entries(byRoot).map(([root, lines]) => `type ${root} {\n${lines.join("\n")}\n}`));
    }

    const holders = fieldsReferencing(this.schema, this.roots, type.name, depth);
    const maxFields = o.maxFieldRefs ?? 5;
    const shownHolders = maxFields > 0 ? holders.slice(0, maxFields) : holders;
    if (shownHolders.length) {
      const dist = distancesTo(this.schema, type.name, Math.max(depth - 1, 0));
      const count = shownHolders.length < holders.length ? ` (showing ${shownHolders.length} of ${holders.length})` : "";
      blocks.push(`# Schema fields referencing ${type.name}${count}`);
      for (const h of shownHolders) {
        if (!(isObjectType(h) || isInterfaceType(h) || isInputObjectType(h))) continue;
        if (isInputObjectType(h)) {
          const lines = Object.values(h.getFields())
            .filter((f) => dist[getNamedType(f.type)!.name] !== undefined)
            .map((f) => `  ${f.name}: ${String(f.type)}`);
          blocks.push(`input ${h.name} {\n${lines.join("\n")}\n}`);
          continue;
        }
        const lines = Object.values(h.getFields())
          .filter((f) => dist[getNamedType(f.type)!.name] !== undefined || f.args.some((a) => dist[getNamedType(a.type)!.name] !== undefined))
          .map((f) => `  ${fieldSignature(f, pOpts.args)}`);
        blocks.push(`${isObjectType(h) ? "type" : "interface"} ${h.name} {\n${lines.join("\n")}\n}`);
      }
    }
    return blocks.join("\n\n");
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
    throw new Error(`type "${name}" not found${near.length ? `; similar: ${near.join(", ")}` : ""}`);
  }

  private describeField(typeName: string, fieldName: string, depth: number, p: PrintOptions): string {
    const type = this.requireType(typeName);
    if (!(isObjectType(type) || isInterfaceType(type))) throw new Error(`type "${typeName}" has no fields to describe`);
    const field = type.getFields()[fieldName];
    if (!field) {
      const near = Object.keys(type.getFields()).filter((n) => n.toLowerCase().includes(fieldName.toLowerCase())).slice(0, 10);
      throw new Error(`field "${fieldName}" not found on ${typeName}${near.length ? `; similar: ${near.join(", ")}` : ""}`);
    }
    const doc = p.desc && field.description ? `  # ${field.description.trim().replaceAll("\n", "\n  # ")}\n` : "";
    const blocks = [`type ${typeName} {\n${doc}  ${fieldSignature(field, true)}\n}`];
    this.collectReferenced([getNamedType(field.type)!.name, ...field.args.map((a) => getNamedType(a.type)!.name)], depth, new Set([typeName]), blocks, p);
    return blocks.join("\n\n");
  }

  /** Breadth-first expansion of named types into SDL blocks; scalars are skipped. */
  private collectReferenced(roots: string[], depth: number, seen: Set<string>, blocks: string[], p: PrintOptions): void {
    let frontier = roots;
    for (let hop = 0; hop < depth && frontier.length; hop++) {
      const next: string[] = [];
      for (const n of frontier) {
        const t = this.schema.getType(n);
        if (!t || seen.has(n) || n.startsWith("__") || isScalarType(t)) continue;
        seen.add(n);
        blocks.push(printTypeCompact(t, p));
        next.push(...referencedTypeNames(t));
      }
      frontier = next;
    }
  }
}
