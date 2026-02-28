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
	}

	builder.RegisterCommands(app)

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
