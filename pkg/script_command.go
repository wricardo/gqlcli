package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

// GetScriptCommand returns the JavaScript scripting command.
func (b *CLIBuilder) GetScriptCommand() *cli.Command {
	return &cli.Command{
		Name:    "script",
		Aliases: []string{"js"},
		Usage:   "Run a JavaScript automation script that calls GraphQL operations",
		Description: "Execute a JavaScript file to orchestrate GraphQL queries and mutations with loops and conditionals.\n\n" +
			"The script must define a function (default: run) with signature:\n" +
			"  async function run(gql, input) { ... }\n\n" +
			"The gql helper exposes methods:\n" +
			"  gql.query(query, variables?, operationName?)\n" +
			"  gql.mutation(mutation, variables?, operationName?)\n" +
			"  gql.request({ type, query|mutation, variables, operationName })\n" +
			"  gql.each(items, worker, { concurrency?, stopOnError?, onError? })\n\n" +
			"Examples:\n" +
			"  gqlcli script --file ./disableUsers.js\n" +
			"  gqlcli script --file ./job.js --arg '{\"tenantId\":\"abc\"}'\n" +
			"  gqlcli script --file ./job.js --function main --output result.json",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "url",
				Aliases: []string{"u"},
				Usage:   "GraphQL endpoint URL (env: GRAPHQL_URL)",
				Value:   b.config.URL,
				EnvVars: []string{"GRAPHQL_URL"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "Enable debug mode (logs HTTP requests/responses)",
				Value:   b.config.Debug,
			},
			insecureFlag(),
			&cli.IntFlag{Name: "timeout", Usage: "Request timeout in seconds (default: 30)", Value: b.config.Timeout},
			&cli.IntFlag{Name: "retry", Usage: "Retry count for transient failures (connection errors, 408, 429, 5xx; default: 0)", Value: b.config.RetryCount},
			&cli.DurationFlag{Name: "retry-delay", Usage: "Delay between retries (e.g. 500ms, 2s; default: 1s when --retry > 0)", Value: b.config.RetryDelay},
			&cli.BoolFlag{Name: "strict", Usage: "Exit non-zero when response.errors is present (default true; use --strict=false to disable)", Value: b.config.Strict},
			&cli.StringFlag{Name: "env", Usage: "Environment to use from .gqlcli.json (e.g. local, prod)"},
			headerFlag(),
			&cli.StringFlag{
				Name:     "file",
				Aliases:  []string{"f"},
				Usage:    "Path to JavaScript file",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "function",
				Value: "run",
				Usage: "JavaScript function name to call",
			},
			&cli.StringFlag{
				Name:  "arg",
				Usage: "Optional JSON object passed as the second script argument",
			},
			&cli.StringFlag{
				Name:  "arg-file",
				Usage: "Path to JSON file passed as the second script argument",
			},
			&cli.StringFlag{
				Name:  "output",
				Usage: "Write script return value as JSON to a file (default: stdout)",
			},
		},
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			b.client = NewHTTPClient(b.config)

			input, err := readScriptInput(c)
			if err != nil {
				return err
			}

			runner := NewScriptRunner(b.client)
			result, err := runner.RunFile(context.Background(), c.String("file"), c.String("function"), input)
			if err != nil {
				return err
			}
			if result == nil {
				return nil
			}

			out, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to serialize script result: %w", err)
			}
			if outFile := c.String("output"); outFile != "" {
				return os.WriteFile(outFile, out, 0644)
			}
			fmt.Fprintln(c.App.Writer, string(out))
			return nil
		},
	}
}

func readScriptInput(c *cli.Context) (map[string]interface{}, error) {
	var input map[string]interface{}
	if file := c.String("arg-file"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read arg file: %w", err)
		}
		if err := json.Unmarshal(data, &input); err != nil {
			return nil, fmt.Errorf("invalid JSON in arg file: %w", err)
		}
		return input, nil
	}

	if raw := c.String("arg"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			return nil, fmt.Errorf("invalid --arg JSON: %w", err)
		}
	}
	return input, nil
}
