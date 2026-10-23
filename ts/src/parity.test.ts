/**
 * Parity tests: run the Go CLI (`cmd/gqlcli`) and this library against the same GraphQL server and compare results.
 *
 * The Go binary comes from $GQLCLI_GO_BIN, or is built from the repository with `go build`. Without either, the suite
 * is skipped. Bytes are compared where the Go output is a contract (describe SDL, jq, JSON responses, batch, scripts);
 * for listings and validation, which this library returns as structured data, the same facts are compared.
 */
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";
import { promisify } from "node:util";
import { buildSchema, lexicographicSortSchema, printSchema } from "graphql";
import { Gqlcli, jq, type DescribeOptions, type SchemaExplorer } from "./index.ts";
import { startParityServer } from "./parity.fixture.ts";

const exec = promisify(execFile);
const repoRoot = join(import.meta.dirname, "..", "..");
const work = mkdtempSync(join(tmpdir(), "gqlcli-parity-"));

async function locateGo(): Promise<string | undefined> {
  if (process.env["GQLCLI_GO_BIN"]) return process.env["GQLCLI_GO_BIN"];
  const out = join(work, "gqlcli-go");
  try {
    await exec("go", ["build", "-o", out, "./cmd/gqlcli"], { cwd: repoRoot, timeout: 240_000 });
    return out;
  } catch {
    return undefined;
  }
}

const goBin = await locateGo();
const skip = goBin ? false : "Go binary unavailable: set GQLCLI_GO_BIN or install Go";

interface GoResult {
  stdout: string;
  stderr: string;
  code: number;
}

let url: string;
let stop: () => void;
let ts: Gqlcli;
let explorer: SchemaExplorer;

async function go(args: string[], opts: { stdin?: string } = {}): Promise<GoResult> {
  const cwd = join(work, "cwd");
  mkdirSync(cwd, { recursive: true });
  const child = promisify(execFile)(goBin!, args, { cwd, env: { ...process.env, GRAPHQL_URL: url, GQLCLI_SCHEMA_CACHE_TTL: "0" }, timeout: 60_000 });
  if (opts.stdin !== undefined) {
    child.child.stdin?.end(opts.stdin);
  }
  try {
    const { stdout, stderr } = await child;
    return { stdout, stderr, code: 0 };
  } catch (err) {
    const e = err as { stdout?: string; stderr?: string; code?: number };
    return { stdout: e.stdout ?? "", stderr: e.stderr ?? "", code: e.code ?? 1 };
  }
}

before(async () => {
  if (skip) return;
  const s = await startParityServer();
  url = s.url;
  stop = s.close;
  ts = new Gqlcli({ url, config: {}, schemaCache: { ttlMs: 0 } });
  explorer = await ts.schema();
});
after(() => stop?.());

/** Maps `describe` CLI flags onto library options. */
function describeArgs(argv: string[]): { name: string; opts: DescribeOptions } {
  const [name = "", ...flags] = argv;
  const opts: DescribeOptions = {};
  for (let i = 0; i < flags.length; i++) {
    const f = flags[i];
    if (f === "--args") opts.args = true;
    else if (f === "--desc") opts.desc = true;
    else if (f === "--depth") opts.depth = Number(flags[++i]);
    else if (f === "--max-op-refs") opts.maxOpRefs = Number(flags[++i]);
    else if (f === "--max-field-refs") opts.maxFieldRefs = Number(flags[++i]);
    else throw new Error(`unmapped flag ${f}`);
  }
  return { name, opts };
}

