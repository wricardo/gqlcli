import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, readdirSync, renameSync, statSync, unlinkSync, writeFileSync } from "node:fs";
import { homedir, platform } from "node:os";
import { join } from "node:path";
import { getIntrospectionQuery } from "graphql";
import { TransportError, type GraphQLRequest, type GraphQLResponse, type JsonObject } from "./types.ts";

export interface SchemaCacheOptions {
  /** Time-to-live; 0 or negative disables the cache. Default 10 minutes. */
  ttlMs?: number;
  /** Directory for cache files. Default: OS user cache dir + `/gqlcli` (shared with the Go CLI). */
  dir?: string;
  /** Ignore any cached entry and introspect again (the result is still written). */
  refresh?: boolean;
}

export interface ClientOptions {
  url: string;
  headers?: Record<string, string>;
  /** Per-request deadline. Default: none. */
  timeoutMs?: number;
  /** Retries on transient failures (network error, 408, 429, 5xx). Default 0. */
  retry?: number;
  retryDelayMs?: number;
  schemaCache?: SchemaCacheOptions;
  /** Custom fetch, e.g. for self-signed TLS (undici dispatcher), proxies, or in-process tests. */
  fetch?: typeof fetch;
}

export interface RequestOptions {
  signal?: AbortSignal;
  /** Per-request headers; override client headers (case-insensitive). */
  headers?: Record<string, string>;
  timeoutMs?: number;
}

export interface RawResponse {
  status: number;
  statusText: string;
  headers: Record<string, string>;
  body: string;
}

export const DEFAULT_SCHEMA_CACHE_TTL_MS = 10 * 60 * 1000;
const SCHEMA_CACHE_RETENTION_MS = 24 * 60 * 60 * 1000;

export class GraphQLClient {
  readonly url: string;
  readonly headers: Record<string, string>;
  private readonly opts: ClientOptions;
  private readonly doFetch: typeof fetch;

  constructor(opts: ClientOptions) {
    if (!opts.url) throw new Error("GraphQL URL is not configured");
    this.opts = opts;
    this.url = opts.url;
    this.headers = { ...(opts.headers ?? {}) };
    this.doFetch = opts.fetch ?? fetch;
  }

  /** Executes one operation and returns the response envelope, including GraphQL `errors`. Throws TransportError for non-GraphQL failures. */
  async execute<TData = unknown>(req: GraphQLRequest, o: RequestOptions = {}): Promise<GraphQLResponse<TData>> {
    const raw = await this.post(JSON.stringify(req), "application/json", o);
    return parseGraphQLBody(raw) as GraphQLResponse<TData>;
  }

  /** Sends the request and returns status, headers and body untouched (curl -i style). */
  async post(body: string, contentType: string, o: RequestOptions = {}): Promise<RawResponse> {
    const headers = mergeHeaders(this.headers, o.headers, { "content-type": contentType, accept: "application/json, application/graphql-response+json" });
    const attempts = Math.max(0, this.opts.retry ?? 0) + 1;
    const delay = this.opts.retryDelayMs ?? 1000;
    let lastErr: unknown;
    for (let attempt = 1; attempt <= attempts; attempt++) {
      try {
        const timeout = o.timeoutMs ?? this.opts.timeoutMs;
        const signals: AbortSignal[] = [];
        if (o.signal) signals.push(o.signal);
        if (timeout && timeout > 0) signals.push(AbortSignal.timeout(timeout));
        const res = await this.doFetch(this.url, {
          method: "POST",
          headers,
          body,
          ...(signals.length ? { signal: AbortSignal.any(signals) } : {}),
        });
        const text = await res.text();
        const out: RawResponse = {
          status: res.status,
          statusText: res.statusText,
          headers: Object.fromEntries(res.headers.entries()),
          body: text,
        };
        const transient = res.status === 408 || res.status === 429 || res.status >= 500;
        if (transient && attempt < attempts && !looksLikeGraphQL(text)) {
          lastErr = new TransportError(`HTTP ${res.status}`, { status: res.status, body: text });
        } else {
          return out;
        }
      } catch (err) {
        if (o.signal?.aborted) throw err;
        lastErr = err instanceof TransportError ? err : new TransportError(`request failed: ${(err as Error).message}`, { cause: err });
        if (attempt >= attempts) break;
      }
      const backoff = Promise.withResolvers<void>();
      setTimeout(backoff.resolve, delay * attempt);
      await backoff.promise;
    }
    throw lastErr;
  }

