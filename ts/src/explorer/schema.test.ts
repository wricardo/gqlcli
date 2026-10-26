import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

const source = join(import.meta.dirname, "../../../pkg/explorer/schema.graphqls");
const copy = join(import.meta.dirname, "schema.graphql");

test("published explorer SDL matches the canonical Go schema byte for byte", () => {
  assert.deepEqual(readFileSync(copy), readFileSync(source));
});
