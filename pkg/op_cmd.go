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
						Name:  "subscription",
						Usage: "GraphQL subscription string (sets type=subscription)",
					},
					&cli.StringFlag{
						Name:  "defaults",
						Usage: "Default variables as JSON, e.g. '{\"id\":\"123\"}'",
					},
				},
				Action: func(c *cli.Context) error {
					name := c.String("name")
					if name == "" {
						return fmt.Errorf("--name must not be empty")
					}

					queryStr := c.String("query")
					mutationStr := c.String("mutation")
					subscriptionStr := c.String("subscription")
					set := 0
					for _, s := range []string{queryStr, mutationStr, subscriptionStr} {
						if s != "" {
							set++
						}
					}
					if set == 0 {
						return fmt.Errorf("one of --query, --mutation, or --subscription is required")
					}
					if set > 1 {
						return fmt.Errorf("only one of --query, --mutation, or --subscription may be set")
					}

					op := NamedOperation{}
					switch {
					case queryStr != "":
						op.Type = "query"
						op.Query = queryStr
					case mutationStr != "":
						op.Type = "mutation"
						op.Mutation = mutationStr
					default:
						op.Type = "subscription"
						op.Query = subscriptionStr
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

					maxName, maxType := 0, 0
					for _, n := range names {
						if len(n) > maxName {
							maxName = len(n)
						}
						if t := len(cfg.Operations[n].Type); t > maxType {
							maxType = t
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
						fmt.Printf("%-*s  %-*s  %s\n", maxName, n, maxType, op.Type, body)
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
					switch op.Type {
					case "query", "subscription":
						fmt.Printf("%s: %s\n", op.Type, op.Query)
					default:
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
