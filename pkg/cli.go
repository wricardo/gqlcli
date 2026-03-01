package gqlcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

// CLIBuilder creates CLI commands for GraphQL operations
type CLIBuilder struct {
	client    Client
	config    *Config
	formatReg FormatterRegistry
}

// NewCLIBuilder creates a new CLI command builder
func NewCLIBuilder(cfg *Config) *CLIBuilder {
	client := NewHTTPClient(cfg)
	formatReg := NewFormatterRegistry()

	return &CLIBuilder{
		client:    client,
		config:    cfg,
		formatReg: formatReg,
	}
}

// GetQueryCommand returns the query subcommand
func (b *CLIBuilder) GetQueryCommand() *cli.Command {
	return &cli.Command{
		Name:    "query",
		Aliases: []string{"q"},
		Usage:   "Execute a GraphQL query",
		Description: "Execute a read-only GraphQL query against the endpoint. " +
			"Query can come from --query flag, --query-file, or as the first argument. " +
			"Variables can be provided via --variables (inline JSON) or --variables-file.",
		Flags: b.getOperationFlags(),
		Action: func(c *cli.Context) error {
			// Update config with command-line flags
			b.config.URL = c.String("url")
			b.config.Debug = c.Bool("debug")
			b.client = NewHTTPClient(b.config)

			// Get query from various sources
			query, err := b.getQueryString(c)
			if err != nil {
				return err
			}

			// Parse variables
			variables, err := b.getVariables(c)
			if err != nil {
				return err
			}

			// Execute query
			opts := QueryOptions{
				Query:         query,
				Variables:     variables,
				OperationName: c.String("operation"),
			}

			result, err := b.client.Execute(context.Background(), ExecutionModeHTTP, opts)
			if err != nil {
				return b.handleError(c, err)
			}

			// Format and output
			return b.outputResult(c, result)
		},
	}
}

// GetMutationCommand returns the mutation subcommand
func (b *CLIBuilder) GetMutationCommand() *cli.Command {
	return &cli.Command{
		Name:    "mutation",
		Aliases: []string{"m"},
		Usage:   "Execute a GraphQL mutation",
		Description: "Execute a write operation (mutation) against the endpoint. " +
			"Mutation can come from --mutation flag, --mutation-file, or as the first argument. " +
			"Variables can be provided via --variables or --variables-file. " +
			"Use --input to auto-wrap input as {\"input\": {...}}.",
		Flags: append(b.getOperationFlags(),
			&cli.StringFlag{
				Name:  "input",
				Usage: "Input object as JSON - automatically wrapped as {\"input\":{...}} variable",
			},
		),
		Action: func(c *cli.Context) error {
			// Update config with command-line flags
			b.config.URL = c.String("url")
			b.config.Debug = c.Bool("debug")
			b.client = NewHTTPClient(b.config)

			// Get mutation from various sources
			mutation, err := b.getMutationString(c)
			if err != nil {
				return err
			}

			// Parse variables
			variables, err := b.getVariables(c)
			if err != nil {
				return err
			}

			// Parse input if provided
			var input interface{}
			if inputStr := c.String("input"); inputStr != "" {
				if err := json.Unmarshal([]byte(inputStr), &input); err != nil {
					return fmt.Errorf("invalid input JSON: %w", err)
				}
			}

			// Execute mutation
			opts := MutationOptions{
				Mutation:      mutation,
				Variables:     variables,
				OperationName: c.String("operation"),
				Input:         input,
			}

			result, err := b.client.ExecuteMutation(context.Background(), ExecutionModeHTTP, opts)
			if err != nil {
				return b.handleError(c, err)
			}

			// Format and output
			return b.outputResult(c, result)
		},
	}
}