describe("describe is byte-identical to the Go CLI", { skip }, () => {
  const cases: string[][] = [
    ["User"],
    ["User", "--args"],
    ["User", "--desc"],
    ["User", "--args", "--desc"],
    ["Post"],
    ["Query"],
    ["Query", "--args"],
    ["Query", "--args", "--desc"],
    ["Query", "--args", "--depth", "1"],
    ["Query", "--args", "--depth", "2"],
    ["Mutation", "--args", "--depth", "1"],
    ["Node"],
    ["Node", "--depth", "1"],
    ["Role"],
    ["Role", "--depth", "1"],
    ["DateTime"],
    ["SearchHit"],
    ["SearchHit", "--depth", "1"],
    ["CreateUserInput"],
    ["CreateUserInput", "--args", "--depth", "1"],
    ["UserFilter", "--depth", "1"],
    ["User", "--depth", "1"],
    ["User", "--depth", "2"],
    ["User", "--depth", "1", "--args", "--desc"],
    ["Post", "--depth", "1"],
    ["Post", "--depth", "1", "--max-op-refs", "1", "--max-field-refs", "1"],
    ["User", "--depth", "1", "--max-op-refs", "0", "--max-field-refs", "0"],
    ["User", "--depth", "1", "--max-op-refs", "2"],
    ["Query.user"],
    ["Query.user", "--args"],
    ["Query.users", "--args", "--desc"],
    ["Query.users", "--args", "--depth", "1"],
    ["User.posts"],
    ["User.posts", "--args", "--depth", "1"],
    ["Mutation.createUser", "--args", "--depth", "1"],
  ];
  for (const argv of cases) {
    test(`describe ${argv.join(" ")}`, async () => {
      const g = await go(["describe", ...argv]);
      assert.equal(g.code, 0, g.stderr);
      const { name, opts } = describeArgs(argv);
      assert.equal(explorer.describe(name, opts), g.stdout);
    });
  }

  for (const bad of ["Nope", "Query.nope", "User.legacyId", "Nope.field"]) {
    test(`describe ${bad} fails with the same message`, async () => {
      const g = await go(["describe", bad]);
      assert.notEqual(g.code, 0);
      const message = g.stderr.replace(/^\d{4}\/\d\d\/\d\d \d\d:\d\d:\d\d /, "").trim();
      assert.throws(() => explorer.describe(bad), (e: Error) => e.message.startsWith(message), `go said: ${message}`);
    });
  }
});

