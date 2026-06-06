package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
	gqlcli "github.com/wricardo/gqlcli/pkg"
)

const version = "0.2.0"

func main() {
	cfg := &gqlcli.Config{
		URL:     "http://localhost:8080/graphql",
		Format:  "toon",
		Timeout: 30,
	}

	builder := gqlcli.NewCLIBuilder(cfg)

	app := &cli.App{
		Name:    "gqlcli",
		Usage:   "GraphQL CLI — Query and explore any GraphQL API",
		Version: version,
		Description: `gqlcli executes GraphQL queries, mutations, and subscriptions, and explores schemas from any GraphQL endpoint.

TYPICAL AI WORKFLOW
  1. Discover available operations:
       gqlcli queries --args --desc          # list all Query fields with args and descriptions
       gqlcli mutations --args --desc         # list all Mutation fields with args and descriptions

  2. Inspect types:
       gqlcli types                           # all types — compact, token-efficient
       gqlcli types --kind INPUT_OBJECT       # list all input types
       gqlcli describe User                   # SDL definition of a specific type
       gqlcli describe User --args            # include field argument signatures

  3. Execute operations:
       gqlcli query '{ users { id name } }'
       gqlcli mutation 'mutation { deleteUser(id:"1") { ok } }'
       gqlcli mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }' \
         --input '{"name":"Alice","email":"alice@example.com"}'
       gqlcli subscribe 'subscription { messageAdded { id text } }'

  4. Filter output with jq (use --format json first):
       gqlcli query '{ users { id name } }' --format json | jq '.data.users[].name'
       gqlcli query '{ users { id name } }' --format json | jq '.data.users | length'

  5. Batch multiple operations in one request:
       # NDJSON from stdin (default transport, one response line per operation)
       printf '{"query":"{ users { id } }"}\n{"query":"{ posts { id } }"}\n' | gqlcli batch

       # Server-side jq: include "jq" in each line — server filters before returning
       # jq receives the full {"data":...} envelope, so start paths from .data
       printf '{"query":"{ smsCampaigns { campaigns { id name } } }","jq":".data.smsCampaigns.campaigns[].name"}\n' | gqlcli batch
       printf '{"query":"{ users { id name } }","jq":".data.users[] | select(.active)"}\n' | gqlcli batch
       printf '{"query":"{ users { id } }","jq":".data.users | length"}\n' | gqlcli batch

       # Client-side jq: --jq flag applies to every response after the server returns
       printf '{"query":"{ users { id } }"}\n' | gqlcli batch --jq '.data.users | length'

       # JSON array transport (single POST, returns a JSON array)
       gqlcli batch --array --file operations.json

OUTPUT FORMATS
  toon     Default. Readable tree output, good for terminal inspection.
  llm      Compact SDL-like text, minimal noise, best for feeding to an LLM.
  json     Raw JSON response — use when you need to parse the output programmatically.
  compact  Single-line JSON, minimal whitespace.
  table    Tabular layout, useful for listing types or fields.

CONFIG FILE (.gqlcli.json)
  Place a .gqlcli.json in the current directory to define named environments with URLs and headers.
  The "default" key sets which environment is used when --env is omitted.
  Switch environments with --env <name> on any command.

  Example .gqlcli.json:
    {
      "default": "local",
      "environments": {
        "local": {
          "url": "http://localhost:8080/graphql",
          "headers": { "Authorization": "Bearer dev-token" }
        },
        "prod": {
          "url": "https://api.example.com/graphql",
          "headers": { "Authorization": "Bearer prod-token" }
        }
      }
    }

ENVIRONMENT VARIABLES
  GRAPHQL_URL   GraphQL endpoint URL. Overrides .gqlcli.json but is itself overridden by --url.

PRECEDENCE (highest to lowest)
  --url flag  >  GRAPHQL_URL env var  >  .gqlcli.json environment  >  built-in default`,
	}

	builder.RegisterCommands(app)

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