// GetIntrospectCommand returns the introspection command
func (b *CLIBuilder) GetIntrospectCommand() *cli.Command {
	return &cli.Command{
		Name:    "introspect",
		Aliases: []string{"schema"},
		Usage:   "Generate GraphQL schema",
		Description: "Output the GraphQL schema in various formats. " +
			"Default format is 'llm' (human and LLM-friendly). " +
			"Use 'json' for full introspection data, or 'compact' for minimal output.",
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
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Output format: llm (default), json, compact",
				Value:   "llm",
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Output file path (default: stdout)",
			},
			&cli.BoolFlag{
				Name:    "pretty",
				Aliases: []string{"p"},
				Usage:   "Pretty print JSON output (only for json format)",
				Value:   false,
			},
		},
		Action: func(c *cli.Context) error {
			// Update config with command-line flags
			b.config.URL = c.String("url")
			b.config.Debug = c.Bool("debug")
			b.client = NewHTTPClient(b.config)

			result, err := b.client.Introspect(context.Background())
			if err != nil {
				return err
			}

			// Extract schema from response (unwrap the data field)
			var schema interface{} = result
			if data, ok := result["data"]; ok {
				schema = data
			}

			// Format result
			formatter, err := b.formatReg.Get(c.String("format"))
			if err != nil {
				return err
			}

			output, err := formatter.Format(schema.(map[string]interface{}))
			if err != nil {
				return err
			}

			// Write output
			if outputFile := c.String("output"); outputFile != "" {
				return os.WriteFile(outputFile, []byte(output), 0644)
			}

			fmt.Println(output)
			return nil
		},
	}
}

// GetTypesCommand returns the types listing command
func (b *CLIBuilder) GetTypesCommand() *cli.Command {
	return &cli.Command{
		Name:  "types",
		Usage: "List all GraphQL types",
		Description: "Display all available GraphQL types in the schema. " +
			"Optionally filter by name or type kind.",
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
			&cli.StringFlag{
				Name:  "filter",
				Usage: "Filter types by name (case-insensitive substring match)",
			},
			&cli.StringFlag{
				Name:    "kind",
				Aliases: []string{"k"},
				Usage:   "Filter by type kind: OBJECT, ENUM, INPUT_OBJECT, SCALAR, INTERFACE, UNION",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Output format: compact (default), json, table",
				Value:   "compact",
			},
		},
		Action: func(c *cli.Context) error {
			// Update config with command-line flags
			b.config.URL = c.String("url")
			b.config.Debug = c.Bool("debug")
			b.client = NewHTTPClient(b.config)

			result, err := b.client.Introspect(context.Background())
			if err != nil {
				return err
			}

			// Extract types from introspection
			data, ok := result["data"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("invalid introspection response")
			}

			schema, ok := data["__schema"].(map[string]interface{})
			if !ok {
				return fmt.Errorf("invalid schema in response")
			}

			typesList, ok := schema["types"].([]interface{})
			if !ok {
				return fmt.Errorf("invalid types in schema")
			}

			// Format and output
			formatter, err := b.formatReg.Get(c.String("format"))
			if err != nil {
				return err
			}

			output, err := formatter.Format(map[string]interface{}{
				"types": typesList,
			})
			if err != nil {
				return err
			}

			fmt.Println(output)
			return nil
		},
	}
}

// GetQueriesCommand returns the queries listing command
func (b *CLIBuilder) GetQueriesCommand() *cli.Command {
	return &cli.Command{
		Name:    "queries",
		Aliases: []string{"q-list"},
		Usage:   "List all available Query fields",
		Description: "Display all available GraphQL Query fields. " +
			"Optionally include descriptions and arguments. " +
			"Use --filter to search by field name.",
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
			&cli.BoolFlag{
				Name:  "desc",
				Usage: "Include field descriptions",
			},
			&cli.BoolFlag{
				Name:  "args",
				Usage: "Include field arguments",
			},
			&cli.StringFlag{
				Name:  "filter",
				Usage: "Filter fields by name (case-insensitive substring match)",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Output format: toon (default), json, json-pretty, table, compact, llm",
				Value:   "toon",
			},
		},
		Action: func(c *cli.Context) error {
			// Update config with command-line flags
			b.config.URL = c.String("url")
			b.config.Debug = c.Bool("debug")
			b.client = NewHTTPClient(b.config)

			// Build and execute introspection query
			query := buildOperationListQuery("Query", c.Bool("desc"), c.Bool("args"))
			opts := QueryOptions{Query: query}

			result, err := b.client.Execute(context.Background(), ExecutionModeHTTP, opts)
			if err != nil {
				return err
			}

			// Extract fields from response
			fields, err := extractOperationFields(result)
			if err != nil {
				return err
			}

			// Apply filter if provided
			if filter := c.String("filter"); filter != "" {
				fields = filterOperations(fields, filter)
			}

			// Format and output
			formatter, err := b.formatReg.Get(c.String("format"))
			if err != nil {
				return err
			}

			output, err := formatter.Format(map[string]interface{}{
				"queries": fields,
			})
			if err != nil {
				return err
			}

			fmt.Println(output)
			return nil
		},
	}
}

