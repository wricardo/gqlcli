package gqlcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/99designs/gqlgen/graphql/handler"
)

// Describer introspects a schema and returns compact SDL descriptions of types.
// Results are cached after the first introspection call for each type.
//
// Use NewDescriber to create one from an InlineExecutor, or newSchemaHintDescriber
// internally when wiring the schema hint error presenter.
type Describer struct {
	exec  func(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error)
	cache sync.Map
}

// newSchemaHintDescriber creates a Describer backed by the given server.
// Used internally by the schemaHint error presenter.
func newSchemaHintDescriber(srv *handler.Server) *Describer {
	d := &Describer{}
	d.exec = func(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error) {
		body := map[string]interface{}{"query": query}
		if vars != nil {
			body["variables"] = vars
		}
		reqJSON, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/graphql", bytes.NewReader(reqJSON))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		rr := &inlineRecorder{body: &bytes.Buffer{}, header: make(http.Header)}
		srv.ServeHTTP(rr, req)
		return rr.body.Bytes(), nil
	}
	return d
}

// NewDescriber creates a Describer backed by an InlineExecutor.
// Useful for building a describe command or fetching type SDL programmatically.
func NewDescriber(exec *InlineExecutor) *Describer {
	d := &Describer{}
	d.exec = func(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error) {
		return exec.Execute(ctx, query, vars)
	}
	return d
}

