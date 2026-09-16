package gqlcli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

// GetEmbedCommand returns the "embed" command group: build a semantic index of
// the schema's types, then search it with plain language.
func (b *CLIBuilder) GetEmbedCommand() *cli.Command {
	return &cli.Command{
		Name:  "embed",
		Usage: "Build and search a semantic index of the schema's types",
		Description: "Embed every GraphQL type into a vector index, then find the types closest to a description.\n\n" +
			"Useful when you know what you want but not what it is called:\n" +
			"  gqlcli embed index\n" +
			"  gqlcli embed search 'thing that stores a customer billing address'\n\n" +
			"The index is a JSON file (default .gqlcli-embeddings[.<env>].json) holding each type's\n" +
			"SDL and vector, so 'search' makes one embedding call and no introspection call.\n\n" +
			"Embeddings come from the Venu API; set VENU_API_KEY (and VENU_URL to change host).",
		Subcommands: []*cli.Command{
			b.embedIndexCommand(),
			b.embedSearchCommand(),
		},
	}
}

func embedderFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "embedding-url",
			Usage:   "Embeddings API base URL (env: VENU_URL)",
			EnvVars: []string{"VENU_URL"},
		},
		&cli.StringFlag{
			Name:    "embedding-key",
			Usage:   "Embeddings API key, sent as X-API-Key (env: VENU_API_KEY)",
			EnvVars: []string{"VENU_API_KEY"},
		},
		&cli.StringFlag{
			Name:    "embedding-model",
			Usage:   "Embedding model (default: " + DefaultEmbeddingModel + ")",
			EnvVars: []string{"VENU_EMBEDDING_MODEL"},
		},
	}
}

func (b *CLIBuilder) embedderFromFlags(c *cli.Context) (*VenuEmbedder, error) {
	return NewVenuEmbedder(
		WithEmbeddingBaseURL(c.String("embedding-url")),
		WithEmbeddingAPIKey(c.String("embedding-key")),
		WithEmbeddingModel(c.String("embedding-model")),
	)
}

// effectiveEnvName returns the environment the command is running against:
// the --env flag when set, otherwise the default from .gqlcli.json.
func (b *CLIBuilder) effectiveEnvName(c *cli.Context) string {
	if env := c.String("env"); env != "" {
		return env
	}
	if b.projectConfig != nil {
		return b.projectConfig.Default
	}
	return ""
}

// resolveEmbeddingIndexPath picks the index file: an explicit flag wins, then
// the environment's "embeddings" key in .gqlcli.json, then the per-env default.
func (b *CLIBuilder) resolveEmbeddingIndexPath(c *cli.Context, flagName string) string {
	if p := c.String(flagName); p != "" {
		return p
	}
	envName := b.effectiveEnvName(c)
	if b.projectConfig != nil {
		if env, err := b.projectConfig.Resolve(c.String("env")); err == nil && env != nil && env.Embeddings != "" {
			return env.Embeddings
		}
	}
	return EmbeddingIndexPath(envName)
}

