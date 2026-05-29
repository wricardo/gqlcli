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
	client        Client
	config        *Config
	formatReg     FormatterRegistry
	projectConfig *ProjectConfig
}

// NewCLIBuilder creates a new CLI command builder
func NewCLIBuilder(cfg *Config) *CLIBuilder {
	projectConfig, err := LoadProjectConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}

	// Pre-apply the default environment so flag defaults reflect it
	if projectConfig != nil {
		if env, _ := projectConfig.Resolve(""); env != nil && env.URL != "" {
			cfg.URL = env.URL
		}
	}

	client := NewHTTPClient(cfg)
	formatReg := NewFormatterRegistry()

	return &CLIBuilder{
		client:        client,
		config:        cfg,
		formatReg:     formatReg,
		projectConfig: projectConfig,
	}
}

// applyEnvConfig applies the selected environment from .gqlcli.json to b.config,
// then lets explicit CLI flags (--url, --debug) override it.
func (b *CLIBuilder) applyEnvConfig(c *cli.Context) error {
	if b.projectConfig != nil {
		env, err := b.projectConfig.Resolve(c.String("env"))
		if err != nil {
			return err
		}
		if env != nil {
			if env.URL != "" {
				b.config.URL = env.URL
			}
			b.config.Headers = env.Headers
		}
	}
	// Explicit --url / GRAPHQL_URL env var overrides project config
	if c.IsSet("url") {
		b.config.URL = c.String("url")
	}
	b.config.Debug = c.Bool("debug")
	return nil
}

// GetQueryCommand returns the query subcommand
func (b *CLIBuilder) GetQueryCommand() *cli.Command {
	return &cli.Command{
		Name:    "query",
		Aliases: []string{"q"},
		Usage:   "Execute a GraphQL query",
		Description: "Execute a read-only GraphQL query against the endpoint.\n\n" +
			"Query source (pick one): --query flag, --query-file, or first positional argument.\n" +
			"Variables: --variables '{\"id\":\"123\"}' (inline JSON) or --variables-file vars.json.\n\n" +
			"Use --format llm for LLM-friendly output; --format json when parsing programmatically.\n\n" +
			"JQ FILTERING\n" +
			"  Pipe --format json output to jq to extract specific fields:\n" +
			"    gqlcli query '{ users { id name } }' --format json | jq '.data.users[].name'\n\n" +
			"BATCHING\n" +
			"  To run multiple queries in one request, use the 'batch' command:\n" +
			"    echo '{\"query\":\"{ users { id } }\"}' | gqlcli batch\n\n" +
			"Examples:\n" +
			"  gqlcli query '{ users { id name } }'\n" +
			"  gqlcli query '{ user(id:$id) { name } }' --variables '{\"id\":\"42\"}'\n" +
			"  gqlcli query --query-file myquery.graphql --format llm\n" +
			"  gqlcli query '{ users { id name } }' --format json | jq '.data.users'",
		Flags: b.getOperationFlags(),
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
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
		Description: "Execute a write operation (mutation) against the endpoint.\n\n" +
			"Mutation source (pick one): --mutation flag, --mutation-file, or first positional argument.\n" +
			"Variables: --variables '{\"id\":\"123\"}' or --variables-file vars.json.\n" +
			"--input shortcut: pass the input object directly; it is automatically wrapped as {\"input\":{...}}.\n\n" +
			"JQ FILTERING\n" +
			"  Pipe --format json output to jq to extract specific fields:\n" +
			"    gqlcli mutation '...' --format json | jq '.data.createUser.id'\n\n" +
			"BATCHING\n" +
			"  To run multiple mutations in one request, use the 'batch' command:\n" +
			"    printf '{\"query\":\"mutation { a { ok } }\"}\\n{\"query\":\"mutation { b { ok } }\"}\\n' | gqlcli batch\n\n" +
			"Examples:\n" +
			"  gqlcli mutation 'mutation { deleteUser(id:\"1\") { ok } }'\n" +
			"  gqlcli mutation 'mutation CreateUser($input: CreateUserInput!) { createUser(input: $input) { id } }' \\\n" +
			"    --input '{\"name\":\"Alice\",\"email\":\"alice@example.com\"}'\n" +
			"  gqlcli mutation --mutation-file create_user.graphql --variables-file vars.json\n" +
			"  gqlcli mutation '...' --format json | jq '.data.createUser'",
		Flags: append(b.getOperationFlags(),
			&cli.StringFlag{
				Name:  "input",
				Usage: "Input object as JSON - automatically wrapped as {\"input\":{...}} variable",
			},
		),
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
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

// GetDescribeCommand returns the describe command
func (b *CLIBuilder) GetDescribeCommand() *cli.Command {
	return &cli.Command{
		Name:        "describe",
		Aliases:     []string{"d"},
		Usage:       "Show the SDL definition of a GraphQL type",
		ArgsUsage:   "TYPE_NAME",
		Description: "Print the SDL definition of a single named GraphQL type.\n\nUse this to understand the shape of any type before writing a query or mutation.\nAdd --args to see what arguments each field accepts, --descriptions for doc strings.\n\nExamples:\n  gqlcli describe User\n  gqlcli describe CreateUserInput --args\n  gqlcli describe Order --args --descriptions",
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
				Name:    "args",
				Aliases: []string{"a"},
				Usage:   "Expand field argument signatures",
			},
			&cli.BoolFlag{
				Name:  "descriptions",
				Usage: "Include field/type descriptions",
			},
			&cli.StringFlag{
				Name:  "env",
				Usage: "Environment to use from .gqlcli.json (e.g. local, prod)",
			},
		},
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			httpClient := NewHTTPClient(b.config)

			if c.NArg() == 0 {
				return fmt.Errorf("TYPE_NAME argument is required")
			}
			typeName := c.Args().First()
			d := NewDescriberFromHTTPClient(httpClient)
			hint, err := d.DescribeWith(context.Background(), typeName, c.Bool("args"), c.Bool("descriptions"))
			if err != nil {
				return err
			}
			fmt.Print(hint)
			return nil
		},
	}
}

