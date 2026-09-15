package gqlcli

import (
	"strings"
	"testing"
)

func TestDocumentOperationKind(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"explicit query", `query Q { users { id } }`, OperationQuery},
		{"shorthand is a query", `{ users { id } }`, OperationQuery},
		{"explicit mutation", `mutation M { deleteUser(id: 1) { ok } }`, OperationMutation},
		{"anonymous mutation", `mutation { deleteUser(id: 1) { ok } }`, OperationMutation},
		{"subscription", `subscription S { messageAdded { id } }`, OperationSubscription},
		{"mutation wins in a mixed document", "query Q { users { id } }\nmutation M { deleteUser(id: 1) { ok } }", OperationMutation},
		{
			// The reason this is a parse and not a substring scan.
			"the word mutation inside a string literal is not a mutation",
			`query Q { search(term: "run the mutation now") { id } }`,
			OperationQuery,
		},
		{
			"a comment mentioning mutation is not a mutation",
			"# careful, this replaces the mutation below\nquery Q { users { id } }",
			OperationQuery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DocumentOperationKind(tt.doc)
			if err != nil {
				t.Fatalf("DocumentOperationKind(%q): %v", tt.doc, err)
			}
			if got != tt.want {
				t.Errorf("kind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDocumentOperationKindRejectsBadInput(t *testing.T) {
	for _, doc := range []string{"", "   ", "this is not graphql", "query Q { unclosed ", "fragment F on User { id }"} {
		if _, err := DocumentOperationKind(doc); err == nil {
			t.Errorf("DocumentOperationKind(%q) = nil error, want a failure", doc)
		}
	}
}

func TestRequireOperationKind(t *testing.T) {
	if err := RequireOperationKind(`query Q { users { id } }`, OperationQuery); err != nil {
		t.Errorf("a query document was rejected as a query: %v", err)
	}
	if err := RequireOperationKind(`{ users { id } }`, OperationQuery); err != nil {
		t.Errorf("shorthand was rejected as a query: %v", err)
	}

	err := RequireOperationKind(`mutation M { deleteUser(id: 1) { ok } }`, OperationQuery)
	if err == nil {
		t.Fatal("a mutation passed a query-only check")
	}
	if !strings.Contains(err.Error(), "mutation") {
		t.Errorf("error = %v, want it to name the offending kind", err)
	}

	if err := RequireOperationKind("query Q { users { id } }\nmutation M { x { ok } }", OperationQuery); err == nil {
		t.Error("a document mixing a mutation into queries passed a query-only check")
	}
}
