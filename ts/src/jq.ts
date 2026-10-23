import { json } from "jq-wasm";

/** Runs a jq program over `input` using the real jq compiled to WASM (no subprocess). Returns one element per output value. */
export async function jq(input: unknown, expr: string): Promise<unknown[]> {
  try {
    return (await json(input as never, expr)) as unknown[];
  } catch (err) {
    throw new Error(`jq error: ${(err as Error).message}`, { cause: err });
  }
}

/** Like {@link jq} but collapses: zero outputs -> undefined, one -> the value, many -> array. */
export async function jqValue(input: unknown, expr: string): Promise<unknown> {
  const out = await jq(input, expr);
  return out.length <= 1 ? out[0] : out;
}
