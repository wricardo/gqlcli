package gqlcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestReorderArgsForCommand_FlagsAfterPositional(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "variables", Aliases: []string{"v"}},
		&cli.StringFlag{Name: "url", Aliases: []string{"u"}},
		&cli.BoolFlag{Name: "debug", Aliases: []string{"d"}},
	}

	got := ReorderArgsForCommand(
		[]string{"{ ok }", "--variables", `{"id":"1"}`, "--url", "http://x", "--debug"},
		flags,
	)
	want := []string{"--variables", `{"id":"1"}`, "--url", "http://x", "--debug", "{ ok }"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReorderArgsForCommand_AlreadyFlagsFirstIsNoop(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "variables", Aliases: []string{"v"}},
	}
	args := []string{"--variables", `{"id":"1"}`, "{ ok }"}
	got := ReorderArgsForCommand(args, flags)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("got %v, want unchanged %v", got, args)
	}
}

func TestReorderArgsForCommand_InlineEqualsValueNotDoubleConsumed(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "header", Aliases: []string{"H"}},
	}
	got := ReorderArgsForCommand(
		[]string{"{ ok }", "--header=Authorization=Bearer tok", "extra-positional"},
		flags,
	)
	want := []string{"--header=Authorization=Bearer tok", "{ ok }", "extra-positional"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReorderArgsForCommand_BoolFlagDoesNotConsumeNextToken(t *testing.T) {
	flags := []cli.Flag{
		&cli.BoolFlag{Name: "debug", Aliases: []string{"d"}},
	}
	got := ReorderArgsForCommand([]string{"{ ok }", "--debug", "second-positional"}, flags)
	want := []string{"--debug", "{ ok }", "second-positional"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReorderArgsForCommand_DoubleDashStopsReordering(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "variables"},
	}
	got := ReorderArgsForCommand([]string{"{ ok }", "--", "--variables", "literal"}, flags)
	want := []string{"{ ok }", "--", "--variables", "literal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReorderOSArgs_UnknownCommandPassesThrough(t *testing.T) {
	cmds := []*cli.Command{{Name: "query", Flags: []cli.Flag{&cli.StringFlag{Name: "variables"}}}}
	args := []string{"gqlcli", "--help"}
	got := ReorderOSArgs(args, cmds)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("got %v, want unchanged %v", got, args)
	}
}

func TestReorderOSArgs_RewritesMatchedCommand(t *testing.T) {
	cmds := []*cli.Command{{Name: "query", Flags: []cli.Flag{&cli.StringFlag{Name: "variables"}}}}
	got := ReorderOSArgs([]string{"gqlcli", "query", "{ ok }", "--variables", "1"}, cmds)
	want := []string{"gqlcli", "query", "--variables", "1", "{ ok }"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestQueryCommand_VariablesFlagAfterPositional_EndToEnd is the regression
// test for the real bug: gqlcli query '<query>' --variables '{...}' (the
// documented usage — query string first, flags after) silently dropped
// --variables because Go's flag package stops parsing at the first
// non-flag token. It must reach the server with variables intact once
// ReorderOSArgs runs ahead of app.Run, exactly as main.go now does.
func TestQueryCommand_VariablesFlagAfterPositional_EndToEnd(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	osArgs := []string{
		"gqlcli", "query",
		"query($id: ID!) { thing(id: $id) { ok } }",
		"--variables", `{"id":"42"}`,
		"--url", server.URL,
	}
	args := ReorderOSArgs(osArgs, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}

	vars, ok := gotBody["variables"].(map[string]interface{})
	if !ok {
		t.Fatalf("request body had no variables: %#v", gotBody)
	}
	if vars["id"] != "42" {
		t.Fatalf("variables.id = %v, want 42", vars["id"])
	}
}

// TestReorderOSArgs_CommandWithSubcommandsIsUntouched guards against a real
// regression: "config" (and "op") dispatch to a nested subcommand via their
// first positional token (e.g. `config set-default --name X`). Reordering
// using the parent command's own (near-empty) Flags would hoist --name
// ahead of "set-default", breaking the nested dispatch entirely.
func TestReorderOSArgs_CommandWithSubcommandsIsUntouched(t *testing.T) {
	cmds := []*cli.Command{{
		Name:        "config",
		Subcommands: []*cli.Command{{Name: "set-default"}},
	}}
	args := []string{"gqlcli", "config", "set-default", "--name", "smoke-citizen"}
	got := ReorderOSArgs(args, cmds)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("got %v, want unchanged %v", got, args)
	}
}

// The following tests each drive a single flag through the full app.Run path
// with real argv order — positional query string first, flag(s) after,
// exactly like every documented example — rather than unit-testing
// ReorderArgsForCommand in isolation. That distinction matters: a fix that
// only satisfies the parser-helper unit tests can still ship broken if the
// wiring into app.Run (or a flag's own consumer code) regresses, and the
// nested-subcommand bug fixed alongside this one is proof that isolated
// unit tests miss real breakage.

// TestQueryCommand_URLFlagAfterPositional_EndToEnd proves --url placed after
// the query string is honored, not silently ignored in favor of the
// CLIBuilder's default URL (which would 404 against nothing at all).
func TestQueryCommand_URLFlagAfterPositional_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{URL: "http://127.0.0.1:1", Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{"gqlcli", "query", "{ ok }", "--url", server.URL}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
}

// TestQueryCommand_HeaderFlagAfterPositional_EndToEnd proves --header (-H)
// placed after the query string reaches the server, not silently dropped.
func TestQueryCommand_HeaderFlagAfterPositional_EndToEnd(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--header", "Authorization=Bearer end-to-end-token",
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	if gotAuth != "Bearer end-to-end-token" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer end-to-end-token")
	}
}

// TestQueryCommand_FormatFlagAfterPositional_EndToEnd proves --format placed
// after the query string actually changes the rendered output, rather than
// silently falling back to the default "toon" formatter.
func TestQueryCommand_FormatFlagAfterPositional_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "toon", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	var out bytes.Buffer
	app.Writer = &out
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--format", "json-pretty",
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "{\n") {
		t.Fatalf("output = %q, want pretty-printed JSON (with indentation), not toon", got)
	}
}

