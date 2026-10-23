import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { createModels } from "@earendil-works/pi-ai/models";
import { fauxAssistantMessage, fauxProvider, fauxText, fauxToolCall } from "@earendil-works/pi-ai/providers/faux";
import { createRegistry, Harness, MemoryStorage } from "@earendil-works/pi-durable";
import { buildSchema, graphql } from "graphql";
import { createGqlcliTools, gqlcliExtension } from "./index.ts";

const schema = buildSchema(`
  type User { id: ID!, name: String! }
  type Query { users: [User!]!, user(id: ID!): User }
  type Mutation { rename(id: ID!, name: String!): User! }
`);
const users = [{ id: "1", name: "Ada" }];
const rootValue = {
  users: () => users,
  user: ({ id }: { id: string }) => users.find((u) => u.id === id) ?? null,
  rename: ({ id, name }: { id: string; name: string }) => {
    const u = users.find((x) => x.id === id)!;
    u.name = name;
    return u;
  },
};

async function withServer<T>(fn: (url: string) => Promise<T>): Promise<T> {
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", async () => {
      const b = JSON.parse(Buffer.concat(chunks).toString());
      res.setHeader("content-type", "application/json").end(JSON.stringify(await graphql({ schema, source: b.query, rootValue, variableValues: b.variables ?? null })));
    });
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  try {
    return await fn(`http://127.0.0.1:${(server.address() as AddressInfo).port}/graphql`);
  } finally {
    server.close();
  }
}

test("replay policy: read-only by default, mutation tools only on opt-in", () => {
  const ro = createGqlcliTools({ gqlcli: { url: "http://x", config: {} } });
  assert.ok(!ro.some((t) => t.name === "gql_mutation"));
  assert.ok(ro.every((t) => t.replay === "safe"), "every read-only tool must be replay-safe");
  const rw = createGqlcliTools({ gqlcli: { url: "http://x", config: {} }, mutations: true });
  assert.equal(rw.find((t) => t.name === "gql_mutation")?.replay, undefined);
  assert.equal(rw.find((t) => t.name === "gql_script")?.replay, undefined);
  assert.equal(rw.find((t) => t.name === "gql_query")?.replay, "safe");
});

test("a Pi Durable agent explores, queries and mutates through the tools", async () => {
  await withServer(async (url) => {
    const faux = fauxProvider();
    const models = createModels();
    models.setProvider(faux.provider);
    const registry = createRegistry();
    const extension = gqlcliExtension({ gqlcli: { url, config: {}, schemaCache: { dir: mkdtempSync(join(tmpdir(), "h-")) } }, mutations: true });
    registry.install(extension);

    faux.setResponses([
      fauxAssistantMessage([
        fauxToolCall("gql_describe", { name: "Query.user", depth: 1 }, { id: "c1" }),
        fauxToolCall("gql_validate", { query: "{ users { nope } }" }, { id: "c2" }),
        fauxToolCall("gql_query", { query: "{ users { id name } }", jq: ".data.users[0].name" }, { id: "c3" }),
      ], { stopReason: "toolUse" }),
      fauxAssistantMessage([fauxToolCall("gql_mutation", { query: "mutation($id: ID!, $name: String!){ rename(id:$id, name:$name){ name } }", variables: { id: "1", name: "Grace" }, jq: ".data.rename.name" }, { id: "c4" })], { stopReason: "toolUse" }),
      fauxAssistantMessage([fauxText("done")]),
    ]);

    const harness = await Harness.open(new MemoryStorage(), { models, registry }, BACKGROUND_CONTEXT);
    try {
      const m = faux.getModel();
      const root = await harness.root(BACKGROUND_CONTEXT, { agent: { model: { provider: m.provider, modelId: m.id }, extensions: [extension] } });
      const settled = await (await root.submit({ type: "input", content: "rename the first user to Grace" }, BACKGROUND_CONTEXT)).wait(BACKGROUND_CONTEXT);
      assert.equal(settled.status, "done");
      assert.equal(users[0]?.name, "Grace", "mutation must have reached the server");

      const page = await root.entries({ order: "ascending" }, 100, undefined, BACKGROUND_CONTEXT);
      const results = page.items.filter((e) => e.kind === "pi.tool-result").map((e) => JSON.stringify(e.model));
      assert.equal(results.length, 4);
      assert.match(results[0]!, /type Query[\s\S]*user\(id: ID!\): User/);
      assert.match(results[1]!, /Cannot query field[\s\S]*nope/);
      assert.match(results[2]!, /Ada/);
      assert.match(results[3]!, /Grace/);
    } finally {
      await harness.close(BACKGROUND_CONTEXT);
    }
  });
});
