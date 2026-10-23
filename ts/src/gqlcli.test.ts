import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";
import { buildSchema, graphql } from "graphql";
import { Gqlcli, GraphQLClient, SchemaExplorer, executeBatch, operationKind, runScript } from "./index.ts";

const schema = buildSchema(`
  "A person"
  type User { id: ID!, name: String!, active: Boolean!, posts(first: Int = 10): [Post!]! }
  type Post { id: ID!, title: String!, author: User! }
  input CreateUserInput { name: String!, active: Boolean = true }
  enum Role { ADMIN MEMBER }
  type Query { "Find users" users(active: Boolean): [User!]!, user(id: ID!): User, post(id: ID!): Post }
  type Mutation { createUser(input: CreateUserInput!): User! }
`);

const users = [
  { id: "1", name: "Ada", active: true, posts: () => [] },
  { id: "2", name: "Bob", active: false, posts: () => [] },
];
const rootValue = {
  users: ({ active }: { active?: boolean }) => users.filter((u) => active === undefined || u.active === active),
  user: ({ id }: { id: string }) => users.find((u) => u.id === id) ?? null,
  createUser: ({ input }: { input: { name: string; active?: boolean } }) => ({ id: "9", name: input.name, active: input.active ?? true, posts: () => [] }),
};

let server: Server;
let url: string;
let introspections = 0;
let lastHeaders: Record<string, string | string[] | undefined> = {};

before(async () => {
  server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", async () => {
      lastHeaders = req.headers;
      const text = Buffer.concat(chunks).toString();
      const run = async (r: { query: string; variables?: Record<string, unknown>; operationName?: string }) => {
        if (r.query.includes("__schema")) introspections++;
        return JSON.parse(JSON.stringify(await graphql({ schema, source: r.query, rootValue, variableValues: r.variables ?? null, operationName: r.operationName ?? null })));
      };
      if (req.url === "/fail") {
        res.statusCode = 503;
        res.end("down");
        return;
      }
      if (req.headers["content-type"] === "application/x-ndjson") {
        const out = await Promise.all(text.split("\n").filter(Boolean).map((l) => run(JSON.parse(l))));
        res.setHeader("content-type", "application/x-ndjson").end(out.map((o) => JSON.stringify(o)).join("\n"));
        return;
      }
      const body = JSON.parse(text);
      res.setHeader("content-type", "application/json").end(JSON.stringify(Array.isArray(body) ? await Promise.all(body.map(run)) : await run(body)));
    });
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  url = `http://127.0.0.1:${(server.address() as AddressInfo).port}/graphql`;
});
after(() => server.close());

const cacheDir = mkdtempSync(join(tmpdir(), "gqlcli-cache-"));
const make = (extra = {}) => new Gqlcli({ url, config: {}, schemaCache: { dir: cacheDir }, ...extra });

describe("execution", () => {
  test("query with variables and jq", async () => {
    const r = await make().query({ query: "query($id: ID!){ user(id:$id){ name } }", variables: { id: "1" }, jq: ".data.user.name" });
    assert.deepEqual(r.jq, ["Ada"]);
    assert.equal(r.hasErrors, false);
  });

  test("graphql errors are returned, and jq is skipped", async () => {
    const r = await make().query({ query: "{ nope }", jq: ".data" });
    assert.equal(r.hasErrors, true);
    assert.equal(r.jq, undefined);
  });

  test("query method refuses mutations; mutation wraps --input", async () => {
    const g = make();
    await assert.rejects(g.query({ query: 'mutation { createUser(input:{name:"x"}){ id } }' }), /declares a mutation/);
    const r = await g.mutation({ query: "mutation($input: CreateUserInput!){ createUser(input:$input){ id name } }", input: { name: "Cy" }, jq: ".data.createUser.name" });
    assert.deepEqual(r.jq, ["Cy"]);
  });

  test("env headers are overridden per request", async () => {
    const c = new GraphQLClient({ url, headers: { Authorization: "Bearer env", "X-A": "1" } });
    await c.execute({ query: "{ users { id } }" }, { headers: { authorization: "Bearer once" } });
    assert.equal(lastHeaders["authorization"], "Bearer once");
    assert.equal(lastHeaders["x-a"], "1");
  });

  test("retries transient HTTP failures then throws TransportError", async () => {
    const c = new GraphQLClient({ url: url.replace("/graphql", "/fail"), retry: 2, retryDelayMs: 1 });
    await assert.rejects(c.execute({ query: "{ x }" }), { name: "TransportError", status: 503 });
  });

  test("saved operation from config merges defaults under variables", async () => {
    const g = make({ config: { operations: { who: { type: "query", query: "query($id: ID!){ user(id:$id){ name } }", defaults: { id: "2" } } } } });
    assert.deepEqual((await g.query({ op: "who", jq: ".data.user.name" })).jq, ["Bob"]);
    assert.deepEqual((await g.query({ op: "who", variables: { id: "1" }, jq: ".data.user.name" })).jq, ["Ada"]);
  });
});

