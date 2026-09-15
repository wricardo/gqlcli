package gqlcli

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// InlineClient exists to make the inline transport usable anywhere Client is
// expected; if that ever stops holding, this fails to compile.
var _ Client = (*InlineClient)(nil)
var _ OperationExecutor = (*InlineClient)(nil)

// stubSchema is a minimal gqlgen ExecutableSchema: enough for the handler to
// parse and validate an operation, with a canned result.
type stubSchema struct {
	schema *ast.Schema
	seen   *[]string
}

func (s stubSchema) Schema() *ast.Schema { return s.schema }

func (s stubSchema) Complexity(context.Context, string, string, int, map[string]any) (int, bool) {
	return 0, false
}

func (s stubSchema) Exec(ctx context.Context) graphql.ResponseHandler {
	if oc := graphql.GetOperationContext(ctx); oc != nil && oc.Operation != nil {
		*s.seen = append(*s.seen, string(oc.Operation.Operation))
	}
	return graphql.OneShot(&graphql.Response{Data: []byte(`{"ok":true}`)})
}

func newInlineTestClient(t *testing.T) (*InlineClient, *[]string) {
	t.Helper()
	seen := &[]string{}
	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "test", Input: `
		type Query { ok: Boolean }
		type Mutation { touch(id: ID!): Boolean }
	`})
	return NewInlineClient(NewInlineExecutor(stubSchema{schema: schema, seen: seen})), seen
}

func TestInlineClientExecutesInProcess(t *testing.T) {
	client, seen := newInlineTestClient(t)
	ctx := context.Background()

	res, err := client.Execute(ctx, ExecutionModeInline, QueryOptions{Query: "query { ok }"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := res["data"].(map[string]interface{})
	if data["ok"] != true {
		t.Errorf("response = %v, want data.ok true", res)
	}

	if _, err := client.ExecuteMutation(ctx, ExecutionModeInline, MutationOptions{
		Mutation:  "mutation ($id: ID!) { touch(id: $id) }",
		Variables: map[string]interface{}{"id": "1"},
	}); err != nil {
		t.Fatalf("ExecuteMutation: %v", err)
	}

	if len(*seen) != 2 || (*seen)[0] != "query" || (*seen)[1] != "mutation" {
		t.Errorf("schema saw %v, want [query mutation]", *seen)
	}
}

// ScriptRunner hardcodes ExecutionModeHTTP at its dispatch sites, so an inline
// client that honored the mode would reject every call a script made.
func TestInlineClientIgnoresExecutionMode(t *testing.T) {
	client, _ := newInlineTestClient(t)

	for _, mode := range []ExecutionMode{ExecutionModeInline, ExecutionModeHTTP} {
		if _, err := client.Execute(context.Background(), mode, QueryOptions{Query: "query { ok }"}); err != nil {
			t.Errorf("Execute with mode %v: %v", mode, err)
		}
	}
}

func TestInlineClientDrivesScriptRunner(t *testing.T) {
	client, seen := newInlineTestClient(t)

	runner := NewScriptRunner(client)
	script := `function run(gql) {
  const q = gql.query("query { ok }");
  gql.mutation("mutation ($id: ID!) { touch(id: $id) }", { id: "1" });
  return q.data.ok;
}`

	result, err := runner.RunSource(context.Background(), "inline.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if result != true {
		t.Errorf("script result = %v, want true", result)
	}
	if len(*seen) != 2 {
		t.Errorf("schema saw %v, want one query and one mutation", *seen)
	}
}

func TestInlineClientReadOnlyPolicyApplies(t *testing.T) {
	client, seen := newInlineTestClient(t)
	runner := NewScriptRunner(client, WithReadOnly(true))

	script := `function run(gql) {
  try { gql.query("mutation ($id: ID!) { touch(id: $id) }", { id: "1" }); return "ALLOWED"; }
  catch (e) { return String(e); }
}`
	result, err := runner.RunSource(context.Background(), "inline.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if outcome, _ := result.(string); outcome == "ALLOWED" {
		t.Fatal("read-only policy did not apply to the inline transport")
	}
	if len(*seen) != 0 {
		t.Errorf("schema saw %v, want nothing executed", *seen)
	}
}
