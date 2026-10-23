import {
  getNamedType,
  isEnumType,
  isInputObjectType,
  isInterfaceType,
  isObjectType,
  isScalarType,
  isAbstractType,
  isUnionType,
  type GraphQLArgument,
  type GraphQLField,
  type GraphQLNamedType,
  type GraphQLSchema,
} from "graphql";

export interface PrintOptions {
  /** Show field argument signatures. */
  args?: boolean;
  /** Show descriptions as `#` comments. */
  desc?: boolean;
}

/** Field of an object/interface/input type, reduced to what the compact SDL shows. */
export interface FieldView {
  name: string;
  description?: string | null;
  type: string;
  args: { name: string; type: string }[];
}

export interface TypeDef {
  name: string;
  kind: "SCALAR" | "OBJECT" | "INTERFACE" | "UNION" | "ENUM" | "INPUT_OBJECT";
  description?: string | null;
  enumValues?: string[];
  fields?: FieldView[];
  /** Keep `fields` in the given order instead of sorting by type then name (used for operation sections). */
  preserveFieldOrder?: boolean;
}

const BUILTIN_SCALARS = ["String", "Int", "Float", "Boolean", "ID"];

export function isBuiltInScalar(name: string): boolean {
  return BUILTIN_SCALARS.includes(name);
}

function argList(args: readonly GraphQLArgument[]): string {
  return args.length === 0 ? "" : `(${args.map((a) => `${a.name}: ${String(a.type)}`).join(", ")})`;
}

/** `name(arg: T): Ret` for one object/interface field, for operation listings. */
export function fieldSignature(f: GraphQLField, withArgs = true): string {
  return `${f.name}${withArgs ? argList(f.args) : ""}: ${String(f.type)}${f.deprecationReason ? " @deprecated" : ""}`;
}

function kindOf(t: GraphQLNamedType): TypeDef["kind"] {
  if (isObjectType(t)) return "OBJECT";
  if (isInterfaceType(t)) return "INTERFACE";
  if (isInputObjectType(t)) return "INPUT_OBJECT";
  if (isEnumType(t)) return "ENUM";
  if (isUnionType(t)) return "UNION";
  return "SCALAR";
}

export function viewField(f: GraphQLField): FieldView {
  return { name: f.name, description: f.description, type: String(f.type), args: f.args.map((a) => ({ name: a.name, type: String(a.type) })) };
}

/**
 * Definition of a named type as `describe` shows it. Deprecated fields and enum values are omitted unless
 * `includeDeprecated`, as the Go CLI's `describe` does while its `types` listing keeps them.
 */
export function typeDef(type: GraphQLNamedType, includeDeprecated = false): TypeDef {
  const def: TypeDef = { name: type.name, kind: kindOf(type), description: type.description };
  if (isEnumType(type)) def.enumValues = type.getValues().filter((v) => includeDeprecated || !v.deprecationReason).map((v) => v.name);
  if (isObjectType(type) || isInterfaceType(type)) {
    def.fields = Object.values(type.getFields()).filter((f) => includeDeprecated || !f.deprecationReason).map(viewField);
  }
  if (isInputObjectType(type)) {
    def.fields = Object.values(type.getFields()).map((f) => ({ name: f.name, description: f.description, type: String(f.type), args: [] }));
  }
  return def;
}

function formatField(f: FieldView, showArgs: boolean, showDescriptions: boolean): string {
  let out = "";
  if (showDescriptions && f.description) {
    out += f.description.includes("\n") ? `  """\n${f.description.split("\n").map((l) => `  ${l}\n`).join("")}  """\n` : `  # ${f.description}\n`;
  }
  if (showArgs && f.args.length > 0) return `${out}  ${f.name}(${f.args.map((a) => `${a.name}: ${a.type}`).join(", ")}): ${f.type}\n`;
  return `${out}  ${f.name}: ${f.type}\n`;
}

const KEYWORDS: Record<TypeDef["kind"], string> = { OBJECT: "type", INPUT_OBJECT: "input", INTERFACE: "interface", UNION: "union", ENUM: "enum", SCALAR: "scalar" };

/**
 * The Go CLI's compact SDL: fields sorted by type then name, consecutive same-typed plain fields collapsed to
 * `a, b: T`, enums on one line, each block newline-terminated. Unions print an empty body, exactly as Go does.
 */