describe("schema exploration", () => {
  test("operations, types, describe, depth and references", async () => {
    const ex = await make().schema();
    assert.deepEqual(ex.operations("query", { filter: "user", args: true }).operations.map((o) => o.signature), ["users(active: Boolean): [User!]!", "user(id: ID!): User"]);
    assert.deepEqual(ex.types({ kind: "ENUM" }).map((t) => t.name), ["Role"]);
    assert.equal(ex.describe("User"), "type User {\n  id: ID!\n  name: String!\n  active: Boolean!\n  posts: [Post!]!\n}");
    const deep = ex.describe("User", { depth: 1, args: true });
    assert.match(deep, /type Post \{/);
    assert.match(deep, /# Operations referencing User/);
    assert.match(deep, /createUser\(input: CreateUserInput!\): User!/);
    assert.match(deep, /# Schema fields referencing User\n[\s\S]*type Post \{\n  author: User!/);
    assert.equal(ex.describe("Query.user"), "type Query {\n  user(id: ID!): User\n}");
    assert.throws(() => ex.describe("Usr"), /similar|not found/);
  });

  test("validate gives line:column and schema hint; sdl round-trips offline", async () => {
    const ex = await make().schema();
    const bad = ex.validate("{ users { nope } }");
    assert.equal(bad.valid, false);
    assert.equal(bad.errors[0]?.line, 1);
    assert.match(bad.errors[0]?.schemaHint ?? "", /^type User \{/);
    assert.equal(ex.validate("{ users { id } }").valid, true);

    const offline = SchemaExplorer.fromSDL(ex.sdl());
    assert.equal(offline.validate("{ users { nope } }").valid, false);
    const file = join(mkdtempSync(join(tmpdir(), "sdl-")), "s.graphql");
    writeFileSync(file, ex.sdl());
    assert.equal((await make({ url: "http://127.0.0.1:1/never" }).validate("{ users { id } }", { schemaFile: file })).valid, true);
  });

  test("introspection is served from the on-disk cache across clients", async () => {
    const dir = mkdtempSync(join(tmpdir(), "cache-"));
    const before = introspections;
    await new GraphQLClient({ url, schemaCache: { dir } }).introspect();
    await new GraphQLClient({ url, schemaCache: { dir } }).introspect();
    assert.equal(introspections - before, 1);
    await new GraphQLClient({ url, schemaCache: { dir, refresh: true } }).introspect();
    assert.equal(introspections - before, 2);
    await new GraphQLClient({ url, schemaCache: { dir, ttlMs: 0 } }).introspect();
    assert.equal(introspections - before, 3);
  });
});

describe("batch", () => {
  const reqs = [{ query: '{ user(id:"1"){ name } }' }, { query: '{ user(id:"2"){ name } }' }, { query: '{ user(id:"1"){ id } }' }];
  test("ndjson, array and chunking keep order; client jq", async () => {
    const c = new GraphQLClient({ url });
    for (const transport of ["ndjson", "array"] as const) {
      const out = await executeBatch(c, reqs, { transport, jq: ".data.user | to_entries[0].value" });
      assert.deepEqual(out, ["Ada", "Bob", "1"]);
    }
    assert.equal((await executeBatch(c, reqs, { batchSize: 2 })).length, 3);
  });
});

describe("script", () => {
  const execute = (g: Gqlcli) => (req: Parameters<GraphQLClient["execute"]>[0], signal?: AbortSignal) => g.client.execute(req, signal ? { signal } : {});

  test("loops, gql.each concurrency, logs, input", async () => {
    const g = make();
    const r = await runScript({
      execute: execute(g),
      input: { ids: ["1", "2", "3"] },
      source: `async function run(gql, input) {
        console.log("start", input.ids.length);
        const names = [];
        const summary = await gql.each(input.ids, async (id) => {
          const res = await gql.query("query($id: ID!){ user(id:$id){ name } }", { id });
          if (!res.data.user) throw new Error("missing " + id);
          names.push(res.data.user.name);
        }, { concurrency: 2 });
        return { names: names.sort(), summary };
      }`,
    });
    assert.deepEqual(r.result, { names: ["Ada", "Bob"], summary: { total: 3, success: 2, failed: 1, errors: [{ index: 2, error: "missing 3" }] } });
    assert.deepEqual(r.logs, ["start 3"]);
    assert.equal(r.operations, 3);
  });

  test("readOnly judges the document, not the helper; budget; timeout; no host globals", async () => {
    const g = make();
    const mut = `async function run(gql){ return gql.query('mutation { createUser(input:{name:"x"}){ id } }') }`;
    await assert.rejects(runScript({ execute: execute(g), source: mut, readOnly: true }), /read-only/);
    await assert.rejects(runScript({ execute: execute(g), source: `async function run(gql){ await gql.query("{ users { id } }"); await gql.query("{ users { id } }") }`, maxOperations: 1 }), /budget/);
    await assert.rejects(runScript({ execute: execute(g), source: `async function run(){ await new Promise(() => {}) }`, timeoutMs: 50 }), /timed out/);
    await assert.rejects(runScript({ execute: execute(g), source: `function run(){ while(true){} }`, timeoutMs: 50 }), /timed out|Script execution/);
    const r = await runScript({ execute: execute(g), source: `function run(){ return [typeof require, typeof process, typeof fetch] }` });
    assert.deepEqual(r.result, ["undefined", "undefined", "undefined"]);
  });

  test("operationKind", () => {
    assert.equal(operationKind("subscription { a }"), "subscription");
    assert.throws(() => operationKind("query A { a } query B { b }"), /pass operationName/);
    assert.equal(operationKind("query A { a } mutation B { b }", "B"), "mutation");
  });
});
