package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer"
)

// SchemaExplorer runs explorer GraphQL documents against a fixed target schema
// snapshot. It never sends explorer documents to the target endpoint.
type SchemaExplorer struct{ executor *InlineExecutor }

// ExplorerSDL is the GraphQL contract queried by SchemaExplorer.
func ExplorerSDL() string { return explorer.SchemaSDL }

func NewSchemaExplorer(schema *ast.Schema) (*SchemaExplorer, error) {
	executable, err := explorer.New(schema)
	if err != nil {
		return nil, err
	}
	return &SchemaExplorer{executor: NewInlineExecutor(executable)}, nil
}

func NewSchemaExplorerFromSDL(sdl string) (*SchemaExplorer, error) {
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "target.graphql", Input: sdl})
	if err != nil {
		return nil, fmt.Errorf("load target schema: %w", err)
	}
	return NewSchemaExplorer(schema)
}

func NewSchemaExplorerFromIntrospection(introspection map[string]interface{}) (*SchemaExplorer, error) {
	schema, err := SchemaFromIntrospection(introspection)
	if err != nil {
		return nil, err
	}
	return NewSchemaExplorer(schema)
}

func NewSchemaExplorerFromClient(ctx context.Context, client Client) (*SchemaExplorer, error) {
	if client == nil {
		return nil, fmt.Errorf("schema explorer: client is nil")
	}
	result, err := client.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	return NewSchemaExplorerFromIntrospection(result)
}

// Execute returns a standard GraphQL response envelope (data and optional
// errors). Variable values are supplied to gqlgen without querying the target.
func (e *SchemaExplorer) Execute(ctx context.Context, document string, variables map[string]interface{}) (map[string]interface{}, error) {
	return e.ExecuteOperation(ctx, document, variables, "")
}

// ExecuteOperation selects a named explorer operation when needed.
func (e *SchemaExplorer) ExecuteOperation(ctx context.Context, document string, variables map[string]interface{}, operationName string) (map[string]interface{}, error) {
	if e == nil || e.executor == nil {
		return nil, fmt.Errorf("schema explorer: no target schema")
	}
	raw, err := e.executor.ExecuteOperation(ctx, document, variables, operationName)
	if err != nil {
		return nil, err
	}
	var response map[string]interface{}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode explorer response: %w", err)
	}
	return response, nil
}
