package graph

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type pathCursor struct {
	Version   int      `json:"v"`
	Snapshot  string   `json:"s"`
	From      string   `json:"f"`
	To        string   `json:"t"`
	Traversal string   `json:"r"`
	MaxDepth  int      `json:"d"`
	First     int      `json:"n"`
	EdgeIDs   []string `json:"e"`
}

// Paths lists every simple directed path, sorted first by edge count and then
// lexicographically by its sequence of stable edge IDs. Resource exhaustion is
// an error, never an incomplete page. A cursor is bound to the complete query
// and to the schema snapshot that produced it.
func (idx *Index) Paths(from, to, traversal string, maxDepth, first int, after string) (*PathConnection, error) {
	if idx == nil {
		return nil, errors.New("nil graph index")
	}
	if traversal != "OUTPUT" && traversal != "INPUT" && traversal != "ALL" {
		return nil, fmt.Errorf("invalid path traversal %q", traversal)
	}
	if maxDepth < 0 || first <= 0 {
		return nil, fmt.Errorf("maxDepth must be nonnegative and first must be positive")
	}
	start, end := idx.Elements[from], idx.Elements[to]
	if start == nil || end == nil {
		return nil, fmt.Errorf("unknown path endpoint %q or %q", from, to)
	}
	var cursor pathCursor
	if after != "" {
		data, err := base64.RawURLEncoding.DecodeString(after)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 {
			return nil, errors.New("invalid path cursor")
		}
		if cursor.Snapshot != idx.Snapshot {
			return nil, errors.New("stale path cursor: schema changed")
		}
		if cursor.From != from || cursor.To != to || cursor.Traversal != traversal || cursor.MaxDepth != maxDepth || cursor.First != first || cursor.EdgeIDs == nil {
			return nil, errors.New("path cursor does not match query parameters")
		}
	}
	requestedDepth := maxDepth
	if maxDepth >= len(idx.Elements) {
		maxDepth = len(idx.Elements) - 1 // no simple path can be longer
	}
	paths := make([]*Path, 0)
	visited := map[string]bool{from: true}
	stack := make([]*Edge, 0, maxDepth)
	states := 0
	var walk func(node *Element, selected int) error
	walk = func(node *Element, selected int) error {
		states++
		if states > maxExploredStates {
			return ErrResourceLimit
		}
		if node == end {
			if len(paths) == maxEnumeratedPaths {
				return ErrResourceLimit
			}
			p := &Path{From: start, To: end, Depth: len(stack), FieldDepth: selected, Edges: append([]*Edge(nil), stack...)}
			if traversal == "INPUT" || len(stack) == 0 {
				p.FieldDepth = 0
			}
			paths = append(paths, p)
			return nil
		}
		if len(stack) >= maxDepth {
			return nil
		}
		for _, edge := range idx.Outgoing[node.Coordinate] {
			if !allowed(edge.Kind, traversal) || visited[edge.Target.Coordinate] {
				continue
			}
			visited[edge.Target.Coordinate] = true
			stack = append(stack, edge)
			nextSelected := selected
			if traversal != "INPUT" {
				if len(stack) == 1 && start.Kind == "FIELD" {
					nextSelected++
				}
				if edge.Kind == "DECLARES_FIELD" {
					nextSelected++
				}
			}
			if err := walk(edge.Target, nextSelected); err != nil {
				return err
			}
			stack = stack[:len(stack)-1]
			delete(visited, edge.Target.Coordinate)
		}
		return nil
	}
	if err := walk(start, 0); err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Depth != paths[j].Depth {
			return paths[i].Depth < paths[j].Depth
		}
		return compareEdges(paths[i].Edges, paths[j].Edges) < 0
	})
	pos := 0
	if after != "" {
		pos = sort.Search(len(paths), func(i int) bool {
			if paths[i].Depth != len(cursor.EdgeIDs) {
				return paths[i].Depth >= len(cursor.EdgeIDs)
			}
			return compareCursor(paths[i].Edges, cursor.EdgeIDs) >= 0
		})
		if pos == len(paths) || compareCursor(paths[pos].Edges, cursor.EdgeIDs) != 0 {
			return nil, errors.New("invalid path cursor: path not found")
		}
		pos++
	}
	stop := len(paths)
	if first < stop-pos {
		stop = pos + first
	}
	result := &PathConnection{Nodes: paths[pos:stop], HasNextPage: stop < len(paths)}
	if stop > pos {
		cursor = pathCursor{Version: 1, Snapshot: idx.Snapshot, From: from, To: to, Traversal: traversal, MaxDepth: requestedDepth, First: first, EdgeIDs: make([]string, len(paths[stop-1].Edges))}
		for i, edge := range paths[stop-1].Edges {
			cursor.EdgeIDs[i] = edge.ID
		}
		data, err := json.Marshal(cursor)
		if err != nil {
			return nil, err
		}
		result.EndCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return result, nil
}

func allowed(kind, traversal string) bool {
	switch traversal {
	case "OUTPUT":
		switch kind {
		// IMPLEMENTS is not a forward selection step: following it and then
		// POSSIBLE_TYPE could switch from one concrete object to another.
		case "DECLARES_FIELD", "DECLARES_ENUM_VALUE", "RETURNS", "UNION_MEMBER", "POSSIBLE_TYPE":
			return true
		}
	case "INPUT":
		return kind == "DECLARES_INPUT_VALUE" || kind == "ACCEPTS"
	case "ALL":
		return true
	}
	return false
}

func compareEdges(a, b []*Edge) int {
	for i := range a {
		if a[i].ID < b[i].ID {
			return -1
		}
		if a[i].ID > b[i].ID {
			return 1
		}
	}
	return 0
}

func compareCursor(edges []*Edge, ids []string) int {
	for i, edge := range edges {
		if edge.ID < ids[i] {
			return -1
		}
		if edge.ID > ids[i] {
			return 1
		}
	}
	return 0
}
