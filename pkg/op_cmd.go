package gqlcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"
)

// GetOpCommand returns the op subcommand for managing named operations in .gqlcli.json.
func (b *CLIBuilder) GetOpCommand() *cli.Command {
	return &cli.Command{
		Name:  "op",
		Usage: "Manage named GraphQL operations in .gqlcli.json",
		Description: "Save, list, show, and delete named GraphQL operations stored in .gqlcli.json.\n\n" +
			"Execute a saved operation with --op <name> on the query or mutation commands:\n" +
			"  gqlcli query --op get-user\n" +
			"  gqlcli mutation --op create-user --input '{\"name\":\"Alice\"}'\n\n" +
			"Default variables stored with the op are merged with any --variables you pass;\n" +
			"explicitly provided variables always win.",
		Subcommands: []*cli.Command{
			{
				Name:      "save",
				Usage:     "Save or update a named operation",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Operation name",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "query",
						Usage: "GraphQL query string (sets type=query)",
					},
					&cli.StringFlag{
						Name:  "mutation",
						Usage: "GraphQL mutation string (sets type=mutation)",
					},
					&cli.StringFlag{
						Name:  "defaults",
						Usage: "Default variables as JSON, e.g. '{\"id\":\"123\"}'",
					},
				},
				Action: func(c *cli.Context) error {
					name := c.String("name")

					queryStr := c.String("query")
					mutationStr := c.String("mutation")
					if queryStr == "" && mutationStr == "" {
						return fmt.Errorf("one of --query or --mutation is required")
					}
					if queryStr != "" && mutationStr != "" {
						return fmt.Errorf("only one of --query or --mutation may be set")
					}

					op := NamedOperation{}
					if queryStr != "" {
						op.Type = "query"
						op.Query = queryStr
					} else {
						op.Type = "mutation"
						op.Mutation = mutationStr
					}

					if defaultsStr := c.String("defaults"); defaultsStr != "" {
						var defaults map[string]interface{}
						if err := json.Unmarshal([]byte(defaultsStr), &defaults); err != nil {
							return fmt.Errorf("invalid --defaults JSON: %w", err)
						}
						op.Defaults = defaults
					}

					cfg, err := loadOrCreateProjectConfig()
					if err != nil {
						return err
					}
					if cfg.Operations == nil {
						cfg.Operations = make(map[string]NamedOperation)
					}
					cfg.Operations[name] = op
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("operation %q saved (%s)\n", name, op.Type)
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "List all saved operations",
				Action: func(c *cli.Context) error {
					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil || len(cfg.Operations) == 0 {
						fmt.Println("no operations saved")
						return nil
					}

					names := make([]string, 0, len(cfg.Operations))
					for k := range cfg.Operations {
						names = append(names, k)
					}
					sort.Strings(names)

					maxName := 0
					for _, n := range names {
						if len(n) > maxName {
							maxName = len(n)
						}
					}

					for _, n := range names {
						op := cfg.Operations[n]
						body := op.Query
						if op.Type == "mutation" {
							body = op.Mutation
						}
						// truncate long bodies for display
						if len(body) > 60 {
							body = body[:57] + "..."
						}
						body = strings.ReplaceAll(body, "\n", " ")
						fmt.Printf("%-*s  %-8s  %s\n", maxName, n, op.Type, body)
					}
					return nil
				},
			},
			{
				Name:      "show",
				Usage:     "Show full details of a saved operation",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Operation name",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					name := c.String("name")

					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil {
						return fmt.Errorf(".gqlcli.json not found")
					}
					op, ok := cfg.Operations[name]
					if !ok {
						return fmt.Errorf("operation %q not found", name)
					}

					fmt.Printf("name:  %s\n", name)
					fmt.Printf("type:  %s\n", op.Type)
					if op.Type == "query" {
						fmt.Printf("query: %s\n", op.Query)
					} else {
						fmt.Printf("mutation: %s\n", op.Mutation)
					}
					if len(op.Defaults) > 0 {
						data, _ := json.MarshalIndent(op.Defaults, "", "  ")
						fmt.Printf("defaults: %s\n", data)
					}
					return nil
				},
			},
			{
				Name:      "delete",
				Usage:     "Delete a saved operation",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Operation name",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					name := c.String("name")

					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil {
						return fmt.Errorf(".gqlcli.json not found")
					}
					if _, ok := cfg.Operations[name]; !ok {
						return fmt.Errorf("operation %q not found", name)
					}
					delete(cfg.Operations, name)
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("operation %q deleted\n", name)
					return nil
				},
			},
		},
	}
}
