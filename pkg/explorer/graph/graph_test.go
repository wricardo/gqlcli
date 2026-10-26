package graph

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

const fixture = `
schema { query: Root }
type Root {
  search(id: ID!, filter: Filter): Node
  list: [Node!]!
  direct: Item
  lookup: Search
}
interface Node { id: ID! }
type Item implements Node { id: ID! child: Item status: Status }
type Other implements Node { id: ID! }
union Search = Item | Other
input Filter { next: Filter tags: [String!] }
enum Status { ACTIVE INACTIVE }
`

func testIndex(t *testing.T, sdl string) *Index {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "fixture", Input: sdl})
	if err != nil {
		t.Fatal(err)
	}
	index, err := New(schema)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestIndexCoordinatesEdgesAndSnapshot(t *testing.T) {
	idx := testIndex(t, fixture)
	same := testIndex(t, fixture)
	if idx.Snapshot != same.Snapshot {
		t.Fatalf("non-deterministic snapshot: %s != %s", idx.Snapshot, same.Snapshot)
	}
	for _, coord := range []string{"Root", "Root.search", "Root.search(id:)", "Root.search(filter:)", "Filter.next", "Status.ACTIVE", "String", "Int", "ID", "Boolean", "Float"} {
		if idx.Elements[coord] == nil {
			t.Errorf("missing element %q", coord)
		}
	}
	for coord := range idx.Elements {
		if strings.HasPrefix(coord, "__") || strings.Contains(coord, ".__") {
			t.Errorf("indexed introspection element %q", coord)
		}
	}
	if got := idx.Elements["Filter.next"]; got.Kind != "INPUT_VALUE" || got.Field == nil || got.InputValue != nil || got.Parent.Name != "Filter" {
		t.Errorf("input object field metadata: %+v", got)
	}
	if got := idx.Elements["Root.search(id:)"]; got.Kind != "INPUT_VALUE" || got.InputValue == nil || got.Parent.Name != "Root" {
		t.Errorf("argument metadata: %+v", got)
	}
	for _, tc := range []struct{ from, to, kind string }{
		{"Root", "Root.search", "DECLARES_FIELD"},
		{"Root.search", "Node", "RETURNS"},
		{"Root.search", "Root.search(filter:)", "DECLARES_INPUT_VALUE"},
		{"Root.search(filter:)", "Filter", "ACCEPTS"},
		{"Filter", "Filter.next", "DECLARES_INPUT_VALUE"},
		{"Filter.next", "Filter", "ACCEPTS"},
		{"Status", "Status.ACTIVE", "DECLARES_ENUM_VALUE"},
		{"Item", "Node", "IMPLEMENTS"},
		{"Node", "Item", "POSSIBLE_TYPE"},
		{"Search", "Item", "UNION_MEMBER"},
	} {
		var found bool
		for _, edge := range idx.Outgoing[tc.from] {
			if edge.Target.Coordinate == tc.to && edge.Kind == tc.kind {
				found = true
				if edge.ID == "" {
					t.Errorf("edge has no stable ID: %+v", tc)
				}
				if !reflect.DeepEqual(edge, findIncoming(idx, tc.to, edge.ID)) {
					t.Errorf("incoming edge differs from outgoing: %s", edge.ID)
				}
			}
		}
		if !found {
			t.Errorf("missing %s %s -> %s", tc.kind, tc.from, tc.to)
		}
	}
	for coord, edges := range idx.Outgoing {
		for i := 1; i < len(edges); i++ {
			if edges[i-1].ID >= edges[i].ID {
				t.Errorf("unordered or duplicated edge from %s", coord)
			}
		}
	}
	if idx.Snapshot == testIndex(t, strings.Replace(fixture, "status: Status", "status: Status!", 1)).Snapshot {
		t.Error("type expression change did not invalidate snapshot")
	}
}

