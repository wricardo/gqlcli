package explorer

import (
	_ "embed"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
)

// SchemaSDL is the versioned contract executed locally by the explorer.
//
//go:embed schema.graphqls
var SchemaSDL string

// New constructs a fresh explorer for a target schema snapshot. The graph index
// is derived from the supplied schema and cannot outlive a cache refresh.
// Pass the returned executable schema to gqlcli.NewInlineExecutor to run
// explorer documents with variables, fragments and introspection locally.
func New(schema *ast.Schema) (graphql.ExecutableSchema, error) {
	if schema == nil {
		return nil, fmt.Errorf("explorer: target schema is nil")
	}
	index, err := graph.New(schema)
	if err != nil {
		return nil, err
	}
	return NewExecutableSchema(Config{Resolvers: &Resolver{Index: index, Schema: schema}}), nil
}
