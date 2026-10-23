import { createClient } from "graphql-ws";
import WebSocket from "ws";
import type { JsonObject } from "./types.ts";

export type SubscriptionEvent =
  | { type: "next"; payload: unknown }
  | { type: "error"; payload: unknown }
  | { type: "complete" };

export interface SubscribeOptions {
  /** http(s) URLs are mapped to ws(s); explicit ws(s) URLs pass through. */
  url: string;
  query: string;
  variables?: JsonObject;
  operationName?: string;
  headers?: Record<string, string>;
  /** Stop after this many `next` events. Default 10, so an agent tool cannot block forever. */
  maxEvents?: number;
  /** Stop after this long, without error. Default 30s. */
  timeoutMs?: number;
  /** Skip TLS verification for wss:// endpoints. */
  insecure?: boolean;
  signal?: AbortSignal;
  /** Called per event as it arrives, e.g. to stream into `api.output()`. */
  onEvent?: (event: SubscriptionEvent) => void;
}

/** Streams a subscription over `graphql-transport-ws` and resolves with the events collected before it stopped. */
export async function subscribe(o: SubscribeOptions): Promise<{ events: SubscriptionEvent[]; stoppedBy: "complete" | "maxEvents" | "timeout" | "aborted" | "error" }> {
  const wsUrl = o.url.replace(/^http/i, "ws");
  const headers = o.headers ?? {};
  class HeaderWebSocket extends WebSocket {
    constructor(address: string | URL, protocols?: string | string[]) {
      super(address, protocols, { headers, ...(o.insecure ? { rejectUnauthorized: false } : {}) });
    }
  }
  const client = createClient({ url: wsUrl, webSocketImpl: HeaderWebSocket, retryAttempts: 0, lazy: true });
  const events: SubscriptionEvent[] = [];
  const done = Promise.withResolvers<"complete" | "maxEvents" | "timeout" | "aborted" | "error">();
  const maxEvents = o.maxEvents ?? 10;
  let nexts = 0;
  const push = (e: SubscriptionEvent) => {
    events.push(e);
    o.onEvent?.(e);
  };
  const timer = setTimeout(() => done.resolve("timeout"), o.timeoutMs ?? 30_000);
  const onAbort = () => done.resolve("aborted");
  o.signal?.addEventListener("abort", onAbort, { once: true });
  const dispose = client.subscribe(
    { query: o.query, ...(o.variables ? { variables: o.variables } : {}), ...(o.operationName ? { operationName: o.operationName } : {}) },
    {
      next: (payload) => {
        push({ type: "next", payload });
        if (++nexts >= maxEvents) done.resolve("maxEvents");
      },
      error: (payload) => {
        push({ type: "error", payload: payload instanceof Error ? { message: payload.message } : payload });
        done.resolve("error");
      },
      complete: () => {
        push({ type: "complete" });
        done.resolve("complete");
      },
    },
  );
  try {
    return { events, stoppedBy: await done.promise };
  } finally {
    clearTimeout(timer);
    o.signal?.removeEventListener("abort", onAbort);
    dispose();
    await client.dispose();
  }
}