// NewDescriberFromHTTPClient creates a Describer that fetches type information
// via introspection against the given HTTP client.
func NewDescriberFromHTTPClient(c *HTTPClient) *Describer {
	d := &Describer{}
	d.exec = func(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error) {
		result, err := c.executeOperation(ctx, query, vars, "")
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	return d
}

// Describe returns a compact SDL string for the named type with default formatting
// (no field argument signatures, no descriptions). Results are cached.
// For custom formatting options use DescribeWith.
func (d *Describer) Describe(ctx context.Context, typeName string) (string, error) {
	typeInfo, err := d.fetch(ctx, typeName)
	if err != nil {
		return "", err
	}
	return FormatTypeSDL(typeInfo, false, true), nil
}

// DescribeWith returns a formatted SDL string for the named type with explicit options.
// showArgs includes field argument signatures; showDescriptions includes doc comments.
// Results from the underlying introspection call are cached.
func (d *Describer) DescribeWith(ctx context.Context, typeName string, showArgs, showDescriptions bool) (string, error) {
	typeInfo, err := d.fetch(ctx, typeName)
	if err != nil {
		return "", err
	}
	return FormatTypeSDL(typeInfo, showArgs, !showDescriptions), nil
}

// DescribeWithDepth returns SDL for typeName and, when depth > 0, recursively includes
// referenced non-scalar types up to the requested depth.
//
// depth behavior:
//   - 0: only the requested type
//   - 1: requested type + directly referenced non-scalar types
//   - N: recurse through non-scalar references N levels deep
func (d *Describer) DescribeWithDepth(ctx context.Context, typeName string, showArgs, showDescriptions bool, depth int) (string, error) {
	if depth < 0 {
		depth = 0
	}

	root, err := d.fetch(ctx, typeName)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	seen := map[string]bool{}
	if err := d.appendTypeSDLRecursive(ctx, &out, root, showArgs, showDescriptions, depth, seen); err != nil {
		return "", err
	}
	return out.String(), nil
}

// DescribeWithFieldFilter returns SDL for typeName with only fields whose names
// contain fieldFilter (case-insensitive). If no fields match, it returns "".
//
// This is used by schema hint enrichment for unknown field errors, so users see
// the closest matching fields first. Callers should fall back to Describe when
// the returned hint is empty.
func (d *Describer) DescribeWithFieldFilter(ctx context.Context, typeName, fieldFilter string) (string, error) {
	if strings.TrimSpace(fieldFilter) == "" {
		return "", nil
	}

	typeInfo, err := d.fetch(ctx, typeName)
	if err != nil {
		return "", err
	}

	fields, ok := typeInfo["fields"].([]interface{})
	if !ok || len(fields) == 0 {
		return "", nil
	}

	filtered := filterOperations(fields, fieldFilter)
	if len(filtered) == 0 {
		return "", nil
	}

	filteredType := map[string]interface{}{
		"name":   typeInfo["name"],
		"kind":   typeInfo["kind"],
		"fields": filtered,
	}

	return "# Closest matches\n" + FormatTypeSDL(filteredType, false, true), nil
}

func (d *Describer) appendTypeSDLRecursive(
	ctx context.Context,
	out *strings.Builder,
	typeInfo map[string]interface{},
	showArgs bool,
	showDescriptions bool,
	depth int,
	seen map[string]bool,
) error {
	name, _ := typeInfo["name"].(string)
	if name == "" || seen[name] {
		return nil
	}
	if strings.HasPrefix(name, "__") {
		return nil
	}
	seen[name] = true

	out.WriteString(FormatTypeSDL(typeInfo, showArgs, !showDescriptions))

	if depth == 0 {
		return nil
	}

	for _, depName := range collectReferencedTypeNames(typeInfo) {
		if depName == "" || seen[depName] || isBuiltInScalar(depName) || strings.HasPrefix(depName, "__") {
			continue
		}
		depInfo, err := d.fetch(ctx, depName)
		if err != nil {
			return err
		}
		if err := d.appendTypeSDLRecursive(ctx, out, depInfo, showArgs, showDescriptions, depth-1, seen); err != nil {
			return err
		}
	}

	return nil
}

func collectReferencedTypeNames(typeData map[string]interface{}) []string {
	names := map[string]struct{}{}
	var out []string

	addTypeRef := func(ref interface{}) {
		name := baseTypeName(ref)
		if name == "" {
			return
		}
		if _, exists := names[name]; exists {
			return
		}
		names[name] = struct{}{}
		out = append(out, name)
	}

	if fields, ok := typeData["fields"].([]interface{}); ok {
		for _, f := range fields {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			addTypeRef(fm["type"])
			if args, ok := fm["args"].([]interface{}); ok {
				for _, a := range args {
					am, ok := a.(map[string]interface{})
					if !ok {
						continue
					}
					addTypeRef(am["type"])
				}
			}
		}
	}

	if inputFields, ok := typeData["inputFields"].([]interface{}); ok {
		for _, f := range inputFields {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			addTypeRef(fm["type"])
		}
	}

	if possibleTypes, ok := typeData["possibleTypes"].([]interface{}); ok {
		for _, p := range possibleTypes {
			addTypeRef(p)
		}
	}

	sort.Strings(out)
	return out
}

func baseTypeName(typeData interface{}) string {
	tm, ok := typeData.(map[string]interface{})
	if !ok {
		return ""
	}
	kind, _ := tm["kind"].(string)
	name, _ := tm["name"].(string)
	switch kind {
	case "NON_NULL", "LIST":
		return baseTypeName(tm["ofType"])
	default:
		return name
	}
}

func isBuiltInScalar(name string) bool {
	switch name {
	case "String", "Int", "Float", "Boolean", "ID":
		return true
	default:
		return false
	}
}

// fetch retrieves and caches the raw introspection data for a type.
func (d *Describer) fetch(ctx context.Context, typeName string) (map[string]interface{}, error) {
	if cached, ok := d.cache.Load(typeName); ok {
		return cached.(map[string]interface{}), nil
	}

	raw, err := d.exec(ctx, buildDescribeQuery(typeName), nil)
	if err != nil {
		return nil, fmt.Errorf("introspection failed: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to parse introspection response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing data in introspection response")
	}

	typeInfo, ok := data["__type"].(map[string]interface{})
	if !ok || typeInfo == nil {
		return nil, fmt.Errorf("type %q not found in schema", typeName)
	}

	d.cache.Store(typeName, typeInfo)
	return typeInfo, nil
}

func buildDescribeQuery(typeName string) string {
	const frag = `fragment TypeRef on __Type {
  kind name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } }
}`
	return fmt.Sprintf(`query {
  __type(name: %q) {
    name kind description
    fields { name description type { ...TypeRef } args { name type { ...TypeRef } } }
    inputFields { name description type { ...TypeRef } }
    enumValues { name }
    possibleTypes { ...TypeRef }
  }
}
%s`, typeName, frag)
}

// FormatTypeSDL returns a compact SDL-like string for a type from introspection data.
// showArgs controls whether field arguments are expanded.
// noDescriptions suppresses inline description comments.
func FormatTypeSDL(typeData map[string]interface{}, showArgs, noDescriptions bool) string {
	var b strings.Builder
	name, _ := typeData["name"].(string)
	description, _ := typeData["description"].(string)
	kind, _ := typeData["kind"].(string)

	if !noDescriptions && description != "" {
		fmt.Fprintf(&b, "# %s\n", description)
	}

	switch kind {
	case "SCALAR":
		fmt.Fprintf(&b, "scalar %s\n", name)
		return b.String()
	case "ENUM":
		vals, _ := typeData["enumValues"].([]interface{})
		parts := make([]string, 0, len(vals))
		for _, v := range vals {
			if vm, ok := v.(map[string]interface{}); ok {
				if n, _ := vm["name"].(string); n != "" {
					parts = append(parts, n)
				}
			}
		}
		fmt.Fprintf(&b, "enum %s { %s }\n", name, strings.Join(parts, " "))
		return b.String()
	}

	fmt.Fprintf(&b, "%s %s {\n", sdlKeyword(kind), name)

	printFields := func(fields []interface{}) {
		sorted := sortFieldsByType(fields)
		i := 0
		for i < len(sorted) {
			fm := sorted[i]
			ftype := formatTypeRef(fm["type"])
			fmArgs, _ := fm["args"].([]interface{})
			fmDesc, _ := fm["description"].(string)
			hasDesc := !noDescriptions && fmDesc != ""

			// Fields with args, or with a description to show, always get their own line
			if hasDesc || (showArgs && len(fmArgs) > 0) {
				b.WriteString(formatSDLField(fm, showArgs, !noDescriptions))
				i++
				continue
			}

			// Collect consecutive fields with the same type
			group := []map[string]interface{}{fm}
			j := i + 1
			for j < len(sorted) {
				next := sorted[j]
				nextArgs, _ := next["args"].([]interface{})
				nextDesc, _ := next["description"].(string)
				if formatTypeRef(next["type"]) != ftype || (showArgs && len(nextArgs) > 0) || (!noDescriptions && nextDesc != "") {
					break
				}
				group = append(group, next)
				j++
			}

			if len(group) == 1 {
				b.WriteString(formatSDLField(fm, showArgs, !noDescriptions))
			} else {
				names := make([]string, len(group))
				for k, g := range group {
					names[k], _ = g["name"].(string)
				}
				fmt.Fprintf(&b, "  %s: %s\n", strings.Join(names, ", "), ftype)
			}
			i = j
		}
	}

	if fields, ok := typeData["fields"].([]interface{}); ok && len(fields) > 0 {
		printFields(fields)
	}
	if inputFields, ok := typeData["inputFields"].([]interface{}); ok && len(inputFields) > 0 {
		printFields(inputFields)
	}

	b.WriteString("}\n")
	return b.String()
}

func sdlKeyword(kind string) string {
	switch kind {
	case "OBJECT":
		return "type"
	case "INPUT_OBJECT":
		return "input"
	case "INTERFACE":
		return "interface"
	case "UNION":
		return "union"
	default:
		return strings.ToLower(kind)
	}
}

func formatTypeRef(typeData interface{}) string {
	tm, ok := typeData.(map[string]interface{})
	if !ok {
		return "Unknown"
	}
	kind, _ := tm["kind"].(string)
	name, _ := tm["name"].(string)
	switch kind {
	case "NON_NULL":
		return formatTypeRef(tm["ofType"]) + "!"
	case "LIST":
		return "[" + formatTypeRef(tm["ofType"]) + "]"
	default:
		if name != "" {
			return name
		}
	}
	return "Unknown"
}

func sortFieldsByType(fields []interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(fields))
	for _, f := range fields {
		if fm, ok := f.(map[string]interface{}); ok {
			out = append(out, fm)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ti := formatTypeRef(out[i]["type"])
		tj := formatTypeRef(out[j]["type"])
		if ti != tj {
			return ti < tj
		}
		ni, _ := out[i]["name"].(string)
		nj, _ := out[j]["name"].(string)
		return ni < nj
	})
	return out
}

