// Package graph indexes the declarations and references of a GraphQL schema.
package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// Element is a uniquely addressable schema declaration. Exactly one of Definition,
// Field, InputValue, and EnumValue identifies its originating AST declaration.
// Input object fields use Field; gqlparser represents them as FieldDefinitions.
type Element struct {
	Coordinate  string
	Name        string
	Description string
	Kind        string
	Definition  *ast.Definition
	Field       *ast.FieldDefinition
	InputValue  *ast.ArgumentDefinition
	EnumValue   *ast.EnumValueDefinition
	Parent      *ast.Definition
}

// Edge is a directed, declaration-specific relationship between two elements.
type Edge struct {
	ID         string
	Kind       string
	Source     *Element
	Target     *Element
	Field      *Element
	InputValue *Element
}

type Index struct {
	Snapshot string
	Elements map[string]*Element
	Outgoing map[string][]*Edge
	Incoming map[string][]*Edge
	edgeIDs  map[string]bool
}

type Path struct {
	Edges      []*Edge
	Depth      int
	FieldDepth int
	From       *Element
	To         *Element
}

type PathConnection struct {
	Nodes       []*Path
	HasNextPage bool
	EndCursor   string
}

// ErrResourceLimit is returned rather than silently omitting qualifying paths.
var ErrResourceLimit = errors.New("schema path exploration resource limit exceeded")

const (
	maxExploredStates  = 1_000_000
	maxEnumeratedPaths = 100_000
)

// New builds a self-contained index. Callers must not mutate the schema after indexing.
func New(schema *ast.Schema) (*Index, error) {
	if schema == nil {
		return nil, errors.New("nil schema")
	}
	idx := &Index{Elements: make(map[string]*Element), Outgoing: make(map[string][]*Edge), Incoming: make(map[string][]*Edge), edgeIDs: make(map[string]bool)}
	definitions := make(map[string]*ast.Definition, len(schema.Types)+5)
	for name, def := range schema.Types {
		if strings.HasPrefix(name, "__") {
			continue
		}
		if def == nil || def.Name != name {
			return nil, fmt.Errorf("invalid schema type %q", name)
		}
		definitions[name] = def
	}
	for _, name := range []string{"String", "Int", "Float", "Boolean", "ID"} {
		if definitions[name] == nil {
			definitions[name] = &ast.Definition{Kind: ast.Scalar, Name: name, BuiltIn: true}
		}
	}
	keys := make([]string, 0, len(definitions))
	for name := range definitions {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		def := definitions[name]
		if err := idx.add(&Element{Coordinate: name, Name: name, Description: def.Description, Kind: "TYPE", Definition: def}); err != nil {
			return nil, err
		}
	}
	for _, name := range keys {
		def := definitions[name]
		parent := idx.Elements[name]
		switch def.Kind {
		case ast.Object, ast.Interface:
			for _, field := range def.Fields {
				if field == nil || strings.HasPrefix(field.Name, "__") {
					continue
				}
				e := &Element{Coordinate: name + "." + field.Name, Name: field.Name, Description: field.Description, Kind: "FIELD", Field: field, Parent: def}
				if err := idx.add(e); err != nil {
					return nil, err
				}
				idx.link("DECLARES_FIELD", parent, e, e, nil)
				if err := idx.reference("RETURNS", e, field.Type, e, nil); err != nil {
					return nil, err
				}
				for _, arg := range field.Arguments {
					if arg == nil {
						continue
					}
					input := &Element{Coordinate: e.Coordinate + "(" + arg.Name + ":)", Name: arg.Name, Description: arg.Description, Kind: "INPUT_VALUE", InputValue: arg, Parent: def}
					if err := idx.add(input); err != nil {
						return nil, err
					}
					idx.link("DECLARES_INPUT_VALUE", e, input, e, input)
					if err := idx.reference("ACCEPTS", input, arg.Type, e, input); err != nil {
						return nil, err
					}
				}
			}
		case ast.InputObject:
			for _, field := range def.Fields {
				if field == nil {
					continue
				}
				// gqlparser stores input fields as FieldDefinition, unlike field arguments.
				input := &Element{Coordinate: name + "." + field.Name, Name: field.Name, Description: field.Description, Kind: "INPUT_VALUE", Field: field, Parent: def}
				if err := idx.add(input); err != nil {
					return nil, err
				}
				idx.link("DECLARES_INPUT_VALUE", parent, input, nil, input)
				if err := idx.reference("ACCEPTS", input, field.Type, nil, input); err != nil {
					return nil, err
				}
			}
		case ast.Enum:
			for _, value := range def.EnumValues {
				if value == nil {
					continue
				}
				e := &Element{Coordinate: name + "." + value.Name, Name: value.Name, Description: value.Description, Kind: "ENUM_VALUE", EnumValue: value, Parent: def}
				if err := idx.add(e); err != nil {
					return nil, err
				}
				idx.link("DECLARES_ENUM_VALUE", parent, e, nil, nil)
			}
		case ast.Union, ast.Scalar:
		default:
			return nil, fmt.Errorf("unsupported type kind %q for %s", def.Kind, name)
		}
	}
	for _, name := range keys {
		def := definitions[name]
		source := idx.Elements[name]
		for _, iface := range def.Interfaces {
			if def.Kind != ast.Object && def.Kind != ast.Interface {
				return nil, fmt.Errorf("%s cannot implement %s", name, iface)
			}
			target := idx.Elements[iface]
			if target == nil || target.Definition.Kind != ast.Interface {
				return nil, fmt.Errorf("unknown interface %s implemented by %s", iface, name)
			}
			idx.link("IMPLEMENTS", source, target, nil, nil)
		}
		for _, member := range def.Types {
			if def.Kind != ast.Union {
				return nil, fmt.Errorf("%s is not a union", name)
			}
			target := idx.Elements[member]
			if target == nil || target.Definition.Kind != ast.Object {
				return nil, fmt.Errorf("unknown union member %s of %s", member, name)
			}
			idx.link("UNION_MEMBER", source, target, nil, nil)
		}
	}
	// Implemented interfaces are selectable as abstract output types. Recurse
	// through interface inheritance, even when schema.PossibleTypes is unfilled.
	for _, name := range keys {
		if definitions[name].Kind != ast.Object {
			continue
		}
		seen := make(map[string]bool)
		var visit func(string)
		visit = func(n string) {
			for _, iface := range definitions[n].Interfaces {
				if seen[iface] {
					continue
				}
				seen[iface] = true
				idx.link("POSSIBLE_TYPE", idx.Elements[iface], idx.Elements[name], nil, nil)
				visit(iface)
			}
		}
		visit(name)
	}
	for _, edges := range idx.Outgoing {
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	}
	for _, edges := range idx.Incoming {
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	}
	idx.Snapshot = idx.fingerprint(schema, keys)
	return idx, nil
}

