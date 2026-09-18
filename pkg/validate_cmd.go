package gqlcli

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

// GetValidateCommand returns the validate subcommand.
func (b *CLIBuilder) GetValidateCommand() *cli.Command {
	return &cli.Command{
		Name:    "validate",
		Aliases: []string{"check"},
		Usage:   "Check a query or mutation against the schema without running it",
		Description: "Validate a GraphQL document against the schema and report any problems.\n\n" +
			"The operation is never sent to the server: only the schema is fetched, by\n" +
			"introspection, and the document is checked against it locally. Nothing is\n" +
			"executed, so no resolver runs and no data changes.\n\n" +
			"Document source (pick one): --query flag, --query-file, --mutation,\n" +
			"--mutation-file, or the first positional argument.\n\n" +
			"Exits 0 when the document is valid and 1 when it is not, so it can gate a\n" +
			"build. Errors are reported as line:column: message; --format json (or --jq)\n" +
			"switches to the machine-readable shape {\"valid\":false,\"errors\":[...]}.\n\n" +
			"OFFLINE\n" +
			"  --schema-file reads SDL from disk instead of introspecting, so validation\n" +
			"  needs no network and no credentials. Produce the file with 'gqlcli sdl':\n" +
			"    gqlcli sdl > schema.graphql\n" +
			"    gqlcli validate --schema-file schema.graphql '{ users { id } }'\n\n" +
			"NOTE\n" +
			"  Variable values are not part of the document and are not checked. A\n" +
			"  document declaring required variables validates on its own.\n\n" +
			"Examples:\n" +
			"  gqlcli validate '{ users { id name } }'\n" +
			"  gqlcli validate --query-file myquery.graphql\n" +
			"  gqlcli validate --mutation 'mutation { deleteUser(id:\"1\") { ok } }'\n" +
			"  gqlcli validate '{ users { nope } }' --format json",
		Flags: append(b.getOperationFlags(), schemaFileFlag()),
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			b.client = NewHTTPClient(b.config)

			document, err := b.getValidateDocument(c)
			if err != nil {
				return err
			}
			return b.validateDocument(c, document)
		},
	}
}

// GetSdlCommand returns the sdl subcommand.
func (b *CLIBuilder) GetSdlCommand() *cli.Command {
	return &cli.Command{
		Name:  "sdl",
		Usage: "Print the endpoint's schema as SDL",
		Description: "Introspect the endpoint and print its schema as an SDL document.\n\n" +
			"Unlike 'describe', which renders a compact view of selected types for\n" +
			"reading, this prints the whole schema in a form that can be loaded back:\n" +
			"save it and pass it to 'gqlcli validate --schema-file' to validate\n" +
			"documents with no network access and no credentials.\n\n" +
			"Builtin scalars and directives are left out, since every GraphQL parser\n" +
			"already supplies them.\n\n" +
			"Examples:\n" +
			"  gqlcli sdl\n" +
			"  gqlcli sdl --output schema.graphql\n" +
			"  gqlcli sdl --env prod > prod.graphql",
		Flags: b.getOperationFlags(),
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			b.client = NewHTTPClient(b.config)

			introspection, err := b.client.Introspect(context.Background())
			if err != nil {
				return err
			}
			sdl, err := SchemaSDL(introspection)
			if err != nil {
				return err
			}

			if outputFile := c.String("output"); outputFile != "" {
				return os.WriteFile(outputFile, []byte(sdl), 0644)
			}
			fmt.Fprint(c.App.Writer, sdl)
			return nil
		},
	}
}

func schemaFileFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "schema-file",
		Usage: "Validate against SDL read from this file instead of introspecting the endpoint",
	}
}

// getValidateDocument accepts a document from any of the operation sources, so
// a query or a mutation can be checked with the same command.
func (b *CLIBuilder) getValidateDocument(c *cli.Context) (string, error) {
	if c.String("mutation") != "" || c.String("mutation-file") != "" {
		return b.getMutationString(c)
	}
	document, err := b.getQueryString(c)
	if err != nil {
		return "", fmt.Errorf("document is required (use --query, --query-file, --mutation, --mutation-file, or provide as argument)")
	}
	return document, nil
}

// validateDocument checks document against the schema and reports the verdict,
// exiting non-zero when it does not hold up. It backs both the validate command
// and --validate-only, so the two report identically.
func (b *CLIBuilder) validateDocument(c *cli.Context, document string) error {
	validator, err := b.schemaValidator(c)
	if err != nil {
		return err
	}

	result := validator.Validate(document)

	// A verdict belongs in the exit code, so --format/--jq only change the
	// rendering, never whether an invalid document fails the command.
	if c.IsSet("format") || c.String("jq") != "" || c.String("output") != "" {
		if err := b.outputResultOpts(c, result.Map(), true); err != nil {
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
}

// schemaValidator builds a validator from --schema-file when given, and by
// introspecting the endpoint otherwise.
func (b *CLIBuilder) schemaValidator(c *cli.Context) (*SchemaValidator, error) {
	if schemaFile := c.String("schema-file"); schemaFile != "" {
		sdl, err := os.ReadFile(schemaFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read schema file: %w", err)
		}
		return NewSchemaValidatorFromSDL(string(sdl))
	}
	return NewSchemaValidatorFromClient(context.Background(), b.client)
}
