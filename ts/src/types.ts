export type JsonObject = { [key: string]: unknown };

export interface GraphQLRequest {
  query: string;
  variables?: JsonObject;
  operationName?: string;
  /** Extra fields sent verbatim (e.g. a server-side `jq` extension). */
  [extra: string]: unknown;
}

export interface GraphQLErrorEntry {
  message: string;
  locations?: { line: number; column: number }[];
  path?: (string | number)[];
  extensions?: JsonObject;
  [extra: string]: unknown;
}

export interface GraphQLResponse<TData = unknown> {
  data?: TData | null;
  errors?: GraphQLErrorEntry[];
  extensions?: JsonObject;
}

export type OperationKind = "query" | "mutation" | "subscription";

/** Thrown for transport failures: network errors, non-GraphQL HTTP errors, timeouts. */
export class TransportError extends Error {
  readonly status?: number;
  readonly body?: string;
  constructor(message: string, opts: { status?: number; body?: string; cause?: unknown } = {}) {
    super(message, opts.cause === undefined ? undefined : { cause: opts.cause });
    this.name = "TransportError";
    if (opts.status !== undefined) this.status = opts.status;
    if (opts.body !== undefined) this.body = opts.body;
  }
}

/** Thrown in strict mode when the response carries an `errors` array. The full response stays on `.response`. */
export class GraphQLResponseError extends Error {
  readonly response: GraphQLResponse;
  constructor(response: GraphQLResponse) {
    const first = response.errors?.[0]?.message ?? "GraphQL errors";
    super(`GraphQL error: ${first}`);
    this.name = "GraphQLResponseError";
    this.response = response;
  }
}

export function hasErrors(res: GraphQLResponse): boolean {
  return Array.isArray(res.errors) && res.errors.length > 0;
}
