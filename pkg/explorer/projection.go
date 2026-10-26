package explorer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
	"github.com/wricardo/gqlcli/pkg/explorer/model"
)

func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deprecation(ds ast.DirectiveList) (bool, *string) {
	d := ds.ForName("deprecated")
	if d == nil {
		return false, nil
	}
	reason := d.Arguments.ForName("reason")
	if reason == nil || reason.Value == nil {
		return true, text("No longer supported")
	}
	rendered := reason.Value.Raw
	return true, &rendered
}

func (r *Resolver) element(coordinate string) *graph.Element {
	if r.Index == nil {
		return nil
	}
	return r.Index.Elements[coordinate]
}

func (r *Resolver) schemaType(name string) *model.SchemaType {
	e := r.element(name)
	if e == nil || e.Kind != "TYPE" {
		return nil
	}
	return &model.SchemaType{Coordinate: e.Coordinate, Name: e.Name, Description: text(e.Description), Kind: model.TypeKind(e.Definition.Kind)}
}

func (r *Resolver) schemaField(coordinate string) *model.SchemaField {
	e := r.element(coordinate)
	if e == nil || e.Kind != "FIELD" {
		return nil
	}
	deprecated, reason := deprecation(e.Field.Directives)
	return &model.SchemaField{Coordinate: e.Coordinate, Name: e.Name, Description: text(e.Description), IsDeprecated: deprecated, DeprecationReason: reason}
}

func (r *Resolver) schemaInput(coordinate string) *model.SchemaInputValue {
	e := r.element(coordinate)
	if e == nil || e.Kind != "INPUT_VALUE" {
		return nil
	}
	var typ *ast.Type
	var directives ast.DirectiveList
	var defaultValue *ast.Value
	if e.InputValue != nil {
		typ, directives, defaultValue = e.InputValue.Type, e.InputValue.Directives, e.InputValue.DefaultValue
	} else if e.Field != nil {
		// gqlparser represents input-object fields as FieldDefinition, not ArgumentDefinition.
		typ, directives, defaultValue = e.Field.Type, e.Field.Directives, e.Field.DefaultValue
	}
	deprecated, reason := deprecation(directives)
	value := &model.SchemaInputValue{
		Coordinate: e.Coordinate, Name: e.Name, Description: text(e.Description),
		IsDeprecated: deprecated, DeprecationReason: reason,
		Required: typ != nil && typ.NonNull && defaultValue == nil,
	}
	if defaultValue != nil {
		rendered := defaultValue.String()
		value.DefaultValue = &rendered
	}
	return value
}

func (r *Resolver) schemaEnum(coordinate string) *model.EnumValue {
	e := r.element(coordinate)
	if e == nil || e.Kind != "ENUM_VALUE" {
		return nil
	}
	deprecated, reason := deprecation(e.EnumValue.Directives)
	return &model.EnumValue{Coordinate: e.Coordinate, Name: e.Name, Description: text(e.Description), IsDeprecated: deprecated, DeprecationReason: reason}
}

func (r *Resolver) project(e *graph.Element) model.SchemaElement {
	if e == nil {
		return nil
	}
	switch e.Kind {
	case "TYPE":
		return r.schemaType(e.Coordinate)
	case "FIELD":
		return r.schemaField(e.Coordinate)
	case "INPUT_VALUE":
		return r.schemaInput(e.Coordinate)
	case "ENUM_VALUE":
		return r.schemaEnum(e.Coordinate)
	}
	return nil
}

// A non-null reference is an outer wrapper, followed by a list wrapper (if any)
// and then the element type. Each node retains its own exact SDL signature.
func (r *Resolver) typeRef(t *ast.Type) (*model.TypeRef, error) {
	if t == nil {
		return nil, fmt.Errorf("missing type reference")
	}
	name := t.Name()
	named := r.schemaType(name)
	if named == nil {
		return nil, fmt.Errorf("unknown type %q", name)
	}
	ref := &model.TypeRef{
		Signature: t.String(), Name: name, Kind: named.Kind, NamedType: named,
		IsList: t.Elem != nil && !t.NonNull, IsNonNull: t.NonNull,
	}
	var inner *ast.Type
	if t.NonNull {
		copy := *t
		copy.NonNull = false
		inner = &copy
	} else if t.Elem != nil {
		inner = t.Elem
	}
	if inner != nil {
		var err error
		ref.OfType, err = r.typeRef(inner)
		if err != nil {
			return nil, err
		}
	}
	return ref, nil
}

