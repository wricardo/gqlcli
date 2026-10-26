package gqlcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestInlineExploreNeverExecutesTarget(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "inline", Input: validatorSDL})
	seen := &[]string{}
	app := &cli.App{Name: "inline"}
	NewInlineCommandSet(NewInlineExecutor(stubSchema{schema: schema, seen: seen})).Mount(app)
	path := filepath.Join(t.TempDir(), "result.json")
	if err := app.Run([]string{"inline", "explore", "--output", path, `{ type(name:"User") { name } }`}); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"name":"User"`) {
		t.Fatalf("unexpected response: %s", result)
	}
	if len(*seen) != 0 {
		t.Fatalf("target executor ran %v", *seen)
	}
}

func TestInlineExplorePrintScopedSchemaWithoutTarget(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "inline", Input: validatorSDL})
	seen := &[]string{}
	app := &cli.App{Name: "inline"}
	var out bytes.Buffer
	app.Writer = &out
	NewInlineCommandSet(NewInlineExecutor(stubSchema{schema: schema, seen: seen})).Mount(app)
	if err := app.Run([]string{"inline", "explore", "--print-schema-field", "reachableFields"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reachableFields(matching: String!") || strings.Contains(out.String(), "overview: SchemaOverview!") {
		t.Fatalf("incorrect scoped explorer schema: %s", out.String())
	}
	if len(*seen) != 0 {
		t.Fatalf("target executor ran %v", *seen)
	}
}
