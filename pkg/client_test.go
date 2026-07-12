package gqlcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
)

func TestTimeoutFlagPropagatesToConfigAndHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	cfg := &Config{URL: server.URL, Format: "json", Timeout: 30}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	builder.RegisterCommands(app)

	if err := app.Run([]string{"gqlcli", "query", "--timeout", "7", "{ ok }"}); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if cfg.Timeout != 7 {
		t.Fatalf("timeout flag did not update config: got %d, want 7", cfg.Timeout)
	}
	client := NewHTTPClient(cfg)
	if got, want := client.client.GetClient().Timeout, 7*time.Second; got != want {
		t.Fatalf("timeout did not propagate to HTTP client: got %v, want %v", got, want)
	}
}

func TestHTTPClientRetriesTransientFailures(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "try again", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5, RetryCount: 2, RetryDelay: time.Millisecond})
	result, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "{ ok }"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if fmt.Sprint(result["data"]) == "<nil>" {
		t.Fatalf("missing data in result: %#v", result)
	}
}

func TestFailOnGraphQLErrorsControlsErrorBehavior(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"boom"}]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})
	if _, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "{ boom }"}); err != nil {
		t.Fatalf("Execute without FailOnGraphQLErrors returned error: %v", err)
	}

	client = NewHTTPClient(&Config{URL: server.URL, Timeout: 5, FailOnGraphQLErrors: true})
	_, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "{ boom }"})
	var gqlErr *GraphQLResponseError
	if !errors.As(err, &gqlErr) {
		t.Fatalf("Execute with FailOnGraphQLErrors error = %T %v, want *GraphQLResponseError", err, err)
	}
}

func TestGraphQLErrorHandlerExitsNonZero(t *testing.T) {
	cfg := &Config{Format: "json"}
	builder := NewCLIBuilder(cfg)

	set := flag.NewFlagSet("query", flag.ContinueOnError)
	set.String("format", "json", "")
	set.String("output", "", "")
	ctx := cli.NewContext(cli.NewApp(), set, nil)

	err := builder.handleError(ctx, &GraphQLResponseError{
		Query:    "{ boom }",
		Response: map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": "boom"}}},
	})
	var exitErr cli.ExitCoder
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want cli.ExitCoder", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.ExitCode())
	}
}

func TestHTTPClientSchemaHintFiltersRootFieldMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		q, _ := req["query"].(string)
		if strings.Contains(q, "__type") {
			_, _ = w.Write([]byte(`{"data":{"__type":{"name":"Query","kind":"OBJECT","fields":[{"name":"checkBookAccess","type":{"kind":"SCALAR","name":"Boolean","ofType":null},"args":[]},{"name":"appointments","type":{"kind":"LIST","name":null,"ofType":{"kind":"SCALAR","name":"String","ofType":null}},"args":[]}]}}}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Cannot query field \"checkBook\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})
	result, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "query { checkBook }"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	errs, ok := result["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("missing errors in result: %#v", result)
	}
	em, ok := errs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected error shape: %#v", errs[0])
	}
	ext, ok := em["extensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing extensions: %#v", em)
	}
	hint, _ := ext["schemaHint"].(string)
	if hint == "" {
		t.Fatalf("missing schemaHint: %#v", ext)
	}
	if !strings.Contains(hint, "checkBookAccess") {
		t.Fatalf("filtered hint missing expected close match: %q", hint)
	}
	if !strings.Contains(hint, "# Closest matches") {
		t.Fatalf("filtered hint missing closest-matches marker: %q", hint)
	}
	if strings.Contains(hint, "appointments") {
		t.Fatalf("filtered hint should not include unrelated full Query fields: %q", hint)
	}
}

func TestHTTPClientSchemaHintFallsBackWhenNoRootFieldMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		q, _ := req["query"].(string)
		if strings.Contains(q, "__type") {
			_, _ = w.Write([]byte(`{"data":{"__type":{"name":"Query","kind":"OBJECT","fields":[{"name":"checkBookAccess","type":{"kind":"SCALAR","name":"Boolean","ofType":null},"args":[]},{"name":"appointments","type":{"kind":"LIST","name":null,"ofType":{"kind":"SCALAR","name":"String","ofType":null}},"args":[]}]}}}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Cannot query field \"zzzzNotFound\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})
	result, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "query { zzzzNotFound }"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	errs, ok := result["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("missing errors in result: %#v", result)
	}
	em, ok := errs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected error shape: %#v", errs[0])
	}
	ext, ok := em["extensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing extensions: %#v", em)
	}
	hint, _ := ext["schemaHint"].(string)
	if hint == "" {
		t.Fatalf("missing schemaHint: %#v", ext)
	}
	if !strings.Contains(hint, "appointments") {
		t.Fatalf("fallback hint should include full Query fields, got: %q", hint)
	}
	if strings.Contains(hint, "# Closest matches") {
		t.Fatalf("fallback full hint should not include closest-matches marker: %q", hint)
	}
}