func findIncoming(idx *Index, coord, id string) *Edge {
	for _, e := range idx.Incoming[coord] {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func TestPathsAbstractTypesDepthAndTraversal(t *testing.T) {
	idx := testIndex(t, fixture)
	for _, tc := range []struct {
		from, to, via string
		depth, fields int
	}{
		{"Root.search", "Item.status", "POSSIBLE_TYPE", 3, 2},
		{"Root.lookup", "Item.status", "UNION_MEMBER", 3, 2},
		{"Root.search", "Root.search(id:)", "", -1, 0},
	} {
		got, err := idx.Paths(tc.from, tc.to, "OUTPUT", 4, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		if tc.depth < 0 {
			if len(got.Nodes) != 0 {
				t.Errorf("OUTPUT reached input argument: %+v", got.Nodes)
			}
			continue
		}
		if len(got.Nodes) != 1 || got.Nodes[0].Depth != tc.depth || got.Nodes[0].FieldDepth != tc.fields {
			t.Errorf("%s to %s: %+v", tc.from, tc.to, got.Nodes)
			continue
		}
		var found bool
		for _, edge := range got.Nodes[0].Edges {
			if edge.Kind == tc.via {
				found = true
			}
		}
		if !found {
			t.Errorf("path missing %s edge", tc.via)
		}
	}
	input, err := idx.Paths("Root.search", "Filter.next", "INPUT", 4, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Nodes) != 1 || input.Nodes[0].Depth != 3 || input.Nodes[0].FieldDepth != 0 {
		t.Errorf("input path: %+v", input.Nodes)
	}
	output, err := idx.Paths("Root.search", "Filter.next", "OUTPUT", 4, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Nodes) != 0 {
		t.Errorf("output traversed input: %+v", output.Nodes)
	}
	all, err := idx.Paths("Root.search", "Filter.next", "ALL", 4, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Nodes) != 1 {
		t.Errorf("ALL failed to traverse input: %+v", all.Nodes)
	}
	short, err := idx.Paths("Root.search", "Item.status", "OUTPUT", 2, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Nodes) != 0 {
		t.Errorf("maxDepth ignored: %+v", short.Nodes)
	}
	// A concrete Item cannot turn into a different concrete Other merely by
	// passing through the interface both happen to implement.
	invalidOutput, err := idx.Paths("Root.direct", "Other", "OUTPUT", 5, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(invalidOutput.Nodes) != 0 {
		t.Errorf("OUTPUT changed concrete type: %+v", invalidOutput.Nodes)
	}
	if got, err := idx.Paths("Item", "Node", "ALL", 1, 100, ""); err != nil || len(got.Nodes) != 1 {
		t.Errorf("ALL should expose IMPLEMENTS edge: nodes=%v err=%v", got, err)
	}
	self, err := idx.Paths("Root.search", "Root.search", "OUTPUT", 0, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(self.Nodes) != 1 || self.Nodes[0].Depth != 0 || self.Nodes[0].FieldDepth != 0 {
		t.Errorf("zero-edge path: %+v", self.Nodes)
	}
}

func TestPathsPaginationAndCursorBinding(t *testing.T) {
	idx := testIndex(t, fixture)
	all, err := idx.Paths("Root", "Item", "OUTPUT", 9, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Nodes) != 4 {
		t.Fatalf("expected four paths, got %d", len(all.Nodes))
	}
	for i := 1; i < len(all.Nodes); i++ {
		if all.Nodes[i-1].Depth > all.Nodes[i].Depth {
			t.Error("paths not sorted by depth")
		}
	}
	var paged []*Path
	var cursor string
	for {
		page, err := idx.Paths("Root", "Item", "OUTPUT", 9, 1, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Nodes) != 1 {
			t.Fatalf("expected single path, got %d", len(page.Nodes))
		}
		paged = append(paged, page.Nodes...)
		if !page.HasNextPage {
			if page.EndCursor == "" {
				t.Fatal("missing last page cursor")
			}
			break
		}
		cursor = page.EndCursor
	}
	for i := range paged {
		if !reflect.DeepEqual(pathIDs(paged[i]), pathIDs(all.Nodes[i])) {
			t.Errorf("page %d differed from unpaged result", i)
		}
	}
	first, err := idx.Paths("Root", "Item", "OUTPUT", 9, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to, traversal string
		depth               int
	}{
		{"Root.search", "Item", "OUTPUT", 9},
		{"Root", "Other", "OUTPUT", 9},
		{"Root", "Item", "ALL", 9},
		{"Root", "Item", "OUTPUT", 8},
	} {
		if _, err := idx.Paths(tc.from, tc.to, tc.traversal, tc.depth, 1, first.EndCursor); err == nil {
			t.Errorf("cursor accepted different parameters: %+v", tc)
		}
	}
	if _, err := idx.Paths("Root", "Item", "OUTPUT", 9, 2, first.EndCursor); err == nil {
		t.Error("cursor accepted different first")
	}
	changed := testIndex(t, strings.Replace(fixture, "status: Status", "status: Status!", 1))
	if _, err := changed.Paths("Root", "Item", "OUTPUT", 9, 1, first.EndCursor); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("changed schema cursor error = %v", err)
	}
	if _, err := idx.Paths("Root", "Item", "OUTPUT", 9, 1, "invalid"); err == nil {
		t.Error("malformed cursor accepted")
	}
	if _, err := idx.Paths("Root", "Item", "INVALID", 9, 1, ""); err == nil {
		t.Error("invalid traversal accepted")
	}
}

func pathIDs(p *Path) []string {
	ids := make([]string, len(p.Edges))
	for i, e := range p.Edges {
		ids[i] = e.ID
	}
	return ids
}

func TestPathsExplicitResourceFailure(t *testing.T) {
	// 18 diamond layers yield more paths than can be returned completely.
	idx := &Index{Elements: make(map[string]*Element), Outgoing: make(map[string][]*Edge), Incoming: make(map[string][]*Edge), edgeIDs: make(map[string]bool), Snapshot: "fixture"}
	start := &Element{Coordinate: "start", Kind: "TYPE"}
	end := &Element{Coordinate: "end", Kind: "TYPE"}
	idx.Elements[start.Coordinate], idx.Elements[end.Coordinate] = start, end
	previous := []*Element{start}
	for layer := range 18 {
		next := []*Element{{Coordinate: strings.Repeat("a", layer+1), Kind: "TYPE"}, {Coordinate: strings.Repeat("b", layer+1), Kind: "TYPE"}}
		for _, node := range next {
			idx.Elements[node.Coordinate] = node
		}
		for _, src := range previous {
			for _, dst := range next {
				idx.link("POSSIBLE_TYPE", src, dst, nil, nil)
			}
		}
		previous = next
	}
	for _, node := range previous {
		idx.link("POSSIBLE_TYPE", node, end, nil, nil)
	}
	if _, err := idx.Paths("start", "end", "OUTPUT", 20, 1, ""); !errors.Is(err, ErrResourceLimit) {
		t.Errorf("resource overflow = %v, want ErrResourceLimit", err)
	}
}
