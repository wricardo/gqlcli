package gqlcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestCLIHeaderOverridesEnvironmentHeaderOnQuery(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if got := r.Header.Get("X-Trace"); got != "cli" {
			t.Errorf("X-Trace = %q, want cli", got)
		}
		if got := r.Header.Get("X-Env"); got != "env-only" {
			t.Errorf("X-Env = %q, want env-only", got)
		}
		if got := r.Header.Get("X-CLI"); got != "cli-only" {
			t.Errorf("X-CLI = %q, want cli-only", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	cfg := map[string]interface{}{
		"default": "local",
		"environments": map[string]interface{}{
			"local": map[string]interface{}{
				"url": server.URL,
				"headers": map[string]string{
					"X-Trace": "env",
					"X-Env":   "env-only",
				},
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".gqlcli.json", data, 0644); err != nil {
		t.Fatal(err)
	}

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	err = app.Run([]string{"gqlcli", "query", "--format", "json", "--output", "result.json", "--header", "X-Trace=cli", "--header", "X-CLI=cli-only", "{ ok }"})
	if err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
}

func TestHeaderFlagRejectsInvalidSyntax(t *testing.T) {
	builder := NewCLIBuilder(&Config{URL: "http://example.invalid/graphql", Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	err := app.Run([]string{"gqlcli", "query", "--header", "missing-equals", "{ ok }"})
	if err == nil {
		t.Fatal("expected invalid header error")
	}
	if got := err.Error(); !strings.Contains(got, "invalid --header") || !strings.Contains(got, "KEY=VALUE") {
		t.Fatalf("error = %q, want clear invalid --header KEY=VALUE message", got)
	}
}

func TestHTTPCommandsExposeRepeatableHeaderFlag(t *testing.T) {
	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	commands := []*cli.Command{
		builder.GetQueryCommand(),
		builder.GetMutationCommand(),
		builder.GetBatchCommand(),
		builder.GetQueriesCommand(),
		builder.GetMutationsCommand(),
		builder.GetTypesCommand(),
		builder.GetDescribeCommand(),
	}

	for _, cmd := range commands {
		t.Run(cmd.Name, func(t *testing.T) {
			flag, ok := findStringSliceFlag(cmd.Flags, "header")
			if !ok {
				t.Fatalf("%s missing --header flag", cmd.Name)
			}
			if !contains(flag.Aliases, "H") {
				t.Fatalf("%s --header aliases = %v, want -H", cmd.Name, flag.Aliases)
			}
		})
	}
}

func findStringSliceFlag(flags []cli.Flag, name string) (*cli.StringSliceFlag, bool) {
	for _, flag := range flags {
		if f, ok := flag.(*cli.StringSliceFlag); ok && f.Name == name {
			return f, true
		}
	}
	return nil, false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