// TestQueryCommand_OutputFlagAfterPositional_EndToEnd proves --output placed
// after the query string actually redirects the response to a file, instead
// of silently writing to stdout.
func TestQueryCommand_OutputFlagAfterPositional_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	outFile := t.TempDir() + "/result.json"

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--output", outFile,
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not written: %v", err)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("output file content = %q, want it to contain the response", string(data))
	}
}

// TestQueryCommand_IncludeHeadersFlagAfterPositional_EndToEnd proves
// --include-headers placed after the query string prepends response
// header/status info to the printed output.
func TestQueryCommand_IncludeHeadersFlagAfterPositional_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	var out bytes.Buffer
	app.Writer = &out
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--include-headers",
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "200") {
		t.Fatalf("output = %q, want it to include the HTTP status line", out.String())
	}
}

// TestQueryCommand_FailOnGraphQLErrorsFlagAfterPositional_EndToEnd proves
// --fail-on-graphql-errors placed after the query string still causes
// app.Run to return a non-zero exit when the response contains errors.
func TestQueryCommand_FailOnGraphQLErrorsFlagAfterPositional_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	var out bytes.Buffer
	app.Writer = &out
	// cli.Exit errors (returned by handleError for fail-on-graphql-errors)
	// trigger urfave/cli's default ExitErrHandler, which calls os.Exit and
	// would kill the test process. Override it so the error is simply
	// returned from app.Run instead.
	app.ExitErrHandler = func(*cli.Context, error) {}
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--fail-on-graphql-errors",
	}, app.Commands)
	if err := app.Run(args); err == nil {
		t.Fatal("app.Run() error = nil, want non-nil (fail-on-graphql-errors)")
	}
}

// TestQueryCommand_RetryFlagAfterPositional_EndToEnd proves --retry placed
// after the query string actually causes a retry on a transient (5xx)
// failure, rather than giving up on the first attempt.
func TestQueryCommand_RetryFlagAfterPositional_EndToEnd(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--retry", "1",
		"--retry-delay", "10ms",
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v, want success after one retry", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("server saw %d attempts, want 2 (one failure + one retry)", got)
	}
}

// TestQueryCommand_TimeoutFlagAfterPositional_EndToEnd proves --timeout
// placed after the query string is actually applied to the request, by
// using a timeout shorter than the server's response delay and expecting
// the request to fail rather than hang for the default 30s.
func TestQueryCommand_TimeoutFlagAfterPositional_EndToEnd(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))

	builder := NewCLIBuilder(&Config{Format: "json"})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--timeout", "1",
	}, app.Commands)
	err := app.Run(args)

	// Unblock the handler and shut the server down before asserting, so a
	// failed assertion can't leave server.Close() deadlocked on the still-
	// blocked handler goroutine.
	close(release)
	server.Close()

	if err == nil {
		t.Fatal("app.Run() error = nil, want a timeout error")
	}
}

// TestQueryCommand_DebugFlagAfterPositional_EndToEnd proves --debug placed
// after the query string actually turns on resty's request/response trace,
// instead of silently producing no diagnostic output.
func TestQueryCommand_DebugFlagAfterPositional_EndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)

	// go-resty's debug logger writes to stderr (util.go: createLogger uses
	// log.New(os.Stderr, ...)), and does so against the real fd 2 rather
	// than the Go-level os.Stderr *File variable, so redirect the actual OS
	// file descriptor.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origFD, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Dup2(int(w.Fd()), int(os.Stderr.Fd())); err != nil {
		t.Fatal(err)
	}

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "{ ok }",
		"--url", server.URL,
		"--debug",
	}, app.Commands)
	runErr := app.Run(args)

	_ = syscall.Dup2(origFD, int(os.Stderr.Fd()))
	_ = syscall.Close(origFD)
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if runErr != nil {
		t.Fatalf("app.Run() error = %v", runErr)
	}
	if !strings.Contains(buf.String(), "REQUEST") {
		t.Fatalf("stderr = %q, want resty debug trace containing REQUEST", buf.String())
	}
}
