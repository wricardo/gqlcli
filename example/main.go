package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
	"github.com/wricardo/gqlcli/pkg"
)

func main() {
	// Create config with default values
	cfg := &gqlcli.Config{
		URL:    "http://localhost:8080/graphql",
		Format: "toon",  // Token-optimized format (40-60% smaller than JSON)
		Pretty: false,
		Timeout: 30,
	}

	// Create CLI builder
	builder := gqlcli.NewCLIBuilder(cfg)

	// Create CLI app
	app := &cli.App{
		Name:    "gqlcli",
		Usage:   "Powerful GraphQL CLI tool",
		Version: "1.0.0",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "url",
				Aliases: []string{"u"},
				Usage:   "GraphQL endpoint URL",
				Value:   "http://localhost:8080/graphql",
				EnvVars: []string{"GRAPHQL_URL"},
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Usage:   "Output format: json, table, compact, toon, llm",
				Value:   "json",
			},
			&cli.BoolFlag{
				Name:    "pretty",
				Aliases: []string{"p"},
				Usage:   "Pretty print JSON output",
				Value:   false,
			},
		},
		Before: func(c *cli.Context) error {
			// Update config from flags
			cfg.URL = c.String("url")
			cfg.Format = c.String("format")
			cfg.Pretty = c.Bool("pretty")
			return nil
		},
	}

	// Register all commands
	builder.RegisterCommands(app)

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
