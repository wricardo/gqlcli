package gqlcli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestScriptRunner_ReadOnlyCannotBeBypassedByHelperChoice is the regression
// test for the hole this policy was built to close: the runner classified an
// operation by which JS helper the script called, so a mutation handed to
// gql.query was waved through as a read.
func TestScriptRunner_ReadOnlyCannotBeBypassedByHelperChoice(t *testing.T) {
	bypasses := []struct {
		name string
		call string
	}{
		{"mutation document passed to gql.query", `gql.query("mutation Evil { deleteEverything { ok } }")`},
		{"mutation document passed to gql.request as a string", `gql.request("mutation Evil { deleteEverything { ok } }")`},
		{"mutation document under an explicit query type", `gql.request({ type: "query", query: "mutation Evil { deleteEverything { ok } }" })`},
	}

	for _, tt := range bypasses {
		t.Run(tt.name, func(t *testing.T) {
			server, received := recordingServer(t)
			runner := testRunner(t, server.URL, WithReadOnly(true))

			script := `function run(gql) {
  try { ` + tt.call + `; return "ALLOWED"; } catch (e) { return String(e); }
}`
			result, err := runner.RunSource(context.Background(), "bypass.js", script, "run", nil)
			if err != nil {
				t.Fatalf("RunSource: %v", err)
			}

			for _, req := range *received {
				if q, _ := req["query"].(string); strings.Contains(q, "deleteEverything") {
					t.Fatalf("read-only runner sent a mutation to the server: %q", q)
				}
			}
			outcome, _ := result.(string)
			if outcome == "ALLOWED" {
				t.Fatal("the mutation was not blocked")
			}
			if !strings.Contains(outcome, "read-only") {
				t.Errorf("script saw %q, want a read-only rejection", outcome)
			}
		})
	}
}

func TestScriptRunner_TypeReportedIsTheDocumentsOwnKind(t *testing.T) {
	server, _ := recordingServer(t)

	var seen []RequestInfo
	runner := testRunner(t, server.URL, WithOnRequest(func(info RequestInfo) {
		seen = append(seen, info)
	}))

	// Every call here uses a helper that disagrees with its document.
	script := `function run(gql) {
  gql.query("mutation A { ok }");
  gql.mutation("query B { ok }");
  gql.request({ type: "mutation", query: "query C { ok }" });
  gql.query("{ ok }");
  return true;
}`

	if _, err := runner.RunSource(context.Background(), "kinds.js", script, "run", nil); err != nil {
		t.Fatalf("RunSource: %v", err)
	}

	want := []string{OperationMutation, OperationQuery, OperationQuery, OperationQuery}
	if len(seen) != len(want) {
		t.Fatalf("onRequest fired %d times, want %d", len(seen), len(want))
	}
	for i := range want {
		if seen[i].Type != want[i] {
			t.Errorf("call %d type = %q, want %q (document: %q)", i, seen[i].Type, want[i], seen[i].Operation)
		}
	}
}

func TestScriptRunner_ApproverSeesTheDocumentsOwnKind(t *testing.T) {
	server, received := recordingServer(t)

	runner := testRunner(t, server.URL, WithApprover(func(_ context.Context, info RequestInfo) error {
		if info.Type == OperationMutation {
			return errors.New("writes need a human")
		}
		return nil
	}))

	script := `function run(gql) {
  const out = [];
  try { gql.query("mutation Sneaky { ok }"); out.push("allowed"); } catch (e) { out.push("blocked"); }
  try { gql.query("query Fine { ok }"); out.push("allowed"); } catch (e) { out.push("blocked"); }
  return out;
}`

	result, err := runner.RunSource(context.Background(), "approve.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	got, _ := result.([]interface{})
	if len(got) != 2 || got[0] != "blocked" || got[1] != "allowed" {
		t.Fatalf("results = %v, want [blocked allowed]", got)
	}
	if len(*received) != 1 {
		t.Errorf("server saw %d requests, want 1", len(*received))
	}
}

func TestScriptRunner_UnparseableDocumentIsRefusedOnlyUnderPolicy(t *testing.T) {
	const script = `function run(gql) {
  try { gql.query("this is not graphql"); return "SENT"; } catch (e) { return String(e); }
}`

	t.Run("refused when a policy is in force", func(t *testing.T) {
		server, received := recordingServer(t)
		runner := testRunner(t, server.URL, WithReadOnly(true))

		result, err := runner.RunSource(context.Background(), "bad.js", script, "run", nil)
		if err != nil {
			t.Fatalf("RunSource: %v", err)
		}
		if outcome, _ := result.(string); outcome == "SENT" {
			t.Fatal("an unclassifiable document was sent while a policy was in force")
		} else if !strings.Contains(outcome, "classif") {
			t.Errorf("script saw %q, want a classification refusal", outcome)
		}
		if len(*received) != 0 {
			t.Errorf("server saw %d requests, want 0", len(*received))
		}
	})

	t.Run("passed to the server when no policy is set", func(t *testing.T) {
		server, received := recordingServer(t)
		runner := testRunner(t, server.URL)

		if _, err := runner.RunSource(context.Background(), "bad.js", script, "run", nil); err != nil {
			t.Fatalf("RunSource: %v", err)
		}
		if len(*received) != 1 {
			t.Errorf("server saw %d requests, want 1 — without a policy the server stays the authority", len(*received))
		}
	})
}

// TestNewScriptRunner_AcceptsATwoMethodExecutor pins the narrowed parameter:
// an embedder adapting its own transport must not have to stub Introspect or
// LastResponseMetadata.
func TestNewScriptRunner_AcceptsATwoMethodExecutor(t *testing.T) {
	server, received := recordingServer(t)
	inner := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})

	var exec OperationExecutor = minimalExecutor{inner: inner}
	runner := NewScriptRunner(exec)

	if _, err := runner.RunSource(context.Background(), "x.js",
		`function run(gql){ return gql.query("query { ok }"); }`, "run", nil); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if len(*received) != 1 {
		t.Errorf("server saw %d requests, want 1", len(*received))
	}
}

type minimalExecutor struct{ inner *HTTPClient }

func (m minimalExecutor) Execute(ctx context.Context, mode ExecutionMode, opts QueryOptions) (map[string]interface{}, error) {
	return m.inner.Execute(ctx, mode, opts)
}

func (m minimalExecutor) ExecuteMutation(ctx context.Context, mode ExecutionMode, opts MutationOptions) (map[string]interface{}, error) {
	return m.inner.ExecuteMutation(ctx, mode, opts)
}
