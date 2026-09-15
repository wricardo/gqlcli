package gqlcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// recordingServer returns a GraphQL server that records every operation it
// receives and answers queries and mutations with a trivial payload.
func recordingServer(t *testing.T) (*httptest.Server, *[]map[string]interface{}) {
	t.Helper()
	var received []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		received = append(received, req)
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	t.Cleanup(server.Close)
	return server, &received
}

func testRunner(t *testing.T, serverURL string, opts ...ScriptOption) *ScriptRunner {
	t.Helper()
	client := NewHTTPClient(&Config{URL: serverURL, Timeout: 5, Strict: true})
	return NewScriptRunner(client, opts...)
}

func TestScriptRunner_ConsoleRedirectedToConfiguredWriters(t *testing.T) {
	server, _ := recordingServer(t)

	var stdout, stderr bytes.Buffer
	runner := testRunner(t, server.URL, WithStdout(&stdout), WithStderr(&stderr))

	script := `function run(gql) {
  console.log("to stdout");
  console.error("to stderr");
  return { done: true };
}`

	// Capture the process stdio to prove nothing escapes to it.
	realOut, realErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW

	_, runErr := runner.RunSource(context.Background(), "console.js", script, "run", nil)

	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = realOut, realErr
	leakedOut, _ := io.ReadAll(outR)
	leakedErr, _ := io.ReadAll(errR)

	if runErr != nil {
		t.Fatalf("RunSource: %v", runErr)
	}
	if got := stdout.String(); !strings.Contains(got, "to stdout") {
		t.Errorf("configured stdout = %q, want it to contain %q", got, "to stdout")
	}
	if got := stderr.String(); !strings.Contains(got, "to stderr") {
		t.Errorf("configured stderr = %q, want it to contain %q", got, "to stderr")
	}
	if len(leakedOut) != 0 {
		t.Errorf("process stdout got %q, want nothing", leakedOut)
	}
	if len(leakedErr) != 0 {
		t.Errorf("process stderr got %q, want nothing", leakedErr)
	}
}

func TestScriptRunner_DefaultsWriteToProcessStdio(t *testing.T) {
	server, _ := recordingServer(t)

	// The default writers are resolved when the runner is constructed, so the
	// swap has to happen first.
	realOut := os.Stdout
	outR, outW, _ := os.Pipe()
	os.Stdout = outW
	runner := testRunner(t, server.URL)

	_, err := runner.RunSource(context.Background(), "console.js", `function run(){ console.log("hi"); }`, "run", nil)

	outW.Close()
	os.Stdout = realOut
	captured, _ := io.ReadAll(outR)

	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if !strings.Contains(string(captured), "hi") {
		t.Errorf("process stdout = %q, want it to contain %q", captured, "hi")
	}
}