func TestHTTPClientSchemaHintFiltersUnknownArgumentOnField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		q, _ := req["query"].(string)
		if strings.Contains(q, "__type") {
			_, _ = w.Write([]byte(`{"data":{"__type":{"name":"Query","kind":"OBJECT","fields":[{"name":"citizens","type":{"kind":"LIST","name":null,"ofType":{"kind":"SCALAR","name":"String","ofType":null}},"args":[]},{"name":"orders","type":{"kind":"LIST","name":null,"ofType":{"kind":"SCALAR","name":"String","ofType":null}},"args":[]}]}}}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Unknown argument \"foo\" on field \"Query.citizens\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})
	result, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "query { citizens(foo: 1) }"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	errs, ok := result["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("missing errors in result: %#v", result)
	}
	em, ok := errs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected error shape: %#v", errs[0])
	}
	ext, ok := em["extensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing extensions: %#v", em)
	}
	hint, _ := ext["schemaHint"].(string)
	if hint == "" {
		t.Fatalf("missing schemaHint: %#v", ext)
	}
	if !strings.Contains(hint, "citizens") {
		t.Fatalf("expected filtered hint for citizens field, got: %q", hint)
	}
	if !strings.Contains(hint, "# Closest matches") {
		t.Fatalf("expected closest-matches marker, got: %q", hint)
	}
	if strings.Contains(hint, "orders") {
		t.Fatalf("filtered hint should not include unrelated fields, got: %q", hint)
	}
}

func TestHTTPClientSchemaHintFiltersMissingSubfieldSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		q, _ := req["query"].(string)
		if strings.Contains(q, "__type") {
			_, _ = w.Write([]byte(`{"data":{"__type":{"name":"Query","kind":"OBJECT","fields":[{"name":"citizen","type":{"kind":"OBJECT","name":"Citizen","ofType":null},"args":[]},{"name":"city","type":{"kind":"OBJECT","name":"City","ofType":null},"args":[]}]}}}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Field \"citizen\" of type \"Citizen\" must have a selection of subfields. Did you mean \"citizen { ... }\"?","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})
	result, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "query { citizen }"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	errs, ok := result["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("missing errors in result: %#v", result)
	}
	em, ok := errs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected error shape: %#v", errs[0])
	}
	ext, ok := em["extensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing extensions: %#v", em)
	}
	hint, _ := ext["schemaHint"].(string)
	if hint == "" {
		t.Fatalf("missing schemaHint: %#v", ext)
	}
	if !strings.Contains(hint, "citizen") {
		t.Fatalf("expected filtered hint for citizen field, got: %q", hint)
	}
	if !strings.Contains(hint, "# Closest matches") {
		t.Fatalf("expected closest-matches marker, got: %q", hint)
	}
	if strings.Contains(hint, "city") {
		t.Fatalf("filtered hint should not include unrelated fields, got: %q", hint)
	}
}
