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
    fields { name type { ...TypeRef } args { name type { ...TypeRef } } }
    inputFields { name type { ...TypeRef } }
    enumValues { name }
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
		for _, fm := range sorted {
			b.WriteString(formatSDLField(fm, showArgs))
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

func formatSDLField(field map[string]interface{}, showArgs bool) string {
	var b strings.Builder
	fname, _ := field["name"].(string)
	ftype := formatTypeRef(field["type"])

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
