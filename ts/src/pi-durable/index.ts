import { Type } from "@earendil-works/pi-ai";
import { defineExtension, defineTool, section, type Extension, type ToolExecutionResult, type ToolRegistration } from "@earendil-works/pi-durable";
import { executeBatch } from "../batch.ts";
import { explorerFieldSDL, explorerSDL } from "../explorer/sdl.ts";
import { jq as runJq } from "../jq.ts";
import { formatOutput, OUTPUT_FORMATS, type OutputFormat } from "../format.ts";
import { Gqlcli, type ExecutionResult, type GqlcliOptions } from "../gqlcli.ts";
import { operationKind } from "../operation.ts";
import { ScriptError } from "../script.ts";
import { TransportError, type GraphQLRequest, type JsonObject } from "../types.ts";

export interface GqlcliToolsOptions {
  /** A ready handle, or options/factory for one. A factory is called lazily on first use, so a durable restart rebuilds it. */
  gqlcli?: Gqlcli | GqlcliOptions | (() => Gqlcli);
  /** Tool name prefix. Default `gql_`. */
  prefix?: string;
  /**
   * Offer `mutation` and a script tool that may mutate. Default false: the extension is then read-only, every tool is
   * `replay: "safe"`, and scripts are forced read-only. Set true to opt in; mutation and script tools stay non-replayable,
   * so Pi Durable reports an interrupted write to the model instead of repeating it.
   */
  mutations?: boolean;
  /** Output encoding for structured results. Default `toon` (token-efficient). */
  format?: OutputFormat;
  /** Cap on GraphQL operations per script run. Default 50. */
  scriptMaxOperations?: number;
  /** Wall-clock cap per script run. Default 60s. */
  scriptTimeoutMs?: number;
}

type Out = ToolExecutionResult;

const ok = (text: string): Out => ({ output: [{ type: "text", text }] });
const fail = (text: string): Out => ({ isError: true, output: [{ type: "text", text }] });

const Format = Type.Optional(Type.Union(OUTPUT_FORMATS.map((f) => Type.Literal(f)), { description: "Output encoding override" }));
const Vars = Type.Optional(Type.Record(Type.String(), Type.Unknown(), { description: "GraphQL variables" }));