func TestScriptRunner_ContextCancellationInterruptsRunawayScript(t *testing.T) {
	server, _ := recordingServer(t)
	runner := testRunner(t, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := runner.RunSource(ctx, "hang.js", `function run() { while (true) {} }`, "run", nil)
		done <- err
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("RunSource returned nil error, want interruption")
		}
		if !errors.Is(err, ErrScriptInterrupted) {
			t.Fatalf("error = %v, want it to wrap ErrScriptInterrupted", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
		if elapsed > 5*time.Second {
			t.Errorf("took %v to interrupt, want prompt cancellation", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunSource hung past the context deadline")
	}
}

func TestScriptRunner_WithTimeoutInterruptsRunawayScript(t *testing.T) {
	server, _ := recordingServer(t)
	runner := testRunner(t, server.URL, WithTimeout(300*time.Millisecond))

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunSource(context.Background(), "hang.js", `function run() { while (true) {} }`, "run", nil)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrScriptInterrupted) {
			t.Fatalf("error = %v, want it to wrap ErrScriptInterrupted", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WithTimeout did not interrupt the script")
	}
}

func TestScriptRunner_InterruptIsDistinguishableFromScriptThrow(t *testing.T) {
	server, _ := recordingServer(t)
	runner := testRunner(t, server.URL)

	_, err := runner.RunSource(context.Background(), "throw.js", `function run() { throw new Error("boom"); }`, "run", nil)
	if err == nil {
		t.Fatal("RunSource returned nil error, want the script throw")
	}
	if errors.Is(err, ErrScriptInterrupted) {
		t.Fatalf("script throw %v was misreported as an interruption", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to carry the thrown message", err)
	}
}

func TestScriptRunner_InterruptDoesNotAffectLaterRuns(t *testing.T) {
	server, _ := recordingServer(t)
	runner := testRunner(t, server.URL)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.RunSource(cancelled, "hang.js", `function run() { while (true) {} }`, "run", nil); !errors.Is(err, ErrScriptInterrupted) {
		t.Fatalf("first run error = %v, want ErrScriptInterrupted", err)
	}

	script := `function run(gql) { return gql.query("query { ok }"); }`

	// The same runner must still work afterwards...
	if _, err := runner.RunSource(context.Background(), "ok.js", script, "run", nil); err != nil {
		t.Fatalf("reusing the runner after an interrupt failed: %v", err)
	}
	// ...and so must a fresh one.
	if _, err := testRunner(t, server.URL).RunSource(context.Background(), "ok.js", script, "run", nil); err != nil {
		t.Fatalf("fresh runner after an interrupt failed: %v", err)
	}
}

func TestScriptRunner_InterruptGoroutineDoesNotLeak(t *testing.T) {
	server, _ := recordingServer(t)
	runner := testRunner(t, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	script := `function run(gql) { return gql.query("query { ok }"); }`
	if _, err := runner.RunSource(ctx, "ok.js", script, "run", nil); err != nil {
		t.Fatalf("warmup run: %v", err)
	}

	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		if _, err := runner.RunSource(ctx, "ok.js", script, "run", nil); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	var after int
	for i := 0; i < 50; i++ {
		after = runtime.NumGoroutine()
		if after <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines grew from %d to %d across 20 runs, want the interrupt watchers to exit", before, after)
}

func TestScriptRunner_OnRequestFiresForEveryCallForm(t *testing.T) {
	server, _ := recordingServer(t)

	var seen []RequestInfo
	runner := testRunner(t, server.URL, WithOnRequest(func(info RequestInfo) {
		seen = append(seen, info)
	}))

	script := `function run(gql) {
  gql.query("query A { ok }");
  gql.mutation("mutation B { ok }");
  gql.request("query C { ok }");
  gql.request({ type: "mutation", mutation: "mutation D { ok }" });
  gql.request({ mutation: "mutation E { ok }" });
  return true;
}`

	if _, err := runner.RunSource(context.Background(), "forms.js", script, "run", nil); err != nil {
		t.Fatalf("RunSource: %v", err)
	}

	wantTypes := []string{"query", "mutation", "query", "mutation", "mutation"}
	wantOps := []string{"A", "B", "C", "D", "E"}
	if len(seen) != len(wantTypes) {
		t.Fatalf("onRequest fired %d times, want %d (%+v)", len(seen), len(wantTypes), seen)
	}
	for i := range wantTypes {
		if seen[i].Type != wantTypes[i] {
			t.Errorf("call %d type = %q, want %q", i, seen[i].Type, wantTypes[i])
		}
		if !strings.Contains(seen[i].Operation, wantOps[i]) {
			t.Errorf("call %d operation = %q, want it to contain %q", i, seen[i].Operation, wantOps[i])
		}
	}
}

func TestScriptRunner_OnResponseReportsResultsAndFailures(t *testing.T) {
	server, _ := recordingServer(t)

	type outcome struct {
		info   RequestInfo
		result map[string]interface{}
		err    error
	}
	var outcomes []outcome
	runner := testRunner(t, server.URL,
		WithReadOnly(true),
		WithOnResponse(func(info RequestInfo, result map[string]interface{}, err error) {
			outcomes = append(outcomes, outcome{info, result, err})
		}),
	)

	script := `function run(gql) {
  gql.query("query { ok }");
  try { gql.mutation("mutation { ok }"); } catch (e) {}
  return true;
}`

	if _, err := runner.RunSource(context.Background(), "resp.js", script, "run", nil); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("onResponse fired %d times, want 2", len(outcomes))
	}
	if outcomes[0].err != nil {
		t.Errorf("query outcome err = %v, want nil", outcomes[0].err)
	}
	if outcomes[0].result == nil {
		t.Error("query outcome result = nil, want the response payload")
	}
	if !errors.Is(outcomes[1].err, ErrReadOnly) {
		t.Errorf("mutation outcome err = %v, want ErrReadOnly", outcomes[1].err)
	}
}

func TestScriptRunner_CallbackCannotMutateSentVariables(t *testing.T) {
	server, received := recordingServer(t)

	runner := testRunner(t, server.URL, WithOnRequest(func(info RequestInfo) {
		info.Variables["id"] = "tampered"
	}))

	script := `function run(gql) {
  return gql.query("query Q($id: ID!) { ok(id: $id) }", { id: "original" });
}`

	if _, err := runner.RunSource(context.Background(), "vars.js", script, "run", nil); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if len(*received) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(*received))
	}
	vars, _ := (*received)[0]["variables"].(map[string]interface{})
	if got := vars["id"]; got != "original" {
		t.Errorf("server received id = %v, want %q", got, "original")
	}
}

func TestScriptRunner_ReadOnlyBlocksEveryMutationForm(t *testing.T) {
	server, received := recordingServer(t)
	runner := testRunner(t, server.URL, WithReadOnly(true))

	script := `function run(gql) {
  const errs = [];
  try { gql.mutation("mutation B { ok }"); errs.push("mutation-allowed"); } catch (e) { errs.push(String(e)); }
  try { gql.request({ type: "mutation", mutation: "mutation D { ok }" }); errs.push("request-type-allowed"); } catch (e) { errs.push(String(e)); }
  try { gql.request({ mutation: "mutation E { ok }" }); errs.push("request-mutation-allowed"); } catch (e) { errs.push(String(e)); }
  const res = gql.query("query A { ok }");
  return { errs: errs, queryWorked: res !== null && res !== undefined };
}`

	result, err := runner.RunSource(context.Background(), "readonly.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}

	out, _ := result.(map[string]interface{})
	if out["queryWorked"] != true {
		t.Error("queries should still run under WithReadOnly")
	}
	errs, _ := out["errs"].([]interface{})
	if len(errs) != 3 {
		t.Fatalf("got %d results, want 3 (%v)", len(errs), errs)
	}
	for i, e := range errs {
		msg := fmt.Sprint(e)
		if !strings.Contains(msg, "read-only") {
			t.Errorf("mutation form %d = %q, want a catchable read-only rejection", i, msg)
		}
	}

	for _, req := range *received {
		if q, _ := req["query"].(string); strings.Contains(q, "mutation") {
			t.Errorf("mutation reached the server: %q", q)
		}
	}
	if len(*received) != 1 {
		t.Errorf("server saw %d requests, want only the query", len(*received))
	}
}

func TestScriptRunner_ApproverBlocksOneOperationAndLetsOthersProceed(t *testing.T) {
	server, received := recordingServer(t)

	runner := testRunner(t, server.URL, WithApprover(func(_ context.Context, info RequestInfo) error {
		if id, _ := info.Variables["id"].(string); id == "2" {
			return errors.New("id 2 is protected")
		}
		return nil
	}))

	script := `function run(gql) {
  const results = [];
  for (const id of ["1", "2", "3"]) {
    try {
      gql.mutation("mutation Disable($id: ID!) { disableUser(id: $id) { ok } }", { id: id });
      results.push("ok:" + id);
    } catch (e) {
      results.push("blocked:" + id);
    }
  }
  return results;
}`

	result, err := runner.RunSource(context.Background(), "approve.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}

	got, _ := result.([]interface{})
	want := []string{"ok:1", "blocked:2", "ok:3"}
	if len(got) != len(want) {
		t.Fatalf("results = %v, want %v", got, want)
	}
	for i := range want {
		if fmt.Sprint(got[i]) != want[i] {
			t.Errorf("result %d = %v, want %q", i, got[i], want[i])
		}
	}
	if len(*received) != 2 {
		t.Errorf("server saw %d requests, want 2 (the approved ones)", len(*received))
	}
}

func TestScriptRunner_MaxOperationsCapsRunawayScripts(t *testing.T) {
	server, received := recordingServer(t)
	runner := testRunner(t, server.URL, WithMaxOperations(3))

	script := `function run(gql) {
  let completed = 0;
  try {
    for (let i = 0; i < 100; i++) { gql.query("query { ok }"); completed++; }
  } catch (e) {
    return { completed: completed, error: String(e) };
  }
  return { completed: completed, error: "" };
}`

	result, err := runner.RunSource(context.Background(), "budget.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}

	out, _ := result.(map[string]interface{})
	if got := fmt.Sprint(out["completed"]); got != "3" {
		t.Errorf("completed = %v, want 3", out["completed"])
	}
	if msg := fmt.Sprint(out["error"]); !strings.Contains(msg, "budget") {
		t.Errorf("error = %q, want a budget rejection", msg)
	}
	if len(*received) != 3 {
		t.Errorf("server saw %d requests, want 3", len(*received))
	}
}

func TestScriptRunner_BlockedOperationsCountAgainstBudget(t *testing.T) {
	server, received := recordingServer(t)
	runner := testRunner(t, server.URL, WithReadOnly(true), WithMaxOperations(2))

	script := `function run(gql) {
  const results = [];
  for (let i = 0; i < 5; i++) {
    try { gql.mutation("mutation { ok }"); results.push("ok"); } catch (e) { results.push(String(e)); }
  }
  return results;
}`

	result, err := runner.RunSource(context.Background(), "budget2.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}

	got, _ := result.([]interface{})
	if len(got) != 5 {
		t.Fatalf("results = %v, want 5 entries", got)
	}
	if msg := fmt.Sprint(got[2]); !strings.Contains(msg, "budget") {
		t.Errorf("third attempt = %q, want the budget to be exhausted by rejected mutations", msg)
	}
	if len(*received) != 0 {
		t.Errorf("server saw %d requests, want 0", len(*received))
	}
}

func TestScriptRunner_NoOptionsPreservesExistingBehavior(t *testing.T) {
	server, received := recordingServer(t)
	runner := testRunner(t, server.URL)

	script := `function run(gql) {
  gql.query("query { ok }");
  gql.mutation("mutation { ok }");
  return "done";
}`

	result, err := runner.RunSource(context.Background(), "plain.js", script, "run", nil)
	if err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if result != "done" {
		t.Errorf("result = %v, want %q", result, "done")
	}
	if len(*received) != 2 {
		t.Errorf("server saw %d requests, want 2 (no policy applied by default)", len(*received))
	}
}

func TestProjectConfig_ResolveScript(t *testing.T) {
	cfg := &ProjectConfig{Scripts: map[string]NamedScript{
		"with-defaults": {Source: "function run(){}", Defaults: map[string]interface{}{"a": 1, "b": 2}},
		"named-fn":      {Source: "function go(){}", Function: "go"},
		"empty":         {Source: "   "},
	}}

	t.Run("defaults the function name to run", func(t *testing.T) {
		s, err := cfg.ResolveScript("with-defaults")
		if err != nil {
			t.Fatalf("ResolveScript: %v", err)
		}
		if s.Function != "run" {
			t.Errorf("Function = %q, want %q", s.Function, "run")
		}
	})

	t.Run("keeps an explicit function name", func(t *testing.T) {
		s, err := cfg.ResolveScript("named-fn")
		if err != nil {
			t.Fatalf("ResolveScript: %v", err)
		}
		if s.Function != "go" {
			t.Errorf("Function = %q, want %q", s.Function, "go")
		}
	})

	t.Run("rejects unknown, empty-named and sourceless scripts", func(t *testing.T) {
		for _, name := range []string{"missing", "", "empty"} {
			if _, err := cfg.ResolveScript(name); err == nil {
				t.Errorf("ResolveScript(%q) = nil error, want a failure", name)
			}
		}
	})

	t.Run("MergeInput layers caller input over defaults", func(t *testing.T) {
		s, err := cfg.ResolveScript("with-defaults")
		if err != nil {
			t.Fatalf("ResolveScript: %v", err)
		}
		merged := s.MergeInput(map[string]interface{}{"b": 99, "c": 3})
		if merged["a"] != 1 || fmt.Sprint(merged["b"]) != "99" || merged["c"] != 3 {
			t.Errorf("merged = %v, want a=1 b=99 c=3", merged)
		}
		if s.Defaults["b"] != 2 {
			t.Errorf("MergeInput mutated Defaults: b = %v, want 2", s.Defaults["b"])
		}
	})
}
