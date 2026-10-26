package render_test

import (
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/render"
)

func TestRenderEscapesDescriptionAndDefaultLiterals(t *testing.T) {
	doc, err := parser.ParseSchema(&ast.Source{Input: `
type Edge {
  "quoted \"description\""
  simple(flag: Boolean = false, note: String = "ctrl\u0001"): Int @deprecated
}
`})
	if err != nil {
		t.Fatal(err)
	}
	field := doc.Definitions.ForName("Edge").Fields.ForName("simple")
	got, err := render.Element(&graph.Element{Kind: "FIELD", Field: field})
	if err != nil {
		t.Fatal(err)
	}
	want := `"quoted \"description\""
simple(flag: Boolean = false, note: String = "ctrl\u0001"): Int @deprecated`
	if got != want {
		t.Errorf("SDL = %q; want %q", got, want)
	}
	if _, err := parser.ParseSchema(&ast.Source{Input: "type Holder {\n" + got + "\n}"}); err != nil {
		t.Errorf("rendered SDL cannot be parsed: %v", err)
	}
}
