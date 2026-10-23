import { createHash } from "node:crypto";
import { existsSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { isEnumType, isInputObjectType, isInterfaceType, isObjectType, isScalarType, isUnionType, type GraphQLNamedType } from "graphql";
import type { SchemaExplorer } from "./schema.ts";
import { fieldSignature, printTypeCompact } from "./typeprint.ts";

export const DEFAULT_EMBEDDING_BASE_URL = "https://z-ask.zasperhub.com/api/v1";
export const DEFAULT_EMBEDDING_MODEL = "nomic-embed-text-v1.5";
export const DEFAULT_EMBEDDING_MAX_CHARS = 8000;
const BUILTIN_SCALARS = ["String", "Int", "Float", "Boolean", "ID"];

export interface Embedder {
  readonly model: string;
  embed(text: string, signal?: AbortSignal): Promise<number[]>;
  /** For models that embed queries differently from documents. */
  embedQuery?(text: string, signal?: AbortSignal): Promise<number[]>;
}

export interface VenuEmbedderOptions {
  baseUrl?: string;
  apiKey?: string;
  model?: string;
  /** Task prefixes; defaults to nomic's `search_document: ` / `search_query: ` when the model name contains "nomic". */
  documentPrefix?: string;
  queryPrefix?: string;
  fetch?: typeof fetch;
}

/** Embedding provider for the Venu API (`POST {base}/embeddings`, `X-API-Key`). Same env vars as the Go CLI. */
export class VenuEmbedder implements Embedder {
  readonly model: string;
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly docPrefix: string;
  private readonly queryPrefix: string;
  private readonly doFetch: typeof fetch;

  constructor(o: VenuEmbedderOptions = {}) {
    this.baseUrl = (o.baseUrl ?? process.env["VENU_URL"] ?? DEFAULT_EMBEDDING_BASE_URL).replace(/\/$/, "");
    this.model = o.model ?? process.env["VENU_EMBEDDING_MODEL"] ?? DEFAULT_EMBEDDING_MODEL;
    const key = o.apiKey ?? process.env["VENU_API_KEY"];
    if (!key) throw new Error("embedding API key missing: set VENU_API_KEY or pass apiKey");
    this.apiKey = key;
    const nomic = this.model.toLowerCase().includes("nomic");
    this.docPrefix = o.documentPrefix ?? (nomic ? "search_document: " : "");
    this.queryPrefix = o.queryPrefix ?? (nomic ? "search_query: " : "");
    this.doFetch = o.fetch ?? fetch;
  }

  embed(text: string, signal?: AbortSignal): Promise<number[]> {
    return this.request(this.docPrefix, text, signal);
  }

  embedQuery(text: string, signal?: AbortSignal): Promise<number[]> {
    return this.request(this.queryPrefix, text, signal);
  }

  private async request(prefix: string, text: string, signal?: AbortSignal): Promise<number[]> {
    if (!text.trim()) throw new Error("embed: text is empty");
    const res = await this.doFetch(`${this.baseUrl}/embeddings`, {
      method: "POST",
      headers: { "content-type": "application/json", "x-api-key": this.apiKey },
      body: JSON.stringify({ model: this.model, text: prefix + text }),
      signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(60_000)]) : AbortSignal.timeout(60_000),
    });
    const raw = await res.text();
    let parsed: { embedding?: number[]; answer?: string; code?: string };
    try {
      parsed = JSON.parse(raw);
    } catch {
      throw new Error(`embed: HTTP ${res.status}, unparseable response: ${raw.slice(0, 300)}`);
    }
    if (parsed.answer && !parsed.embedding?.length) throw new Error(`embed: ${parsed.answer} (${parsed.code})`);
    if (!res.ok) throw new Error(`embed: HTTP ${res.status}: ${raw.slice(0, 300)}`);
    if (!parsed.embedding?.length) throw new Error("embed: response contained no embedding");
    return parsed.embedding;
  }
}

export interface IndexedEntry {
  name: string;
  description?: string;
  sdl: string;
  text_hash: string;
  embedding: number[];
}
export interface TypeEmbedding extends IndexedEntry {
  kind: string;
}
export interface OperationEmbedding extends IndexedEntry {
  return_type: string;
}

/** On-disk format; same JSON shape as the Go CLI's `.gqlcli-embeddings.json`. */
export interface EmbeddingIndexFile {
  version: number;
  env?: string;
  endpoint?: string;
  embedding_endpoint?: string;
  model: string;
  dim: number;
  created_at: string;
  queries?: OperationEmbedding[];
  mutations?: OperationEmbedding[];
  types: TypeEmbedding[];
}

