package explorer

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/model"
)

func fixtureResolver(t *testing.T) *Resolver {
	t.Helper()
	data, err := os.ReadFile("../../testdata/explorer/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Input: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	index, err := graph.New(schema)
	if err != nil {
		t.Fatal(err)
	}
	return &Resolver{Index: index, Schema: schema}
}

func TestReachableFieldsKindFiltersAndBindsCursor(t *testing.T) {
	r := fixtureResolver(t)
	ctx := context.Background()
	kind := model.OperationKindQuery
	first := 1
	page, err := r.Query().ReachableFields(ctx, "Lead.contactEmail", &kind, nil, nil, &first, nil)
	if err != nil || len(page.Nodes) != 1 || page.Nodes[0].Operation.Kind != kind || !page.PageInfo.HasNextPage {
		t.Fatalf("first query page: %#v, %v", page, err)
	}
	second, err := r.Query().ReachableFields(ctx, "Lead.contactEmail", &kind, nil, nil, &first, page.PageInfo.EndCursor)
	if err != nil || len(second.Nodes) != 1 || second.Nodes[0].Operation.Kind != kind || second.Nodes[0].Operation.Coordinate == page.Nodes[0].Operation.Coordinate {
		t.Fatalf("second query page: %#v, %v", second, err)
	}
	mutation := model.OperationKindMutation
	if _, err := r.Query().ReachableFields(ctx, "Lead.contactEmail", &mutation, nil, nil, &first, page.PageInfo.EndCursor); err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("kind mismatch accepted: %v", err)
	}
	unfiltered, err := r.Query().ReachableFields(ctx, "Lead.contactEmail", nil, nil, nil, nil, nil)
	if err != nil || len(unfiltered.Nodes) <= len(page.Nodes)+len(second.Nodes) {
		t.Fatalf("unfiltered reachability lost other root kinds: %#v, %v", unfiltered, err)
	}
}

func TestCompactPathRetainsAbstractTransition(t *testing.T) {
	r := fixtureResolver(t)
	first := 1
	path, err := r.Query().Paths(context.Background(), "RootQuery.search", "Agent.name", nil, nil, &first, nil)
	if err != nil || len(path.Nodes) != 1 {
		t.Fatalf("search path: %#v, %v", path, err)
	}
	coordinates, err := r.SchemaPath().Coordinates(context.Background(), path.Nodes[0])
	if err != nil || strings.Join(coordinates, ",") != "RootQuery.search,SearchResult,Agent,Agent.name" {
		t.Fatalf("coordinates: %v, %v", coordinates, err)
	}
	kinds, err := r.SchemaPath().EdgeKinds(context.Background(), path.Nodes[0])
	if err != nil || len(kinds) != len(coordinates)-1 || kinds[1] != model.ReferenceKindUnionMember {
		t.Fatalf("edge kinds: %v, %v", kinds, err)
	}
}

func TestOperationShapeIncludesDirectInputAndOutput(t *testing.T) {
	r := fixtureResolver(t)
	shape, err := r.Operation().Shape(context.Background(), r.operation("RootMutation.createLead"))
	if err != nil || !strings.Contains(shape, "createLead(input: CreateLeadInput!): Lead!") || !strings.Contains(shape, "input CreateLeadInput") || !strings.Contains(shape, "email: String!") || !strings.Contains(shape, "status: LeadStatus = NEW") || !strings.Contains(shape, "type Lead") || !strings.Contains(shape, "contactEmail: String") {
		t.Fatalf("operation shape: %q, %v", shape, err)
	}
	if strings.Contains(shape, "type Agent") || strings.Contains(shape, "type RootMutation") {
		t.Fatalf("shape expanded unrelated types: %q", shape)
	}
}
