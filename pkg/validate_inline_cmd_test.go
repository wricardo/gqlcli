package gqlcli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestInlineExecutorExposesSchema covers the accessor inline validation is
// built on: the executor has to keep the schema it was handed.
func TestInlineExecutorExposesSchema(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "inline", Input: validatorSDL})
	exec := NewInlineExecutor(stubSchema{schema: schema, seen: &[]string{}})

	got := exec.Schema()
	if got == nil {
		t.Fatal("Schema() = nil, want the schema the executor was built with")
	}
	if got.Query == nil || got.Query.Name != "Query" {
		t.Errorf("Schema().Query = %v, want Query", got.Query)
	}
	if (*InlineExecutor)(nil).Schema() != nil {
		t.Error("Schema() on a nil executor did not return nil")
	}
}

func TestInlineValidateCommand(t *testing.T) {
	previousExiter := cli.OsExiter
	cli.OsExiter = func(int) {}
	t.Cleanup(func() { cli.OsExiter = previousExiter })

	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "inline", Input: validatorSDL})
	seen := &[]string{}

	newApp := func() (*cli.App, *bytes.Buffer) {
		out := &bytes.Buffer{}
		cs := NewInlineCommandSet(NewInlineExecutor(stubSchema{schema: schema, seen: seen}))
		app := &cli.App{Name: "inline", Writer: out, ErrWriter: &bytes.Buffer{}}
		cs.Mount(app)
		return app, out
	}

	app, out := newApp()
	if err := app.Run([]string{"inline", "validate", "{ me { id name } }"}); err != nil {
		t.Fatalf("valid document: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("output = %q, want a valid verdict", out.String())
	}

	app, _ = newApp()
	if got := exitCode(t, app.Run([]string{"inline", "validate", "{ me { nope } }"})); got != 1 {
		t.Errorf("invalid document: exit code = %d, want 1", got)
	}

	app, _ = newApp()
	if got := exitCode(t, app.Run([]string{"inline", "validate", "--format", "json", "{ me { nope } }"})); got != 1 {
		t.Errorf("invalid document with --format json: exit code = %d, want 1", got)
	}

	// Validation must never reach the schema's executor.
	if len(*seen) != 0 {
		t.Errorf("the schema executed %v, want nothing executed", *seen)
	}
}