func (b *CLIBuilder) embedIndexCommand() *cli.Command {
	flags := []cli.Flag{
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
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "Index file to write (default: .gqlcli-embeddings[.<env>].json)",
		},
		&cli.StringSliceFlag{
			Name:    "kind",
			Aliases: []string{"k"},
			Usage:   "Restrict to type kinds: OBJECT, INPUT_OBJECT, ENUM, INTERFACE, UNION, SCALAR (repeatable; default: all)",
		},
		&cli.StringSliceFlag{
			Name:  "include",
			Usage: "Only index type names matching this glob (repeatable)",
		},
		&cli.StringSliceFlag{
			Name:  "exclude",
			Usage: "Skip type names matching this glob (repeatable)",
		},
		&cli.BoolFlag{
			Name:  "args",
			Usage: "Include field argument signatures in the indexed SDL (default true)",
			Value: true,
		},
		&cli.IntFlag{
			Name:  "concurrency",
			Usage: "Parallel embedding requests",
			Value: 4,
		},
		&cli.IntFlag{
			Name:  "max-chars",
			Usage: "Truncate each type's text to this many characters before embedding",
			Value: DefaultEmbeddingMaxChars,
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "Re-embed every type instead of reusing unchanged vectors from the existing index",
		},
		&cli.BoolFlag{
			Name:    "quiet",
			Aliases: []string{"q"},
			Usage:   "Suppress per-type progress output",
		},
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"d"},
			Usage:   "Enable debug mode (logs HTTP requests/responses)",
			Value:   b.config.Debug,
		},
		insecureFlag(),
		&cli.IntFlag{Name: "timeout", Usage: "Request timeout in seconds (default: 30)", Value: b.config.Timeout},
		&cli.IntFlag{Name: "retry", Usage: "Retry count for transient failures", Value: b.config.RetryCount},
		&cli.DurationFlag{Name: "retry-delay", Usage: "Delay between retries (e.g. 500ms, 2s)", Value: b.config.RetryDelay},
		&cli.BoolFlag{Name: "strict", Usage: "Exit non-zero when response.errors is present", Value: b.config.Strict},
		headerFlag(),
	}
	flags = append(flags, embedderFlags()...)

	return &cli.Command{
		Name:  "index",
		Usage: "Introspect the schema and write a type embedding index",
		Description: "Introspect the endpoint, embed each type's SDL, and write the index as JSON.\n\n" +
			"Re-running reuses vectors for types whose text has not changed, so only new or\n" +
			"edited types cost an API call. Use --force to re-embed everything.\n\n" +
			"Examples:\n" +
			"  gqlcli embed index\n" +
			"  gqlcli embed index --env prod --kind OBJECT --kind INPUT_OBJECT\n" +
			"  gqlcli embed index --exclude '__*' --exclude '*Connection' --concurrency 8",
		Flags: flags,
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			embedder, err := b.embedderFromFlags(c)
			if err != nil {
				return err
			}

			b.client = NewHTTPClient(b.config)
			outPath := b.resolveEmbeddingIndexPath(c, "output")

			var previous *EmbeddingIndex
			if !c.Bool("force") {
				if _, statErr := os.Stat(outPath); statErr == nil {
					previous, err = LoadEmbeddingIndex(outPath)
					if err != nil {
						fmt.Fprintf(os.Stderr, "warning: ignoring existing index: %v\n", err)
						previous = nil
					}
				}
			}

			opts := EmbeddingIndexOptions{
				Kinds:       c.StringSlice("kind"),
				Include:     c.StringSlice("include"),
				Exclude:     c.StringSlice("exclude"),
				ShowArgs:    c.Bool("args"),
				MaxChars:    c.Int("max-chars"),
				Concurrency: c.Int("concurrency"),
				Previous:    previous,
				Force:       c.Bool("force"),
				Env:         b.effectiveEnvName(c),
				Endpoint:    b.config.URL,
			}

			reused := 0
			showProgress := !c.Bool("quiet") && stderrIsTerminal()
			if showProgress {
				opts.Progress = func(done, total int, name string, wasReused bool) {
					if wasReused {
						reused++
					}
					fmt.Fprintf(os.Stderr, "\r[%d/%d] %-40s", done, total, name)
				}
			} else {
				opts.Progress = func(_, _ int, _ string, wasReused bool) {
					if wasReused {
						reused++
					}
				}
			}

			ix, err := BuildEmbeddingIndexFromClient(context.Background(), b.client, embedder, opts)
			if showProgress {
				fmt.Fprint(os.Stderr, "\r\033[K")
			}
			if err != nil {
				return err
			}

			if err := ix.Save(outPath); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "wrote %s: %d types, %d dims, model %s (%d reused)\n",
				outPath, len(ix.Types), ix.Dim, ix.Model, reused)
			return nil
		},
	}
}

