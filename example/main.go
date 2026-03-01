// Package main shows how to build a CLI backed by an inline GraphQL schema.
// Queries and mutations persist to store.json.
package main

import (
	"log"
	"os"

	"github.com/urfave/cli/v2"
	gqlcli "github.com/wricardo/gqlcli/pkg"

	"github.com/wricardo/gqlcli/example/graph"
)

func main() {
	r := graph.NewResolver()
	execSchema := graph.NewExecutableSchema(graph.Config{Resolvers: r})

	exec := gqlcli.NewInlineExecutor(execSchema,
		gqlcli.WithSchemaHints(),
	)

	commands := gqlcli.NewInlineCommandSet(exec)

	app := &cli.App{
		Name:    "myapp",
		Usage:   "Book management CLI",
		Version: "0.1.0",
	}
	commands.Mount(app)

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
