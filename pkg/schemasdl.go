package gqlcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// builtinDirectives are supplied by gqlparser's prelude. Emitting them again is
// harmless for these six names but pointless, and every other duplicate
// directive is a hard error, so the emitter skips them.
var builtinDirectives = map[string]bool{
	"skip":        true,
	"include":     true,
	"deprecated":  true,
	"specifiedBy": true,
	"defer":       true,
	"oneOf":       true,
}

// SchemaSDL renders an introspection result as an SDL document.
//
// The output is meant to be fed back to gqlparser: builtin scalars and
// directives are left out because the prelude already declares them, and
// introspection's own __-prefixed types are skipped because names beginning
// with __ are reserved. Unlike the compact SDL that describe emits for humans,
// this output is complete enough to rebuild a schema from.
//
// It accepts either a full response envelope ({"data":{"__schema":...}}, what
// Client.Introspect returns) or a bare __schema map.
func SchemaSDL(introspection map[string]interface{}) (string, error) {
	root, err := introspectionSchemaRoot(introspection)
	if err != nil {
		return "", err
	}

	typesList := introspectionList(root["types"])
	if len(typesList) == 0 {
		return "", fmt.Errorf("introspection result contains no types")
	}

	defs := make([]map[string]interface{}, 0, len(typesList))
	for _, t := range typesList {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		if name == "" || strings.HasPrefix(name, "__") || isBuiltInScalar(name) {
			continue
		}
		defs = append(defs, tm)
	}
	if len(defs) == 0 {
		return "", fmt.Errorf("introspection result contains no user-defined types")
	}
	sort.Slice(defs, func(i, j int) bool {
		ni, _ := defs[i]["name"].(string)
		nj, _ := defs[j]["name"].(string)
		return ni < nj
	})

	var b strings.Builder
	writeSchemaBlock(&b, root)
	for _, tm := range defs {
		if err := writeTypeSDL(&b, tm); err != nil {
			return "", err
		}
	}

	dirs := introspectionList(root["directives"])
	sorted := make([]map[string]interface{}, 0, len(dirs))
	for _, d := range dirs {
		if dm, ok := d.(map[string]interface{}); ok {
			sorted = append(sorted, dm)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		ni, _ := sorted[i]["name"].(string)
		nj, _ := sorted[j]["name"].(string)
		return ni < nj
	})
	for _, dm := range sorted {
		if err := writeDirectiveSDL(&b, dm); err != nil {
			return "", err
		}
	}

	return b.String(), nil
}

// SchemaFromIntrospection rebuilds a schema from an introspection result so
// documents can be validated against it without contacting the server again.
func SchemaFromIntrospection(introspection map[string]interface{}) (*ast.Schema, error) {
	sdl, err := SchemaSDL(introspection)
	if err != nil {
		return nil, err
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "introspection.graphql", Input: sdl})
	if err != nil {
		return nil, fmt.Errorf("rebuild schema from introspection: %w", err)
	}
	return schema, nil
}

// introspectionSchemaRoot digs the __schema object out of a response envelope.
//
// An endpoint with introspection disabled answers with an errors array and no
// data, and HTTPClient only turns that into a Go error when Strict is set, so a
// nil error from Introspect is not by itself proof of success.
func introspectionSchemaRoot(introspection map[string]interface{}) (map[string]interface{}, error) {
	if len(introspection) == 0 {
		return nil, fmt.Errorf("introspection result is empty")
	}
	if errs := introspectionList(introspection["errors"]); len(errs) > 0 {
		return nil, fmt.Errorf("introspection failed: %s", introspectionErrorSummary(errs))
	}
	if data, ok := introspection["data"].(map[string]interface{}); ok {
		if s, ok := data["__schema"].(map[string]interface{}); ok {
			return s, nil
		}
	}
	if s, ok := introspection["__schema"].(map[string]interface{}); ok {
		return s, nil
	}
	if _, ok := introspection["types"]; ok {
		return introspection, nil
	}
	return nil, fmt.Errorf("introspection result has no __schema (is introspection disabled on this endpoint?)")
}

func introspectionErrorSummary(errs []interface{}) string {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if msg, _ := em["message"].(string); msg != "" {
			msgs = append(msgs, msg)
		}
	}
	if len(msgs) == 0 {
		return "endpoint returned errors without messages"
	}
	return strings.Join(msgs, "; ")
}

