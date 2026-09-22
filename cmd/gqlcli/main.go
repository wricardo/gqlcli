package main

import (
	"log"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	gqlcli "github.com/wricardo/gqlcli/pkg"
)

// Version is set at build time via -ldflags "-X main.Version=<value>".
var Version = "dev"

func cliVersion() string {
	if strings.TrimSpace(Version) == "" {
		return "dev"
	}
	return Version
}

func main() {
	cfg := &gqlcli.Config{
		URL:    "http://localhost:8080/graphql",
		Format: "toon",
		Strict: true,
	}

	builder := gqlcli.NewCLIBuilder(cfg)

	app := &cli.App{
		Name:    "gqlcli",
		Usage:   "GraphQL CLI — Query and explore any GraphQL API",
		Version: cliVersion(),
		Description: `gqlcli executes GraphQL queries, mutations, subscriptions, and JavaScript workflow scripts, and explores schemas from any GraphQL endpoint.

TYPICAL AI WORKFLOW
  1. Discover available operations:
       gqlcli queries --args --desc          # list all Query fields with args and descriptions
       gqlcli mutations --args --desc         # list all Mutation fields with args and descriptions

  2. Inspect types:
       gqlcli types                           # all types — compact, token-efficient
       gqlcli types --kind INPUT_OBJECT       # list all input types
       gqlcli describe User                   # SDL definition of a specific type
       gqlcli describe User --args            # include field argument signatures
       gqlcli describe SmsCampaign --depth 1  # also show matching operations and schema fields
       gqlcli describe SmsCampaign --depth 1 --max-op-refs 0 --max-field-refs 0

  3. Check an operation before running it (nothing is executed):
       gqlcli validate '{ users { id name } }'        # exits 1 and reports line:column on error
       gqlcli query --validate-only '{ users { id } }'
       gqlcli sdl > schema.graphql                    # then validate offline, no network or auth:
       gqlcli validate --schema-file schema.graphql '{ users { id } }'

  4. Execute operations:
       gqlcli query '{ users { id name } }'
       gqlcli mutation 'mutation { deleteUser(id:"1") { ok } }'
       gqlcli mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }' \
         --input '{"name":"Alice","email":"alice@example.com"}'
       gqlcli subscribe 'subscription { messageAdded { id text } }'

  5. Filter output with jq (use --format json first):
       gqlcli query '{ users { id name } }' --format json | jq '.data.users[].name'
       gqlcli query '{ users { id name } }' --format json | jq '.data.users | length'

  6. Batch multiple operations in one request:
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

  7. Script complex workflows with JavaScript (async/await + concurrency):
       gqlcli script --file ./disableUsers.js
       gqlcli script --file ./disableUsers.js --arg '{"tenantId":"acme"}'
       gqlcli script --op disable-inactive-users

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

	if err := gqlcli.RunApp(app, os.Args); err != nil {
		log.Fatal(err)
	}
}