// GetTypesCommand returns the types listing command
func (b *CLIBuilder) GetTypesCommand() *cli.Command {
	return &cli.Command{
		Name:  "types",
		Usage: "List all GraphQL types",
		Description: "List all GraphQL types in the schema.\n\n" +
			"Use --kind to narrow to a specific category:\n" +
			"  OBJECT        Regular output types (the ones queries return)\n" +
			"  INPUT_OBJECT  Input types used as mutation arguments\n" +
			"  ENUM          Enumeration types\n" +
			"  SCALAR        Scalar types (String, Int, custom scalars)\n" +
			"  INTERFACE     Interface definitions\n" +
			"  UNION         Union types\n\n" +
			"After finding a type name, use 'describe <TYPE>' to see its field definitions.\n\n" +
			"Examples:\n" +
			"  gqlcli types\n" +
			"  gqlcli types --kind INPUT_OBJECT\n" +
			"  gqlcli types --filter user",
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
				Usage:   "Output format: toon (default), json, table, compact",
				Value:   "toon",
			},
			&cli.StringFlag{
				Name:  "env",
				Usage: "Environment to use from .gqlcli.json (e.g. local, prod)",
			},
		},
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
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

			// Apply filter if provided
			if filter := c.String("filter"); filter != "" {
				typesList = filterOperations(typesList, filter)
			}

			// Apply kind filter if provided
			if kind := c.String("kind"); kind != "" {
				var filtered []interface{}
				for _, t := range typesList {
					if tm, ok := t.(map[string]interface{}); ok {
						if k, _ := tm["kind"].(string); strings.EqualFold(k, kind) {
							filtered = append(filtered, t)
						}
					}
				}
				typesList = filtered
			}

			// Default (toon) format: SDL-like output per type
			if c.String("format") == "toon" {
				fmt.Print(formatTypesAsSDL(typesList))
				return nil
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
		Description: "List all top-level Query fields available on the endpoint.\n\n" +
			"Run this first to discover what queries exist before executing one.\n" +
			"Add --args to see what arguments each field accepts.\n" +
			"Add --desc to include schema descriptions (useful for understanding intent).\n\n" +
			"Examples:\n" +
			"  gqlcli queries\n" +
			"  gqlcli queries --args --desc\n" +
			"  gqlcli queries --filter user --args",
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
			&cli.StringFlag{
				Name:  "env",
				Usage: "Environment to use from .gqlcli.json (e.g. local, prod)",
			},
		},
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
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

			// Default (toon) format: SDL-like output matching 'describe Query'
			if c.String("format") == "toon" {
				typeInfo := map[string]interface{}{
					"name":   "Query",
					"kind":   "OBJECT",
					"fields": fields,
				}
				fmt.Print(FormatTypeSDL(typeInfo, c.Bool("args"), !c.Bool("desc")))
				return nil
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
		Description: "List all top-level Mutation fields available on the endpoint.\n\n" +
			"Run this first to discover what mutations exist before executing one.\n" +
			"Add --args to see what arguments (and their types) each mutation accepts.\n" +
			"Add --desc to include schema descriptions.\n\n" +
			"Examples:\n" +
			"  gqlcli mutations\n" +
			"  gqlcli mutations --args --desc\n" +
			"  gqlcli mutations --filter create --args",
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
			&cli.StringFlag{
				Name:  "env",
				Usage: "Environment to use from .gqlcli.json (e.g. local, prod)",
			},
		},
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
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

			// Default (toon) format: SDL-like output matching 'describe Mutation'
			if c.String("format") == "toon" {
				typeInfo := map[string]interface{}{
					"name":   "Mutation",
					"kind":   "OBJECT",
					"fields": fields,
				}
				fmt.Print(FormatTypeSDL(typeInfo, c.Bool("args"), !c.Bool("desc")))
				return nil
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
		b.GetBatchCommand(),
		b.GetTypesCommand(),
		b.GetDescribeCommand(),
		b.GetQueriesCommand(),
		b.GetMutationsCommand(),
		b.GetInstallSkillCommand(),
		b.GetConfigCommand(),
		b.GetLoginCommand(),
		b.GetLogoutCommand(),
		b.GetOpCommand(),
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
			Name:  "mutation-file",
			Usage: "Path to .graphql file containing mutation",
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
		&cli.StringFlag{
			Name:  "env",
			Usage: "Environment to use from .gqlcli.json (e.g. local, prod)",
		},
		&cli.StringFlag{
			Name:  "op",
			Usage: "Named operation from .gqlcli.json (provides query/mutation string + default variables)",
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

	// Fall back to named operation
	if opName := c.String("op"); opName != "" {
		if b.projectConfig == nil {
			return "", fmt.Errorf("--op requires .gqlcli.json")
		}
		op, ok := b.projectConfig.Operations[opName]
		if !ok {
			return "", fmt.Errorf("operation %q not found in .gqlcli.json", opName)
		}
		if op.Type != "query" {
			return "", fmt.Errorf("operation %q is a %s, not a query; use the mutation command", opName, op.Type)
		}
		return op.Query, nil
	}

	return "", fmt.Errorf("query is required (use --query, --query-file, --op, or provide as argument)")
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

	// Fall back to named operation
	if opName := c.String("op"); opName != "" {
		if b.projectConfig == nil {
			return "", fmt.Errorf("--op requires .gqlcli.json")
		}
		op, ok := b.projectConfig.Operations[opName]
		if !ok {
			return "", fmt.Errorf("operation %q not found in .gqlcli.json", opName)
		}
		if op.Type != "mutation" {
			return "", fmt.Errorf("operation %q is a %s, not a mutation; use the query command", opName, op.Type)
		}
		return op.Mutation, nil
	}

	return "", fmt.Errorf("mutation is required (use --mutation, --mutation-file, --op, or provide as argument)")
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
	} else if varStr := c.String("variables"); varStr != "" {
		if err := json.Unmarshal([]byte(varStr), &variables); err != nil {
			return nil, fmt.Errorf("invalid variables JSON: %w", err)
		}
	}

	// Merge named op defaults (explicit --variables wins over defaults)
	if opName := c.String("op"); opName != "" && b.projectConfig != nil {
		if op, ok := b.projectConfig.Operations[opName]; ok && len(op.Defaults) > 0 {
			variables = mergeVariables(op.Defaults, variables)
		}
	}

	return variables, nil
}

// mergeVariables returns a new map with defaults overlaid by overrides.
// Keys in overrides take precedence.
func mergeVariables(defaults, overrides map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(defaults)+len(overrides))
	for k, v := range defaults {
		result[k] = v
	}
	for k, v := range overrides {
		result[k] = v
	}
	return result
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
	const typeRefFragment = `{
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
				}`

	query := fmt.Sprintf(`
	{
		__type(name: "%s") {
			fields {
				name
				type %s`, typeName, typeRefFragment)

	if includeDesc {
		query += `
				description`
	}

	if includeArgs {
		query += `
				args {
					name
					type ` + typeRefFragment + `
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
