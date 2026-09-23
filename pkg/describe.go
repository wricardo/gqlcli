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
	exec       func(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error)
	cache      sync.Map
	schemaOnce sync.Once
	schema     schemaIndex
	schemaErr  error

	reverseOnce sync.Once
	reverse     map[string][]string
	reverseErr  error
	distances   sync.Map
}

type schemaIndex struct {
	types            []interface{}
	queryType        string
	mutationType     string
	subscriptionType string
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

// NewDescriberFromExecFunc creates a Describer backed by any function that can
// run a GraphQL operation. Use it to reach Describer — its per-type caching,
// depth recursion and field filtering — from an embedder's own HTTP stack,
// rather than reimplementing introspection around FormatTypeSDL.
//
// exec must return the full GraphQL response envelope (the object with the
// "data" key), not just the data payload.
func NewDescriberFromExecFunc(exec func(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error)) *Describer {
	return &Describer{exec: exec}
}

// DescribeOptions controls DescribeWithOptions.
type DescribeOptions struct {
	// FieldFilter keeps only members whose name contains it, case-insensitively,
	// across fields, input fields and enum values. Empty keeps everything.
	FieldFilter string
	// ShowArgs expands field argument signatures. Worth enabling for the Query
	// and Mutation roots, where the arguments are the call signature.
	ShowArgs bool
	// ShowDescriptions includes doc comments.
	ShowDescriptions bool
	// Depth recursively includes referenced non-scalar types, as DescribeWithDepth.
	Depth int
}

// DescribeWithOptions returns SDL for typeName under opts. It returns an empty
// string when FieldFilter matches nothing, which lets a caller tell "no such
// field" apart from a type that has none.
//
// Filtering happens before formatting, so a type with hundreds of fields can be
// inspected a slice at a time — handing all of them to a model is the context
// blowup that filtering exists to prevent. When Depth is set, recursion follows
// only the surviving fields.
func (d *Describer) DescribeWithOptions(ctx context.Context, typeName string, opts DescribeOptions) (string, error) {
	depth := opts.Depth
	if depth < 0 {
		depth = 0
	}

	typeInfo, err := d.fetch(ctx, typeName)
	if err != nil {
		return "", err
	}

	filtered, matched := filterTypeMembers(typeInfo, opts.FieldFilter)
	if matched == 0 {
		return "", nil
	}

	var out strings.Builder
	seen := map[string]bool{}
	if err := d.appendTypeSDLRecursive(ctx, &out, filtered, opts.ShowArgs, opts.ShowDescriptions, depth, seen); err != nil {
		return "", err
	}
	return out.String(), nil
}

// filterTypeMembers returns a shallow copy of typeInfo keeping only members
// whose name contains filter, and how many survived. A copy is essential: fetch
// hands back the cached map, and filtering it in place would corrupt every
// later lookup of that type.
//
// The count is -1 when no filter was given, so "unfiltered" is distinguishable
// from "filtered down to nothing".
func filterTypeMembers(typeInfo map[string]interface{}, filter string) (map[string]interface{}, int) {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return typeInfo, -1
	}

	out := make(map[string]interface{}, len(typeInfo))
	for k, v := range typeInfo {
		out[k] = v
	}

	matched := 0
	for _, key := range []string{"fields", "inputFields", "enumValues"} {
		entries, ok := typeInfo[key].([]interface{})
		if !ok {
			continue
		}
		kept := make([]interface{}, 0, len(entries))
		for _, entry := range entries {
			member, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := member["name"].(string)
			if strings.Contains(strings.ToLower(name), filter) {
				kept = append(kept, entry)
			}
		}
		out[key] = kept
		matched += len(kept)
	}
	return out, matched
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
//
// When depth >= 1, reverse-reference sections are also appended, capped at 5
// top-level operation references and 5 referencing schema types by default.
func (d *Describer) DescribeWithDepth(ctx context.Context, typeName string, showArgs, showDescriptions bool, depth int) (string, error) {
	return d.DescribeWithDepthLimits(ctx, typeName, showArgs, showDescriptions, depth, 5, 5)
}

// DescribeWithDepthLimits is like DescribeWithDepth, but lets callers override
// the caps for reverse-reference sections. maxOperationRefs and maxFieldRefs use
// 0 to mean unlimited.
func (d *Describer) DescribeWithDepthLimits(ctx context.Context, typeName string, showArgs, showDescriptions bool, depth, maxOperationRefs, maxFieldRefs int) (string, error) {
	if depth < 0 {
		depth = 0
	}
	if maxOperationRefs < 0 {
		maxOperationRefs = 0
	}
	if maxFieldRefs < 0 {
		maxFieldRefs = 0
	}

	if depth >= 1 {
		// The reverse-reference sections need the full schema anyway. Loading
		// it first seeds the per-type cache, so the forward recursion and the
		// reverse lookup never fall back to one __type round trip per type.
		if _, err := d.schemaTypes(ctx); err != nil {
			return "", err
		}
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
	if depth >= 1 {
		if err := d.appendReferencingOperations(ctx, &out, typeName, showDescriptions, depth, maxOperationRefs); err != nil {
			return "", err
		}
		if err := d.appendReferencingFields(ctx, &out, typeName, showDescriptions, depth, maxFieldRefs); err != nil {
			return "", err
		}
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

func (d *Describer) appendReferencingOperations(ctx context.Context, out *strings.Builder, typeName string, showDescriptions bool, depth, maxRefs int) error {
	if strings.HasPrefix(typeName, "__") {
		return nil
	}

	schema, err := d.schemaTypes(ctx)
	if err != nil {
		return err
	}
	if typeName == schema.queryType || typeName == schema.mutationType || typeName == schema.subscriptionType {
		return nil
	}

	var sections []string
	remaining := maxRefs
	totalMatches := 0
	shownMatches := 0
	for _, rootName := range []string{schema.queryType, schema.mutationType} {
		if rootName == "" {
			continue
		}
		root, err := d.fetch(ctx, rootName)
		if err != nil {
			return err
		}
		fields, _ := root["fields"].([]interface{})
		matching, err := d.rankOperationFieldsByReferencedType(ctx, fields, typeName, depth)
		if err != nil {
			return err
		}
		totalMatches += len(matching)
		matching = limitOperationMatches(matching, remaining)
		if len(matching) == 0 {
			continue
		}
		shownMatches += len(matching)
		if remaining > 0 {
			remaining -= len(matching)
		}
		orderedFields := make([]interface{}, 0, len(matching))
		for _, match := range matching {
			orderedFields = append(orderedFields, match.field)
		}
		sections = append(sections, FormatTypeSDL(map[string]interface{}{
			"name":                root["name"],
			"kind":                root["kind"],
			"fields":              orderedFields,
			"_preserveFieldOrder": true,
		}, true, !showDescriptions))
		if remaining == 0 && maxRefs > 0 {
			break
		}
	}

	if len(sections) == 0 {
		return nil
	}

	out.WriteString("\n# Referenced by top-level operations")
	if totalMatches > shownMatches {
		fmt.Fprintf(out, " (showing %d of %d)", shownMatches, totalMatches)
	}
	out.WriteString("\n")
	for _, section := range sections {
		out.WriteString(section)
	}
	return nil
}

func (d *Describer) appendReferencingFields(ctx context.Context, out *strings.Builder, typeName string, showDescriptions bool, depth, maxRefs int) error {
	if strings.HasPrefix(typeName, "__") {
		return nil
	}

	schema, err := d.schemaTypes(ctx)
	if err != nil {
		return err
	}
	rootNames := map[string]bool{}
	for _, name := range []string{schema.queryType, schema.mutationType, schema.subscriptionType} {
		if name != "" {
			rootNames[name] = true
		}
	}

	type namedSection struct {
		name string
		sdl  string
	}
	sections := make([]namedSection, 0)
	totalMatches := 0
	for _, raw := range schema.types {
		tm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		kind, _ := tm["kind"].(string)
		if name == "" || strings.HasPrefix(name, "__") || rootNames[name] {
			continue
		}
		if kind != "OBJECT" && kind != "INTERFACE" {
			continue
		}
		fields, _ := tm["fields"].([]interface{})
		if len(fields) == 0 {
			continue
		}
		matching, err := d.filterOperationFieldsByReferencedType(ctx, fields, typeName, depth)
		if err != nil {
			return err
		}
		if len(matching) == 0 {
			continue
		}
		totalMatches++
		sections = append(sections, namedSection{
			name: name,
			sdl: FormatTypeSDL(map[string]interface{}{
				"name":        name,
				"kind":        kind,
				"description": tm["description"],
				"fields":      matching,
			}, true, !showDescriptions),
		})
	}

	if len(sections) == 0 {
		return nil
	}

	sort.Slice(sections, func(i, j int) bool { return sections[i].name < sections[j].name })
	if maxRefs > 0 && len(sections) > maxRefs {
		sections = sections[:maxRefs]
	}

	out.WriteString("\n# Referenced by fields")
	if totalMatches > len(sections) {
		fmt.Fprintf(out, " (showing %d of %d)", len(sections), totalMatches)
	}
	out.WriteString("\n")
	for _, section := range sections {
		out.WriteString(section.sdl)
	}
	return nil
}

func limitInterfaces(items []interface{}, max int) []interface{} {
	if max <= 0 || len(items) <= max {
		return items
	}
	return items[:max]
}

type operationFieldMatch struct {
	field map[string]interface{}
	score operationMatchScore
}

type operationMatchScore struct {
	locationRank int
	depth        int
	name         string
}

func limitOperationMatches(items []operationFieldMatch, max int) []operationFieldMatch {
	if max <= 0 || len(items) <= max {
		return items
	}
	return items[:max]
}

func (d *Describer) rankOperationFieldsByReferencedType(ctx context.Context, fields []interface{}, targetType string, depth int) ([]operationFieldMatch, error) {
	matched := make([]operationFieldMatch, 0, len(fields))
	for _, field := range fields {
		fm, ok := field.(map[string]interface{})
		if !ok {
			continue
		}
		score, usesType, err := d.operationFieldMatchScore(ctx, fm, targetType, depth)
		if err != nil {
			return nil, err
		}
		if usesType {
			matched = append(matched, operationFieldMatch{field: fm, score: score})
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].score.locationRank != matched[j].score.locationRank {
			return matched[i].score.locationRank < matched[j].score.locationRank
		}
		if matched[i].score.depth != matched[j].score.depth {
			return matched[i].score.depth < matched[j].score.depth
		}
		return matched[i].score.name < matched[j].score.name
	})
	return matched, nil
}

func (d *Describer) filterOperationFieldsByReferencedType(ctx context.Context, fields []interface{}, targetType string, depth int) ([]interface{}, error) {
	matched := make([]interface{}, 0, len(fields))
	for _, field := range fields {
		fm, ok := field.(map[string]interface{})
		if !ok {
			continue
		}
		usesType, err := d.fieldUsesReferencedType(ctx, fm, targetType, depth)
		if err != nil {
			return nil, err
		}
		if usesType {
			matched = append(matched, field)
		}
	}
	return matched, nil
}

func (d *Describer) operationFieldMatchScore(ctx context.Context, field map[string]interface{}, targetType string, depth int) (operationMatchScore, bool, error) {
	name, _ := field["name"].(string)
	bestArgDepth := -1
	if args, ok := field["args"].([]interface{}); ok {
		for _, arg := range args {
			am, ok := arg.(map[string]interface{})
			if !ok {
				continue
			}
			argDepth, ok, err := d.typeRefMatchDepth(ctx, am["type"], targetType, depth)
			if err != nil {
				return operationMatchScore{}, false, err
			}
			if ok && (bestArgDepth == -1 || argDepth < bestArgDepth) {
				bestArgDepth = argDepth
			}
		}
	}
	if bestArgDepth >= 0 {
		return operationMatchScore{locationRank: 0, depth: bestArgDepth, name: name}, true, nil
	}
	returnDepth, ok, err := d.typeRefMatchDepth(ctx, field["type"], targetType, depth)
	if err != nil {
		return operationMatchScore{}, false, err
	}
	if ok {
		return operationMatchScore{locationRank: 1, depth: returnDepth, name: name}, true, nil
	}
	return operationMatchScore{}, false, nil
}

func (d *Describer) fieldUsesReferencedType(ctx context.Context, field map[string]interface{}, targetType string, depth int) (bool, error) {
	_, ok, err := d.operationFieldMatchScore(ctx, field, targetType, depth)
	return ok, err
}

func (d *Describer) typeRefMatchDepth(ctx context.Context, typeRef interface{}, targetType string, depth int) (int, bool, error) {
	typeName := baseTypeName(typeRef)
	if typeName == "" {
		return 0, false, nil
	}
	if typeName == targetType {
		return 0, true, nil
	}
	dist, err := d.targetDistances(ctx, targetType, depth)
	if err != nil {
		return 0, false, err
	}
	hops, ok := dist[typeName]
	return hops, ok, nil
}

// targetDistances maps every type that reaches targetType within depth
// reference hops to its shortest hop count, via breadth-first search over the
// reverse reference graph. Results are memoized per (targetType, depth), so a
// reverse lookup costs one graph walk rather than one per candidate field.
func (d *Describer) targetDistances(ctx context.Context, targetType string, depth int) (map[string]int, error) {
	key := fmt.Sprintf("%s\x00%d", targetType, depth)
	if cached, ok := d.distances.Load(key); ok {
		return cached.(map[string]int), nil
	}
	reverse, err := d.reverseReferences(ctx)
	if err != nil {
		return nil, err
	}
	dist := map[string]int{targetType: 0}
	frontier := []string{targetType}
	for hops := 1; hops <= depth && len(frontier) > 0; hops++ {
		var next []string
		for _, name := range frontier {
			for _, referrer := range reverse[name] {
				if _, done := dist[referrer]; done {
					continue
				}
				dist[referrer] = hops
				next = append(next, referrer)
			}
		}
		frontier = next
	}
	d.distances.Store(key, dist)
	return dist, nil
}

// reverseReferences maps each type name to the schema types whose fields,
// arguments, input fields or possible types reference it directly. Built-in
// scalars and introspection types never act as referrers.
func (d *Describer) reverseReferences(ctx context.Context) (map[string][]string, error) {
	d.reverseOnce.Do(func() {
		schema, err := d.schemaTypes(ctx)
		if err != nil {
			d.reverseErr = err
			return
		}
		reverse := map[string][]string{}
		for _, raw := range schema.types {
			tm, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := tm["name"].(string)
			if name == "" || isBuiltInScalar(name) || strings.HasPrefix(name, "__") {
				continue
			}
			typeInfo, err := d.fetch(ctx, name)
			if err != nil {
				d.reverseErr = err
				return
			}
			for _, dep := range collectReferencedTypeNames(typeInfo) {
				reverse[dep] = append(reverse[dep], name)
			}
		}
		d.reverse = reverse
	})
	return d.reverse, d.reverseErr
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

func (d *Describer) schemaTypes(ctx context.Context) (schemaIndex, error) {
	d.schemaOnce.Do(func() {
		raw, err := d.exec(ctx, FullIntrospectionQuery, nil)
		if err != nil {
			d.schemaErr = fmt.Errorf("introspection failed: %w", err)
			return
		}

		var result map[string]interface{}
		if err := json.Unmarshal(raw, &result); err != nil {
			d.schemaErr = fmt.Errorf("failed to parse introspection response: %w", err)
			return
		}
		data, ok := result["data"].(map[string]interface{})
		if !ok {
			d.schemaErr = fmt.Errorf("missing data in introspection response")
			return
		}
		schema, ok := data["__schema"].(map[string]interface{})
		if !ok {
			d.schemaErr = fmt.Errorf("missing __schema in introspection response")
			return
		}
		types, ok := schema["types"].([]interface{})
		if !ok {
			d.schemaErr = fmt.Errorf("missing types in introspection response")
			return
		}
		d.schema.types = types
		if qt, ok := schema["queryType"].(map[string]interface{}); ok {
			d.schema.queryType, _ = qt["name"].(string)
		}
		if mt, ok := schema["mutationType"].(map[string]interface{}); ok {
			d.schema.mutationType, _ = mt["name"].(string)
		}
		if st, ok := schema["subscriptionType"].(map[string]interface{}); ok {
			d.schema.subscriptionType, _ = st["name"].(string)
		}
		for _, raw := range types {
			tm, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if name, _ := tm["name"].(string); name != "" {
				d.cache.LoadOrStore(name, typeInfoFromFullType(tm))
			}
		}
	})
	return d.schema, d.schemaErr
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

// typeInfoFromFullType projects a type from FullIntrospectionQuery onto the
// shape buildDescribeQuery returns, so a cache entry renders the same whichever
// query filled it. The full query includes deprecated fields and enum values;
// the per-type query does not.
func typeInfoFromFullType(tm map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"name":          tm["name"],
		"kind":          tm["kind"],
		"description":   tm["description"],
		"fields":        nil,
		"inputFields":   nil,
		"enumValues":    nil,
		"possibleTypes": tm["possibleTypes"],
	}
	if fields, ok := tm["fields"].([]interface{}); ok {
		projected := make([]interface{}, 0, len(fields))
		for _, f := range fields {
			fm, ok := f.(map[string]interface{})
			if !ok || isDeprecated(fm) {
				continue
			}
			args := []interface{}{}
			if rawArgs, ok := fm["args"].([]interface{}); ok {
				for _, a := range rawArgs {
					if am, ok := a.(map[string]interface{}); ok {
						args = append(args, map[string]interface{}{"name": am["name"], "type": am["type"]})
					}
				}
			}
			projected = append(projected, map[string]interface{}{
				"name":        fm["name"],
				"description": fm["description"],
				"type":        fm["type"],
				"args":        args,
			})
		}
		out["fields"] = projected
	}
	if inputFields, ok := tm["inputFields"].([]interface{}); ok {
		projected := make([]interface{}, 0, len(inputFields))
		for _, f := range inputFields {
			if fm, ok := f.(map[string]interface{}); ok {
				projected = append(projected, map[string]interface{}{
					"name":        fm["name"],
					"description": fm["description"],
					"type":        fm["type"],
				})
			}
		}
		out["inputFields"] = projected
	}
	if enumValues, ok := tm["enumValues"].([]interface{}); ok {
		projected := make([]interface{}, 0, len(enumValues))
		for _, v := range enumValues {
			if vm, ok := v.(map[string]interface{}); ok && !isDeprecated(vm) {
				projected = append(projected, map[string]interface{}{"name": vm["name"]})
			}
		}
		out["enumValues"] = projected
	}
	return out
}

func isDeprecated(m map[string]interface{}) bool {
	dep, _ := m["isDeprecated"].(bool)
	return dep
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

	preserveFieldOrder, _ := typeData["_preserveFieldOrder"].(bool)

	printFields := func(fields []interface{}) {
		sorted := sortFieldsByType(fields)
		if preserveFieldOrder {
			sorted = sortFieldsPreservingInputOrder(fields)
		}
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

func sortFieldsPreservingInputOrder(fields []interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(fields))
	for _, f := range fields {
		if fm, ok := f.(map[string]interface{}); ok {
			out = append(out, fm)
		}
	}
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