export interface BuildIndexOptions {
  env?: string;
  endpoint?: string;
  /** Restrict the types category to these kinds (OBJECT, INPUT_OBJECT, ...). */
  kinds?: string[];
  /** Name globs (`*Connection`) removed from every category. */
  exclude?: string[];
  /** Include argument signatures in indexed type SDL. */
  showArgs?: boolean;
  noQueries?: boolean;
  noMutations?: boolean;
  /** Re-embed everything instead of reusing entries whose text hash is unchanged. */
  force?: boolean;
  /** Previous index to reuse vectors from. */
  previous?: EmbeddingIndex;
  concurrency?: number;
  maxChars?: number;
  signal?: AbortSignal;
  onProgress?: (done: number, total: number, name: string, reused: boolean) => void;
}

export interface TypeMatch { name: string; kind: string; description?: string; sdl: string; score: number }
export interface OperationMatch { name: string; returnType: string; description?: string; sdl: string; score: number }
export interface SearchResults { queries: OperationMatch[]; mutations: OperationMatch[]; types: TypeMatch[] }

interface Entry {
  bucket: "query" | "mutation" | "type";
  name: string;
  kind: string;
  returnType: string;
  description: string;
  sdl: string;
  text: string;
  hash: string;
}

export function embeddingIndexPath(env?: string): string {
  const e = env?.trim();
  return e ? `.gqlcli-embeddings.${e}.json` : ".gqlcli-embeddings.json";
}

function globMatch(name: string, globs: string[]): boolean {
  return globs.some((g) => new RegExp(`^${g.replace(/[.+^${}()|[\]\\]/g, "\\$&").replaceAll("*", ".*").replaceAll("?", ".")}$`).test(name));
}

function embedText(bucket: string, name: string, description: string, sdl: string, maxChars: number): string {
  const text = `GraphQL ${bucket}: ${name}\n${description ? `Description: ${description}\n` : ""}${sdl}`;
  return maxChars > 0 && text.length > maxChars ? text.slice(0, maxChars) : text;
}

function normalize(v: number[]): number[] {
  const norm = Math.sqrt(v.reduce((s, x) => s + x * x, 0));
  return norm === 0 || Math.abs(norm - 1) < 1e-6 ? v : v.map((x) => x / norm);
}

function rank<T extends IndexedEntry, M>(items: T[], q: number[], topN: number, toMatch: (t: T, score: number) => M): M[] {
  return items
    .filter((t) => t.embedding.length === q.length)
    .map((t) => ({ t, score: t.embedding.reduce((s, x, i) => s + x * (q[i] ?? 0), 0) }))
    .sort((a, b) => b.score - a.score || a.t.name.localeCompare(b.t.name))
    .slice(0, topN)
    .map((x) => toMatch(x.t, x.score));
}

/** Semantic index over a schema's queries, mutations and types; search is split into those three categories. */
export class EmbeddingIndex {
  private embedder: Embedder | undefined;
  readonly file: EmbeddingIndexFile;

  constructor(file: EmbeddingIndexFile, embedder?: Embedder) {
    this.file = file;
    this.embedder = embedder;
    for (const e of [...(file.queries ?? []), ...(file.mutations ?? []), ...file.types]) e.embedding = normalize(e.embedding);
  }

  static load(path: string, embedder?: Embedder): EmbeddingIndex {
    return new EmbeddingIndex(JSON.parse(readFileSync(path, "utf8")) as EmbeddingIndexFile, embedder);
  }

  static loadIfExists(path: string, embedder?: Embedder): EmbeddingIndex | undefined {
    return existsSync(path) ? EmbeddingIndex.load(path, embedder) : undefined;
  }

  save(path: string): void {
    const tmp = `${path}.${process.pid}.tmp`;
    writeFileSync(tmp, JSON.stringify(this.file));
    renameSync(tmp, path);
  }

  setEmbedder(e: Embedder): void {
    this.embedder = e;
  }

