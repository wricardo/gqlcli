import type { GraphQLClient, RequestOptions } from "./client.ts";
import { jq } from "./jq.ts";
import { TransportError, type GraphQLRequest, type GraphQLResponse } from "./types.ts";

export interface BatchOptions extends RequestOptions {
  /** `ndjson` (default): application/x-ndjson, one response per line. `array`: standard JSON array batch. */
  transport?: "ndjson" | "array";
  /** Max operations per HTTP request, sent sequentially in order. 0/undefined: all in one request. */
  batchSize?: number;
  /** Client-side jq applied to every response; each output value becomes one result entry. */
  jq?: string;
}

/**
 * Sends independent operations in one or several requests. Each request may carry a `jq` field, passed through for
 * servers that apply it server-side. Results keep request order; with client-side `jq`, one response may expand to many.
 */
export async function executeBatch(client: GraphQLClient, requests: GraphQLRequest[], o: BatchOptions = {}): Promise<unknown[]> {
  if (o.batchSize !== undefined && (o.batchSize < 0 || !Number.isInteger(o.batchSize))) throw new Error("batchSize must be a non-negative integer");
  const size = o.batchSize && o.batchSize > 0 ? o.batchSize : requests.length || 1;
  const array = o.transport === "array";
  const out: unknown[] = [];
  for (let i = 0; i < requests.length; i += size) {
    const chunk = requests.slice(i, i + size);
    const body = array ? JSON.stringify(chunk) : chunk.map((r) => JSON.stringify(r)).join("\n") + "\n";
    const raw = await client.post(body, array ? "application/json" : "application/x-ndjson", o);
    let responses: GraphQLResponse[];
    try {
      responses = array
        ? (JSON.parse(raw.body) as GraphQLResponse[])
        : raw.body.split("\n").filter((l) => l.trim()).map((l) => JSON.parse(l) as GraphQLResponse);
    } catch {
      throw new TransportError(`HTTP ${raw.status}: cannot parse batch response: ${raw.body.slice(0, 300)}`, { status: raw.status, body: raw.body });
    }
    if (!Array.isArray(responses)) throw new TransportError(`HTTP ${raw.status}: batch response is not an array`, { status: raw.status, body: raw.body });
    for (const r of responses) out.push(...(o.jq ? await jq(r, o.jq) : [r]));
  }
  return out;
}