// GetMutationsCommand returns the mutations listing command
func (b *CLIBuilder) GetMutationsCommand() *cli.Command {
	return &cli.Command{
		Name:    "mutations",
		Aliases: []string{"m-list"},
		Usage:   "List all available Mutation fields",
		Description: "Display all available GraphQL Mutation fields. " +
			"Optionally include descriptions and arguments. " +
			"Use --filter to search by field name.",
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
			&cli.BoolFlag{
				Name:  "desc",
				Usage: "Include field descriptions",
			},
			&cli.BoolFlag{
				Name:  "args",
				Usage: "Include field arguments",
			},
			&cli.StringFlag{
				Name:  "filter",
				Usage: "Filter fields by name (case-insensitive substring match)",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Output format: toon (default), json, json-pretty, table, compact, llm",
				Value:   "toon",
			},
		},
		Action: func(c *cli.Context) error {
			// Update config with command-line flags
			b.config.URL = c.String("url")
			b.config.Debug = c.Bool("debug")
			b.client = NewHTTPClient(b.config)

			// Build and execute introspection query
			query := buildOperationListQuery("Mutation", c.Bool("desc"), c.Bool("args"))
			opts := QueryOptions{Query: query}

			result, err := b.client.Execute(context.Background(), ExecutionModeHTTP, opts)
			if err != nil {
				return err
			}

			// Extract fields from response
			fields, err := extractOperationFields(result)
			if err != nil {
				return err
			}

			// Apply filter if provided
			if filter := c.String("filter"); filter != "" {
				fields = filterOperations(fields, filter)
			}

			// Format and output
			formatter, err := b.formatReg.Get(c.String("format"))
			if err != nil {
				return err
			}

			output, err := formatter.Format(map[string]interface{}{
				"mutations": fields,
			})
			if err != nil {
				return err
			}

			fmt.Println(output)
			return nil
		},
	}
}

// RegisterCommands returns all CLI commands for the app
func (b *CLIBuilder) RegisterCommands(app *cli.App) {
	app.Commands = append(app.Commands,
		b.GetQueryCommand(),
		b.GetMutationCommand(),
		b.GetIntrospectCommand(),
		b.GetTypesCommand(),
		b.GetQueriesCommand(),
		b.GetMutationsCommand(),
		b.GetInstallSkillCommand(),
	)
}

// Helper methods

func (b *CLIBuilder) getOperationFlags() []cli.Flag {
	return []cli.Flag{
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
		&cli.StringFlag{
			Name:     "query",
			Aliases:  []string{"q"},
			Usage:    "GraphQL query string",
			Required: false,
		},
		&cli.StringFlag{
			Name:    "query-file",
			Aliases: []string{"file"},
			Usage:   "Path to .graphql file containing query",
		},
		&cli.StringFlag{
			Name:     "mutation",
			Aliases:  []string{"m"},
			Usage:    "GraphQL mutation string",
			Required: false,
		},
		&cli.StringFlag{
			Name:    "mutation-file",
			Usage:   "Path to .graphql file containing mutation",
		},
		&cli.StringFlag{
			Name:    "variables",
			Aliases: []string{"v"},
			Usage:   "Variables as JSON string, e.g. '{\"id\":\"123\"}'",
		},
		&cli.StringFlag{
			Name:    "variables-file",
			Aliases: []string{"var-file"},
			Usage:   "Path to JSON file containing variables",
		},
		&cli.StringFlag{
			Name:    "operation",
			Aliases: []string{"o"},
			Usage:   "Operation name (for files with multiple operations)",
		},
		&cli.StringFlag{
			Name:    "format",
			Aliases: []string{"f"},
			Usage:   "Output format: json, table, compact, toon, llm",
			Value:   b.config.Format,
		},
		&cli.BoolFlag{
			Name:    "pretty",
			Aliases: []string{"p"},
			Usage:   "Pretty print JSON output",
			Value:   b.config.Pretty,
		},
		&cli.StringFlag{
			Name:  "output",
			Usage: "Output file path (default: stdout)",
		},
	}
}