// formatTypesAsSDL renders a list of type introspection objects as SDL definitions,
// skipping built-in introspection types (names starting with "__").
// showArgs expands field argument signatures; showDescriptions includes doc comments.
func formatTypesAsSDL(typesList []interface{}, showArgs, showDescriptions bool) string {
	var b strings.Builder
	for _, t := range typesList {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		if strings.HasPrefix(name, "__") {
			continue
		}
		b.WriteString(FormatTypeSDL(tm, showArgs, !showDescriptions))
	}
	return b.String()
}

func formatSDLField(field map[string]interface{}, showArgs, showDescriptions bool) string {
	var b strings.Builder
	fname, _ := field["name"].(string)
	ftype := formatTypeRef(field["type"])

	if showDescriptions {
		if desc, _ := field["description"].(string); desc != "" {
			if strings.Contains(desc, "\n") {
				fmt.Fprintf(&b, "  \"\"\"\n")
				for _, line := range strings.Split(desc, "\n") {
					fmt.Fprintf(&b, "  %s\n", line)
				}
				fmt.Fprintf(&b, "  \"\"\"\n")
			} else {
				fmt.Fprintf(&b, "  # %s\n", desc)
			}
		}
	}

	if showArgs {
		if args, ok := field["args"].([]interface{}); ok && len(args) > 0 {
			argParts := make([]string, 0, len(args))
			for _, a := range args {
				if am, ok := a.(map[string]interface{}); ok {
					aname, _ := am["name"].(string)
					atype := formatTypeRef(am["type"])
					argParts = append(argParts, aname+": "+atype)
				}
			}
			fmt.Fprintf(&b, "  %s(%s): %s\n", fname, strings.Join(argParts, ", "), ftype)
			return b.String()
		}
	}
	fmt.Fprintf(&b, "  %s: %s\n", fname, ftype)
	return b.String()
}
