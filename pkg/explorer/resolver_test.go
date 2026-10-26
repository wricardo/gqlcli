package explorer

import (
	"context"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/model"
)

const testSchema = `
 type Query { people(limit: Int! = 5): People! }
 type People { direct: Target indirect: Wrapper }
 type Wrapper { child: Target ids: [String!]! }
 type Target { name: String! label: String }
 `

func testResolver(t *testing.T) *Resolver {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{Input: testSchema})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := graph.New(schema)
	if err != nil {
		t.Fatal(err)
	}
	return &Resolver{Index: idx, Schema: schema}
}

func TestTypeReferencesAndInputValues(t *testing.T) {
	r := testResolver(t)
	ctx := context.Background()
	field := r.schemaField("Query.people")
	ref, err := r.SchemaField().Type(ctx, field)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Signature != "People!" || !ref.IsNonNull || ref.OfType == nil || ref.OfType.IsNonNull || ref.OfType.Name != "People" {
		t.Fatalf("incorrect wrapper: %#v", ref)
	}
	list, err := r.SchemaField().Type(ctx, r.schemaField("Wrapper.ids"))
	if err != nil {
		t.Fatal(err)
	}
	if list.Signature != "[String!]!" || list.IsList || !list.IsNonNull || list.OfType == nil || !list.OfType.IsList || list.OfType.OfType == nil || !list.OfType.OfType.IsNonNull {
		t.Fatalf("list wrappers: %#v", list)
	}
	args, err := r.SchemaField().Arguments(ctx, field)
	if err != nil || len(args) != 1 {
		t.Fatalf("arguments: %v, %v", args, err)
	}
	if args[0].Required || args[0].DefaultValue == nil || *args[0].DefaultValue != "5" {
		t.Fatalf("default/required: %#v", args[0])
	}
	parent, err := r.SchemaInputValue().ParentField(ctx, args[0])
	if err != nil || parent == nil || parent.Coordinate != "Query.people" {
		t.Fatalf("parent field: %v, %v", parent, err)
	}
}
func TestOperationsSearchEdgesAndSDL(t *testing.T) {
	r := testResolver(t)
	ctx := context.Background()
	overview, err := r.Query().Overview(ctx)
	if err != nil || overview.QueryCount != 1 || overview.QueryRoot == nil || overview.QueryRoot.Name != "Query" {
		t.Fatalf("overview: %#v, %v", overview, err)
	}
	kind := model.OperationKindQuery
	returned := "People"
	ops, err := r.Query().Operations(ctx, &kind, nil, &returned, nil, nil)
	if err != nil || len(ops) != 1 || ops[0].Coordinate != "Query.people" {
		t.Fatalf("operations: %#v, %v", ops, err)
	}
	rendered, err := r.Operation().Sdl(ctx, ops[0], nil)
	if err != nil || !strings.Contains(rendered, "people(limit: Int! = 5): People!") {
		t.Fatalf("operation SDL: %q, %v", rendered, err)
	}
	edges, err := r.SchemaType().Incoming(ctx, r.schemaType("People"), nil)
	if err != nil || len(edges) != 1 || edges[0].Kind != model.ReferenceKindReturns {
		t.Fatalf("incoming: %#v, %v", edges, err)
	}
	source, err := r.SchemaEdge().Source(ctx, edges[0])
	if err != nil || source.GetCoordinate() != "Query.people" {
		t.Fatalf("edge source: %v, %v", source, err)
	}
	target, err := r.SchemaEdge().Target(ctx, edges[0])
	if err != nil || target.GetCoordinate() != "People" {
		t.Fatalf("edge target: %v, %v", target, err)
	}
}

func TestReachabilityPagesAndIndependentPaths(t *testing.T) {
	r := testResolver(t)
	ctx := context.Background()
	from := "Query.people"
	depth := 6
	first := 1
	outer, err := r.Query().ReachableFields(ctx, "Target.name", nil, &from, &depth, &first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outer.Nodes) != 1 || outer.Nodes[0].Target.Coordinate != "Target.name" {
		t.Fatalf("reachability: %#v", outer)
	}
	paths, err := r.ReachableField().Paths(ctx, outer.Nodes[0], &first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths.Nodes) != 1 || !paths.PageInfo.HasNextPage || paths.PageInfo.EndCursor == nil {
		t.Fatalf("first path page: %#v", paths)
	}
	next, err := r.ReachableField().Paths(ctx, outer.Nodes[0], &first, paths.PageInfo.EndCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Nodes) != 1 || next.Nodes[0].Depth == paths.Nodes[0].Depth {
		t.Fatalf("second path page: %#v", next)
	}
	if outer.PageInfo.EndCursor == nil {
		t.Fatal("missing outer cursor")
	}
	differentFrom := "Query.missing"
	if _, err = r.Query().ReachableFields(ctx, "Target.name", nil, &differentFrom, &depth, &first, outer.PageInfo.EndCursor); err == nil {
		t.Fatal("cursor accepted for a different query")
	}
	outerPage, err := r.Query().ReachableFields(ctx, "Target.", nil, &from, &depth, &first, nil)
	if err != nil || !outerPage.PageInfo.HasNextPage || outerPage.PageInfo.EndCursor == nil {
		t.Fatalf("outer page: %#v, %v", outerPage, err)
	}
	nextOuter, err := r.Query().ReachableFields(ctx, "Target.", nil, &from, &depth, &first, outerPage.PageInfo.EndCursor)
	if err != nil || len(nextOuter.Nodes) != 1 || nextOuter.Nodes[0].Target.Coordinate == outerPage.Nodes[0].Target.Coordinate {
		t.Fatalf("next outer page: %#v, %v", nextOuter, err)
	}
	wider := 2
	if _, err = r.Query().ReachableFields(ctx, "Target.", nil, &from, &depth, &wider, outerPage.PageInfo.EndCursor); err == nil {
		t.Fatal("cursor accepted after page size changed")
	}
	changed, err := gqlparser.LoadSchema(&ast.Source{Input: testSchema + ` extend type Target { newer: String }`})
	if err != nil {
		t.Fatal(err)
	}
	index, err := graph.New(changed)
	if err != nil {
		t.Fatal(err)
	}
	changedResolver := &Resolver{Index: index, Schema: changed}
	if _, err = changedResolver.Query().ReachableFields(ctx, "Target.", nil, &from, &depth, &first, outerPage.PageInfo.EndCursor); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale cursor error = %v", err)
	}
}

func TestValidationDiagnosticsUseTargetSchema(t *testing.T) {
	r := testResolver(t)
	result, err := r.Query().Validate(context.Background(), `{ people(limt: 3) { direct { nmae } } }`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || len(result.Errors) < 2 {
		t.Fatalf("expected validation errors: %#v", result)
	}
	var fieldSuggestion, inputSuggestion bool
	for _, d := range result.Errors {
		for _, s := range d.Suggestions {
			if s.ReplacementField != nil && s.ReplacementField.Coordinate == "Target.name" {
				fieldSuggestion = true
			}
			if s.ReplacementInputValue != nil && s.ReplacementInputValue.Coordinate == "Query.people(limit:)" {
				inputSuggestion = true
			}
		}
	}
	if !fieldSuggestion || !inputSuggestion {
		t.Fatalf("schema suggestions absent: %#v", result.Errors)
	}
	valid, err := r.Query().Validate(context.Background(), `{ people { direct { name } } }`)
	if err != nil || !valid.Valid || len(valid.Errors) != 0 {
		t.Fatalf("valid document: %#v %v", valid, err)
	}
}
