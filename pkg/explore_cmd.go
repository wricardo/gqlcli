package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

// GetExploreCommand returns the local GraphQL schema exploration command.
func (b *CLIBuilder) GetExploreCommand() *cli.Command {
	return &cli.Command{
		Name:  "explore",
		Usage: "Query a local GraphQL API describing the target's cached schema",
		Description: "Explorer documents run locally, never against the target endpoint. " +
			"On cache miss or expiry, target schema introspection may contact the endpoint. " +
			"Use --offline to require an existing cache, or --schema-file for SDL on disk. " +
			"Use 'schema refresh --env NAME' or --refresh-schema after a deploy. " +
			"The explorer API is printed with --print-schema.",
		Flags: append(b.exploreFlags(), schemaFileFlag(),
			&cli.BoolFlag{Name: "offline", Usage: "Require an existing cached introspection result; never contact the target"},
			&cli.BoolFlag{Name: "print-schema", Usage: "Print the explorer's GraphQL schema and exit"}),
		Action: func(c *cli.Context) error {
			if c.Bool("print-schema") {
				_, err := fmt.Fprint(c.App.Writer, ExplorerSDL())
				return err
			}
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			b.client = NewHTTPClient(b.config)
			document, err := b.getQueryString(c)
			if err != nil {
				return err
			}
			variables, err := b.getVariables(c)
			if err != nil {
				return err
			}
			var engine *SchemaExplorer
			if file := c.String("schema-file"); file != "" {
				data, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("read target schema file: %w", err)
				}
				engine, err = NewSchemaExplorerFromSDL(string(data))
				if err != nil {
					return err
				}
			} else if c.Bool("offline") {
				client := b.client.(*HTTPClient)
				path := client.schemaCachePath()
				if path == "" {
					return fmt.Errorf("offline explorer requires schema caching (--schema-cache-ttl > 0)")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("offline explorer: no cached schema for selected endpoint: %w", err)
				}
				var result map[string]interface{}
				if err := json.Unmarshal(data, &result); err != nil || !hasIntrospectedSchema(result) {
					return fmt.Errorf("offline explorer: invalid cached target schema at %s", path)
				}
				engine, err = NewSchemaExplorerFromIntrospection(result)
				if err != nil {
					return err
				}
			} else {
				engine, err = NewSchemaExplorerFromClient(context.Background(), b.client)
				if err != nil {
					return err
				}
			}
			result, err := engine.ExecuteOperation(c.Context, document, variables, c.String("operation"))
			if err != nil {
				return err
			}
			_, hasErrors := result["errors"]
			if err := b.outputExplorerResult(c, result, !hasErrors); err != nil {
				return err
			}
			if hasErrors && c.Bool("strict") {
				return cli.Exit("", 1)
			}
			return nil
		},
	}
}

// Explorer JSON retains selected null fields, as required by GraphQL. Other
// commands keep their established JSON formatter's null-stripping behavior.
func (b *CLIBuilder) outputExplorerResult(c *cli.Context, result map[string]interface{}, allowJQ bool) error {
	if c.String("format") != "json" {
		return b.outputResultOpts(c, result, allowJQ)
	}
	if allowJQ && c.String("jq") != "" {
		return b.outputJQResult(c, result, c.String("jq"))
	}
	return writeExplorerJSON(c, result)
}

func writeExplorerJSON(c *cli.Context, result map[string]interface{}) error {
	var data []byte
	var err error
	if c.Bool("pretty") {
		data, err = json.MarshalIndent(result, "", "  ")
	} else {
		data, err = json.Marshal(result)
	}
	if err != nil {
		return err
	}
	if path := c.String("output"); path != "" {
		return os.WriteFile(path, data, 0644)
	}
	_, err = fmt.Fprintln(c.App.Writer, string(data))
	return err
}

func (b *CLIBuilder) exploreFlags() []cli.Flag {
	flags := b.getOperationFlags()
	for i, flag := range flags {
		if names := flag.Names(); len(names) > 0 && names[0] == "format" {
			flags[i] = &cli.StringFlag{Name: "format", Aliases: []string{"f"}, Value: "json", Usage: "Output format (json by default; preserves GraphQL null fields)"}
			break
		}
	}
	return flags
}