// writeSchemaBlock always emits the root operation block rather than relying on
// the default Query/Mutation/Subscription names, which a schema is free to
// reassign to differently named types.
func writeSchemaBlock(b *strings.Builder, root map[string]interface{}) {
	roots := []struct{ field, operation string }{
		{"queryType", "query"},
		{"mutationType", "mutation"},
		{"subscriptionType", "subscription"},
	}
	var lines []string
	for _, r := range roots {
		m, ok := root[r.field].(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := m["name"].(string); name != "" {
			lines = append(lines, fmt.Sprintf("  %s: %s\n", r.operation, name))
		}
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("schema {\n")
	for _, l := range lines {
		b.WriteString(l)
	}
	b.WriteString("}\n\n")
}

func writeTypeSDL(b *strings.Builder, t map[string]interface{}) error {
	name, _ := t["name"].(string)
	kind, _ := t["kind"].(string)
	desc, _ := t["description"].(string)

	switch kind {
	case "SCALAR":
		writeDescription(b, desc, "")
		fmt.Fprintf(b, "scalar %s\n\n", name)

	case "ENUM":
		writeDescription(b, desc, "")
		values := introspectionList(t["enumValues"])
		if len(values) == 0 {
			fmt.Fprintf(b, "enum %s\n\n", name)
			return nil
		}
		fmt.Fprintf(b, "enum %s {\n", name)
		for _, v := range values {
			vm, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			vn, _ := vm["name"].(string)
			if vn == "" {
				continue
			}
			vd, _ := vm["description"].(string)
			writeDescription(b, vd, "  ")
			fmt.Fprintf(b, "  %s%s\n", vn, deprecatedSuffix(vm))
		}
		b.WriteString("}\n\n")

	case "UNION":
		writeDescription(b, desc, "")
		var members []string
		for _, p := range introspectionList(t["possibleTypes"]) {
			if pn := baseTypeName(p); pn != "" {
				members = append(members, pn)
			}
		}
		if len(members) == 0 {
			fmt.Fprintf(b, "union %s\n\n", name)
			return nil
		}
		fmt.Fprintf(b, "union %s = %s\n\n", name, strings.Join(members, " | "))

	case "OBJECT", "INTERFACE":
		keyword := "type"
		if kind == "INTERFACE" {
			keyword = "interface"
		}
		header := keyword + " " + name
		if impl := implementedInterfaceNames(t); len(impl) > 0 {
			header += " implements " + strings.Join(impl, " & ")
		}
		writeDescription(b, desc, "")
		return writeMemberBlock(b, header, introspectionList(t["fields"]))

	case "INPUT_OBJECT":
		writeDescription(b, desc, "")
		return writeMemberBlock(b, "input "+name, introspectionList(t["inputFields"]))

	default:
		return fmt.Errorf("type %q has unsupported kind %q", name, kind)
	}
	return nil
}

// writeMemberBlock renders fields or input fields. A braced block with nothing
// in it is a syntax error, so a memberless type is emitted bare.
func writeMemberBlock(b *strings.Builder, header string, members []interface{}) error {
	var body strings.Builder
	for _, m := range members {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if err := writeMemberLine(&body, mm); err != nil {
			return fmt.Errorf("%s: %w", header, err)
		}
	}
	if body.Len() == 0 {
		fmt.Fprintf(b, "%s\n\n", header)
		return nil
	}
	fmt.Fprintf(b, "%s {\n", header)
	b.WriteString(body.String())
	b.WriteString("}\n\n")
	return nil
}

// writeMemberLine handles both output fields and input fields: output fields
// carry args and deprecation, input fields carry a default value, and the keys
// each one lacks are simply absent.
func writeMemberLine(b *strings.Builder, f map[string]interface{}) error {
	name, _ := f["name"].(string)
	if name == "" {
		return nil
	}
	typeRef, err := sdlTypeRef(f["type"])
	if err != nil {
		return fmt.Errorf("field %q: %w", name, err)
	}
	args, err := sdlArgList(introspectionList(f["args"]))
	if err != nil {
		return fmt.Errorf("field %q: %w", name, err)
	}
	desc, _ := f["description"].(string)
	writeDescription(b, desc, "  ")
	fmt.Fprintf(b, "  %s%s: %s%s%s\n", name, args, typeRef, sdlDefaultValue(f), deprecatedSuffix(f))
	return nil
}

// sdlArgList renders an argument list. Default values arrive from introspection
// already encoded as GraphQL literals, so they are emitted verbatim; dropping
// them would make a defaulted non-null argument look required.
func sdlArgList(args []interface{}) (string, error) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		am, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := am["name"].(string)
		if name == "" {
			continue
		}
		typeRef, err := sdlTypeRef(am["type"])
		if err != nil {
			return "", fmt.Errorf("argument %q: %w", name, err)
		}
		parts = append(parts, name+": "+typeRef+sdlDefaultValue(am))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, ", ") + ")", nil
}

