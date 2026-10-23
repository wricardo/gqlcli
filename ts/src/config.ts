import { existsSync, readFileSync, renameSync, writeFileSync, chmodSync } from "node:fs";
import { join } from "node:path";
import type { JsonObject, OperationKind } from "./types.ts";

/** Mirrors the Go `.gqlcli.json` schema so both tools can share one file. */
export interface EnvLoginConfig {
  mutation: string;
  token_path: string;
  credentials?: JsonObject;
  header_name?: string;
  header_prefix?: string;
}

export interface EnvConfig {
  url: string;
  headers?: Record<string, string>;
  login?: EnvLoginConfig;
  embeddings?: string;
}

export interface NamedOperation {
  type: OperationKind;
  query?: string;
  mutation?: string;
  defaults?: JsonObject;
}

export interface NamedScript {
  lang?: string;
  function?: string;
  source: string;
  defaults?: JsonObject;
  description?: string;
}

export interface ProjectConfig {
  default?: string;
  environments?: Record<string, EnvConfig>;
  operations?: Record<string, NamedOperation>;
  scripts?: Record<string, NamedScript>;
}

export const CONFIG_FILE = ".gqlcli.json";

/** Reads `.gqlcli.json` from `dir`. Returns undefined when the file does not exist. */
export function loadProjectConfig(dir: string = process.cwd()): ProjectConfig | undefined {
  const path = join(dir, CONFIG_FILE);
  if (!existsSync(path)) return undefined;
  try {
    return JSON.parse(readFileSync(path, "utf8")) as ProjectConfig;
  } catch (err) {
    throw new Error(`parsing ${path}: ${(err as Error).message}`, { cause: err });
  }
}

/** Atomically writes the config; 0600 when plaintext login credentials are present. */
export function saveProjectConfig(cfg: ProjectConfig, dir: string = process.cwd()): void {
  const path = join(dir, CONFIG_FILE);
  const hasCreds = Object.values(cfg.environments ?? {}).some((e) => e.login?.credentials && Object.keys(e.login.credentials).length > 0);
  const mode = hasCreds ? 0o600 : 0o644;
  const tmp = `${path}.${process.pid}.tmp`;
  writeFileSync(tmp, JSON.stringify(cfg, null, 2) + "\n", { mode });
  renameSync(tmp, path);
  chmodSync(path, mode);
}

/** Environment for `name`, or the config default when `name` is empty. Undefined when neither is set. */
export function resolveEnv(cfg: ProjectConfig | undefined, name?: string): { name: string; env: EnvConfig } | undefined {
  if (!cfg) {
    if (name) throw new Error(`environment "${name}" requested but no ${CONFIG_FILE} was found`);
    return undefined;
  }
  const envName = name || cfg.default;
  if (!envName) return undefined;
  const env = cfg.environments?.[envName];
  if (!env) throw new Error(`environment "${envName}" not found in ${CONFIG_FILE}`);
  return { name: envName, env };
}

export interface ResolvedOperation {
  kind: OperationKind;
  document: string;
  defaults: JsonObject;
}

export function resolveOperation(cfg: ProjectConfig | undefined, name: string, expected?: OperationKind): ResolvedOperation {
  const op = cfg?.operations?.[name];
  if (!op) throw new Error(`saved operation "${name}" not found in ${CONFIG_FILE}`);
  const kind = op.type;
  if (expected && kind !== expected) throw new Error(`saved operation "${name}" is a ${kind}, not a ${expected}`);
  const document = kind === "mutation" ? (op.mutation ?? op.query) : op.query;
  if (!document?.trim()) throw new Error(`saved operation "${name}" has no document`);
  return { kind, document, defaults: op.defaults ?? {} };
}

export function resolveScript(cfg: ProjectConfig | undefined, name: string): Required<Pick<NamedScript, "source" | "function">> & NamedScript {
  const s = cfg?.scripts?.[name];
  if (!s) throw new Error(`script "${name}" not found in ${CONFIG_FILE}`);
  if (!s.source?.trim()) throw new Error(`script "${name}" has empty source`);
  return { ...s, function: s.function?.trim() || "run" };
}

/** Shallow merge, explicit values win; matches Go `mergeVariables`. */
export function mergeVariables(defaults: JsonObject | undefined, explicit: JsonObject | undefined): JsonObject {
  return { ...(defaults ?? {}), ...(explicit ?? {}) };
}

/** Saved-operation management, equivalent to `gqlcli op save|delete`. */
export function saveOperation(name: string, op: NamedOperation, dir: string = process.cwd()): void {
  if (!name.trim()) throw new Error("operation name is required");
  const cfg = loadProjectConfig(dir) ?? {};
  cfg.operations = { ...(cfg.operations ?? {}), [name]: op };
  saveProjectConfig(cfg, dir);
}

export function deleteOperation(name: string, dir: string = process.cwd()): boolean {
  const cfg = loadProjectConfig(dir);
  if (!cfg?.operations || !(name in cfg.operations)) return false;
  delete cfg.operations[name];
  saveProjectConfig(cfg, dir);
  return true;
}
