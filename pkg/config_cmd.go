package gqlcli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"
)

func saveProjectConfig(cfg *ProjectConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding .gqlcli.json: %w", err)
	}
	if err := os.WriteFile(".gqlcli.json", data, 0644); err != nil {
		return fmt.Errorf("writing .gqlcli.json: %w", err)
	}
	return nil
}

func loadOrCreateProjectConfig() (*ProjectConfig, error) {
	cfg, err := LoadProjectConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &ProjectConfig{Environments: make(map[string]EnvConfig)}
	}
	if cfg.Environments == nil {
		cfg.Environments = make(map[string]EnvConfig)
	}
	return cfg, nil
}

func parseHeaders(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid --header %q: must be KEY=VALUE with a non-empty KEY", pair)
		}
		headers[parts[0]] = parts[1]
	}
	return headers, nil
}

// GetConfigCommand returns the config subcommand for managing .gqlcli.json.
func (b *CLIBuilder) GetConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Manage .gqlcli.json project configuration",
		Description: "Create and manage the .gqlcli.json file that defines named environments\n" +
			"(URLs + headers) for use with --env on any gqlcli command.",
		Subcommands: []*cli.Command{
			{
				Name:  "init",
				Usage: "Create a new .gqlcli.json with a sample environment",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "force",
						Usage: "Overwrite existing .gqlcli.json",
					},
				},
				Action: func(c *cli.Context) error {
					if _, err := os.Stat(".gqlcli.json"); err == nil && !c.Bool("force") {
						return fmt.Errorf(".gqlcli.json already exists; use --force to overwrite")
					}
					cfg := &ProjectConfig{
						Default: "local",
						Environments: map[string]EnvConfig{
							"local": {
								URL:     "http://localhost:8080/graphql",
								Headers: map[string]string{"Authorization": "Bearer dev-token"},
							},
						},
					}
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Println("created .gqlcli.json")
					return nil
				},
			},
			{
				Name:  "add-env",
				Usage: "Add or update a named environment",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Environment name",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "url",
						Usage:    "GraphQL endpoint URL",
						Required: true,
					},
					&cli.StringSliceFlag{
						Name:    "header",
						Aliases: []string{"H"},
						Usage:   "Header as KEY=VALUE (repeatable)",
					},
				},
				Action: func(c *cli.Context) error {
					headers, err := parseHeaders(c.StringSlice("header"))
					if err != nil {
						return err
					}
					cfg, err := loadOrCreateProjectConfig()
					if err != nil {
						return err
					}
					cfg.Environments[c.String("name")] = EnvConfig{
						URL:     c.String("url"),
						Headers: headers,
					}
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("environment %q saved\n", c.String("name"))
					return nil
				},
			},
			{
				Name:  "remove-env",
				Usage: "Remove a named environment",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Environment name",
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
					if _, ok := cfg.Environments[name]; !ok {
						return fmt.Errorf("environment %q not found", name)
					}
					delete(cfg.Environments, name)
					if cfg.Default == name {
						cfg.Default = ""
						fmt.Fprintf(os.Stderr, "warning: removed the default environment; use 'config set-default' to set a new one\n")
					}
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("environment %q removed\n", name)
					return nil
				},
			},
			{
				Name:  "set-default",
				Usage: "Set the default environment",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Aliases:  []string{"n"},
						Usage:    "Environment name",
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
					if _, ok := cfg.Environments[name]; !ok {
						return fmt.Errorf("environment %q not found; add it first with 'config add-env'", name)
					}
					cfg.Default = name
					if err := saveProjectConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("default environment set to %q\n", name)
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "List all configured environments",
				Action: func(c *cli.Context) error {
					cfg, err := LoadProjectConfig()
					if err != nil {
						return err
					}
					if cfg == nil {
						fmt.Println("no .gqlcli.json found")
						return nil
					}
					if len(cfg.Environments) == 0 {
						fmt.Println("no environments configured")
						return nil
					}
					names := make([]string, 0, len(cfg.Environments))
					for k := range cfg.Environments {
						names = append(names, k)
					}
					sort.Strings(names)

					maxNameLen := 0
					maxURLLen := 0
					for _, name := range names {
						if len(name) > maxNameLen {
							maxNameLen = len(name)
						}
						if u := len(cfg.Environments[name].URL); u > maxURLLen {
							maxURLLen = u
						}
					}

					for _, name := range names {
						env := cfg.Environments[name]
						prefix := "  "
						if name == cfg.Default {
							prefix = "* "
						}
						keys := make([]string, 0, len(env.Headers))
						for k := range env.Headers {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						headerStr := ""
						if len(keys) > 0 {
							headerStr = "[" + strings.Join(keys, ", ") + "]"
						}
						fmt.Printf("%s%-*s  %-*s  %s\n", prefix, maxNameLen, name, maxURLLen, env.URL, headerStr)
					}
					return nil
				},
			},
		},
	}
}
