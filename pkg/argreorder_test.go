package gqlcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
