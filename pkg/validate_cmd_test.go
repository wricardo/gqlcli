package gqlcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

// validateCLI stands up the real command set against an endpoint that only
// answers introspection, so any attempt to execute an operation fails the test.
func validateCLI(t *testing.T) (*cli.App, *bytes.Buffer, *int) {
	t.Helper()
	srv, requests := introspectionServer(t, validatorSDL)

	// urfave/cli hands an ExitCoder to OsExiter, which would take the test
	// binary down with it; the returned error still carries the code.
	previousExiter := cli.OsExiter
	cli.OsExiter = func(int) {}
	t.Cleanup(func() { cli.OsExiter = previousExiter })

	out := &bytes.Buffer{}
	builder := NewCLIBuilder(&Config{URL: srv.URL, Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli", Writer: out, ErrWriter: &bytes.Buffer{}}
	builder.RegisterCommands(app)
	return app, out, requests
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	coder, ok := err.(cli.ExitCoder)
	if !ok {
		t.Fatalf("app.Run() returned a non-exit error: %v", err)
	}
	return coder.ExitCode()
}

func TestValidateCommandVerdicts(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"valid query", []string{"gqlcli", "validate", "{ me { id name } }"}, 0},
		{"unknown field", []string{"gqlcli", "validate", "{ me { nope } }"}, 1},
		{"query flag", []string{"gqlcli", "validate", "--query", "{ me { id } }"}, 0},
		{"mutation flag rejects unknown root", []string{"gqlcli", "validate", "--mutation", "mutation { nope }"}, 1},
		{"syntax error", []string{"gqlcli", "validate", "{ me { id "}, 1},
		// A document declaring required variables is valid without values.
		{"declared variables", []string{"gqlcli", "validate", "query ($id: ID!) { user(id: $id) { name } }"}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _, _ := validateCLI(t)
			if got := exitCode(t, app.Run(tc.args)); got != tc.wantCode {
				t.Errorf("exit code = %d, want %d", got, tc.wantCode)
			}
		})
	}
}

// An invalid document must fail the command even when the output is formatted
// as JSON — the verdict lives in the exit code, not in the rendering.
func TestValidateCommandJSONOutputStillExitsNonZero(t *testing.T) {
	app, out, _ := validateCLI(t)

	if got := exitCode(t, app.Run([]string{"gqlcli", "validate", "--format", "json", "{ me { nope } }"})); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	rendered := out.String()
	if !strings.Contains(rendered, `"valid"`) || !strings.Contains(rendered, `"errors"`) {
		t.Errorf("output = %s, want a valid/errors JSON shape", rendered)
	}
	if !strings.Contains(rendered, "nope") {
		t.Errorf("output = %s, want it to name the unknown field", rendered)
	}
}

func TestValidateCommandSchemaFileNeedsNoNetwork(t *testing.T) {
	app, _, requests := validateCLI(t)

	// Capture the SDL through the sdl command first, then validate against it.
	schemaPath := filepath.Join(t.TempDir(), "schema.graphql")
	if err := app.Run([]string{"gqlcli", "sdl", "--output", schemaPath}); err != nil {
		t.Fatalf("sdl command: %v", err)
	}
	sdl, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read dumped schema: %v", err)
	}
	if !strings.Contains(string(sdl), "type User") {
		t.Errorf("dumped SDL missing the User type:\n%s", sdl)
	}

	afterDump := *requests
	if got := exitCode(t, app.Run([]string{"gqlcli", "validate", "--schema-file", schemaPath, "{ me { id } }"})); got != 0 {
		t.Errorf("valid document against --schema-file: exit code = %d, want 0", got)
	}
	if got := exitCode(t, app.Run([]string{"gqlcli", "validate", "--schema-file", schemaPath, "{ me { nope } }"})); got != 1 {
		t.Errorf("invalid document against --schema-file: exit code = %d, want 1", got)
	}
	if *requests != afterDump {
		t.Errorf("--schema-file issued %d requests, want none", *requests-afterDump)
	}

	if err := app.Run([]string{"gqlcli", "validate", "--schema-file", "does-not-exist.graphql", "{ me { id } }"}); err == nil {
		t.Error("a missing schema file was accepted, want an error")
	}
}

// Without --schema-file the schema comes from the endpoint, so validate has to
// resolve the URL and headers the same way every other HTTP command does.
func TestValidateResolvesEndpointFromProjectConfig(t *testing.T) {
	srv, requests := introspectionServer(t, validatorSDL)

	previousExiter := cli.OsExiter
	cli.OsExiter = func(int) {}
	t.Cleanup(func() { cli.OsExiter = previousExiter })

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	projectConfig := map[string]interface{}{
		"default": "local",
		"environments": map[string]interface{}{
			"local": map[string]interface{}{"url": srv.URL},
		},
	}
	data, err := json.Marshal(projectConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".gqlcli.json", data, 0644); err != nil {
		t.Fatal(err)
	}

	// No --url and no --schema-file: the endpoint must come from .gqlcli.json.
	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli", Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	builder.RegisterCommands(app)

	if err := app.Run([]string{"gqlcli", "validate", "{ me { id } }"}); err != nil {
		t.Fatalf("validate against the default environment: %v", err)
	}
	if *requests != 1 {
		t.Errorf("server saw %d requests, want 1 introspection call", *requests)
	}
	if got := exitCode(t, app.Run([]string{"gqlcli", "validate", "--env", "local", "{ me { nope } }"})); got != 1 {
		t.Errorf("invalid document via --env: exit code = %d, want 1", got)
	}

	// An endpoint that cannot be reached must fail, never report "valid".
	if err := app.Run([]string{"gqlcli", "validate", "--url", "http://127.0.0.1:1/graphql", "{ me { id } }"}); err == nil {
		t.Error("validate succeeded against an unreachable endpoint, want an error")
	}
}

// --validate-only must stop before the operation is sent. The test server
// errors on anything but introspection, so an executed operation would surface.
func TestValidateOnlyFlagDoesNotExecute(t *testing.T) {
	for _, command := range []string{"query", "mutation", "subscribe"} {
		t.Run(command, func(t *testing.T) {
			app, _, requests := validateCLI(t)

			document := "{ me { nope } }"
			if command == "mutation" {
				document = "mutation { nope }"
			}
			if command == "subscribe" {
				document = "subscription { nope }"
			}

			if got := exitCode(t, app.Run([]string{"gqlcli", command, "--validate-only", document})); got != 1 {
				t.Errorf("exit code = %d, want 1 for an invalid document", got)
			}
			if *requests != 1 {
				t.Errorf("server saw %d requests, want exactly the introspection call", *requests)
			}
		})
	}
}

func TestValidateOnlyFlagAcceptsValidDocument(t *testing.T) {
	app, out, requests := validateCLI(t)

	if err := app.Run([]string{"gqlcli", "query", "--validate-only", "{ me { id } }"}); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("output = %q, want a valid verdict", out.String())
	}
	if *requests != 1 {
		t.Errorf("server saw %d requests, want only introspection", *requests)
	}
}