func (r *Resolver) edge(e *graph.Edge) *model.SchemaEdge {
	if e == nil {
		return nil
	}
	return &model.SchemaEdge{ID: e.ID, Kind: model.ReferenceKind(e.Kind)}
}

func (r *Resolver) path(p *graph.Path) *model.SchemaPath {
	result := &model.SchemaPath{
		From: r.project(p.From), To: r.project(p.To), Depth: p.Depth, FieldDepth: p.FieldDepth,
		Segments: make([]*model.PathSegment, 0, len(p.Edges)),
	}
	for i, e := range p.Edges {
		result.Segments = append(result.Segments, &model.PathSegment{Index: i, Edge: r.edge(e)})
	}
	return result
}

func (r *Resolver) paths(from, to, traversal string, depth, first *int, after *string) (*model.SchemaPathConnection, error) {
	d := 8
	if depth != nil {
		d = *depth
	}
	n := 100
	if first != nil {
		n = *first
	}
	cursor := ""
	if after != nil {
		cursor = *after
	}
	connection, err := r.Index.Paths(from, to, traversal, d, n, cursor)
	if err != nil {
		return nil, err
	}
	nodes := make([]*model.SchemaPath, 0, len(connection.Nodes))
	for _, p := range connection.Nodes {
		nodes = append(nodes, r.path(p))
	}
	return &model.SchemaPathConnection{Nodes: nodes, PageInfo: &model.PageInfo{
		HasNextPage: connection.HasNextPage, EndCursor: text(connection.EndCursor),
	}}, nil
}

func orderedElements(index *graph.Index) []*graph.Element {
	elements := make([]*graph.Element, 0, len(index.Elements))
	for _, e := range index.Elements {
		elements = append(elements, e)
	}
	sort.Slice(elements, func(i, j int) bool { return elements[i].Coordinate < elements[j].Coordinate })
	return elements
}

func contains(s, filter string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(filter))
}
func clampFirst(first *int, defaultCount int) (int, error) {
	if first == nil {
		return defaultCount, nil
	}
	if *first <= 0 {
		return 0, fmt.Errorf("first must be positive")
	}
	return *first, nil
}

func (r *Resolver) roots() []struct {
	kind model.OperationKind
	def  *ast.Definition
} {
	return []struct {
		kind model.OperationKind
		def  *ast.Definition
	}{
		{model.OperationKindQuery, r.Schema.Query},
		{model.OperationKindMutation, r.Schema.Mutation},
		{model.OperationKindSubscription, r.Schema.Subscription},
	}
}

func (r *Resolver) operation(coordinate string) *model.Operation {
	e := r.element(coordinate)
	if e == nil || e.Kind != "FIELD" {
		return nil
	}
	for _, root := range r.roots() {
		if root.def != nil && e.Parent != nil && root.def.Name == e.Parent.Name {
			return &model.Operation{Name: e.Name, Coordinate: coordinate, Kind: root.kind, Description: text(e.Description)}
		}
	}
	return nil
}

func (r *Resolver) operations() []*model.Operation {
	result := []*model.Operation{}
	for _, root := range r.roots() {
		if root.def == nil {
			continue
		}
		for _, field := range root.def.Fields {
			if op := r.operation(root.def.Name + "." + field.Name); op != nil {
				result = append(result, op)
			}
		}
	}
	return result
}

func (r *Resolver) fieldType(coordinate string) (*model.TypeRef, error) {
	e := r.element(coordinate)
	if e == nil || e.Field == nil {
		return nil, fmt.Errorf("unknown field %q", coordinate)
	}
	return r.typeRef(e.Field.Type)
}

func (r *Resolver) inputType(coordinate string) (*model.TypeRef, error) {
	e := r.element(coordinate)
	if e == nil {
		return nil, fmt.Errorf("unknown input %q", coordinate)
	}
	if e.InputValue != nil {
		return r.typeRef(e.InputValue.Type)
	}
	if e.Field != nil {
		return r.typeRef(e.Field.Type)
	}
	return nil, fmt.Errorf("missing input type at %q", coordinate)
}

func (r *Resolver) arguments(coordinate string) []*model.SchemaInputValue {
	result := []*model.SchemaInputValue{}
	e := r.element(coordinate)
	if e == nil || e.Field == nil {
		return result
	}
	for _, arg := range e.Field.Arguments {
		if value := r.schemaInput(coordinate + "(" + arg.Name + ":)"); value != nil {
			result = append(result, value)
		}
	}
	return result
}
