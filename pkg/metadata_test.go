package gqlcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestResponseMetadataFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-123")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"ping":"pong"}}`))
	}))
	defer server.Close()

	t.Run("default json remains machine parseable", func(t *testing.T) {
		stdout, err := runTestCLI(server.URL, "query", "--format", "json", "{ ping }")
		if err != nil {
			t.Fatalf("run gqlcli: %v", err)
		}
		if strings.Contains(stdout, "X-Request-Id") || strings.Contains(stdout, "status-code") {
			t.Fatalf("default output included metadata: %q", stdout)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
			t.Fatalf("default output is not JSON: %v\n%s", err, stdout)
		}
	})

	t.Run("include headers prepends response headers", func(t *testing.T) {
		stdout, err := runTestCLI(server.URL, "query", "--format", "json", "--include-headers", "{ ping }")
		if err != nil {
			t.Fatalf("run gqlcli: %v", err)
		}
		if !strings.HasPrefix(stdout, "HTTP/1.1 202 Accepted\n") {
			t.Fatalf("missing status line in output: %q", stdout)
		}
		if !strings.Contains(stdout, "X-Request-Id: req-123\n") {
			t.Fatalf("missing response header in output: %q", stdout)
		}
		if !strings.Contains(stdout, `{"data":{"ping":"pong"}}`) {
			t.Fatalf("missing response body in output: %q", stdout)
		}
	})

	t.Run("dump headers writes file without changing json output", func(t *testing.T) {
		headerFile := filepath.Join(t.TempDir(), "headers.txt")
		stdout, err := runTestCLI(server.URL, "query", "--format", "json", "--dump-headers", headerFile, "{ ping }")
		if err != nil {
			t.Fatalf("run gqlcli: %v", err)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
		}
		headers, err := os.ReadFile(headerFile)
		if err != nil {
			t.Fatalf("read dumped headers: %v", err)
		}
		headerText := string(headers)
		if !strings.Contains(headerText, "HTTP/1.1 202 Accepted\n") || !strings.Contains(headerText, "X-Request-Id: req-123\n") {
			t.Fatalf("dumped headers missing metadata: %q", headerText)
		}
	})

	t.Run("metadata prints selected fields", func(t *testing.T) {
		stdout, err := runTestCLI(server.URL, "query", "--format", "json", "--metadata", "status-code", "--metadata", "header:X-Request-Id", "{ ping }")
		if err != nil {
			t.Fatalf("run gqlcli: %v", err)
		}
		if !strings.Contains(stdout, "status-code: 202\n") {
			t.Fatalf("missing status-code metadata: %q", stdout)
		}
		if !strings.Contains(stdout, "header.X-Request-Id: req-123\n") {
			t.Fatalf("missing selected header metadata: %q", stdout)
		}
	})
}

func runTestCLI(url string, args ...string) (string, error) {
	cfg := &Config{URL: url, Format: "json", Timeout: 5}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	app.Name = "gqlcli"
	app.Writer = &bytes.Buffer{}
	builder.RegisterCommands(app)

	argv := append([]string{"gqlcli"}, args...)
	if err := app.Run(argv); err != nil {
		return app.Writer.(*bytes.Buffer).String(), err
	}
	return strings.TrimSuffix(app.Writer.(*bytes.Buffer).String(), "\n"), nil
}