func (b *CLIBuilder) getQueryString(c *cli.Context) (string, error) {
	if queryFile := c.String("query-file"); queryFile != "" {
		data, err := os.ReadFile(queryFile)
		if err != nil {
			return "", fmt.Errorf("failed to read query file: %w", err)
		}
		return string(data), nil
	}

	if query := c.String("query"); query != "" {
		return query, nil
	}

	if c.NArg() > 0 {
		return c.Args().First(), nil
	}

	return "", fmt.Errorf("query is required (use --query, --query-file, or provide as argument)")
}

func (b *CLIBuilder) getMutationString(c *cli.Context) (string, error) {
	if mutationFile := c.String("mutation-file"); mutationFile != "" {
		data, err := os.ReadFile(mutationFile)
		if err != nil {
			return "", fmt.Errorf("failed to read mutation file: %w", err)
		}
		return string(data), nil
	}

	if mutation := c.String("mutation"); mutation != "" {
		return mutation, nil
	}

	if c.NArg() > 0 {
		return c.Args().First(), nil
	}

	return "", fmt.Errorf("mutation is required (use --mutation, --mutation-file, or provide as argument)")
}

func (b *CLIBuilder) getVariables(c *cli.Context) (map[string]interface{}, error) {
	var variables map[string]interface{}

	if varFile := c.String("variables-file"); varFile != "" {
		data, err := os.ReadFile(varFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read variables file: %w", err)
		}
		if err := json.Unmarshal(data, &variables); err != nil {
			return nil, fmt.Errorf("invalid variables JSON in file: %w", err)
		}
		return variables, nil
	}

	if varStr := c.String("variables"); varStr != "" {
		if err := json.Unmarshal([]byte(varStr), &variables); err != nil {
			return nil, fmt.Errorf("invalid variables JSON: %w", err)
		}
		return variables, nil
	}

	return nil, nil
}

// handleError checks whether err is a *GraphQLResponseError and, if so, formats
// and prints the response using the selected formatter, then returns a silent
// non-zero exit. For all other errors it returns err unchanged.
func (b *CLIBuilder) handleError(c *cli.Context, err error) error {
	var gqlErr *GraphQLResponseError
	if !errors.As(err, &gqlErr) {
		return err
	}
	fmt.Fprintf(os.Stderr, "Query:\n%s\n\n", formatQueryForError(gqlErr.Query))
	_ = b.outputResult(c, gqlErr.Response)
	return cli.Exit("", 1)
}

func (b *CLIBuilder) outputResult(c *cli.Context, result map[string]interface{}) error {
	// Get formatter
	formatName := c.String("format")
	formatter, err := b.formatReg.Get(formatName)
	if err != nil {
		// Fallback to JSON if format not found
		formatter, _ = b.formatReg.Get("json")
	}

	// Format result
	output, err := formatter.Format(result)
	if err != nil {
		return err
	}

	// Write to file or stdout
	if outputFile := c.String("output"); outputFile != "" {
		return os.WriteFile(outputFile, []byte(output), 0644)
	}

	fmt.Println(output)
	return nil
}

// buildOperationListQuery constructs an introspection query for Query or Mutation type
func buildOperationListQuery(typeName string, includeDesc, includeArgs bool) string {
	query := fmt.Sprintf(`
	{
		__type(name: "%s") {
			fields {
				name`, typeName)

	if includeDesc {
		query += `
				description`
	}

	if includeArgs {
		query += `
				args {
					name
					type {
						kind
						name
						ofType {
							kind
							name
							ofType {
								kind
								name
								ofType {
									kind
									name
								}
							}
						}
					}
				}`
	}

	query += `
			}
		}
	}`

	return query
}

// extractOperationFields extracts the fields array from the introspection response
func extractOperationFields(result map[string]interface{}) ([]interface{}, error) {
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response structure: missing 'data' field")
	}

	typeInfo, ok := data["__type"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response structure: missing '__type' field")
	}

	fields, ok := typeInfo["fields"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid response structure: missing 'fields' array")
	}

	return fields, nil
}

// filterOperations filters operations by name using case-insensitive substring matching
func filterOperations(operations []interface{}, filter string) []interface{} {
	var filtered []interface{}

	for _, op := range operations {
		if opMap, ok := op.(map[string]interface{}); ok {
			if name, ok := opMap["name"].(string); ok {
				// Case-insensitive substring match
				if strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
					filtered = append(filtered, op)
				}
			}
		}
	}

	return filtered
}
