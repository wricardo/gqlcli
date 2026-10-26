import { readFileSync } from "node:fs";
import { Kind, parse, print, type DefinitionNode, type FieldDefinitionNode, type InputValueDefinitionNode, type TypeNode } from "graphql";

const sdl = readFileSync(new URL("./schema.graphql", import.meta.url), "utf8");

/** The full explorer GraphQL schema (what `gqlcli explore --print-schema` prints). */
export const explorerSDL = (): string => sdl;

const typeName = (t: TypeNode): string => (t.kind === Kind.NAMED_TYPE ? t.name.value : typeName(t.type));

/**
 * Compact explorer schema for one Query root field (what `--print-schema-field` prints): the field plus the types it
 * references. Composite-typed child fields are only followed from types directly referenced by the field, which keeps
 * hub types such as SchemaType from pulling in the whole schema.
 */
export function explorerFieldSDL(field: string): string {
  const doc = parse(sdl);
  const defs = new Map<string, DefinitionNode & { name: { value: string } }>();
  for (const d of doc.definitions) if ("name" in d && d.name) defs.set(d.name.value, d as never);
  const query = defs.get("Query");
  if (query?.kind !== Kind.OBJECT_TYPE_DEFINITION) throw new Error("explorer schema has no Query type");
  const root = query.fields?.find((f) => f.name.value === field);
  if (!root) throw new Error(`unknown explorer root field "${field}" (available: ${(query.fields ?? []).map((f) => f.name.value).join(", ")})`);

  const isComposite = (name: string) => {
    const k = defs.get(name)?.kind;
    return k === Kind.OBJECT_TYPE_DEFINITION || k === Kind.INTERFACE_TYPE_DEFINITION || k === Kind.UNION_TYPE_DEFINITION;
  };
  const out: DefinitionNode[] = [{ ...query, fields: [root] }];
  const seen = new Set(["Query"]);
  const queue: { name: string; depth: number }[] = [];
  const refs = (f: FieldDefinitionNode | InputValueDefinitionNode, depth: number) => {
    queue.push({ name: typeName(f.type), depth });
    if ("arguments" in f) for (const a of f.arguments ?? []) queue.push({ name: typeName(a.type), depth });
  };
  refs(root, 1);
  while (queue.length) {
    const { name, depth } = queue.shift()!;
    const def = defs.get(name);
    if (!def || seen.has(name)) continue;
    seen.add(name);
    let next: DefinitionNode = def;
    if (def.kind === Kind.OBJECT_TYPE_DEFINITION || def.kind === Kind.INTERFACE_TYPE_DEFINITION) {
      const kept = (def.fields ?? []).filter((f) => !isComposite(typeName(f.type)) || depth <= 1);
      kept.forEach((f) => refs(f, depth + 1));
      next = { ...def, fields: kept };
      for (const i of def.interfaces ?? []) queue.push({ name: i.name.value, depth: depth + 1 });
    } else if (def.kind === Kind.INPUT_OBJECT_TYPE_DEFINITION) {
      (def.fields ?? []).forEach((f) => refs(f, depth + 1));
    } else if (def.kind === Kind.UNION_TYPE_DEFINITION) {
      for (const m of def.types ?? []) queue.push({ name: m.name.value, depth: depth + 1 });
    }
    out.push(next);
  }
  return print({ kind: Kind.DOCUMENT, definitions: out }) + "\n";
}
