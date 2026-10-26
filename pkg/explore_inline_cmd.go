package gqlcli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v2"
)

func (cs *InlineCommandSet) exploreCommand() *cli.Command {
	return &cli.Command{
		Name:  "explore",
		Usage: "Query the embedded executor's schema locally",
		Flags: append(inlineOperationFlags("json"), &cli.StringFlag{Name: "operation", Usage: "Named explorer operation"},
			&cli.BoolFlag{Name: "print-schema", Usage: "Print the explorer GraphQL schema"}),
		Action: func(c *cli.Context) error {
			if c.Bool("print-schema") {
				_, err := fmt.Fprint(c.App.Writer, ExplorerSDL())
				return err
			}
			document, vars, err := readInlineOperation(c)
			if err != nil {
				return err
			}
			explorer, err := NewSchemaExplorer(cs.exec.Schema())
			if err != nil {
				return err
			}
			result, err := explorer.ExecuteOperation(context.Background(), document, vars, c.String("operation"))
			if err != nil {
				return err
			}
			if c.String("format") == "json" {
				if err := writeExplorerJSON(c, result); err != nil {
					return err
				}
			} else if err := printInlineValue(c, result); err != nil {
				return err
			}
			if _, hasErrors := result["errors"]; hasErrors {
				return cli.Exit("", 1)
			}
			return nil
		},
	}
}
