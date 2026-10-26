package explorer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/model"
)

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (r *Resolver) rootType(d *ast.Definition) *model.SchemaType {
	if d == nil {
		return nil
	}
	return r.schemaType(d.Name)
}
func (r *Resolver) matchesType(t *ast.Type, term string) bool {
	if t == nil {
		return false
	}
	return strings.EqualFold(t.Name(), term)
}
func (r *Resolver) acceptsType(f *ast.FieldDefinition, term string) bool {
	if f == nil {
		return false
	}
	for _, a := range f.Arguments {
		if r.matchesType(a.Type, term) {
			return true
		}
	}
	return false
}
func (r *Resolver) reachableTypes(from string, depth int) []*model.SchemaType {
	result := []*model.SchemaType{}
	if r.element(from) == nil {
		return result
	}
	seen := map[string]bool{from: true}
	type step struct {
		coord string
		depth int
	}
	queue := []step{{from, 0}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth >= depth {
			continue
		}
		for _, edge := range r.Index.Outgoing[current.coord] {
			if !outputEdge(edge.Kind) || edge.Target == nil || seen[edge.Target.Coordinate] {
				continue
			}
			coord := edge.Target.Coordinate
			seen[coord] = true
			if edge.Target.Kind == "TYPE" {
				result = append(result, r.schemaType(coord))
			}
			queue = append(queue, step{coord, current.depth + 1})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Coordinate < result[j].Coordinate })
	return result
}
func outputEdge(kind string) bool {
	switch kind {
	case "DECLARES_FIELD", "DECLARES_ENUM_VALUE", "RETURNS", "UNION_MEMBER", "POSSIBLE_TYPE":
		return true
	}
	return false
}
func (r *Resolver) edges(coord string, outgoing bool, kind *model.ReferenceKind) []*model.SchemaEdge {
	edges := r.Index.Incoming[coord]
	if outgoing {
		edges = r.Index.Outgoing[coord]
	}
	result := make([]*model.SchemaEdge, 0, len(edges))
	for _, e := range edges {
		if kind == nil || e.Kind == string(*kind) {
			result = append(result, r.edge(e))
		}
	}
	return result
}
func (r *Resolver) edgeByID(id string) *graph.Edge {
	_, rest, ok := strings.Cut(id, "|")
	if !ok {
		return nil
	}
	source, _, ok := strings.Cut(rest, "|")
	if !ok {
		return nil
	}
	for _, edge := range r.Index.Outgoing[source] {
		if edge.ID == id {
			return edge
		}
	}
	return nil
}

type reachCursor struct {
	Snapshot, From, Matching, Kind string
	Depth, Position, First         int
}

func (r *Resolver) reachableFields(matching string, kind *model.OperationKind, from *string, maxDepth, first *int, after *string) (*model.ReachableFieldConnection, error) {
	n, err := clampFirst(first, 50)
	if err != nil {
		return nil, err
	}
	depth := 8
	if maxDepth != nil {
		depth = *maxDepth
	}
	if depth < 0 {
		return nil, fmt.Errorf("maxDepth must not be negative")
	}
	origin := deref(from)
	if origin != "" && r.operation(origin) == nil {
		return nil, fmt.Errorf("unknown operation %q", origin)
	}
	operationKind := ""
	if kind != nil {
		operationKind = string(*kind)
	}
	position := 0
	if after != nil && *after != "" {
		data, err := base64.RawURLEncoding.DecodeString(*after)
		if err != nil {
			return nil, fmt.Errorf("invalid reachability cursor: %w", err)
		}
		var c reachCursor
		if err = json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("invalid reachability cursor: %w", err)
		}
		if c.Snapshot != r.Index.Snapshot {
			return nil, fmt.Errorf("stale reachability cursor: schema changed")
		}
		if c.From != origin || c.Matching != matching || c.Kind != operationKind || c.Depth != depth || c.First != n || c.Position <= 0 {
			return nil, fmt.Errorf("reachability cursor does not match this query")
		}
		position = c.Position
	}
	result := &model.ReachableFieldConnection{Nodes: []*model.ReachableField{}, PageInfo: &model.PageInfo{}}
	fields := []*graph.Element{}
	for _, e := range orderedElements(r.Index) {
		if e.Kind == "FIELD" && contains(e.Coordinate+" "+e.Description, matching) {
			fields = append(fields, e)
		}
	}
	found := 0
	for _, op := range r.operations() {
		if origin != "" && origin != op.Coordinate {
			continue
		}
		if kind != nil && op.Kind != *kind {
			continue
		}
		for _, target := range fields {
			if op.Coordinate != target.Coordinate {
				paths, err := r.Index.Paths(op.Coordinate, target.Coordinate, "OUTPUT", depth, 1, "")
				if err != nil {
					return nil, err
				}
				if len(paths.Nodes) == 0 {
					continue
				}
			}
			found++
			if found <= position {
				continue
			}
			if len(result.Nodes) == n {
				result.PageInfo.HasNextPage = true
				return r.reachPage(result, matching, operationKind, origin, depth, found-1, n), nil
			}
			result.Nodes = append(result.Nodes, model.NewReachableField(op, r.schemaField(target.Coordinate), depth))
		}
	}
	if found < position {
		return nil, fmt.Errorf("reachability cursor is past the end")
	}
	return r.reachPage(result, matching, operationKind, origin, depth, found, n), nil
}

func (r *Resolver) reachPage(page *model.ReachableFieldConnection, matching, kind, from string, depth, position, first int) *model.ReachableFieldConnection {
	if len(page.Nodes) != 0 {
		data, _ := json.Marshal(reachCursor{Snapshot: r.Index.Snapshot, From: from, Matching: matching, Kind: kind, Depth: depth, Position: position, First: first})
		cursor := base64.RawURLEncoding.EncodeToString(data)
		page.PageInfo.EndCursor = &cursor
	}
	return page
}
