import { readFileSync } from "node:fs";
import type { ExecutionResult as GraphQLExecutionResult } from "graphql";
import { executeBatch, type BatchOptions } from "./batch.ts";
import { GraphQLClient, type ClientOptions, type RequestOptions } from "./client.ts";
import { loadProjectConfig, mergeVariables, resolveEnv, resolveOperation, resolveScript, type ProjectConfig } from "./config.ts";
import { jq } from "./jq.ts";
import { operationKind } from "./operation.ts";
import { SchemaExplorer, type ValidationResult } from "./schema.ts";
import { runScript, type ScriptOptions, type ScriptResult } from "./script.ts";
import { subscribe, type SubscribeOptions } from "./subscribe.ts";
import { hasErrors, type GraphQLRequest, type GraphQLResponse, type JsonObject, type OperationKind } from "./types.ts";

export interface GqlcliOptions {
  /** Directory holding `.gqlcli.json`. Default: process.cwd(). */
  cwd?: string;
  /** Environment name from `.gqlcli.json`; default: the file's `default`. */
  env?: string;
  /** Endpoint. Precedence: this > GRAPHQL_URL > `.gqlcli.json` environment. */
  url?: string;
  /** Extra headers layered over the environment's headers. */
  headers?: Record<string, string>;
  /** Preloaded config instead of reading `.gqlcli.json`. */
  config?: ProjectConfig;
  timeoutMs?: number;
  retry?: number;
  retryDelayMs?: number;
  schemaCache?: ClientOptions["schemaCache"];
  fetch?: typeof fetch;
}

export interface OperationInput {
  /** GraphQL document. Exactly one of `query` / `op` is required. */
  query?: string;
  /** Saved operation name from `.gqlcli.json`; its defaults are merged under `variables`. */
  op?: string;
  variables?: JsonObject;
  /** Mutation convenience: wrapped as `{input: ...}` like `gqlcli mutation --input`. */
  input?: JsonObject;
  operationName?: string;
  /** jq program applied to the response when it has no errors. */
  jq?: string;
}

export type ScriptRunOptions = { input?: JsonObject } & Pick<ScriptOptions, "readOnly" | "strict" | "maxOperations" | "timeoutMs" | "signal" | "approve" | "onResponse">;
export interface ExecutionResult {
  response: GraphQLResponse;
  hasErrors: boolean;
  /** The jq outputs (one entry per value) when `jq` was given and the response had no errors. */
  jq?: unknown[];
}

/** One handle bundling config, HTTP client, schema exploration, execution and scripts. No subprocesses. */
export class Gqlcli {
  readonly client: GraphQLClient;
  readonly config: ProjectConfig | undefined;
  readonly envName: string | undefined;
  readonly cwd: string;
  private explorer: Promise<SchemaExplorer> | undefined;

  constructor(o: GqlcliOptions = {}) {
    this.cwd = o.cwd ?? process.cwd();
    this.config = o.config ?? loadProjectConfig(this.cwd);
    const resolved = resolveEnv(this.config, o.env);
    this.envName = resolved?.name;
    const url = o.url ?? process.env["GRAPHQL_URL"] ?? resolved?.env.url;
    if (!url) throw new Error("no GraphQL endpoint: pass url, set GRAPHQL_URL, or configure .gqlcli.json");
    this.client = new GraphQLClient({
      url,
      headers: { ...(resolved?.env.headers ?? {}), ...(o.headers ?? {}) },
      ...(o.timeoutMs !== undefined ? { timeoutMs: o.timeoutMs } : {}),
      ...(o.retry !== undefined ? { retry: o.retry } : {}),
      ...(o.retryDelayMs !== undefined ? { retryDelayMs: o.retryDelayMs } : {}),
      ...(o.schemaCache ? { schemaCache: o.schemaCache } : {}),
      ...(o.fetch ? { fetch: o.fetch } : {}),
    });
  }