func (idx *Index) add(e *Element) error {
	if idx.Elements[e.Coordinate] != nil {
		return fmt.Errorf("duplicate schema coordinate %q", e.Coordinate)
	}
	idx.Elements[e.Coordinate] = e
	return nil
}

func (idx *Index) reference(kind string, source *Element, typ *ast.Type, field, input *Element) error {
	if typ == nil {
		return fmt.Errorf("missing type of %s", source.Coordinate)
	}
	name := typ.Name()
	if strings.HasPrefix(name, "__") {
		return nil
	}
	target := idx.Elements[name]
	if target == nil {
		return fmt.Errorf("unknown type %s referenced by %s", name, source.Coordinate)
	}
	idx.link(kind, source, target, field, input)
	return nil
}

func (idx *Index) link(kind string, source, target, field, input *Element) {
	fieldID, inputID := "", ""
	if field != nil {
		fieldID = field.Coordinate
	}
	if input != nil {
		inputID = input.Coordinate
	}
	id := strings.Join([]string{kind, source.Coordinate, target.Coordinate, fieldID, inputID}, "|")
	if idx.edgeIDs[id] {
		return
	}
	idx.edgeIDs[id] = true
	e := &Edge{ID: id, Kind: kind, Source: source, Target: target, Field: field, InputValue: input}
	idx.Outgoing[source.Coordinate] = append(idx.Outgoing[source.Coordinate], e)
	idx.Incoming[target.Coordinate] = append(idx.Incoming[target.Coordinate], e)
}

// fingerprint includes declaration order and metadata (as well as topology),
// because a cursor from an older SDL must not silently resume against new SDL.
func (idx *Index) fingerprint(schema *ast.Schema, names []string) string {
	h := sha256.New()
	encode := json.NewEncoder(h)
	root := func(d *ast.Definition) string {
		if d == nil {
			return ""
		}
		return d.Name
	}
	_ = encode.Encode([]string{root(schema.Query), root(schema.Mutation), root(schema.Subscription), schema.Description, directives(schema.SchemaDirectives)})
	for _, name := range names {
		def := idx.Elements[name].Definition
		_ = encode.Encode([]string{name, string(def.Kind), def.Description, directives(def.Directives)})
		for _, iface := range def.Interfaces {
			_ = encode.Encode([]string{"interface", iface})
		}
		for _, member := range def.Types {
			_ = encode.Encode([]string{"member", member})
		}
		for _, field := range def.Fields {
			if field == nil || strings.HasPrefix(field.Name, "__") {
				continue
			}
			_ = encode.Encode([]string{"field", field.Name, field.Description, typeString(field.Type), valueString(field.DefaultValue), directives(field.Directives)})
			for _, arg := range field.Arguments {
				if arg == nil {
					continue
				}
				_ = encode.Encode([]string{"arg", arg.Name, arg.Description, typeString(arg.Type), valueString(arg.DefaultValue), directives(arg.Directives)})
			}
		}
		for _, value := range def.EnumValues {
			if value != nil {
				_ = encode.Encode([]string{"enum", value.Name, value.Description, directives(value.Directives)})
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func directives(list ast.DirectiveList) string {
	var b strings.Builder
	for _, d := range list {
		if d == nil {
			continue
		}
		fmt.Fprintf(&b, "%q", d.Name)
		for _, arg := range d.Arguments {
			if arg != nil {
				fmt.Fprintf(&b, "%q%q", arg.Name, valueString(arg.Value))
			}
		}
	}
	return b.String()
}
func valueString(v *ast.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}
func typeString(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}