/** Splits compact SDL output into one entry per definition. */
function blocks(sdl: string): string[] {
  return sdl.split(/^(?=(?:#.*\n)*(?:type|input|interface|enum|union|scalar) )/m).filter(Boolean).sort();
}

describe("schema listings match the Go CLI", { skip }, () => {
  const typeCases: string[][] = [[], ["--filter", "user"], ["--kind", "INPUT_OBJECT"], ["--kind", "ENUM"], ["--kind", "SCALAR"], ["--kind", "INTERFACE"], ["--kind", "UNION"], ["--filter", "post", "--kind", "OBJECT"]];
  for (const argv of typeCases) {
    test(`types ${argv.join(" ")}`, async () => {
      const g = await go(["types", ...argv]);
      assert.equal(g.code, 0, g.stderr);
      const filter = argv[argv.indexOf("--filter") + 1];
      const kind = argv[argv.indexOf("--kind") + 1];
      const mine = explorer.types({ ...(argv.includes("--filter") && filter ? { filter } : {}), ...(argv.includes("--kind") && kind ? { kind } : {}), sdl: true });
      assert.deepEqual(blocks(mine.map((t) => t.sdl).join("")), blocks(g.stdout));
    });
  }

  /** Name(args): Return, from the raw introspection typeRef the Go CLI emits as JSON. */
  type Ref = { kind: string; name?: string; ofType?: Ref };
  const show = (r: Ref): string => (r.kind === "NON_NULL" ? `${show(r.ofType!)}!` : r.kind === "LIST" ? `[${show(r.ofType!)}]` : r.name!);

  for (const [cmd, kind] of [["queries", "query"], ["mutations", "mutation"]] as const) {
    for (const argv of [[], ["--filter", "user"], ["--filter", "post"]]) {
      test(`${cmd} ${argv.join(" ")} --args`, async () => {
        const g = await go([cmd, ...argv, "--args", "-f", "json"]);
        assert.equal(g.code, 0, g.stderr);
        const parsed = JSON.parse(g.stdout) as Record<string, { name: string; type: Ref; args?: { name: string; type: Ref }[] }[]>;
        const goSigs = (parsed[cmd] ?? []).map((o) => `${o.name}${o.args?.length ? `(${o.args.map((a) => `${a.name}: ${show(a.type)}`).join(", ")})` : ""}: ${show(o.type)}`);
        const filter = argv[1];
        const mine = explorer.operations(kind, { args: true, ...(filter ? { filter } : {}) }).operations.map((o) => o.signature);
        assert.deepEqual(mine, goSigs);
      });
    }
  }

  test("queries --args --depth 1 expands the same referenced types", async () => {
    const g = await go(["queries", "--filter", "user", "--args", "--depth", "1"]);
    assert.equal(g.code, 0, g.stderr);
    const { types } = explorer.operations("query", { filter: "user", args: true, depth: 1 });
    assert.ok(types, "depth 1 returns referenced types");
    assert.ok(g.stdout.endsWith(types), `go output should end with the referenced-type blocks:\n${g.stdout}\n---\n${types}`);
  });

  test("sdl describes the same schema", async () => {
    const g = await go(["sdl"]);
    assert.equal(g.code, 0, g.stderr);
    assert.equal(printSchema(lexicographicSortSchema(buildSchema(g.stdout))), printSchema(lexicographicSortSchema(buildSchema(explorer.sdl()))));
  });
});

describe("validate agrees with the Go CLI", { skip }, () => {
  /**
   * The two validators (gqlparser in Go, graphql-js here) agree on verdicts and positions. Their wording differs for
   * some rules, so messages are compared only for the documents listed in `sameMessage`; the rest are compared after
   * normalising graphql-js's "Syntax Error: " prefix and trailing period.
   */
  const docs: { doc: string; sameMessage: boolean }[] = [
    { doc: "{ users { id name } }", sameMessage: true },
    { doc: "{ users { nope } }", sameMessage: true },
    { doc: "{ users { id ", sameMessage: true },
    { doc: "{ user { id } }", sameMessage: false },
    { doc: "{ user(id: 1) { id } }", sameMessage: true },
    { doc: "query Q($x: Int) { users { id } }", sameMessage: true },
    { doc: "{ users { ...Missing } }", sameMessage: true },
    { doc: "mutation { createUser(input: { name: 1 }) { id } }", sameMessage: true },
    { doc: "{ users { id } } { users { id } }", sameMessage: false },
    { doc: "subscription { a }", sameMessage: false },
  ];
  const normalise = (m: string) => m.replace(/^Syntax Error: /, "").replace(/\.$/, "");
  for (const { doc, sameMessage } of docs) {
    test(`validate ${JSON.stringify(doc)}`, async () => {
      const g = await go(["validate", doc]);
      const goIssues = [...(g.stdout + g.stderr).matchAll(/^(\d+):(\d+): (.*)$/gm)].map((m) => ({ line: Number(m[1]), column: Number(m[2]), message: m[3] ?? "" }));
      const r = explorer.validate(doc);
      assert.equal(r.valid, g.code === 0, `go exit ${g.code}: ${g.stdout}${g.stderr}`);
      const positions = (xs: { line?: number | undefined; column?: number | undefined }[]) => [...new Set(xs.map((x) => `${x.line}:${x.column}`))];
      assert.deepEqual(positions(r.errors), positions(goIssues));
      if (sameMessage) assert.deepEqual(r.errors.map((e) => normalise(e.message)), goIssues.map((i) => normalise(i.message)));
    });
  }

  test("offline validation with --schema-file reaches the same verdict", async () => {
    const file = join(work, "schema.graphql");
    writeFileSync(file, (await go(["sdl"])).stdout);
    for (const { doc } of docs.slice(0, 2)) {
      const g = await go(["validate", "--schema-file", file, doc]);
      assert.equal((await ts.validate(doc, { schemaFile: file })).valid, g.code === 0);
    }
  });
});

describe("execution matches the Go CLI", { skip }, () => {
  test("query response", async () => {
    const g = await go(["query", "{ users { id name } }", "-f", "json"]);
    assert.deepEqual(JSON.parse(g.stdout), (await ts.query({ query: "{ users { id name } }" })).response);
  });

  test("variables and operationName", async () => {
    const doc = "query A($id: ID!) { user(id: $id) { name } } query B { users { id } }";
    const g = await go(["query", doc, "--operation", "A", "--variables", '{"id":"1"}', "-f", "json"]);
    assert.deepEqual(JSON.parse(g.stdout), (await ts.query({ query: doc, operationName: "A", variables: { id: "1" } })).response);
  });

  test("--jq prints the same values", async () => {
    for (const expr of [".data.users[].name", ".data.users[0]", ".data.users | length", "[.data.users[].id]"]) {
      const g = await go(["query", "{ users { id name } }", "--jq", expr]);
      const goValues = g.stdout.split("\n").filter(Boolean).map((l) => JSON.parse(l) as unknown);
      assert.deepEqual((await ts.query({ query: "{ users { id name } }", jq: expr })).jq, goValues, expr);
    }
  });

  test("GraphQL errors: same messages and locations, and both signal failure", async () => {
    const g = await go(["query", "{ nope }", "-f", "json"]);
    const goErrors = (JSON.parse(g.stdout.slice(g.stdout.indexOf('{"errors"'))) as { errors: { message: string; locations: unknown }[] }).errors;
    const r = await ts.query({ query: "{ nope }" });
    assert.notEqual(g.code, 0);
    assert.equal(r.hasErrors, true);
    assert.deepEqual(r.response.errors?.map((e) => ({ message: e.message, locations: e.locations })), goErrors.map((e) => ({ message: e.message, locations: e.locations })));
  });

  test("mutation with --input", async () => {
    const doc = "mutation($input: CreateUserInput!){ createUser(input:$input){ id name role } }";
    const g = await go(["mutation", doc, "--input", '{"name":"Cy"}', "-f", "json"]);
    assert.deepEqual(JSON.parse(g.stdout), (await ts.mutation({ query: doc, input: { name: "Cy" } })).response);
  });

  test("batch: ndjson, array, chunking and client jq", async () => {
    const ops = [{ query: '{ user(id:"1"){ name } }' }, { query: "{ users { id } }" }, { query: '{ user(id:"2"){ name } }' }];
    const ndjson = ops.map((o) => JSON.stringify(o)).join("\n") + "\n";
    const lines = (s: string) => s.split("\n").filter(Boolean).map((l) => JSON.parse(l) as unknown);

    assert.deepEqual(await ts.batch(ops), lines((await go(["batch"], { stdin: ndjson })).stdout));
    assert.deepEqual(await ts.batch(ops, { batchSize: 2 }), lines((await go(["batch", "--batch-size", "2"], { stdin: ndjson })).stdout));
    assert.deepEqual(await ts.batch(ops, { jq: ".data" }), lines((await go(["batch", "--jq", ".data"], { stdin: ndjson })).stdout));

    const file = join(work, "ops.json");
    writeFileSync(file, JSON.stringify(ops));
    assert.deepEqual(await ts.batch(ops, { transport: "array" }), lines((await go(["batch", "--array", "--file", file])).stdout));
  });

  test("scripts return the same result", async () => {
    const source = `async function run(gql, input) {
      const names = [];
      const summary = await gql.each(input.ids, async (id) => {
        const res = await gql.query("query($id: ID!){ user(id:$id){ name } }", { id });
        if (!res.data.user) throw new Error("missing " + id);
        names.push(res.data.user.name);
      }, { concurrency: 1, stopOnError: false });
      const m = await gql.mutation("mutation { deletePost(id: \\"1\\") }");
      return { names, summary, hasErrors: !!m.errors, input };
    }`;
    const file = join(work, "s.js");
    writeFileSync(file, source);
    const input = { ids: ["1", "2", "1"] };
    const g = await go(["script", "--file", file, "--arg", JSON.stringify(input), "--strict=false"]);
    assert.equal(g.code, 0, g.stderr);
    const r = await ts.script({ source }, { input, strict: false });
    assert.deepEqual(r.result, JSON.parse(g.stdout));
  });

  test("script defaults agree: strict GraphQL errors and stopOnError both abort the script", async () => {
    const strict = `async function run(gql) { await gql.query("{ nope }"); return "unreachable" }`;
    const each = `async function run(gql) { return gql.each([1, 2, 3], async (n) => { if (n === 2) throw new Error("boom") }) }`;
    for (const source of [strict, each]) {
      const file = join(work, "abort.js");
      writeFileSync(file, source);
      const g = await go(["script", "--file", file]);
      assert.notEqual(g.code, 0, `go should fail: ${g.stdout}`);
      await assert.rejects(ts.script({ source }), { name: "ScriptError" });
    }
  });
});

test("jq helper agrees with the Go CLI on a spread of programs", { skip }, async () => {
  const programs = ['.data.users | map(select(.active)) | length', '.data.users[] | {id, upper: (.name | ascii_upcase)}', '[.data.users[] | .name] | join(",")', '.data.users | group_by(.active) | map(length)', '.data.users[] | select(.name | test("^A"))'];
  for (const p of programs) {
    const g = await go(["query", "{ users { id name active } }", "--jq", p]);
    const goValues = g.stdout.split("\n").filter(Boolean).map((l) => JSON.parse(l) as unknown);
    const res = (await ts.query({ query: "{ users { id name active } }" })).response;
    assert.deepEqual(await jq(res, p), goValues, p);
  }
});

