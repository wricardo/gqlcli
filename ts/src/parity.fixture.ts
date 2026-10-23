import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { buildSchema, graphql } from "graphql";

/** Schema exercising descriptions, defaults, enums, interfaces, unions, inputs and deprecation, shared by the parity tests. */
export const parityTypeDefs = `
  "A person"
  type User implements Node {
    id: ID!
    "Display name"
    name: String!
    active: Boolean!
    role: Role!
    posts(first: Int = 10, after: String): [Post!]!
    legacyId: String @deprecated(reason: "use id")
  }
  type Post implements Node { id: ID!, title: String!, author: User! }
  interface Node { id: ID! }
  union SearchHit = User | Post
  enum Role { ADMIN MEMBER }
  input CreateUserInput { name: String!, active: Boolean = true, role: Role = MEMBER }
  input UserFilter { role: Role, nameContains: String }
  scalar DateTime
  type Query {
    "Find users"
    users(filter: UserFilter, active: Boolean): [User!]!
    user(id: ID!): User
    post(id: ID!): Post
    search(text: String!): [SearchHit!]!
  }
  type Mutation {
    createUser(input: CreateUserInput!): User!
    deletePost(id: ID!): Boolean!
  }
`;

export const parityRoot = {
  users: ({ active }: { active?: boolean }) =>
    [
      { id: "1", name: "Ada", active: true, role: "ADMIN", posts: [] },
      { id: "2", name: "Bob", active: false, role: "MEMBER", posts: [] },
    ].filter((u) => active === undefined || u.active === active),
  user: ({ id }: { id: string }) => (id === "1" ? { id: "1", name: "Ada", active: true, role: "ADMIN", posts: [] } : null),
  createUser: ({ input }: { input: { name: string; active?: boolean; role?: string } }) => ({ id: "9", name: input.name, active: input.active ?? true, role: input.role ?? "MEMBER", posts: [] }),
};

export const paritySchema = buildSchema(parityTypeDefs);

interface WireRequest {
  query: string;
  variables?: Record<string, unknown>;
  operationName?: string;
}

/** GraphQL-over-HTTP server that also speaks the batch transports (JSON array, NDJSON), like the servers gqlcli targets. */
export async function startParityServer(): Promise<{ url: string; close: () => void; server: Server }> {
  const run = async (r: WireRequest) =>
    JSON.parse(JSON.stringify(await graphql({ schema: paritySchema, source: r.query, rootValue: parityRoot, variableValues: r.variables ?? null, operationName: r.operationName ?? null }))) as unknown;
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", async () => {
      const text = Buffer.concat(chunks).toString();
      if (req.headers["content-type"] === "application/x-ndjson") {
        const out = await Promise.all(text.split("\n").filter(Boolean).map((l) => run(JSON.parse(l) as WireRequest)));
        res.setHeader("content-type", "application/x-ndjson").end(out.map((o) => JSON.stringify(o)).join("\n"));
        return;
      }
      const body = JSON.parse(text) as WireRequest | WireRequest[];
      res.setHeader("content-type", "application/json").end(JSON.stringify(Array.isArray(body) ? await Promise.all(body.map(run)) : await run(body)));
    });
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  return { server, url: `http://127.0.0.1:${(server.address() as AddressInfo).port}/graphql`, close: () => server.close() };
}
