import {
  astFromValue,
  getNamedType,
  isEnumType,
  isInputObjectType,
  isInterfaceType,
  isObjectType,
  isScalarType,
  isUnionType,
  print,
  type GraphQLArgument,
  type GraphQLField,
  type GraphQLInputField,
  type GraphQLNamedType,
  type GraphQLObjectType,
  type GraphQLSchema,
} from "graphql";

export interface PrintOptions {
  /** Show field argument signatures. */
  args?: boolean;
  /** Show descriptions as `#` comments. */
  desc?: boolean;
}

type DefaultInput = { value?: unknown; literal?: Parameters<typeof print>[0] } | undefined;

function defaultSuffix(item: GraphQLArgument | GraphQLInputField): string {
  const d = item.default as DefaultInput;
  if (!d) return "";
  const node = d.literal ?? astFromValue(d.value, item.type);
  return node ? ` = ${print(node)}` : "";
}

function argList(args: readonly GraphQLArgument[]): string {
  return args.length === 0 ? "" : `(${args.map((a) => `${a.name}: ${String(a.type)}${defaultSuffix(a)}`).join(", ")})`;
}

/** `name(arg: T = d): Ret @deprecated` for one object/interface field. */
export function fieldSignature(f: GraphQLField, withArgs = true): string {
  const dep = f.deprecationReason ? " @deprecated" : "";
  return `${f.name}${withArgs ? argList(f.args) : ""}: ${String(f.type)}${dep}`;
}

function comment(text: string | null | undefined, indent: string): string {
  return text ? text.trim().split("\n").map((l) => `${indent}# ${l}`).join("\n") + "\n" : "";
}

/** Compact SDL for one named type; omits builtin noise and (by default) args and descriptions. */
export function printTypeCompact(type: GraphQLNamedType, o: PrintOptions = {}): string {
  const head = o.desc ? comment(type.description, "") : "";
  if (isScalarType(type)) return `${head}scalar ${type.name}`;
  if (isEnumType(type)) {
    const vals = type.getValues().map((v) => `${comment(o.desc ? v.description : undefined, "  ")}  ${v.name}${v.deprecationReason ? " @deprecated" : ""}`);
    return `${head}enum ${type.name} {\n${vals.join("\n")}\n}`;
  }
  if (isUnionType(type)) return `${head}union ${type.name} = ${type.getTypes().map(String).join(" | ")}`;
  if (isInputObjectType(type)) {
    const fields = Object.values(type.getFields()).map((f) => `${comment(o.desc ? f.description : undefined, "  ")}  ${f.name}: ${String(f.type)}${defaultSuffix(f)}`);
    return `${head}input ${type.name}${type.isOneOf ? " @oneOf" : ""} {\n${fields.join("\n")}\n}`;
  }
  if (isObjectType(type) || isInterfaceType(type)) {
    const kw = isObjectType(type) ? "type" : "interface";
    const impl = type.getInterfaces().length ? ` implements ${type.getInterfaces().map(String).join(" & ")}` : "";
    const fields = Object.values(type.getFields()).map((f) => `${comment(o.desc ? f.description : undefined, "  ")}  ${fieldSignature(f, o.args)}`);
    return `${head}${kw} ${type.name}${impl} {\n${fields.join("\n")}\n}`;
  }
  throw new Error(`cannot print type ${String(type)}`);
}

/** Named, non-scalar types referenced by a type's fields, args, union members and interfaces. */
export function referencedTypeNames(type: GraphQLNamedType): string[] {
  const names = new Set<string>();
  const add = (t: Parameters<typeof getNamedType>[0]) => {
    const n = getNamedType(t);
    if (n && !isScalarType(n)) names.add(n.name);
  };
  if (isUnionType(type)) type.getTypes().forEach(add);
  if (isObjectType(type) || isInterfaceType(type)) {
    type.getInterfaces().forEach(add);
    for (const f of Object.values(type.getFields())) {
      add(f.type);
      f.args.forEach((a) => add(a.type));
    }
  }
  if (isInputObjectType(type)) Object.values(type.getFields()).forEach((f) => add(f.type));
  names.delete(type.name);
  return [...names];
}

