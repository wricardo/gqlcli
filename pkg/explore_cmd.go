package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"
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
			"Print the full explorer API with --print-schema or a focused field view with --print-schema-field NAME.",
		Flags: append(b.exploreFlags(), schemaFileFlag(),
			&cli.BoolFlag{Name: "offline", Usage: "Require an existing cached introspection result; never contact the target"},
			&cli.BoolFlag{Name: "print-schema", Usage: "Print the full explorer GraphQL schema and exit"},
			&cli.StringFlag{Name: "print-schema-field", Usage: "Print a compact explorer schema for one Query root field and exit (e.g. paths)"}),
		Action: func(c *cli.Context) error {
			if c.IsSet("print-schema-field") {
				field := c.String("print-schema-field")
				sdl, err := explorerFieldSDL(field)
				if err != nil {
					return err
				}
				_, err = fmt.Fprint(c.App.Writer, sdl)
				return err
			}
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

// explorerFieldSDL prints a bounded SDL view of one explorer Query field. Deep
// links in the graph (notably paths -> segments -> edges) remain navigable,
// while cyclic type/field relationships do not pull in the entire contract.
func explorerFieldSDL(name string) (string, error) {
	doc, err := parser.ParseSchema(&ast.Source{Name: "explorer.graphqls", Input: ExplorerSDL()})
	if err != nil {
		return "", fmt.Errorf("parse explorer schema: %w", err)
	}
	definitions := make(map[string]*ast.Definition, len(doc.Definitions))
	for _, definition := range doc.Definitions {
		definitions[definition.Name] = definition
	}
	query := definitions["Query"]
	if query == nil {
		return "", fmt.Errorf("explorer schema has no Query type")
	}
	field := query.Fields.ForName(name)
	if field == nil {
		names := make([]string, 0, len(query.Fields))
		for _, candidate := range query.Fields {
			names = append(names, candidate.Name)
		}
		return "", fmt.Errorf("unknown explorer root field %q (available: %s)", name, strings.Join(names, ", "))
	}

	root := *query
	root.Fields = ast.FieldList{field}
	output := &ast.SchemaDocument{Definitions: ast.DefinitionList{&root}}
	type pendingType struct {
		name  string
		depth int
	}
	pending := make([]pendingType, 0, 16)
	addType := func(name string, depth int) {
		if definition := definitions[name]; definition != nil && name != "Query" {
			pending = append(pending, pendingType{name, depth})
		}
	}
	addFieldTypes := func(field *ast.FieldDefinition, depth int) {
		addType(field.Type.Name(), depth)
		for _, arg := range field.Arguments {
			addType(arg.Type.Name(), depth)
		}
	}
	addFieldTypes(field, 1)
	seen := map[string]bool{"Query": true}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if seen[next.name] {
			continue
		}
		seen[next.name] = true
		source := definitions[next.name]
		definition := *source
		if source.Kind == ast.Object || source.Kind == ast.Interface || source.Kind == ast.InputObject {
			definition.Fields = nil
			for _, child := range source.Fields {
				childType := definitions[child.Type.Name()]
				composite := childType != nil && (childType.Kind == ast.Object || childType.Kind == ast.Interface || childType.Kind == ast.Union)
				if composite && !explorerScopedLink(next.name, child.Name, next.depth) {
					continue
				}
				definition.Fields = append(definition.Fields, child)
				addFieldTypes(child, next.depth+1)
			}
		}
		for _, iface := range source.Interfaces {
			addType(iface, next.depth+1)
		}
		for _, member := range source.Types {
			addType(member, next.depth+1)
		}
		// An interface result can be selected with fragments on its concrete
		// implementors, so expose their names and scalar fields as well.
		if source.Kind == ast.Interface {
			for _, candidate := range doc.Definitions {
				for _, iface := range candidate.Interfaces {
					if iface == source.Name {
						addType(candidate.Name, next.depth+1)
					}
				}
			}
		}
		output.Definitions = append(output.Definitions, &definition)
	}
	var result strings.Builder
	formatter.NewFormatter(&result, formatter.WithIndent("  ")).FormatSchemaDocument(output)
	return result.String(), nil
}

func explorerScopedLink(parent, field string, depth int) bool {
	if depth >= 10 {
		return false
	}
	// SchemaType is a hub that links back to nearly every explorer type.
	// A direct lookup includes its declaration fields, but not graph navigation;
	// indirect references only need its scalar identifiers and SDL.
	if parent == "SchemaType" {
		if depth > 1 {
			return false
		}
		switch field {
		case "fields", "inputFields", "enumValues", "possibleTypes", "interfaces":
			return true
		default:
			return false
		}
	}
	if depth < 3 {
		return true
	}
	switch field {
	case "nodes", "pageInfo", "segments", "edge", "source", "target",
		"field", "inputValue", "operation", "paths", "type", "rootField",
		"arguments", "returnType", "namedType", "ofType", "parentType":
		return true
	default:
		return false
	}
}