/** Tools mirroring the gqlcli commands: explore (explore, queries, mutations, types, describe, sdl, validate), run (query, batch, subscribe, script, mutation). */
export function createGqlcliTools(opts: GqlcliToolsOptions = {}): ToolRegistration[] {
  const p = opts.prefix ?? "gql_";
  const allowMutations = opts.mutations ?? false;
  const defaultFormat = opts.format ?? "toon";
  let handle: Gqlcli | undefined;
  const g = (): Gqlcli => {
    const src = opts.gqlcli;
    handle ??= src instanceof Gqlcli ? src : typeof src === "function" ? src() : new Gqlcli(src);
    return handle;
  };
  const fmt = (value: unknown, f?: OutputFormat) => formatOutput(value, f ?? defaultFormat);

  /** Execution result to tool output: errors flip isError but keep the body, plus SDL hints for validation problems. */
  const present = async (res: ExecutionResult, document: string | undefined, f?: OutputFormat): Promise<Out> => {
    const body = res.jq ?? res.response;
    if (!res.hasErrors) return ok(fmt(body, f));
    let hints = "";
    if (document) {
      try {
        const v = (await g().schema()).validate(document);
        const lines = v.errors.map((e) => `${e.line ?? "?"}:${e.column ?? "?"}: ${e.message}${e.schemaHint ? `\n${e.schemaHint}` : ""}`);
        if (lines.length) hints = `\n\nValidation against the schema:\n${lines.join("\n")}`;
      } catch {
        // hints are best-effort; the original errors are what matter
      }
    }
    return fail(fmt(res.response, f) + hints);
  };

  const guard = async (run: () => Promise<Out>): Promise<Out> => {
    try {
      return await run();
    } catch (err) {
      if (err instanceof ScriptError) return fail(`${err.message}${err.logs.length ? `\n\nlogs:\n${err.logs.join("\n")}` : ""}`);
      if (err instanceof TransportError) return fail(err.message);
      if (err instanceof Error && err.name === "AbortError") throw err;
      return fail(err instanceof Error ? err.message : String(err));
    }
  };

  const listArgs = {
    filter: Type.Optional(Type.String({ description: "Case-insensitive substring of the field name" })),
    args: Type.Optional(Type.Boolean({ description: "Include argument signatures" })),
    desc: Type.Optional(Type.Boolean({ description: "Include descriptions" })),
    depth: Type.Optional(Type.Integer({ minimum: 0, description: "Also return SDL of referenced arg/return types this many hops deep" })),
    format: Format,
  };

  const queries = defineTool({
    name: `${p}queries`,
    description: "List the GraphQL API's Query root fields. Start here to discover what can be read; use filter, args and depth to learn an operation in one call.",
    parameters: Type.Object(listArgs),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        const ex = await g().schema({ ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}) });
        return ok(fmt(ex.operations("query", { ...(a.filter ? { filter: a.filter } : {}), args: a.args ?? false, desc: a.desc ?? false, depth: a.depth ?? 0 }), a.format));
      }),
  });

  const mutationsList = defineTool({
    name: `${p}mutations`,
    description: "List the GraphQL API's Mutation root fields (discovery only; nothing is executed).",
    parameters: Type.Object(listArgs),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        const ex = await g().schema({ ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}) });
        return ok(fmt(ex.operations("mutation", { ...(a.filter ? { filter: a.filter } : {}), args: a.args ?? false, desc: a.desc ?? false, depth: a.depth ?? 0 }), a.format));
      }),
  });

  const types = defineTool({
    name: `${p}types`,
    description: "List schema types, optionally filtered by name substring or kind (OBJECT, ENUM, INPUT_OBJECT, SCALAR, INTERFACE, UNION).",
    parameters: Type.Object({
      filter: Type.Optional(Type.String()),
      kind: Type.Optional(Type.String()),
      args: Type.Optional(Type.Boolean({ description: "Include SDL with field argument signatures" })),
      desc: Type.Optional(Type.Boolean({ description: "Include SDL with descriptions" })),
      format: Format,
    }),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        const ex = await g().schema({ ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}) });
        return ok(fmt(ex.types({ ...(a.filter ? { filter: a.filter } : {}), ...(a.kind ? { kind: a.kind } : {}), args: a.args ?? false, desc: a.desc ?? false }), a.format));
      }),
  });

  const describe = defineTool({
    name: `${p}describe`,
    description:
      "Print compact SDL for a type, or for exactly one operation with `Type.field` (e.g. `Query.user`). depth>=1 also includes referenced types and the operations/fields that reference it.",
    parameters: Type.Object({
      name: Type.String({ description: "Type name, or Type.field such as Query.smsCampaign" }),
      args: Type.Optional(Type.Boolean()),
      desc: Type.Optional(Type.Boolean()),
      depth: Type.Optional(Type.Integer({ minimum: 0 })),
      maxOpRefs: Type.Optional(Type.Integer({ minimum: 0, description: "Cap on referencing operations; default 5, 0 = unlimited" })),
      maxFieldRefs: Type.Optional(Type.Integer({ minimum: 0, description: "Cap on referencing types; default 5, 0 = unlimited" })),
    }),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        const ex = await g().schema({ ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}) });
        return ok(ex.describe(a.name, { args: a.args ?? true, desc: a.desc ?? false, depth: a.depth ?? 0, ...(a.maxOpRefs !== undefined ? { maxOpRefs: a.maxOpRefs } : {}), ...(a.maxFieldRefs !== undefined ? { maxFieldRefs: a.maxFieldRefs } : {}) }));
      }),
  });

  const sdl = defineTool({
    name: `${p}sdl`,
    description: "Print the whole schema as SDL. Large; prefer describe/queries/types unless the full schema is needed.",
    parameters: Type.Object({}),
    replay: "safe",
    execute: (_a, _api, ctx) => guard(async () => ok((await g().schema({ ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}) })).sdl())),
  });

  const validate = defineTool({
    name: `${p}validate`,
    description: "Check a GraphQL document against the schema without executing it. Reports line:column errors with SDL for the type involved. Run before executing unfamiliar operations.",
    parameters: Type.Object({ query: Type.String({ description: "Query, mutation or subscription document" }) }),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        const r = (await g().schema({ ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}) })).validate(a.query);
        if (r.valid) return ok("valid");
        return fail(r.errors.map((e) => `${e.line ?? "?"}:${e.column ?? "?"}: ${e.message}${e.schemaHint ? `\n${e.schemaHint}` : ""}`).join("\n\n"));
      }),
  });

  const explore = defineTool({
    name: `${p}explore`,
    description:
      "Run a GraphQL document against the local explorer schema (not the target API) to answer structural questions about the target schema, e.g. `paths` between types/operations, `reachableFields`, `operations`. Executed locally from the cached schema. Use printSchema or printSchemaField (e.g. `paths`) first to learn the explorer API.",
    parameters: Type.Object({
      query: Type.Optional(Type.String({ description: "Explorer GraphQL document" })),
      variables: Vars,
      operation: Type.Optional(Type.String({ description: "Operation to run in a multi-operation document" })),
      printSchema: Type.Optional(Type.Boolean({ description: "Return the full explorer GraphQL schema instead of executing" })),
      printSchemaField: Type.Optional(Type.String({ description: "Return a compact explorer schema for one Query root field (e.g. paths) instead of executing" })),
      jq: Type.Optional(Type.String({ description: "jq program applied to the response (skipped when it has errors). Paths start at .data" })),
      format: Format,
    }),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        if (a.printSchemaField) return ok(explorerFieldSDL(a.printSchemaField));
        if (a.printSchema) return ok(explorerSDL());
        if (!a.query) return fail("query is required unless printSchema or printSchemaField is set");
        const res = await g().explore(a.query, a.variables as Record<string, unknown> | undefined, a.operation);
        const hasErrors = !!res.errors?.length;
        let body: unknown = res;
        if (a.jq && !hasErrors) body = await runJq(res, a.jq);
        return hasErrors ? fail(fmt(JSON.parse(JSON.stringify(res)), a.format)) : ok(fmt(JSON.parse(JSON.stringify(body)), a.format));
      }),
  });

  const opParams = {
    query: Type.Optional(Type.String({ description: "GraphQL document" })),
    op: Type.Optional(Type.String({ description: "Saved operation name from .gqlcli.json (instead of query)" })),
    variables: Vars,
    operationName: Type.Optional(Type.String({ description: "Operation to run in a multi-operation document" })),
    jq: Type.Optional(Type.String({ description: "jq program applied to the response (skipped when it has errors). Paths start at .data" })),
    format: Format,
  };

  const query = defineTool({
    name: `${p}query`,
    description: "Execute a GraphQL query. Documents that declare a mutation or subscription are rejected. Use jq to return only what you need.",
    parameters: Type.Object(opParams),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        const res = await g().query(
          { ...(a.query ? { query: a.query } : {}), ...(a.op ? { op: a.op } : {}), ...(a.variables ? { variables: a.variables as JsonObject } : {}), ...(a.operationName ? { operationName: a.operationName } : {}), ...(a.jq ? { jq: a.jq } : {}) },
          ctx.abortSignal ? { signal: ctx.abortSignal } : {},
        );
        return present(res, a.query, a.format);
      }),
  });

  const mutation = defineTool({
    name: `${p}mutation`,
    description: "Execute a GraphQL mutation. NOT replayed after a crash: an interrupted call is reported, so check state before retrying. `input` is wrapped as {input: ...}.",
    parameters: Type.Object({ ...opParams, input: Type.Optional(Type.Record(Type.String(), Type.Unknown(), { description: "Wrapped as variables.input" })) }),
    execute: (a, _api, ctx) =>
      guard(async () => {
        const res = await g().mutation(
          {
            ...(a.query ? { query: a.query } : {}),
            ...(a.op ? { op: a.op } : {}),
            ...(a.variables ? { variables: a.variables as JsonObject } : {}),
            ...(a.input ? { input: a.input as JsonObject } : {}),
            ...(a.operationName ? { operationName: a.operationName } : {}),
            ...(a.jq ? { jq: a.jq } : {}),
          },
          ctx.abortSignal ? { signal: ctx.abortSignal } : {},
        );
        return present(res, a.query, a.format);
      }),
  });

  const batch = defineTool({
    name: `${p}batch`,
    description: "Run many independent QUERIES (not mutations) in one request, or sequential chunks with batchSize. For dependent calls or loops use the script tool.",
    parameters: Type.Object({
      operations: Type.Array(Type.Object({ query: Type.String(), variables: Vars, operationName: Type.Optional(Type.String()) }), { minItems: 1 }),
      transport: Type.Optional(Type.Union([Type.Literal("ndjson"), Type.Literal("array")])),
      batchSize: Type.Optional(Type.Integer({ minimum: 0, description: "Max operations per HTTP request; 0 = all in one" })),
      jq: Type.Optional(Type.String({ description: "Client-side jq applied to each response" })),
      format: Format,
    }),
    replay: "safe",
    execute: (a, _api, ctx) =>
      guard(async () => {
        for (const [i, o] of a.operations.entries()) {
          const kind = operationKind(o.query, o.operationName);
          if (kind !== "query") return fail(`operations[${i}] is a ${kind}; ${p}batch only runs queries`);
        }
        const results = await executeBatch(g().client, a.operations as GraphQLRequest[], {
          ...(a.transport ? { transport: a.transport } : {}),
          ...(a.batchSize !== undefined ? { batchSize: a.batchSize } : {}),
          ...(a.jq ? { jq: a.jq } : {}),
          ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}),
        });
        return ok(fmt(results, a.format));
      }),
  });

  const subscribeTool = defineTool({
    name: `${p}subscribe`,
    description: "Listen to a GraphQL subscription over WebSocket and return the events seen before maxEvents or timeoutMs.",
    parameters: Type.Object({
      query: Type.String({ description: "Subscription document" }),
      variables: Vars,
      operationName: Type.Optional(Type.String()),
      maxEvents: Type.Optional(Type.Integer({ minimum: 1, maximum: 100, description: "Default 10" })),
      timeoutMs: Type.Optional(Type.Integer({ minimum: 100, maximum: 120_000, description: "Default 30000" })),
      format: Format,
    }),
    replay: "safe",
    execute: (a, api, ctx) =>
      guard(async () => {
        if (operationKind(a.query, a.operationName) !== "subscription") return fail("document is not a subscription");
        const r = await g().subscribe({
          query: a.query,
          ...(a.variables ? { variables: a.variables as JsonObject } : {}),
          ...(a.operationName ? { operationName: a.operationName } : {}),
          ...(a.maxEvents ? { maxEvents: a.maxEvents } : {}),
          ...(a.timeoutMs ? { timeoutMs: a.timeoutMs } : {}),
          ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}),
          onEvent: (e) => api.output(`${e.type}\n`),
        });
        return ok(fmt(r, a.format));
      }),
  });

  const script = defineTool({
    name: `${p}script`,
    description:
      `Run a JavaScript workflow for loops, branching or dependent GraphQL calls: \`async function run(gql, input) { ... }\` with gql.query(q, vars?, opName?), ${allowMutations ? "gql.mutation(m, vars?, opName?), " : ""}gql.each(items, worker, {concurrency, stopOnError}). Return the result; console.log is captured. ` +
      (allowMutations ? "Operations may mutate, and the script is NOT replayed after a crash." : "Read-only: mutations are rejected."),
    parameters: Type.Object({
      source: Type.Optional(Type.String({ description: "Script source defining the entry function" })),
      name: Type.Optional(Type.String({ description: "Saved script from .gqlcli.json (instead of source)" })),
      functionName: Type.Optional(Type.String({ description: "Default: run" })),
      strict: Type.Optional(Type.Boolean({ description: "Default true: a response with GraphQL errors makes gql.query/mutation throw. false returns it for the script to inspect." })),
      input: Type.Optional(Type.Record(Type.String(), Type.Unknown())),
      format: Format,
    }),
    ...(allowMutations ? {} : { replay: "safe" as const }),
    execute: (a, api, ctx) =>
      guard(async () => {
        if (!a.source === !a.name) return fail("pass exactly one of source or name");
        const r = await g().script(
          a.name ? { name: a.name } : { source: a.source!, ...(a.functionName ? { functionName: a.functionName } : {}) },
          {
            ...(a.input ? { input: a.input as JsonObject } : {}),
            readOnly: !allowMutations,
            ...(a.strict !== undefined ? { strict: a.strict } : {}),
            maxOperations: opts.scriptMaxOperations ?? 50,
            timeoutMs: opts.scriptTimeoutMs ?? 60_000,
            ...(ctx.abortSignal ? { signal: ctx.abortSignal } : {}),
          },
        );
        if (r.logs.length) api.output(r.logs.join("\n") + "\n");
        return ok(fmt({ result: r.result, ...(r.logs.length ? { logs: r.logs } : {}), operations: r.operations }, a.format));
      }),
  });

  const list: ToolRegistration[] = [queries, mutationsList, types, describe, sdl, validate, explore, query, batch, subscribeTool, script];
  if (allowMutations) list.push(mutation);
  return list;
}

const GUIDE = `You can explore and call a GraphQL API with the gql_* tools. Workflow: discover (queries/mutations/types) -> describe an operation with args and depth=1 -> validate -> execute with a jq filter to keep output small. Prefer batch for independent queries and script for loops or dependent calls.`;

/** Extension bundling the tools plus a short usage section for the system prompt. Install with `registry.install(gqlcliExtension({...}))`. */
export function gqlcliExtension(opts: GqlcliToolsOptions & { name?: string } = {}): Extension {
  return defineExtension({
    name: opts.name ?? "gqlcli",
    tools: createGqlcliTools(opts),
    sections: [section("gqlcli", () => GUIDE)],
  });
}