export function formatTypeSDL(def: TypeDef, showArgs: boolean, noDescriptions: boolean): string {
  let out = !noDescriptions && def.description ? `# ${def.description}\n` : "";
  if (def.kind === "SCALAR") return `${out}scalar ${def.name}\n`;
  if (def.kind === "ENUM") return `${out}enum ${def.name} { ${(def.enumValues ?? []).join(" ")} }\n`;
  out += `${KEYWORDS[def.kind]} ${def.name} {\n`;
  const fields = def.fields ?? [];
  const sorted = def.preserveFieldOrder ? fields : [...fields].sort((a, b) => (a.type !== b.type ? (a.type < b.type ? -1 : 1) : a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  const own = (f: FieldView) => (!noDescriptions && !!f.description) || (showArgs && f.args.length > 0);
  for (let i = 0; i < sorted.length; ) {
    const f = sorted[i]!;
    if (own(f)) {
      out += formatField(f, showArgs, !noDescriptions);
      i++;
      continue;
    }
    let j = i + 1;
    while (j < sorted.length && sorted[j]!.type === f.type && !own(sorted[j]!)) j++;
    out += j - i === 1 ? formatField(f, showArgs, !noDescriptions) : `  ${sorted.slice(i, j).map((g) => g.name).join(", ")}: ${f.type}\n`;
    i = j;
  }
  return `${out}}\n`;
}

/** Compact SDL of one named type. */
export function printTypeCompact(type: GraphQLNamedType, o: PrintOptions = {}, includeDeprecated = false): string {
  return formatTypeSDL(typeDef(type, includeDeprecated), o.args ?? false, !(o.desc ?? false));
}

/** Names a type's (non-deprecated) fields, args, input fields and possible types (union members, implementers) point at, sorted. */
export function referencedTypeNames(type: GraphQLNamedType, schema: GraphQLSchema): string[] {
  const names = new Set<string>();
  if (isAbstractType(type)) for (const t of schema.getPossibleTypes(type)) names.add(t.name);
  if (isObjectType(type) || isInterfaceType(type)) {
    for (const f of Object.values(type.getFields())) {
      if (f.deprecationReason) continue;
      names.add(getNamedType(f.type).name);
      for (const a of f.args) names.add(getNamedType(a.type).name);
    }
  }
  if (isInputObjectType(type)) for (const f of Object.values(type.getFields())) names.add(getNamedType(f.type).name);
  return [...names].sort();
}

/**
 * Hop distance from every type to `target` through the reverse reference graph, up to `depth` hops (target = 0).
 * Builtin scalars and introspection types never act as referrers.
 */
export function targetDistances(schema: GraphQLSchema, target: string, depth: number): Record<string, number> {
  const reverse: Record<string, string[]> = {};
  for (const t of Object.values(schema.getTypeMap())) {
    if (t.name.startsWith("__") || (isScalarType(t) && isBuiltInScalar(t.name))) continue;
    for (const dep of referencedTypeNames(t, schema)) (reverse[dep] ??= []).push(t.name);
  }
  const dist: Record<string, number> = { [target]: 0 };
  let frontier = [target];
  for (let hops = 1; hops <= depth && frontier.length > 0; hops++) {
    const next: string[] = [];
    for (const name of frontier) {
      for (const referrer of reverse[name] ?? []) {
        if (referrer in dist) continue;
        dist[referrer] = hops;
        next.push(referrer);
      }
    }
    frontier = next;
  }
  return dist;
}

export interface FieldScore {
  /** 0: an argument reaches the target; 1: only the return type does. */
  locationRank: number;
  depth: number;
}

/** Whether (and how near) a field's args or return type reach the target; args win over the return type. */
export function fieldScore(f: FieldView, target: string, dist: Record<string, number>): FieldScore | undefined {
  const baseName = (ref: string) => ref.replaceAll(/[[\]!]/g, "");
  const hops = (ref: string) => (baseName(ref) === target ? 0 : dist[baseName(ref)]);
  const argDepths = f.args.map((a) => hops(a.type)).filter((d): d is number => d !== undefined);
  if (argDepths.length > 0) return { locationRank: 0, depth: Math.min(...argDepths) };
  const ret = hops(f.type);
  return ret === undefined ? undefined : { locationRank: 1, depth: ret };
}
