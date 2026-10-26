package explorer

import (
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
)

const suggestionSchema = `
 schema { query: RootQuery mutation: RootMutation subscription: RootSubscription }
 type RootQuery {
   lead(id: ID!, optional: Int, page: Int! = 3): Lead!
   search(term: String!): Search!
   lookup: Result!
 }
 type RootMutation { setLead(id: ID!): Lead! }
 type RootSubscription { watch: Lead! }
 type Lead { contactEmail: String! agent(id: ID!): Agent! }
 interface Result { name: String! }
 type Agent implements Result { name: String! rank: Int! }
 union Search = Agent
`

func suggestionResolver(t *testing.T) *Resolver {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{Input: suggestionSchema})
	if err != nil {
		t.Fatal(err)
	}
	index, err := graph.New(schema)
	if err != nil {
		t.Fatal(err)
	}
	return &Resolver{Schema: schema, Index: index}
}

func suggestedPath(t *testing.T, r *Resolver, from, to, traversal string) *graph.Path {
	t.Helper()
	paths, err := r.Index.Paths(from, to, traversal, 9, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths.Nodes) == 0 {
		t.Fatalf("no path from %q to %q", from, to)
	}
	return paths.Nodes[0]
}

func TestSuggestedDocumentOutputPaths(t *testing.T) {
	r := suggestionResolver(t)
	cases := []struct {
		from, to, want string
	}{
		{"RootQuery.lead", "Lead.contactEmail", "query($v0_id: ID!) { lead(id: $v0_id) { contactEmail } }"},
		{"RootQuery.search", "Agent.name", "query($v0_term: String!) { search(term: $v0_term) { ... on Agent { name } } }"},
		{"RootQuery.lookup", "Agent.rank", "query { lookup { ... on Agent { rank } } }"},
		{"RootQuery.lead", "Agent.rank", "query($v0_id: ID!, $v1_id: ID!) { lead(id: $v0_id) { agent(id: $v1_id) { rank } } }"},
		{"RootQuery.lead", "RootQuery.lead", "query($v0_id: ID!) { lead(id: $v0_id) { __typename } }"},
		{"RootQuery.lookup", "RootQuery.lookup", "query { lookup { __typename } }"},
		{"RootMutation.setLead", "Lead.contactEmail", "mutation($v0_id: ID!) { setLead(id: $v0_id) { contactEmail } }"},
		{"RootSubscription.watch", "Lead.contactEmail", "subscription { watch { contactEmail } }"},
		{"RootQuery", "Lead.contactEmail", "query($v0_id: ID!) { lead(id: $v0_id) { contactEmail } }"},
	}
	for _, tc := range cases {
		t.Run(tc.from+"-"+tc.to, func(t *testing.T) {
			path := r.path(suggestedPath(t, r, tc.from, tc.to, "OUTPUT"))
			got, err := r.suggestedDocument(path)
			if err != nil || got == nil || *got != tc.want {
				t.Fatalf("suggestedDocument = %v, %v; want %q", got, err, tc.want)
			}
			if !r.validate(*got).Valid {
				t.Fatalf("invalid suggested document: %q", *got)
			}
		})
	}
}

func TestSuggestedDocumentUnrepresentablePaths(t *testing.T) {
	r := suggestionResolver(t)
	cases := []struct {
		from, to, traversal string
	}{
		{"Lead.contactEmail", "Lead.contactEmail", "OUTPUT"},
		{"RootQuery.lead", "RootQuery.lead(id:)", "INPUT"},
		{"RootQuery.lead", "Lead", "OUTPUT"},
	}
	for _, tc := range cases {
		path := r.path(suggestedPath(t, r, tc.from, tc.to, tc.traversal))
		got, err := r.suggestedDocument(path)
		if got != nil || err != nil {
			t.Fatalf("%s -> %s: unexpected document %v, error %v", tc.from, tc.to, got, err)
		}
	}

	// A segment with a real ID but a different source must not change the
	// document's starting operation.
	path := r.path(suggestedPath(t, r, "RootQuery.lead", "Lead.contactEmail", "OUTPUT"))
	other := r.path(suggestedPath(t, r, "RootQuery.search", "Agent.name", "OUTPUT"))
	path.Segments[0] = other.Segments[0]
	if got, err := r.suggestedDocument(path); got != nil || err != nil {
		t.Fatalf("disconnected path: %v, %v", got, err)
	}
}

func TestSuggestedDocumentRejectsValidationFailure(t *testing.T) {
	r := suggestionResolver(t)
	path := r.path(suggestedPath(t, r, "RootQuery.lead", "Lead.contactEmail", "OUTPUT"))
	// Deliberately make the schema disagree with the indexed path. Validation
	// must prevent an obsolete selection from being returned.
	r.Schema.Query.Fields.ForName("lead").Name = "renamed"
	if got, err := r.suggestedDocument(path); got != nil || err != nil {
		t.Fatalf("invalid query returned: %v, %v", got, err)
	}
}
