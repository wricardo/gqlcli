package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"
)

// GetScriptCommand returns the JavaScript scripting command.
func (b *CLIBuilder) GetScriptCommand() *cli.Command {
	return &cli.Command{
		Name:    "script",
		Aliases: []string{"js"},
		Usage:   "Run or manage JavaScript automation scripts",
		Description: "Execute JavaScript workflows that orchestrate GraphQL queries and mutations with loops/conditionals.\n\n" +
			"Run sources from:\n" +
			"  --file PATH         (read script from file)\n" +
			"  --source '...'      (inline script)\n" +
			"  --op NAME           (saved inline script from .gqlcli.json scripts)\n\n" +
			"Script signature:\n" +
			"  async function run(gql, input) { ... }\n\n" +
			"The gql helper exposes:\n" +
			"  gql.query(query, variables?, operationName?)\n" +
			"  gql.mutation(mutation, variables?, operationName?)\n" +
			"  gql.request({ type, query|mutation, variables, operationName })\n" +
			"  gql.each(items, worker, { concurrency?, stopOnError?, onError? })\n\n" +
			"Examples:\n" +
			"  gqlcli script --file ./disableUsers.js\n" +
			"  gqlcli script --op disable-inactive-users\n" +
			"  gqlcli script save --name disable-inactive-users --source-file ./disableUsers.js",
		Flags: scriptRunFlags(b),
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			b.client = NewHTTPClient(b.config)

			sourceName, source, fnName, defaults, err := resolveScriptSource(c)
			if err != nil {
				return err
			}

			input, err := readScriptInput(c)
			if err != nil {
				return err
			}
			if len(defaults) > 0 {
				input = mergeVariables(defaults, input)
			}

			runner := NewScriptRunner(b.client)
			result, err := runner.RunSource(context.Background(), sourceName, source, fnName, input)
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
		Subcommands: []*cli.Command{
			{
				Name:  "save",
				Usage: "Save or update an inline script in .gqlcli.json scripts",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Aliases: []string{"n"}, Usage: "Script name", Required: true},
					&cli.StringFlag{Name: "source", Usage: "Inline JavaScript source"},
					&cli.StringFlag{Name: "source-file", Usage: "Read JavaScript source from file and store inline"},
					&cli.StringFlag{Name: "function", Value: "run", Usage: "Function name to execute"},
					&cli.StringFlag{Name: "defaults", Usage: "Default input object as JSON"},
					&cli.StringFlag{Name: "description", Usage: "Optional description"},
					&cli.StringFlag{Name: "lang", Value: "javascript", Usage: "Script language metadata (default: javascript)"},
				},
				Action: func(c *cli.Context) error {
					source, err := readScriptSourceFlags(c)
					if err != nil {
						return err
					}
					defaults, err := parseScriptDefaults(c.String("defaults"))
					if err != nil {
						return err
					}

					cfg, err := loadOrCreateProjectConfig()
					if err != nil {
						return err
					}
					if cfg.Scripts == nil {
						cfg.Scripts = make(map[string]NamedScript)
					}

					cfg.Scripts[c.String("name")] = NamedScript{
						Lang:        c.String("lang"),
						Function:    c.String("function"),
						Source:      source,
						Defaults:    defaults,
						Description: c.String("description"),
					}
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("script %q saved\n", c.String("name"))
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "List saved scripts",
				Action: func(c *cli.Context) error {
					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil || len(cfg.Scripts) == 0 {
						fmt.Println("no scripts saved")
						return nil
					}

					names := make([]string, 0, len(cfg.Scripts))
					for n := range cfg.Scripts {
						names = append(names, n)
					}
					sort.Strings(names)

					maxName := 0
					for _, n := range names {
						if len(n) > maxName {
							maxName = len(n)
						}
					}

					for _, n := range names {
						s := cfg.Scripts[n]
						desc := s.Description
						if strings.TrimSpace(desc) == "" {
							desc = strings.ReplaceAll(strings.TrimSpace(s.Source), "\n", " ")
							if len(desc) > 60 {
								desc = desc[:57] + "..."
							}
						}
						fn := s.Function
						if fn == "" {
							fn = "run"
						}
						fmt.Printf("%-*s  fn=%s  %s\n", maxName, n, fn, desc)
					}
					return nil
				},
			},
			{
				Name:  "show",
				Usage: "Show a saved script",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Aliases: []string{"n"}, Usage: "Script name", Required: true},
				},
				Action: func(c *cli.Context) error {
					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil {
						return fmt.Errorf(".gqlcli.json not found")
					}
					s, ok := cfg.Scripts[c.String("name")]
					if !ok {
						return fmt.Errorf("script %q not found", c.String("name"))
					}
					fn := s.Function
					if fn == "" {
						fn = "run"
					}
					lang := s.Lang
					if lang == "" {
						lang = "javascript"
					}
					fmt.Printf("name: %s\n", c.String("name"))
					fmt.Printf("lang: %s\n", lang)
					fmt.Printf("function: %s\n", fn)
					if s.Description != "" {
						fmt.Printf("description: %s\n", s.Description)
					}
					if len(s.Defaults) > 0 {
						data, _ := json.MarshalIndent(s.Defaults, "", "  ")
						fmt.Printf("defaults: %s\n", data)
					}
					fmt.Printf("source:\n%s\n", s.Source)
					return nil
				},
			},
			{
				Name:  "delete",
				Usage: "Delete a saved script",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Aliases: []string{"n"}, Usage: "Script name", Required: true},
				},
				Action: func(c *cli.Context) error {
					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil {
						return fmt.Errorf(".gqlcli.json not found")
					}
					if _, ok := cfg.Scripts[c.String("name")]; !ok {
						return fmt.Errorf("script %q not found", c.String("name"))
					}
					delete(cfg.Scripts, c.String("name"))
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("script %q deleted\n", c.String("name"))
					return nil
				},
			},
		},
	}
}

