import { createContext, Script } from "node:vm";
import { operationKind } from "./operation.ts";
import { GraphQLResponseError, hasErrors, type GraphQLResponse, type JsonObject, type OperationKind } from "./types.ts";

export interface RequestInfo {
  /** Kind the document actually declares, not the helper the script called. Falls back to the helper's kind if unparsable. */
  type: OperationKind;
  operation: string;
  operationName?: string;
  variables: JsonObject;
}

/** Executes one operation; supplied by the host (HTTP client, in-process schema, mock...). */
export type ScriptExecutor = (req: { query: string; variables?: JsonObject; operationName?: string }, signal?: AbortSignal) => Promise<GraphQLResponse>;

export interface ScriptOptions {
  source: string;
  /** Function to call with `(gql, input)`. Default `run`. */
  functionName?: string;
  input?: JsonObject;
  execute: ScriptExecutor;
  /** Reject any operation whose document declares a mutation or subscription. */
  readOnly?: boolean;
  /** Like the CLI's `--strict` (default true): a response carrying `errors` makes `gql.query`/`gql.mutation` throw. `false` returns it for the script to inspect. */
  strict?: boolean;
  /** Cap on GraphQL operations per run. Default unlimited. */
  maxOperations?: number;
  /** Wall-clock deadline for the whole run, including awaited requests. */
  timeoutMs?: number;
  signal?: AbortSignal;
  /** Throw from here to block a request (e.g. human approval). Runs before every operation. */
  approve?: (info: RequestInfo) => void | Promise<void>;
  onResponse?: (info: RequestInfo, response: GraphQLResponse | undefined, error?: Error) => void;
}

export interface ScriptResult {
  /** JSON-normalized return value of the script function. */
  result: unknown;
  /** console.log/info/warn/error lines, in order. */
  logs: string[];
  /** Operations the script issued. */
  operations: number;
}

export interface EachOptions {
  concurrency?: number;
  /** Default true: the first failure stops the pool and is rethrown. `false` records failures and continues. */
  stopOnError?: boolean;
  onError?: (err: unknown, item: unknown, index: number) => void | Promise<void>;
}

export interface EachSummary {
  total: number;
  success: number;
  failed: number;
  errors: { index: number; error: string }[];
}

export class ScriptError extends Error {
  readonly logs: string[];
  constructor(message: string, logs: string[], cause?: unknown) {
    super(message, cause === undefined ? undefined : { cause });
    this.name = "ScriptError";
    this.logs = logs;
  }
}

/**
 * Runs a JavaScript workflow (`async function run(gql, input)`), the same programming model as `gqlcli script`.
 *
 * The script runs in a fresh `node:vm` context that exposes only `console`, `gql`, and standard JS built-ins: no
 * `require`, `process`, `fetch` or timers. `node:vm` is not a security boundary against hostile code; combine it with
 * `readOnly`, `maxOperations`, `approve` and `timeoutMs` for model-written scripts, and treat it as untrusted-input
 * *hygiene*, not isolation.
 */