  static async build(explorer: SchemaExplorer, embedder: Embedder, o: BuildIndexOptions = {}): Promise<EmbeddingIndex> {
    const maxChars = o.maxChars ?? DEFAULT_EMBEDDING_MAX_CHARS;
    const exclude = o.exclude ?? [];
    const entries: Entry[] = [];
    const rootNames = [explorer.roots.query, explorer.roots.mutation, explorer.roots.subscription].filter(Boolean).map((t) => t!.name);

    const ops: ["query" | "mutation", boolean | undefined][] = [["query", o.noQueries], ["mutation", o.noMutations]];
    for (const [bucket, skip] of ops) {
      const root = explorer.roots[bucket];
      if (!root || skip) continue;
      for (const f of Object.values(root.getFields())) {
        if (globMatch(f.name, exclude)) continue;
        const sdl = fieldSignature(f, true);
        const description = f.description?.trim() ?? "";
        const text = embedText(bucket, f.name, description, sdl, maxChars);
        entries.push({ bucket, name: f.name, kind: "", returnType: String(f.type), description, sdl, text, hash: `sha256:${createHash("sha256").update(text).digest("hex")}` });
      }
    }
    const kinds = o.kinds?.map((k) => k.toUpperCase());
    const typeKind = (t: GraphQLNamedType): string =>
      isObjectType(t) ? "OBJECT" : isInterfaceType(t) ? "INTERFACE" : isInputObjectType(t) ? "INPUT_OBJECT" : isEnumType(t) ? "ENUM" : isUnionType(t) ? "UNION" : "SCALAR";
    for (const t of Object.values(explorer.schema.getTypeMap())) {
      if (t.name.startsWith("__") || rootNames.includes(t.name) || (isScalarType(t) && BUILTIN_SCALARS.includes(t.name)) || globMatch(t.name, exclude)) continue;
      const kind = typeKind(t);
      if (kinds && !kinds.includes(kind)) continue;
      const sdl = printTypeCompact(t, { args: o.showArgs ?? false });
      const description = t.description?.trim() ?? "";
      const text = embedText("type", t.name, description, sdl, maxChars);
      entries.push({ bucket: "type", name: t.name, kind, returnType: "", description, sdl, text, hash: `sha256:${createHash("sha256").update(text).digest("hex")}` });
    }

    const reusable: Record<string, number[]> = {};
    if (!o.force && o.previous && o.previous.file.model === embedder.model) {
      for (const e of [...(o.previous.file.queries ?? []), ...(o.previous.file.mutations ?? []), ...o.previous.file.types]) reusable[e.text_hash] = e.embedding;
    }
    const vectors: number[][] = new Array(entries.length);
    let next = 0;
    let done = 0;
    const lane = async () => {
      for (;;) {
        if (o.signal?.aborted) throw o.signal.reason;
        const i = next++;
        const e = entries[i];
        if (!e) return;
        const cached = reusable[e.hash];
        vectors[i] = cached ?? (await embedder.embed(e.text, o.signal));
        o.onProgress?.(++done, entries.length, e.name, cached !== undefined);
      }
    };
    await Promise.all(Array.from({ length: Math.max(1, o.concurrency ?? 4) }, lane));

    const file: EmbeddingIndexFile = { version: 1, model: embedder.model, dim: vectors[0]?.length ?? 0, created_at: new Date().toISOString(), types: [] };
    if (o.env) file.env = o.env;
    if (o.endpoint) file.endpoint = o.endpoint;
    entries.forEach((e, i) => {
      const base = { name: e.name, ...(e.description ? { description: e.description } : {}), sdl: e.sdl, text_hash: e.hash, embedding: vectors[i]! };
      if (e.bucket === "type") file.types.push({ ...base, kind: e.kind });
      else (e.bucket === "query" ? (file.queries ??= []) : (file.mutations ??= [])).push({ ...base, return_type: e.returnType });
    });
    return new EmbeddingIndex(file, embedder);
  }

  /** Embeds `text` once and returns the top `topN` of each category. Refuses when the embedder's model differs from the index's. */
  async search(text: string, topN = 5, signal?: AbortSignal): Promise<SearchResults> {
    if (!this.embedder) throw new Error("embedding search: no embedder set; call setEmbedder()");
    if (this.embedder.model !== this.file.model) throw new Error(`embedding search: index was built with model "${this.file.model}" but the embedder uses "${this.embedder.model}"; scores across vector spaces are meaningless`);
    const vec = await (this.embedder.embedQuery ?? this.embedder.embed).call(this.embedder, text, signal);
    return this.searchVector(vec, topN);
  }

  searchVector(vec: number[], topN = 5): SearchResults {
    if (vec.length === 0) throw new Error("embedding search: query vector is empty");
    if (this.file.dim > 0 && vec.length !== this.file.dim) throw new Error(`embedding search: query vector has ${vec.length} dimensions, index has ${this.file.dim}`);
    const q = normalize([...vec]);
    const op = (e: OperationEmbedding, score: number): OperationMatch => ({ name: e.name, returnType: e.return_type, ...(e.description ? { description: e.description } : {}), sdl: e.sdl, score });
    return {
      queries: rank(this.file.queries ?? [], q, topN, op),
      mutations: rank(this.file.mutations ?? [], q, topN, op),
      types: rank(this.file.types, q, topN, (e, score): TypeMatch => ({ name: e.name, kind: e.kind, ...(e.description ? { description: e.description } : {}), sdl: e.sdl, score })),
    };
  }
}