func sdlDefaultValue(m map[string]interface{}) string {
	if dv, ok := m["defaultValue"].(string); ok && dv != "" {
		return " = " + dv
	}
	return ""
}

func sdlTypeRef(typeData interface{}) (string, error) {
	tm, ok := typeData.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("missing type reference")
	}
	kind, _ := tm["kind"].(string)
	switch kind {
	case "NON_NULL", "LIST":
		of, ok := tm["ofType"].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("%s type reference has no inner type: the introspection ofType nesting is too shallow for this type", kind)
		}
		inner, err := sdlTypeRef(of)
		if err != nil {
			return "", err
		}
		if kind == "LIST" {
			return "[" + inner + "]", nil
		}
		return inner + "!", nil
	default:
		name, _ := tm["name"].(string)
		if name == "" {
			return "", fmt.Errorf("unnamed %s type reference: the introspection ofType nesting is too shallow for this type", kind)
		}
		return name, nil
	}
}

func implementedInterfaceNames(t map[string]interface{}) []string {
	var names []string
	for _, i := range introspectionList(t["interfaces"]) {
		if n := baseTypeName(i); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func deprecatedSuffix(m map[string]interface{}) string {
	if dep, _ := m["isDeprecated"].(bool); !dep {
		return ""
	}
	reason, _ := m["deprecationReason"].(string)
	if reason == "" {
		return " @deprecated"
	}
	return " @deprecated(reason: " + graphqlString(reason) + ")"
}

func writeDirectiveSDL(b *strings.Builder, d map[string]interface{}) error {
	name, _ := d["name"].(string)
	if name == "" || builtinDirectives[name] {
		return nil
	}
	var locations []string
	for _, l := range introspectionList(d["locations"]) {
		if ls, ok := l.(string); ok && ls != "" {
			locations = append(locations, ls)
		}
	}
	if len(locations) == 0 {
		return nil
	}
	args, err := sdlArgList(introspectionList(d["args"]))
	if err != nil {
		return fmt.Errorf("directive %q: %w", name, err)
	}
	repeatable := ""
	if r, _ := d["isRepeatable"].(bool); r {
		repeatable = " repeatable"
	}
	desc, _ := d["description"].(string)
	writeDescription(b, desc, "")
	fmt.Fprintf(b, "directive @%s%s%s on %s\n\n", name, args, repeatable, strings.Join(locations, " | "))
	return nil
}

// writeDescription emits a block string. Inside one the only escape is \""", and
// a description ending in a quote or backslash would otherwise run into the
// closing delimiter, so those get a newline in between.
func writeDescription(b *strings.Builder, desc, indent string) {
	if strings.TrimSpace(desc) == "" {
		return
	}
	d := strings.ReplaceAll(desc, `"""`, `\"""`)
	if strings.HasSuffix(d, `"`) || strings.HasSuffix(d, `\`) {
		d += "\n"
	}
	if !strings.Contains(d, "\n") {
		fmt.Fprintf(b, "%s\"\"\"%s\"\"\"\n", indent, d)
		return
	}
	fmt.Fprintf(b, "%s\"\"\"\n", indent)
	for _, line := range strings.Split(d, "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(b, "%s%s\n", indent, line)
	}
	fmt.Fprintf(b, "%s\"\"\"\n", indent)
}

// graphqlString quotes a Go string as a GraphQL string literal. JSON's escaping
// rules are a subset of GraphQL's, so the encoder's output is always valid here.
func graphqlString(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func introspectionList(v interface{}) []interface{} {
	list, _ := v.([]interface{})
	return list
}