func (b *CLIBuilder) embedSearchCommand() *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:    "index",
			Aliases: []string{"i"},
			Usage:   "Index file to search (default: .gqlcli-embeddings[.<env>].json)",
		},
		&cli.StringFlag{
			Name:  "env",
			Usage: "Environment whose index to search (e.g. local, prod)",
		},
		&cli.IntFlag{
			Name:    "top",
			Aliases: []string{"n"},
			Usage:   "Number of matches to return",
			Value:   5,
		},
		&cli.StringSliceFlag{
			Name:    "kind",
			Aliases: []string{"k"},
			Usage:   "Only return these type kinds (repeatable)",
		},
		&cli.Float64Flag{
			Name:  "min-score",
			Usage: "Drop matches with cosine similarity below this value",
		},
		&cli.BoolFlag{
			Name:  "no-sdl",
			Usage: "Print only names, kinds and scores",
		},
		&cli.StringFlag{
			Name:    "format",
			Aliases: []string{"f"},
			Usage:   "Output format: llm (default), json, table, compact, toon",
			Value:   "llm",
		},
	}
	flags = append(flags, embedderFlags()...)

	return &cli.Command{
		Name:      "search",
		Usage:     "Find the types closest to a description",
		ArgsUsage: "<text>",
		Description: "Embed the given text and return the closest types from the index.\n\n" +
			"Requires an index built by 'gqlcli embed index'. Only the query is embedded,\n" +
			"so no introspection call is made and the endpoint is not contacted.\n\n" +
			"Examples:\n" +
			"  gqlcli embed search 'sms provider credentials'\n" +
			"  gqlcli embed search --top 10 --kind INPUT_OBJECT 'create a campaign'\n" +
			"  gqlcli embed search --format json --no-sdl 'user email address'",
		Flags: flags,
		Action: func(c *cli.Context) error {
			query := strings.TrimSpace(strings.Join(c.Args().Slice(), " "))
			if query == "" {
				return fmt.Errorf("search text is required: gqlcli embed search '<text>'")
			}

			indexPath := b.resolveEmbeddingIndexPath(c, "index")
			ix, err := LoadEmbeddingIndex(indexPath)
			if err != nil {
				return fmt.Errorf("%w\nrun 'gqlcli embed index' first", err)
			}

			embedder, err := b.embedderFromFlags(c)
			if err != nil {
				return err
			}
			if ix.EmbeddingEndpoint != "" && ix.EmbeddingEndpoint != embedder.Endpoint() {
				fmt.Fprintf(os.Stderr, "warning: index was built against %s, searching with %s\n",
					ix.EmbeddingEndpoint, embedder.Endpoint())
			}
			ix.SetEmbedder(embedder)

			// Over-fetch so kind and score filters still return --top results.
			fetch := c.Int("top")
			if len(c.StringSlice("kind")) > 0 || c.IsSet("min-score") {
				fetch = len(ix.Types)
			}

			matches, err := ix.Search(context.Background(), query, fetch)
			if err != nil {
				return err
			}
			matches = filterMatches(matches, c.StringSlice("kind"), c.Float64("min-score"), c.Int("top"))

			return outputMatches(b.formatReg, c, matches)
		},
	}
}

func filterMatches(matches []TypeMatch, kinds []string, minScore float64, top int) []TypeMatch {
	kindSet := map[string]bool{}
	for _, k := range kinds {
		if k != "" {
			kindSet[strings.ToUpper(strings.TrimSpace(k))] = true
		}
	}

	filtered := make([]TypeMatch, 0, len(matches))
	for _, m := range matches {
		if len(kindSet) > 0 && !kindSet[strings.ToUpper(m.Kind)] {
			continue
		}
		if m.Score < minScore {
			continue
		}
		filtered = append(filtered, m)
	}
	if top > 0 && len(filtered) > top {
		filtered = filtered[:top]
	}
	return filtered
}

func outputMatches(reg FormatterRegistry, c *cli.Context, matches []TypeMatch) error {
	noSDL := c.Bool("no-sdl")

	if c.String("format") == "llm" {
		if len(matches) == 0 {
			fmt.Println("No matching types.")
			return nil
		}
		for _, m := range matches {
			fmt.Printf("## %s (%s) — score %.4f\n", m.Name, m.Kind, m.Score)
			if m.Description != "" {
				fmt.Printf("%s\n", m.Description)
			}
			if !noSDL && m.SDL != "" {
				fmt.Printf("\n```graphql\n%s\n```\n", m.SDL)
			}
			fmt.Println()
		}
		return nil
	}

	rows := make([]interface{}, 0, len(matches))
	for _, m := range matches {
		row := map[string]interface{}{
			"name":  m.Name,
			"kind":  m.Kind,
			"score": m.Score,
		}
		if m.Description != "" {
			row["description"] = m.Description
		}
		if !noSDL {
			row["sdl"] = m.SDL
		}
		rows = append(rows, row)
	}

	formatter, err := reg.Get(c.String("format"))
	if err != nil {
		return err
	}
	output, err := formatter.Format(map[string]interface{}{"matches": rows})
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}
