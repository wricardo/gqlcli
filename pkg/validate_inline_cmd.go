package gqlcli

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

// validateCommand returns the inline-mode "validate" command. The embedded
// schema is already parsed, so validation costs no introspection round trip
// and nothing is executed.
func (cs *InlineCommandSet) validateCommand() *cli.Command {
	return &cli.Command{
		Name:    "validate",
		Aliases: []string{"check"},
		Usage:   "Check a query or mutation against the schema without running it",
		Description: "Validate a GraphQL document against the embedded schema.\n\n" +
			"Nothing is executed, so no resolver runs and no data changes. Exits 0 when\n" +
			"the document is valid and 1 when it is not.\n\n" +
			"Variable values are not part of the document and are not checked.\n\n" +
			"Examples:\n" +
			"  validate '{ users { id name } }'\n" +
			"  validate --file myquery.graphql\n" +
			"  validate '{ users { nope } }' --format json",
		Flags: inlineOperationFlags("toon"),
		Action: func(c *cli.Context) error {
			document, _, err := readInlineOperation(c)
			if err != nil {
				return err
			}

			schema := cs.exec.Schema()
			if schema == nil {
				return fmt.Errorf("validate: the executor has no schema")
			}
			result := NewSchemaValidator(schema).Validate(document)

			if c.IsSet("format") || c.String("output") != "" {
				if err := printInlineValue(c, result.Map()); err != nil {
					return err
				}
			} else if result.Valid {
				fmt.Fprintln(c.App.Writer, "valid")
			} else {
				fmt.Fprintln(c.App.ErrWriter, result.String())
			}

			if !result.Valid {
				return cli.Exit("", 1)
			}
			return nil
		},
	}
}
