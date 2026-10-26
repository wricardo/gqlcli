// Package render renders individual schema declarations as standalone SDL on demand.
package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/wricardo/gqlcli/pkg/explorer/graph"
)

// Element renders one graph element's declaration, not the whole schema. The
// source AST is read without mutation, and no SDL is stored on the graph.
func Element(e *graph.Element, includeDescription bool) (string, error) {
	if e == nil {
		return "", fmt.Errorf("render SDL: nil element")
	}

	var b strings.Builder
	switch e.Kind {
	case "TYPE":
		if e.Definition == nil {
			return "", fmt.Errorf("render SDL for %q: missing type definition", e.Coordinate)
		}
		if err := definition(&b, e.Definition, includeDescription); err != nil {
			return "", fmt.Errorf("render SDL for %q: %w", e.Coordinate, err)
		}
	case "FIELD":
		if e.Field == nil {
			return "", fmt.Errorf("render SDL for %q: missing field definition", e.Coordinate)
		}
		if err := field(&b, e.Field, 0, includeDescription); err != nil {
			return "", fmt.Errorf("render SDL for %q: %w", e.Coordinate, err)
		}
	case "INPUT_VALUE":
		if e.InputValue != nil {
			if err := inputValue(&b, e.InputValue, 0, includeDescription); err != nil {
				return "", fmt.Errorf("render SDL for %q: %w", e.Coordinate, err)
			}
		} else if e.Field != nil && e.Parent != nil && e.Parent.Kind == ast.InputObject {
			if err := field(&b, e.Field, 0, includeDescription); err != nil {
				return "", fmt.Errorf("render SDL for %q: %w", e.Coordinate, err)
			}
		} else {
			return "", fmt.Errorf("render SDL for %q: missing input value definition", e.Coordinate)
		}
	case "ENUM_VALUE":
		if e.EnumValue == nil {
			return "", fmt.Errorf("render SDL for %q: missing enum value definition", e.Coordinate)
		}
		enumValue(&b, e.EnumValue, 0, includeDescription)
	default:
		return "", fmt.Errorf("render SDL for %q: unsupported element kind %q", e.Coordinate, e.Kind)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func definition(b *strings.Builder, def *ast.Definition, includeDescription bool) error {
	if includeDescription {
		writeDescription(b, def.Description, 0)
	}
	switch def.Kind {
	case ast.Scalar:
		b.WriteString("scalar ")
	case ast.Object:
		b.WriteString("type ")
	case ast.Interface:
		b.WriteString("interface ")
	case ast.Union:
		b.WriteString("union ")
	case ast.Enum:
		b.WriteString("enum ")
	case ast.InputObject:
		b.WriteString("input ")
	default:
		return fmt.Errorf("unsupported type kind %q", def.Kind)
	}
	b.WriteString(def.Name)
	if len(def.Interfaces) != 0 {
		b.WriteString(" implements ")
		b.WriteString(strings.Join(def.Interfaces, " & "))
	}
	writeDirectives(b, def.Directives)
	if len(def.Types) != 0 {
		b.WriteString(" = ")
		b.WriteString(strings.Join(def.Types, " | "))
	}
	hasMembers := len(def.EnumValues) != 0
	for _, member := range def.Fields {
		if !strings.HasPrefix(member.Name, "__") {
			hasMembers = true
			break
		}
	}
	if !hasMembers {
		b.WriteByte('\n')
		return nil
	}
	b.WriteString(" {\n")
	for _, member := range def.Fields {
		if strings.HasPrefix(member.Name, "__") {
			continue
		}
		if err := field(b, member, 1, includeDescription); err != nil {
			return fmt.Errorf("field %q: %w", member.Name, err)
		}
	}
	for _, member := range def.EnumValues {
		enumValue(b, member, 1, includeDescription)
	}
	b.WriteString("}\n")
	return nil
}

func field(b *strings.Builder, f *ast.FieldDefinition, depth int, includeDescription bool) error {
	if f.Type == nil {
		return fmt.Errorf("field %q has no type", f.Name)
	}
	if includeDescription {
		writeDescription(b, f.Description, depth)
	}
	indent(b, depth)
	b.WriteString(f.Name)
	if len(f.Arguments) != 0 {
		multiLine := false
		for _, arg := range f.Arguments {
			if includeDescription && arg.Description != "" {
				multiLine = true
				break
			}
		}
		b.WriteByte('(')
		if multiLine {
			b.WriteByte('\n')
		}
		argDepth := 0
		if multiLine {
			argDepth = depth + 1
		}
		for i, arg := range f.Arguments {
			if i != 0 && !multiLine {
				b.WriteString(", ")
			}
			if err := inputValue(b, arg, argDepth, includeDescription); err != nil {
				return err
			}
		}
		if multiLine {
			indent(b, depth)
		}
		b.WriteByte(')')
	}
	b.WriteString(": ")
	b.WriteString(f.Type.String())
	writeDefault(b, f.DefaultValue)
	writeDirectives(b, f.Directives)
	b.WriteByte('\n')
	return nil
}

func inputValue(b *strings.Builder, arg *ast.ArgumentDefinition, depth int, includeDescription bool) error {
	if arg.Type == nil {
		return fmt.Errorf("input value %q has no type", arg.Name)
	}
	if includeDescription {
		writeDescription(b, arg.Description, depth)
	}
	indent(b, depth)
	b.WriteString(arg.Name)
	b.WriteString(": ")
	b.WriteString(arg.Type.String())
	writeDefault(b, arg.DefaultValue)
	writeDirectives(b, arg.Directives)
	if depth != 0 {
		b.WriteByte('\n')
	}
	return nil
}

func enumValue(b *strings.Builder, value *ast.EnumValueDefinition, depth int, includeDescription bool) {
	if includeDescription {
		writeDescription(b, value.Description, depth)
	}
	indent(b, depth)
	b.WriteString(value.Name)
	writeDirectives(b, value.Directives)
	b.WriteByte('\n')
}

func writeDefault(b *strings.Builder, value *ast.Value) {
	if value != nil {
		b.WriteString(" = ")
		writeValue(b, value)
	}
}

// gqlparser's Value.String uses Go's \\xNN escapes for some string controls;
// GraphQL only accepts JSON-style \\uNNNN escapes in string literals.
func writeValue(b *strings.Builder, value *ast.Value) {
	switch value.Kind {
	case ast.StringValue, ast.BlockValue:
		writeString(b, value.Raw)
	case ast.ListValue:
		b.WriteByte('[')
		for i, child := range value.Children {
			if i != 0 {
				b.WriteString(", ")
			}
			writeValue(b, child.Value)
		}
		b.WriteByte(']')
	case ast.ObjectValue:
		b.WriteByte('{')
		for i, child := range value.Children {
			if i != 0 {
				b.WriteString(", ")
			}
			b.WriteString(child.Name)
			b.WriteString(": ")
			writeValue(b, child.Value)
		}
		b.WriteByte('}')
	default:
		b.WriteString(value.String())
	}
}

func writeDirectives(b *strings.Builder, directives ast.DirectiveList) {
	for _, directive := range directives {
		b.WriteString(" @")
		b.WriteString(directive.Name)
		if len(directive.Arguments) != 0 {
			b.WriteByte('(')
			for i, arg := range directive.Arguments {
				if i != 0 {
					b.WriteString(", ")
				}
				b.WriteString(arg.Name)
				b.WriteString(": ")
				writeValue(b, arg.Value)
			}
			b.WriteByte(')')
		}
	}
}

func writeDescription(b *strings.Builder, description string, depth int) {
	if description == "" {
		return
	}
	indent(b, depth)
	writeString(b, description)
	b.WriteByte('\n')
}

func writeString(b *strings.Builder, value string) {
	quoted, _ := json.Marshal(value) // A Go string always marshals successfully.
	b.Write(quoted)
}

func indent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
}