  /** Schema explorer for the endpoint; introspects once per instance (and via the on-disk cache across processes). */
  schema(o: RequestOptions = {}): Promise<SchemaExplorer> {
    this.explorer ??= SchemaExplorer.fromClient(this.client, o).catch((err: unknown) => {
      this.explorer = undefined;
      throw err;
    });
    return this.explorer;
  }

  /** Execute a read-only explorer document locally; only schema introspection may contact the endpoint. */
  async explore(document: string, variables?: Record<string, unknown>, operationName?: string): Promise<GraphQLExecutionResult> {
    return (await this.schema()).explore(document, variables, operationName);
  }

  /** Drops the in-memory and on-disk schema so the next call introspects again. */
  refreshSchema(): void {
    this.explorer = undefined;
    this.client.clearSchemaCache();
  }

  /** Runs a query. Rejects documents that declare a mutation or subscription, so it is safe to replay. */
  query(input: OperationInput, o: RequestOptions = {}): Promise<ExecutionResult> {
    return this.run("query", input, o);
  }

  mutation(input: OperationInput, o: RequestOptions = {}): Promise<ExecutionResult> {
    return this.run("mutation", input, o);
  }

  private async run(expected: "query" | "mutation", input: OperationInput, o: RequestOptions): Promise<ExecutionResult> {
    let document = input.query;
    let defaults: JsonObject = {};
    if (input.op) {
      if (document) throw new Error("pass either query or op, not both");
      const saved = resolveOperation(this.config, input.op, expected);
      document = saved.document;
      defaults = saved.defaults;
    }
    if (!document?.trim()) throw new Error(`${expected} document is required`);
    const declared = operationKind(document, input.operationName);
    if (declared !== expected) throw new Error(`document declares a ${declared}; use the ${declared} method instead`);
    const explicit = input.input ? mergeVariables(input.variables, { input: input.input }) : input.variables;
    const variables = mergeVariables(defaults, explicit);
    const req: GraphQLRequest = { query: document, ...(Object.keys(variables).length ? { variables } : {}), ...(input.operationName ? { operationName: input.operationName } : {}) };
    const response = await this.client.execute(req, o);
    const errored = hasErrors(response);
    return { response, hasErrors: errored, ...(input.jq && !errored ? { jq: await jq(response, input.jq) } : {}) };
  }

  /** Validates a document without sending it. `schemaFile` validates against SDL on disk, fully offline. */
  async validate(document: string, o: { schemaFile?: string } = {}): Promise<ValidationResult> {
    const explorer = o.schemaFile ? SchemaExplorer.fromSDL(readFileSync(o.schemaFile, "utf8")) : await this.schema();
    return explorer.validate(document);
  }

  batch(requests: GraphQLRequest[], o: BatchOptions = {}): Promise<unknown[]> {
    return executeBatch(this.client, requests, o);
  }

  subscribe(o: Omit<SubscribeOptions, "url" | "headers"> & { headers?: Record<string, string> }) {
    return subscribe({ ...o, url: this.client.url, headers: { ...this.client.headers, ...(o.headers ?? {}) } });
  }

  /** Runs a script from source or a saved `.gqlcli.json` script (`name`), executing operations through this client. */
  async script(target: { source: string; functionName?: string } | { name: string }, o: ScriptRunOptions = {}, ro: RequestOptions = {}): Promise<ScriptResult> {
    let source: string;
    let functionName: string | undefined;
    let input = o.input;
    if ("name" in target) {
      const saved = resolveScript(this.config, target.name);
      source = saved.source;
      functionName = saved.function;
      input = mergeVariables(saved.defaults, o.input);
    } else {
      source = target.source;
      functionName = target.functionName;
    }
    const { input: _input, ...policy } = o;
    return runScript({
      ...policy,
      source,
      ...(functionName ? { functionName } : {}),
      ...(input ? { input } : {}),
      execute: (req, signal) => this.client.execute(req, { ...ro, ...(signal ? { signal } : {}) }),
    });
  }
}

export type { OperationKind };
