package explorer

import (
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
)

// Resolver holds the target schema and its immutable exploration index.
type Resolver struct {
	Index  *graph.Index
	Schema *ast.Schema
}