func scriptRunFlags(b *CLIBuilder) []cli.Flag {
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
		insecureFlag(),
		&cli.IntFlag{Name: "timeout", Usage: "Request timeout in seconds (default: 30)", Value: b.config.Timeout},
		&cli.IntFlag{Name: "retry", Usage: "Retry count for transient failures (connection errors, 408, 429, 5xx; default: 0)", Value: b.config.RetryCount},
		&cli.DurationFlag{Name: "retry-delay", Usage: "Delay between retries (e.g. 500ms, 2s; default: 1s when --retry > 0)", Value: b.config.RetryDelay},
		&cli.BoolFlag{Name: "strict", Usage: "Exit non-zero when response.errors is present (default true; use --strict=false to disable)", Value: b.config.Strict},
		&cli.StringFlag{Name: "env", Usage: "Environment to use from .gqlcli.json (e.g. local, prod)"},
		headerFlag(),
		&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "Path to JavaScript file"},
		&cli.StringFlag{Name: "source", Usage: "Inline JavaScript source code"},
		&cli.StringFlag{Name: "op", Usage: "Saved script name from .gqlcli.json scripts"},
		&cli.StringFlag{Name: "function", Value: "run", Usage: "JavaScript function name to call"},
		&cli.StringFlag{Name: "arg", Usage: "Optional JSON object passed as the second script argument"},
		&cli.StringFlag{Name: "arg-file", Usage: "Path to JSON file passed as the second script argument"},
		&cli.StringFlag{Name: "output", Usage: "Write script return value as JSON to a file (default: stdout)"},
	}
}

func resolveScriptSource(c *cli.Context) (sourceName, source, function string, defaults map[string]interface{}, err error) {
	file := c.String("file")
	inlineSource := c.String("source")
	opName := c.String("op")

	set := 0
	if file != "" {
		set++
	}
	if inlineSource != "" {
		set++
	}
	if opName != "" {
		set++
	}
	if set == 0 {
		return "", "", "", nil, fmt.Errorf("one of --file, --source, or --op is required")
	}
	if set > 1 {
		return "", "", "", nil, fmt.Errorf("only one of --file, --source, or --op may be set")
	}

	function = c.String("function")
	if opName != "" {
		cfg, err := LoadProjectConfig()
		if err != nil {
			return "", "", "", nil, err
		}
		if cfg == nil {
			return "", "", "", nil, fmt.Errorf("--op requires .gqlcli.json")
		}
		s, ok := cfg.Scripts[opName]
		if !ok {
			return "", "", "", nil, fmt.Errorf("script %q not found in .gqlcli.json", opName)
		}
		if strings.TrimSpace(s.Source) == "" {
			return "", "", "", nil, fmt.Errorf("script %q has empty source", opName)
		}
		if !c.IsSet("function") && strings.TrimSpace(s.Function) != "" {
			function = s.Function
		}
		if function == "" {
			function = "run"
		}
		return ".gqlcli.json:scripts." + opName, s.Source, function, s.Defaults, nil
	}

	if inlineSource != "" {
		if function == "" {
			function = "run"
		}
		return "--source", inlineSource, function, nil, nil
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("failed to read script file: %w", err)
	}
	if function == "" {
		function = "run"
	}
	return file, string(data), function, nil, nil
}

func readScriptSourceFlags(c *cli.Context) (string, error) {
	source := c.String("source")
	sourceFile := c.String("source-file")
	if source == "" && sourceFile == "" {
		return "", fmt.Errorf("one of --source or --source-file is required")
	}
	if source != "" && sourceFile != "" {
		return "", fmt.Errorf("only one of --source or --source-file may be set")
	}
	if source != "" {
		return source, nil
	}
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		return "", fmt.Errorf("failed to read --source-file: %w", err)
	}
	return string(data), nil
}

func parseScriptDefaults(raw string) (map[string]interface{}, error) {
	if raw == "" {
		return nil, nil
	}
	var defaults map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &defaults); err != nil {
		return nil, fmt.Errorf("invalid --defaults JSON: %w", err)
	}
	return defaults, nil
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