export interface RootFields {
  query?: GraphQLObjectType | null;
  mutation?: GraphQLObjectType | null;
  subscription?: GraphQLObjectType | null;
}

/** Types referenced (directly) by an operation field: its return type and arg types, excluding scalars. */
function fieldRefs(f: GraphQLField): { ret?: string; args: string[] } {
  const ret = getNamedType(f.type);
  return {
    ...(ret && !isScalarType(ret) ? { ret: ret.name } : {}),
    args: f.args.map((a) => getNamedType(a.type)).filter((t) => t && !isScalarType(t)).map((t) => t!.name),
  };
}

/**
 * Reverse distance from every type to `target` in the "references" graph: target is 0, a type with a field or arg
 * naming target is 1, and so on, up to `maxHops`.
 */
export function distancesTo(schema: GraphQLSchema, target: string, maxHops: number): Record<string, number> {
  const referencedBy: Record<string, string[]> = {};
  for (const t of Object.values(schema.getTypeMap())) {
    if (t.name.startsWith("__")) continue;
    for (const r of referencedTypeNames(t)) (referencedBy[r] ??= []).push(t.name);
  }
  const dist: Record<string, number> = { [target]: 0 };
  let frontier = [target];
  for (let hop = 1; hop <= maxHops && frontier.length; hop++) {
    const next: string[] = [];
    for (const n of frontier) {
      for (const parent of referencedBy[n] ?? []) {
        if (!(parent in dist)) {
          dist[parent] = hop;
          next.push(parent);
        }
      }
    }
    frontier = next;
  }
  return dist;
}

export interface OperationMatch {
  root: "Query" | "Mutation" | "Subscription";
  field: GraphQLField;
  argMatch: boolean;
  distance: number;
}

/** Root operations whose args or return type reach `target` within `depth` hops; args first, then nearest return type. */
export function operationsReferencing(schema: GraphQLSchema, roots: RootFields, target: string, depth: number): OperationMatch[] {
  const dist = distancesTo(schema, target, Math.max(depth - 1, 0));
  const out: OperationMatch[] = [];
  const rootList: [OperationMatch["root"], GraphQLObjectType | null | undefined][] = [["Query", roots.query], ["Mutation", roots.mutation], ["Subscription", roots.subscription]];
  for (const [root, type] of rootList) {
    if (!type) continue;
    for (const field of Object.values(type.getFields())) {
      const refs = fieldRefs(field);
      const argDists = refs.args.map((n) => dist[n]).filter((d): d is number => d !== undefined);
      const retDist = refs.ret !== undefined ? dist[refs.ret] : undefined;
      const best = Math.min(...argDists, retDist ?? Infinity);
      if (best === Infinity) continue;
      out.push({ root, field, argMatch: argDists.length > 0, distance: retDist ?? best });
    }
  }
  return out.sort((a, b) => Number(b.argMatch) - Number(a.argMatch) || a.distance - b.distance || a.field.name.localeCompare(b.field.name));
}

/** Non-root object/interface/input types with a field that references `target` within `depth` hops. */
export function fieldsReferencing(schema: GraphQLSchema, roots: RootFields, target: string, depth: number): GraphQLNamedType[] {
  const dist = distancesTo(schema, target, Math.max(depth - 1, 0));
  const rootNames = [roots.query, roots.mutation, roots.subscription].filter(Boolean).map((t) => t!.name);
  return Object.values(schema.getTypeMap())
    .filter((t) => !t.name.startsWith("__") && t.name !== target && !rootNames.includes(t.name) && (isObjectType(t) || isInterfaceType(t) || isInputObjectType(t)))
    .filter((t) => referencedTypeNames(t).some((r) => dist[r] !== undefined))
    .sort((a, b) => a.name.localeCompare(b.name));
}
