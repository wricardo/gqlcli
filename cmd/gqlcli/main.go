package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
	gqlcli "github.com/wricardo/gqlcli/pkg"
)

const version = "0.1.0"

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
		Description: `gqlcli executes GraphQL queries and mutations, and explores schemas from any GraphQL endpoint.

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
