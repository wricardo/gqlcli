package gqlcli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

// embedCommand returns the inline-mode "embed" group. Same index/search
// behavior as HTTP mode, but introspection runs in-process against the
// embedded schema, so only the embedding calls leave the process.
func (cs *InlineCommandSet) embedCommand() *cli.Command {
	return &cli.Command{
		Name:  "embed",
		Usage: "Build and search a semantic index of the schema's types",
		Description: "Embed every GraphQL type into a vector index, then find the types closest to a description.\n\n" +
			"Examples:\n" +
			"  embed index\n" +
			"  embed search 'thing that stores a customer billing address'\n\n" +
			"Embeddings come from the Venu API; set VENU_API_KEY (and VENU_URL to change host).",
		Subcommands: []*cli.Command{
			cs.embedIndexCommand(),
			cs.embedSearchCommand(),
		},
	}
}

func (cs *InlineCommandSet) embedIndexCommand() *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "Index file to write",
			Value:   EmbeddingIndexPath(""),
		},
		&cli.StringSliceFlag{Name: "kind", Aliases: []string{"k"}, Usage: "Restrict to type kinds (repeatable; default: all)"},
		&cli.StringSliceFlag{Name: "include", Usage: "Only index type names matching this glob (repeatable)"},
		&cli.StringSliceFlag{Name: "exclude", Usage: "Skip type names matching this glob (repeatable)"},
		&cli.BoolFlag{Name: "args", Usage: "Include field argument signatures in the indexed SDL (default true)", Value: true},
		&cli.IntFlag{Name: "concurrency", Usage: "Parallel embedding requests", Value: 4},
		&cli.IntFlag{Name: "max-chars", Usage: "Truncate each type's text to this many characters", Value: DefaultEmbeddingMaxChars},
		&cli.BoolFlag{Name: "force", Usage: "Re-embed every type instead of reusing unchanged vectors"},
		&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Usage: "Suppress per-type progress output"},
	}
	flags = append(flags, embedderFlags()...)

	return &cli.Command{
		Name:  "index",
		Usage: "Introspect the schema and write a type embedding index",
		Flags: flags,
		Action: func(c *cli.Context) error {
			embedder, err := NewVenuEmbedder(
				WithEmbeddingBaseURL(c.String("embedding-url")),
				WithEmbeddingAPIKey(c.String("embedding-key")),
				WithEmbeddingModel(c.String("embedding-model")),
			)
			if err != nil {
				return err
			}

			outPath := c.String("output")
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

			reused := 0
			showProgress := !c.Bool("quiet") && stderrIsTerminal()
			opts := EmbeddingIndexOptions{
				Kinds:       c.StringSlice("kind"),
				Include:     c.StringSlice("include"),
				Exclude:     c.StringSlice("exclude"),
				ShowArgs:    c.Bool("args"),
				MaxChars:    c.Int("max-chars"),
				Concurrency: c.Int("concurrency"),
				Previous:    previous,
				Force:       c.Bool("force"),
				Progress: func(done, total int, name string, wasReused bool) {
					if wasReused {
						reused++
					}
					if showProgress {
						fmt.Fprintf(os.Stderr, "\r[%d/%d] %-40s", done, total, name)
					}
				},
			}

			ix, err := BuildEmbeddingIndexFromClient(context.Background(), NewInlineClient(cs.exec), embedder, opts)
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

func (cs *InlineCommandSet) embedSearchCommand() *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:    "index",
			Aliases: []string{"i"},
			Usage:   "Index file to search",
			Value:   EmbeddingIndexPath(""),
		},
		&cli.IntFlag{Name: "top", Aliases: []string{"n"}, Usage: "Number of matches to return", Value: 5},
		&cli.StringSliceFlag{Name: "kind", Aliases: []string{"k"}, Usage: "Only return these type kinds (repeatable)"},
		&cli.Float64Flag{Name: "min-score", Usage: "Drop matches below this cosine similarity"},
		&cli.BoolFlag{Name: "no-sdl", Usage: "Print only names, kinds and scores"},
		&cli.StringFlag{Name: "format", Aliases: []string{"f"}, Usage: "Output format: llm (default), json, table, compact, toon", Value: "llm"},
	}
	flags = append(flags, embedderFlags()...)

	return &cli.Command{
		Name:      "search",
		Usage:     "Find the types closest to a description",
		ArgsUsage: "<text>",
		Flags:     flags,
		Action: func(c *cli.Context) error {
			query := strings.TrimSpace(strings.Join(c.Args().Slice(), " "))
			if query == "" {
				return fmt.Errorf("search text is required: embed search '<text>'")
			}

			ix, err := LoadEmbeddingIndex(c.String("index"))
			if err != nil {
				return fmt.Errorf("%w\nrun 'embed index' first", err)
			}
			embedder, err := NewVenuEmbedder(
				WithEmbeddingBaseURL(c.String("embedding-url")),
				WithEmbeddingAPIKey(c.String("embedding-key")),
				WithEmbeddingModel(c.String("embedding-model")),
			)
			if err != nil {
				return err
			}
			ix.SetEmbedder(embedder)

			fetch := c.Int("top")
			if len(c.StringSlice("kind")) > 0 || c.IsSet("min-score") {
				fetch = len(ix.Types)
			}
			matches, err := ix.Search(context.Background(), query, fetch)
			if err != nil {
				return err
			}
			matches = filterMatches(matches, c.StringSlice("kind"), c.Float64("min-score"), c.Int("top"))

			return outputMatches(NewFormatterRegistry(), c, matches)
		},
	}
}
