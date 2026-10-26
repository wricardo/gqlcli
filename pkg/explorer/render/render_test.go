package render_test

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/render"
)

const schemaSDL = `
interface Node { id: ID! }
"A product\nwith details"
type Product implements Node @tag(label: "catalog") {
  id: ID!
  "Look up a product"
  lookup(
    "Status to search"
    status: Status = READY @deprecated(reason: "changed")
    limit: Int = 5
  ): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")
}
"Statuses"
enum Status @tag(label: "state") {
  "Ready now"
  READY @deprecated(reason: "retired")
  WAITING
}
input Filter @tag(label: "filter") {
  "Requested status"
  status: Status = READY @deprecated(reason: "use statuses")
  statuses: [Status!] = [READY, WAITING]
}
union Result @tag(label: "union") = Product
scalar Date @specifiedBy(url: "https://example.test/date")
`

func sourceDefinitions(t *testing.T) ast.DefinitionList {
	t.Helper()
	doc, err := parser.ParseSchema(&ast.Source{Input: schemaSDL})
	if err != nil {
		t.Fatal(err)
	}
	return doc.Definitions
}

func TestRenderElementsOnDemand(t *testing.T) {
	defs := sourceDefinitions(t)
	product := defs.ForName("Product")
	lookup := product.Fields.ForName("lookup")
	filter := defs.ForName("Filter")
	status := defs.ForName("Status")

	tests := []struct {
		name string
		elem *graph.Element
		want string
	}{
		{"type", &graph.Element{Kind: "TYPE", Coordinate: "Product", Definition: product}, `"A product\nwith details"
type Product implements Node @tag(label: "catalog") {
  id: ID!
  "Look up a product"
  lookup(
    "Status to search"
    status: Status = READY @deprecated(reason: "changed")
    limit: Int = 5
  ): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")
}`},
		{"field", &graph.Element{Kind: "FIELD", Coordinate: "Product.lookup", Field: lookup, Parent: product}, `"Look up a product"
lookup(
  "Status to search"
  status: Status = READY @deprecated(reason: "changed")
  limit: Int = 5
): [Product!]! @deprecated(reason: "old") @tag(label: "lookup")`},
		{"argument", &graph.Element{Kind: "INPUT_VALUE", Coordinate: "Product.lookup(status:)", InputValue: lookup.Arguments.ForName("status"), Parent: product}, `"Status to search"
status: Status = READY @deprecated(reason: "changed")`},
		{"input field", &graph.Element{Kind: "INPUT_VALUE", Coordinate: "Filter.status", Field: filter.Fields.ForName("status"), Parent: filter}, `"Requested status"
status: Status = READY @deprecated(reason: "use statuses")`},
		{"input type", &graph.Element{Kind: "TYPE", Coordinate: "Filter", Definition: filter}, `input Filter @tag(label: "filter") {
  "Requested status"
  status: Status = READY @deprecated(reason: "use statuses")
  statuses: [Status!] = [READY, WAITING]
}`},
		{"enum", &graph.Element{Kind: "TYPE", Coordinate: "Status", Definition: status}, `"Statuses"
enum Status @tag(label: "state") {
  "Ready now"
  READY @deprecated(reason: "retired")
  WAITING
}`},
		{"enum value", &graph.Element{Kind: "ENUM_VALUE", Coordinate: "Status.READY", EnumValue: status.EnumValues.ForName("READY"), Parent: status}, `"Ready now"
READY @deprecated(reason: "retired")`},
		{"union", &graph.Element{Kind: "TYPE", Coordinate: "Result", Definition: defs.ForName("Result")}, `union Result @tag(label: "union") = Product`},
		{"scalar", &graph.Element{Kind: "TYPE", Coordinate: "Date", Definition: defs.ForName("Date")}, `scalar Date @specifiedBy(url: "https://example.test/date")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.Element(tt.elem)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("SDL:\n%s\nwant:\n%s", got, tt.want)
			}
			again, err := render.Element(tt.elem)
			if err != nil || again != got {
				t.Errorf("repeat render = %q, %v; want %q", again, err, got)
			}
			// A member declaration needs its owner to form a complete SDL document.
			parseable := got
			switch tt.elem.Kind {
			case "FIELD":
				parseable = "type Holder {\n" + got + "\n}"
			case "INPUT_VALUE":
				if tt.elem.InputValue != nil {
					parseable = "type Holder { f(" + got + "): Boolean }"
				} else {
					parseable = "input Holder {\n" + got + "\n}"
				}
			case "ENUM_VALUE":
				parseable = "enum Holder {\n" + got + "\n}"
			}
			if _, err := parser.ParseSchema(&ast.Source{Input: parseable}); err != nil {
				t.Errorf("SDL is not parseable: %v\n%s", err, parseable)
			}
		})
	}
}

func TestRenderEmptyAndInvalidElements(t *testing.T) {
	tests := []struct {
		name string
		elem *graph.Element
		want string
		err  bool
	}{
		{"builtin scalar", &graph.Element{Kind: "TYPE", Coordinate: "String", Definition: &ast.Definition{Kind: ast.Scalar, Name: "String", BuiltIn: true}}, "scalar String", false},
		{"empty object", &graph.Element{Kind: "TYPE", Coordinate: "Empty", Definition: &ast.Definition{Kind: ast.Object, Name: "Empty"}}, "type Empty", false},
		{"introspection-only field", &graph.Element{Kind: "TYPE", Coordinate: "Query", Definition: &ast.Definition{Kind: ast.Object, Name: "Query", Fields: ast.FieldList{{Name: "__schema"}}}}, "type Query", false},
		{"nil", nil, "nil element", true},
		{"missing field", &graph.Element{Kind: "FIELD", Coordinate: "Query.empty"}, "missing field definition", true},
		{"missing input value", &graph.Element{Kind: "INPUT_VALUE", Coordinate: "Query.empty(arg:)"}, "missing input value definition", true},
		{"unknown kind", &graph.Element{Kind: "UNKNOWN", Coordinate: "x"}, "unsupported element kind", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.Element(tt.elem)
			if tt.err {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %v; want %q", err, tt.want)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("render = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
