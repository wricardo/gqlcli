package gqlcli

import (
	"flag"
	"testing"

	"github.com/urfave/cli/v2"
)

func categoryContext(t *testing.T, categories ...string) *cli.Context {
	t.Helper()
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	slice := cli.NewStringSlice(categories...)
	set.Var(slice, "category", "")
	set.Float64("min-score", 0, "")
	set.Var(cli.NewStringSlice(), "kind", "")
	set.String("format", "llm", "")
	return cli.NewContext(cli.NewApp(), set, nil)
}

func TestSearchCategories(t *testing.T) {
	cases := []struct {
		name                string
		input               []string
		wantQ, wantM, wantT bool
		wantErr             bool
	}{
		{name: "default is all", input: nil, wantQ: true, wantM: true, wantT: true},
		{name: "queries only", input: []string{"queries"}, wantQ: true},
		{name: "singular accepted", input: []string{"query"}, wantQ: true},
		{name: "mutations only", input: []string{"mutations"}, wantM: true},
		{name: "types only", input: []string{"types"}, wantT: true},
		{name: "operations shorthand", input: []string{"operations"}, wantQ: true, wantM: true},
		{name: "all keyword", input: []string{"all"}, wantQ: true, wantM: true, wantT: true},
		{name: "combined", input: []string{"queries", "types"}, wantQ: true, wantT: true},
		{name: "case insensitive", input: []string{"QUERIES"}, wantQ: true},
		{name: "unknown rejected", input: []string{"nope"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, m, ty, err := searchCategories(categoryContext(t, tc.input...))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error for an unknown category")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if q != tc.wantQ || m != tc.wantM || ty != tc.wantT {
				t.Errorf("got queries=%v mutations=%v types=%v", q, m, ty)
			}
		})
	}
}

func TestFilterResultsAppliesCategoryAndScore(t *testing.T) {
	results := &SearchResults{
		Queries:   []OperationMatch{{Name: "user", Score: 0.8}, {Name: "invoices", Score: 0.2}},
		Mutations: []OperationMatch{{Name: "createUser", Score: 0.7}},
		Types:     []TypeMatch{{Name: "User", Kind: "OBJECT", Score: 0.9}, {Name: "UserInput", Kind: "INPUT_OBJECT", Score: 0.6}},
	}

	// Category filter drops the other two buckets entirely.
	got, err := filterResults(results, categoryContext(t, "queries"), 5)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Queries) != 2 || len(got.Mutations) != 0 || len(got.Types) != 0 {
		t.Fatalf("category filter not applied: %+v", got)
	}

	// top trims each category independently.
	got, err = filterResults(results, categoryContext(t), 1)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Queries) != 1 || len(got.Mutations) != 1 || len(got.Types) != 1 {
		t.Errorf("top not applied per category: %+v", got)
	}
	if got.Queries[0].Name != "user" {
		t.Errorf("trim should keep the best hit, got %s", got.Queries[0].Name)
	}
}

func TestFilterResultsKindAppliesOnlyToTypes(t *testing.T) {
	results := &SearchResults{
		Queries: []OperationMatch{{Name: "user", Score: 0.8}},
		Types:   []TypeMatch{{Name: "User", Kind: "OBJECT", Score: 0.9}, {Name: "UserInput", Kind: "INPUT_OBJECT", Score: 0.6}},
	}

	set := flag.NewFlagSet("test", flag.ContinueOnError)
	set.Var(cli.NewStringSlice(), "category", "")
	set.Float64("min-score", 0, "")
	set.Var(cli.NewStringSlice("INPUT_OBJECT"), "kind", "")
	set.String("format", "llm", "")
	c := cli.NewContext(cli.NewApp(), set, nil)

	got, err := filterResults(results, c, 5)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(got.Types) != 1 || got.Types[0].Name != "UserInput" {
		t.Errorf("kind filter not applied to types: %+v", got.Types)
	}
	if len(got.Queries) != 1 {
		t.Errorf("kind filter must not touch operations: %+v", got.Queries)
	}
}

func TestTrimOperationsRespectsMinScore(t *testing.T) {
	ops := []OperationMatch{{Name: "a", Score: 0.9}, {Name: "b", Score: 0.3}}
	got := trimOperations(ops, 0.5, 10)
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("min-score not applied: %+v", got)
	}
}