  /** Introspection result as a `{data:{__schema}}` envelope, served from the on-disk cache when fresh. */
  async introspect(o: RequestOptions = {}): Promise<GraphQLResponse<{ __schema: unknown }>> {
    const cache = this.opts.schemaCache;
    const ttl = cache?.ttlMs ?? DEFAULT_SCHEMA_CACHE_TTL_MS;
    const path = ttl > 0 ? this.schemaCachePath(cache?.dir) : undefined;
    if (path && !cache?.refresh) {
      try {
        if (Date.now() - statSync(path).mtimeMs < ttl) {
          const cached = JSON.parse(readFileSync(path, "utf8")) as GraphQLResponse<{ __schema: unknown }>;
          if (cached.data?.__schema) return cached;
        }
      } catch {
        // miss or unreadable entry: introspect again
      }
    }
    const res = await this.execute<{ __schema: unknown }>(
      { query: getIntrospectionQuery({ descriptions: true, specifiedByUrl: true, directiveIsRepeatable: true, schemaDescription: true, inputValueDeprecation: true, oneOf: true }) },
      o,
    );
    if (path && res.data?.__schema && !res.errors?.length) {
      try {
        const dir = join(path, "..");
        mkdirSync(dir, { recursive: true, mode: 0o700 });
        const tmp = `${path}.${process.pid}.tmp`;
        writeFileSync(tmp, JSON.stringify(res), { mode: 0o600 });
        renameSync(tmp, path);
        pruneSchemaCache(dir, ttl);
      } catch {
        // a cache that cannot be written only costs the next call a round trip
      }
    }
    return res;
  }

  /** Cache file for this endpoint+credentials, same naming as the Go CLI so both share entries. Hashes headers; never writes them. */
  schemaCachePath(dirOverride?: string): string {
    const h = createHash("sha256");
    h.update(this.url);
    for (const k of Object.keys(this.headers).sort()) h.update(Buffer.concat([Buffer.from([0]), Buffer.from(k), Buffer.from([0]), Buffer.from(this.headers[k] ?? "")]));
    h.update(Buffer.from([0]));
    return join(dirOverride ?? defaultCacheDir(), `schema-${h.digest("hex").slice(0, 32)}.json`);
  }

  /** Deletes this endpoint's cached schema. Returns whether a file was removed. */
  clearSchemaCache(): boolean {
    try {
      unlinkSync(this.schemaCachePath(this.opts.schemaCache?.dir));
      return true;
    } catch {
      return false;
    }
  }
}

export function defaultCacheDir(): string {
  const xdg = process.env["XDG_CACHE_HOME"];
  if (platform() === "darwin") return join(homedir(), "Library", "Caches", "gqlcli");
  if (platform() === "win32") return join(process.env["LOCALAPPDATA"] ?? join(homedir(), "AppData", "Local"), "gqlcli");
  return join(xdg || join(homedir(), ".cache"), "gqlcli");
}

function pruneSchemaCache(dir: string, ttl: number): void {
  const keep = Math.max(SCHEMA_CACHE_RETENTION_MS, ttl);
  for (const f of readdirSync(dir)) {
    if (!/^schema-.*\.json$/.test(f)) continue;
    try {
      if (Date.now() - statSync(join(dir, f)).mtimeMs > keep) unlinkSync(join(dir, f));
    } catch {
      // raced with another process
    }
  }
}

/** Case-insensitive header merge; later sources win. */
export function mergeHeaders(...sources: (Record<string, string> | undefined)[]): Record<string, string> {
  const byLower = new Map<string, [string, string]>();
  for (const src of sources) {
    for (const [k, v] of Object.entries(src ?? {})) byLower.set(k.toLowerCase(), [k, v]);
  }
  return Object.fromEntries(byLower.values());
}

function looksLikeGraphQL(body: string): boolean {
  try {
    const j = JSON.parse(body) as JsonObject;
    return j !== null && typeof j === "object" && ("data" in j || "errors" in j);
  } catch {
    return false;
  }
}

/** Parses a response body as a GraphQL envelope; non-GraphQL bodies become TransportError. */
export function parseGraphQLBody(raw: RawResponse): GraphQLResponse {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw.body);
  } catch {
    throw new TransportError(`HTTP ${raw.status}: response is not JSON: ${raw.body.slice(0, 300)}`, { status: raw.status, body: raw.body });
  }
  if (parsed === null || typeof parsed !== "object" || !("data" in parsed || "errors" in parsed)) {
    throw new TransportError(`HTTP ${raw.status}: not a GraphQL response: ${raw.body.slice(0, 300)}`, { status: raw.status, body: raw.body });
  }
  return parsed as GraphQLResponse;
}