export async function runScript(opts: ScriptOptions): Promise<ScriptResult> {
  const logs: string[] = [];
  const fmt = (args: unknown[]) => args.map((a) => (typeof a === "string" ? a : safeJson(a))).join(" ");
  const consoleShim = Object.fromEntries(["log", "info", "warn", "error", "debug"].map((k) => [k, (...a: unknown[]) => void logs.push(fmt(a))]));

  const deadline = new AbortController();
  const signals = [deadline.signal, ...(opts.signal ? [opts.signal] : [])];
  const signal = AbortSignal.any(signals);
  const timer = opts.timeoutMs && opts.timeoutMs > 0 ? setTimeout(() => deadline.abort(new Error(`script timed out after ${opts.timeoutMs}ms`)), opts.timeoutMs) : undefined;
  const aborted = Promise.withResolvers<never>();
  const onAbort = () => aborted.reject(signal.reason && typeof signal.reason === "object" && "message" in signal.reason ? signal.reason : new Error("script interrupted"));
  signal.addEventListener("abort", onAbort, { once: true });
  aborted.promise.catch(() => undefined);

  let operations = 0;
  const call = async (helper: OperationKind, query: unknown, variables?: unknown, operationName?: unknown): Promise<GraphQLResponse> => {
    if (signal.aborted) throw signal.reason;
    if (typeof query !== "string") throw new TypeError("query must be a string");
    const vars = JSON.parse(JSON.stringify(variables ?? {})) as JsonObject;
    const name = typeof operationName === "string" && operationName ? operationName : undefined;
    let type: OperationKind = helper;
    try {
      type = operationKind(query, name);
    } catch {
      // keep helper kind: the server will report the syntax error
    }
    const info: RequestInfo = { type, operation: query, variables: vars, ...(name ? { operationName: name } : {}) };
    if (opts.readOnly && type !== "query") throw new Error("mutations are disabled: script runner is read-only");
    if (opts.maxOperations !== undefined && operations >= opts.maxOperations) throw new Error("script exceeded its GraphQL operation budget");
    operations++;
    await opts.approve?.(info);
    let res: GraphQLResponse;
    try {
      res = await opts.execute({ query, ...(Object.keys(vars).length ? { variables: vars } : {}), ...(name ? { operationName: name } : {}) }, signal);
    } catch (err) {
      opts.onResponse?.(info, undefined, err as Error);
      throw err;
    }
    opts.onResponse?.(info, res);
    const cloned = JSON.parse(JSON.stringify(res)) as GraphQLResponse;
    if ((opts.strict ?? true) && hasErrors(cloned)) throw new GraphQLResponseError(cloned);
    return cloned;
  };

  const gql = {
    query: (q: unknown, v?: unknown, n?: unknown) => call("query", q, v, n),
    mutation: (q: unknown, v?: unknown, n?: unknown) => call("mutation", q, v, n),
    request: (r: unknown) => {
      if (!r || typeof r !== "object") throw new TypeError("request must be an object");
      const o = r as Record<string, unknown>;
      const type = o["type"] === "mutation" ? "mutation" : o["type"] === "subscription" ? "subscription" : "query";
      return call(type, o["query"] ?? o["mutation"], o["variables"], o["operationName"]);
    },
    each: (items: unknown, worker: unknown, options?: unknown) => each(items, worker, options as EachOptions | undefined, signal),
  };

  try {
    const sandbox = createContext({ console: consoleShim, gql: undefined });
    new Script(opts.source, { filename: "gqlcli-script.js" }).runInContext(sandbox, { timeout: opts.timeoutMs && opts.timeoutMs > 0 ? opts.timeoutMs : undefined });
    const fnName = opts.functionName ?? "run";
    const fn: unknown = new Script(`typeof ${fnName} === "function" ? ${fnName} : undefined`).runInContext(sandbox);
    if (typeof fn !== "function") throw new Error(`script does not define a function named "${fnName}"`);
    const input = JSON.parse(JSON.stringify(opts.input ?? {})) as JsonObject;
    // Invoke inside the context so `timeoutMs` also interrupts a synchronous busy loop in the entry function
    // (the vm timeout covers only synchronous execution; time spent after an await is bounded by the race below).
    Object.assign(sandbox, { __fn: fn, __gql: gql, __input: input });
    const started: unknown = new Script("__fn(__gql, __input)").runInContext(sandbox, { timeout: opts.timeoutMs && opts.timeoutMs > 0 ? opts.timeoutMs : undefined });
    const value: unknown = await Promise.race([Promise.resolve(started), aborted.promise]);
    return { result: value === undefined ? null : JSON.parse(JSON.stringify(value)), logs, operations };
  } catch (err) {
    throw new ScriptError(errorMessage(err), logs, err);
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", onAbort);
  }
}

/**
 * `gql.each`: bounded-concurrency worker pool with a summary. Same semantics as the Go runner: `stopOnError` defaults to
 * true (the first failure aborts the pool and is rethrown), failures are recorded as `String(err)` in occurrence order,
 * and `onError` is awaited with its own failures ignored.
 */
async function each(items: unknown, worker: unknown, o: EachOptions = {}, signal: AbortSignal): Promise<EachSummary> {
  if (!Array.isArray(items)) throw new TypeError("gql.each: items must be an array");
  if (typeof worker !== "function") throw new TypeError("gql.each: worker must be a function");
  const parsed = Number(o.concurrency);
  const concurrency = Number.isFinite(parsed) && parsed > 0 ? Math.max(1, Math.floor(parsed)) : 1;
  const stopOnError = o.stopOnError !== false;
  const summary: EachSummary = { total: items.length, success: 0, failed: 0, errors: [] };
  let next = 0;
  let aborted = false;
  const lane = async () => {
    while (!aborted && !signal.aborted) {
      const index = next++;
      if (index >= items.length) return;
      try {
        await worker(items[index], index);
        summary.success++;
      } catch (err) {
        summary.failed++;
        summary.errors.push({ index, error: String(err) });
        try {
          await o.onError?.(err, items[index], index);
        } catch {
          // onError failures are ignored
        }
        if (stopOnError) {
          aborted = true;
          throw err;
        }
      }
    }
  };
  const lanes = Array.from({ length: Math.min(concurrency, items.length) }, lane);
  if (stopOnError) await Promise.all(lanes);
  else await Promise.allSettled(lanes);
  if (signal.aborted) throw signal.reason;
  return summary;
}

/** Message of anything thrown, including errors from the vm realm where `instanceof Error` is false. */
function errorMessage(err: unknown): string {
  if (err && typeof err === "object" && "message" in err) return String(err.message);
  return String(err);
}

function safeJson(v: unknown): string {
  try {
    return JSON.stringify(v) ?? String(v);
  } catch {
    return String(v);
  }
}
