package gqlcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
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
