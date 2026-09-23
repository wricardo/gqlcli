package gqlcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v2"
)

// GetSchemaCommand returns the schema command group, which manages the
// on-disk introspection cache that schema commands read from.
func (b *CLIBuilder) GetSchemaCommand() *cli.Command {
	return &cli.Command{
		Name:  "schema",
		Usage: "Manage the on-disk schema cache",
		Description: "Schema commands (describe, queries, mutations, types, sdl, validate, embed)\n" +
			"reuse the endpoint's introspection result cached on disk for --schema-cache-ttl.\n" +
			"The cache entry is keyed by URL and headers, so each env and credential has its own.\n\n" +
			"Examples:\n" +
			"  gqlcli schema refresh --env prod   # re-introspect after a schema deploy\n" +
			"  gqlcli schema status --env prod    # path, age and freshness of the entry\n" +
			"  gqlcli schema clear --env prod     # delete this env's entry\n" +
			"  gqlcli schema clear --all          # delete every cached schema",
		Subcommands: []*cli.Command{
			b.schemaRefreshCommand(),
			b.schemaStatusCommand(),
			b.schemaClearCommand(),
		},
	}
}

func (b *CLIBuilder) schemaCacheFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "url",
			Aliases: []string{"u"},
			Usage:   "GraphQL endpoint URL (env: GRAPHQL_URL)",
			Value:   b.config.URL,
			EnvVars: []string{"GRAPHQL_URL"},
		},
		&cli.StringFlag{
			Name:  "env",
			Usage: "Environment to use from .gqlcli.json (e.g. local, prod)",
		},
		headerFlag(),
		insecureFlag(),
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"d"},
			Usage:   "Enable debug mode (logs HTTP requests/responses)",
		},
		&cli.IntFlag{
			Name:  "timeout",
			Usage: "Request timeout in seconds; 0 disables the timeout (default)",
		},
		schemaCacheTTLFlag(),
	}
}

// schemaCacheClient resolves the env like any other command and returns a
// client whose cache path matches what those commands would use.
func (b *CLIBuilder) schemaCacheClient(c *cli.Context, requireEnabled bool) (*HTTPClient, error) {
	if err := b.applyEnvConfig(c); err != nil {
		return nil, err
	}
	// Surface GraphQL errors from introspection instead of a vague "no schema".
	b.config.Strict = true
	client := NewHTTPClient(b.config)
	b.client = client
	if requireEnabled && client.schemaCachePath() == "" {
		return nil, fmt.Errorf("schema cache is disabled (--schema-cache-ttl is 0)")
	}
	return client, nil
}

func (b *CLIBuilder) schemaRefreshCommand() *cli.Command {
	return &cli.Command{
		Name:  "refresh",
		Usage: "Introspect the endpoint and overwrite its cached schema",
		Flags: b.schemaCacheFlags(),
		Action: func(c *cli.Context) error {
			client, err := b.schemaCacheClient(c, true)
			if err != nil {
				return err
			}
			start := time.Now()
			result, err := client.executeOperation(context.Background(), FullIntrospectionQuery, nil, "")
			if err != nil {
				return err
			}
			if err := client.writeSchemaCache(result); err != nil {
				return fmt.Errorf("writing schema cache: %w", err)
			}
			fmt.Fprintf(c.App.Writer, "refreshed %s (%d types, %s)\n",
				client.schemaCachePath(), countSchemaTypes(result), time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
}

func (b *CLIBuilder) schemaStatusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show the cache entry for the selected endpoint",
		Flags: b.schemaCacheFlags(),
		Action: func(c *cli.Context) error {
			client, err := b.schemaCacheClient(c, true)
			if err != nil {
				return err
			}
			path := client.schemaCachePath()
			w := c.App.Writer
			fmt.Fprintf(w, "url:   %s\n", client.config.URL)
			fmt.Fprintf(w, "path:  %s\n", path)
			fmt.Fprintf(w, "ttl:   %s\n", client.config.SchemaCacheTTL)
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				fmt.Fprintln(w, "state: missing (next schema command will introspect)")
				return nil
			}
			if err != nil {
				return err
			}
			age := time.Since(info.ModTime())
			state := "fresh"
			if age >= client.config.SchemaCacheTTL {
				state = "expired (next schema command will introspect)"
			}
			fmt.Fprintf(w, "age:   %s\n", age.Round(time.Second))
			fmt.Fprintf(w, "size:  %d bytes\n", info.Size())
			fmt.Fprintf(w, "state: %s\n", state)
			return nil
		},
	}
}

func (b *CLIBuilder) schemaClearCommand() *cli.Command {
	return &cli.Command{
		Name:  "clear",
		Usage: "Delete the cached schema for the selected endpoint (or all with --all)",
		Flags: append(b.schemaCacheFlags(), &cli.BoolFlag{
			Name:  "all",
			Usage: "Delete every cached schema, for all endpoints and credentials",
		}),
		Action: func(c *cli.Context) error {
			all := c.Bool("all")
			client, err := b.schemaCacheClient(c, !all)
			if err != nil {
				return err
			}
			var paths []string
			if all {
				paths, _ = filepath.Glob(filepath.Join(client.schemaCacheDir(), "schema-*.json"))
			} else {
				paths = []string{client.schemaCachePath()}
			}
			removed := 0
			for _, p := range paths {
				err := os.Remove(p)
				if err == nil {
					removed++
					continue
				}
				if !os.IsNotExist(err) {
					return err
				}
			}
			fmt.Fprintf(c.App.Writer, "removed %d cached schema(s)\n", removed)
			return nil
		},
	}
}

func countSchemaTypes(result map[string]interface{}) int {
	data, _ := result["data"].(map[string]interface{})
	schema, _ := data["__schema"].(map[string]interface{})
	types, _ := schema["types"].([]interface{})
	return len(types)
}
