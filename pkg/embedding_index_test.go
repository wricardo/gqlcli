package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeEmbedder returns a deterministic vector derived from the text, so tests
// can assert ranking without a network call.
type fakeEmbedder struct {
	model string
	calls int32
	vec   func(text string) []float32
}

func (f *fakeEmbedder) Model() string { return f.model }

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.vec != nil {
		return f.vec(text), nil
	}
	// Two dimensions: presence of "user" and presence of "invoice".
	var v []float32
	lower := strings.ToLower(text)
	v = append(v, float32(strings.Count(lower, "user")))
	v = append(v, float32(strings.Count(lower, "invoice")))
	if v[0] == 0 && v[1] == 0 {
		v[0] = 0.01
	}
	return v, nil
}

func testIntrospection(types ...map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, 0, len(types))
	for _, t := range types {
		list = append(list, t)
	}
	return map[string]interface{}{
		"data": map[string]interface{}{
			"__schema": map[string]interface{}{"types": list},
		},
	}
}

func objectType(name, description string, fields ...string) map[string]interface{} {
	fieldList := make([]interface{}, 0, len(fields))
	for _, f := range fields {
		fieldList = append(fieldList, map[string]interface{}{
			"name": f,
			"type": map[string]interface{}{"kind": "SCALAR", "name": "String"},
		})
	}
	return map[string]interface{}{
		"kind":        "OBJECT",
		"name":        name,
		"description": description,
		"fields":      fieldList,
	}
}

func TestBuildEmbeddingIndexRanksClosestTypes(t *testing.T) {
	intro := testIntrospection(
		objectType("User", "a user account", "email", "name"),
		objectType("Invoice", "an invoice for a customer", "total", "dueDate"),
		map[string]interface{}{"kind": "OBJECT", "name": "__Schema", "fields": []interface{}{}},
	)

	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if len(ix.Types) != 2 {
		t.Fatalf("expected 2 types (introspection types skipped), got %d: %+v", len(ix.Types), ix.Types)
	}
	if ix.Model != "fake-1" {
		t.Errorf("model = %q, want fake-1", ix.Model)
	}
	if ix.Dim != 2 {
		t.Errorf("dim = %d, want 2", ix.Dim)
	}

	ix.SetEmbedder(e)
	results, err := ix.Search(context.Background(), "invoice", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	matches := results.Types
	if len(matches) != 1 || matches[0].Name != "Invoice" {
		t.Fatalf("expected Invoice first, got %+v", matches)
	}
	if matches[0].Score <= 0.9 {
		t.Errorf("score = %v, want near 1", matches[0].Score)
	}
	if !strings.Contains(matches[0].SDL, "total") {
		t.Errorf("SDL should carry the type body, got %q", matches[0].SDL)
	}
}

func TestSearchTopNAndOrdering(t *testing.T) {
	intro := testIntrospection(
		objectType("User", "user", "id"),
		objectType("UserInvoice", "user invoice", "id"),
		objectType("Invoice", "invoice", "id"),
	)
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ix.SetEmbedder(e)

	results, err := ix.Search(context.Background(), "user user user", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	matches := results.Types
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Name != "User" {
		t.Errorf("first match = %s, want User", matches[0].Name)
	}
	if matches[0].Score < matches[1].Score {
		t.Errorf("matches not sorted descending: %v", matches)
	}
}

func TestSearchDefaultsToFiveAndRejectsModelMismatch(t *testing.T) {
	var types []map[string]interface{}
	for i := 0; i < 8; i++ {
		types = append(types, objectType(fmt.Sprintf("T%d", i), "user thing", "id"))
	}
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), testIntrospection(types...), e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ix.SetEmbedder(e)
	results, err := ix.Search(context.Background(), "user", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results.Types) != 5 {
		t.Errorf("topN 0 should default to 5, got %d", len(results.Types))
	}

	ix.SetEmbedder(&fakeEmbedder{model: "other-model"})
	if _, err := ix.Search(context.Background(), "user", 3); err == nil {
		t.Fatal("expected model mismatch error")
	}

	ix.embedder = nil
	if _, err := ix.Search(context.Background(), "user", 3); err == nil {
		t.Fatal("expected error when no embedder is attached")
	}
}

func TestSearchVectorDimensionMismatch(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), testIntrospection(objectType("User", "user", "id")), e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := ix.SearchVector([]float32{1, 2, 3}, 5); err == nil {
		t.Fatal("expected dimension mismatch error")
	}
	if _, err := ix.SearchVector(nil, 5); err == nil {
		t.Fatal("expected error for empty query vector")
	}
}

func TestBuildEmbeddingIndexFilters(t *testing.T) {
	intro := testIntrospection(
		objectType("User", "user", "id"),
		objectType("UserConnection", "page of users", "edges"),
		map[string]interface{}{"kind": "ENUM", "name": "Status", "enumValues": []interface{}{
			map[string]interface{}{"name": "ACTIVE"},
		}},
	)

	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{
		Kinds:   []string{"object"},
		Exclude: []string{"*Connection"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(ix.Types) != 1 || ix.Types[0].Name != "User" {
		t.Fatalf("expected only User, got %+v", ix.Types)
	}

	if _, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{Include: []string{"Nope*"}}); err == nil {
		t.Fatal("expected error when no types match")
	}
	if _, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{Include: []string{"["}}); err == nil {
		t.Fatal("expected error for invalid glob")
	}
}

func TestRebuildReusesUnchangedVectors(t *testing.T) {
	intro := testIntrospection(
		objectType("User", "user", "id"),
		objectType("Invoice", "invoice", "id"),
	)
	e := &fakeEmbedder{model: "fake-1"}
	first, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	afterFirst := atomic.LoadInt32(&e.calls)
	if afterFirst != 2 {
		t.Fatalf("expected 2 embed calls, got %d", afterFirst)
	}

	// One type gains a field: only that one is re-embedded.
	changed := testIntrospection(
		objectType("User", "user", "id"),
		objectType("Invoice", "invoice", "id", "total"),
	)
	second, err := BuildEmbeddingIndex(context.Background(), changed, e, EmbeddingIndexOptions{Previous: first})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := atomic.LoadInt32(&e.calls) - afterFirst; got != 1 {
		t.Errorf("expected 1 new embed call, got %d", got)
	}
	if len(second.Types) != 2 {
		t.Errorf("expected 2 types, got %d", len(second.Types))
	}

	// Force ignores the cache entirely.
	before := atomic.LoadInt32(&e.calls)
	if _, err := BuildEmbeddingIndex(context.Background(), changed, e, EmbeddingIndexOptions{Previous: second, Force: true}); err != nil {
		t.Fatalf("forced rebuild: %v", err)
	}
	if got := atomic.LoadInt32(&e.calls) - before; got != 2 {
		t.Errorf("force should re-embed both types, got %d calls", got)
	}
}

func TestBuildEmbeddingIndexErrors(t *testing.T) {
	if _, err := BuildEmbeddingIndex(context.Background(), nil, &fakeEmbedder{}, EmbeddingIndexOptions{}); err == nil {
		t.Fatal("expected error for missing introspection data")
	}
	if _, err := BuildEmbeddingIndex(context.Background(), testIntrospection(), nil, EmbeddingIndexOptions{}); err == nil {
		t.Fatal("expected error for nil embedder")
	}
	if _, err := BuildEmbeddingIndexFromClient(context.Background(), nil, &fakeEmbedder{}, EmbeddingIndexOptions{}); err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), testIntrospection(
		objectType("User", "user", "id"),
		objectType("Invoice", "invoice", "id"),
	), e, EmbeddingIndexOptions{Env: "local", Endpoint: "http://localhost:8080/graphql"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	path := filepath.Join(t.TempDir(), "nested", "index.json")
	if err := ix.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadEmbeddingIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Env != "local" || loaded.Endpoint != "http://localhost:8080/graphql" {
		t.Errorf("env/endpoint not persisted: %+v", loaded)
	}
	if loaded.Dim != ix.Dim || len(loaded.Types) != len(ix.Types) {
		t.Errorf("round trip mismatch: %+v", loaded)
	}

	loaded.SetEmbedder(e)
	results, err := loaded.Search(context.Background(), "invoice", 1)
	if err != nil {
		t.Fatalf("search loaded index: %v", err)
	}
	if results.Types[0].Name != "Invoice" {
		t.Errorf("expected Invoice, got %s", results.Types[0].Name)
	}
}

func TestLoadEmbeddingIndexRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadEmbeddingIndex(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}

	bad := filepath.Join(dir, "bad.json")
	if err := writeFileString(bad, "not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEmbeddingIndex(bad); err == nil {
		t.Fatal("expected parse error")
	}

	empty := filepath.Join(dir, "empty.json")
	if err := writeFileString(empty, `{"version":1,"types":[]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEmbeddingIndex(empty); err == nil {
		t.Fatal("expected error for index with no types")
	}

	future := filepath.Join(dir, "future.json")
	raw, _ := json.Marshal(map[string]interface{}{
		"version": EmbeddingIndexVersion + 1,
		"types":   []TypeEmbedding{{Name: "User", Embedding: []float32{1}}},
	})
	if err := writeFileString(future, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEmbeddingIndex(future); err == nil {
		t.Fatal("expected error for future index version")
	}
}

func TestLoadNormalizesVectors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	raw, _ := json.Marshal(EmbeddingIndex{
		Version: 1,
		Model:   "fake-1",
		Types: []TypeEmbedding{
			{Name: "A", Embedding: []float32{3, 4}},
			{Name: "B", Embedding: []float32{0, 0}},
		},
	})
	if err := writeFileString(path, string(raw)); err != nil {
		t.Fatal(err)
	}

	ix, err := LoadEmbeddingIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ix.Dim != 2 {
		t.Errorf("dim should be inferred, got %d", ix.Dim)
	}
	if got := math.Hypot(float64(ix.Types[0].Embedding[0]), float64(ix.Types[0].Embedding[1])); math.Abs(got-1) > 1e-6 {
		t.Errorf("vector not normalized: norm = %v", got)
	}

	// A zero vector stays zero and scores 0 rather than producing NaN.
	results, err := ix.SearchVector([]float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, m := range results.Types {
		if math.IsNaN(m.Score) {
			t.Fatalf("NaN score for %s", m.Name)
		}
	}
}

func TestEmbeddingIndexPath(t *testing.T) {
	if got := EmbeddingIndexPath(""); got != ".gqlcli-embeddings.json" {
		t.Errorf("default path = %q", got)
	}
	if got := EmbeddingIndexPath("prod"); got != ".gqlcli-embeddings.prod.json" {
		t.Errorf("prod path = %q", got)
	}
	if got := EmbeddingIndexPath("  "); got != ".gqlcli-embeddings.json" {
		t.Errorf("blank env path = %q", got)
	}
}

func TestBuildEmbedTextTruncatesAfterHeader(t *testing.T) {
	text := buildEmbedText("object type", "User", "a user", strings.Repeat("x", 500), 60)
	if len(text) != 60 {
		t.Fatalf("expected truncation to 60 chars, got %d", len(text))
	}
	if !strings.HasPrefix(text, "GraphQL object type: User") {
		t.Errorf("header should survive truncation, got %q", text)
	}
}

func TestBuildEmbeddingIndexPropagatesEmbedError(t *testing.T) {
	failing := &erroringEmbedder{}
	_, err := BuildEmbeddingIndex(context.Background(), testIntrospection(
		objectType("User", "user", "id"),
	), failing, EmbeddingIndexOptions{Concurrency: 2})
	if err == nil || !strings.Contains(err.Error(), "embedding type User") {
		t.Fatalf("expected wrapped embed error, got %v", err)
	}
}

type erroringEmbedder struct{}

func (e *erroringEmbedder) Model() string { return "fake-1" }
func (e *erroringEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, fmt.Errorf("boom")
}

// TestBuildEmbeddingIndexFromClientUsesIntrospection checks the client path
// end to end against a stub GraphQL server.
func TestBuildEmbeddingIndexFromClientUsesIntrospection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(testIntrospection(objectType("User", "user", "id")))
	}))
	defer srv.Close()

	client := NewHTTPClient(&Config{URL: srv.URL})
	ix, err := BuildEmbeddingIndexFromClient(context.Background(), client, &fakeEmbedder{model: "fake-1"}, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build from client: %v", err)
	}
	if len(ix.Types) != 1 || ix.Types[0].Name != "User" {
		t.Fatalf("unexpected types: %+v", ix.Types)
	}
}

// queryAwareEmbedder implements QueryEmbedder so Search must take the query path.
type queryAwareEmbedder struct {
	fakeEmbedder
	queryCalls int
}

func (q *queryAwareEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	q.queryCalls++
	return q.Embed(ctx, text)
}

func TestSearchUsesEmbedQueryWhenAvailable(t *testing.T) {
	e := &queryAwareEmbedder{fakeEmbedder: fakeEmbedder{model: "fake-1"}}
	ix, err := BuildEmbeddingIndex(context.Background(), testIntrospection(objectType("User", "user", "id")), e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ix.SetEmbedder(e)
	if _, err := ix.Search(context.Background(), "user", 1); err != nil {
		t.Fatalf("search: %v", err)
	}
	if e.queryCalls != 1 {
		t.Errorf("EmbedQuery calls = %d, want 1", e.queryCalls)
	}
	if ix.Embedder() == nil {
		t.Error("Embedder() should return the attached embedder")
	}
}

// rootType builds a Query/Mutation root object whose fields carry args, the
// way FullIntrospectionQuery returns them.
func rootType(name string, fields ...map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, 0, len(fields))
	for _, f := range fields {
		list = append(list, f)
	}
	return map[string]interface{}{"kind": "OBJECT", "name": name, "fields": list}
}

func rootField(name, description, returnType string, args ...string) map[string]interface{} {
	argList := make([]interface{}, 0, len(args))
	for _, a := range args {
		argList = append(argList, map[string]interface{}{
			"name": a,
			"type": map[string]interface{}{"kind": "SCALAR", "name": "String"},
		})
	}
	return map[string]interface{}{
		"name":        name,
		"description": description,
		"args":        argList,
		"type":        map[string]interface{}{"kind": "OBJECT", "name": returnType},
	}
}

func categorizedIntrospection() map[string]interface{} {
	intro := testIntrospection(
		objectType("User", "a user account", "email"),
		objectType("Invoice", "an invoice", "total"),
		rootType("Query",
			rootField("user", "fetch a user by id", "User", "id"),
			rootField("invoices", "list invoices", "Invoice"),
		),
		rootType("Mutation",
			rootField("createUser", "create a user account", "User", "email"),
		),
	)
	schema := intro["data"].(map[string]interface{})["__schema"].(map[string]interface{})
	schema["queryType"] = map[string]interface{}{"name": "Query"}
	schema["mutationType"] = map[string]interface{}{"name": "Mutation"}
	return intro
}

func TestBuildEmbeddingIndexSplitsCategories(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), categorizedIntrospection(), e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if len(ix.Queries) != 2 {
		t.Errorf("queries = %d, want 2: %+v", len(ix.Queries), ix.Queries)
	}
	if len(ix.Mutations) != 1 || ix.Mutations[0].Name != "createUser" {
		t.Errorf("mutations = %+v", ix.Mutations)
	}
	// The root types themselves stay in the types category too.
	if len(ix.Types) != 4 {
		t.Errorf("types = %d, want 4: %+v", len(ix.Types), typeNames(ix))
	}

	var createUser OperationEmbedding
	for _, m := range ix.Mutations {
		if m.Name == "createUser" {
			createUser = m
		}
	}
	if createUser.ReturnType != "User" {
		t.Errorf("return type = %q, want User", createUser.ReturnType)
	}
	if createUser.SDL != "createUser(email: String): User" {
		t.Errorf("operation SDL = %q", createUser.SDL)
	}
	if createUser.Description != "create a user account" {
		t.Errorf("description = %q", createUser.Description)
	}
}

func typeNames(ix *EmbeddingIndex) []string {
	var names []string
	for _, t := range ix.Types {
		names = append(names, t.Name)
	}
	return names
}

func TestSearchRanksEachCategoryIndependently(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), categorizedIntrospection(), e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ix.SetEmbedder(e)

	results, err := ix.Search(context.Background(), "invoice", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// topN applies per category, so one hit from each populated category.
	if len(results.Queries) != 1 || results.Queries[0].Name != "invoices" {
		t.Errorf("queries = %+v, want invoices", results.Queries)
	}
	if len(results.Types) != 1 || results.Types[0].Name != "Invoice" {
		t.Errorf("types = %+v, want Invoice", results.Types)
	}
	if len(results.Mutations) != 1 || results.Mutations[0].Name != "createUser" {
		t.Errorf("mutations = %+v", results.Mutations)
	}
}

func TestSkipQueriesAndMutations(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), categorizedIntrospection(), e, EmbeddingIndexOptions{
		SkipQueries:   true,
		SkipMutations: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(ix.Queries) != 0 || len(ix.Mutations) != 0 {
		t.Errorf("expected no operations, got %d queries and %d mutations", len(ix.Queries), len(ix.Mutations))
	}
	if len(ix.Types) == 0 {
		t.Error("types should still be indexed")
	}
}

func TestOperationGlobFiltersApply(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), categorizedIntrospection(), e, EmbeddingIndexOptions{
		Exclude: []string{"invoices", "Invoice"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, q := range ix.Queries {
		if q.Name == "invoices" {
			t.Error("excluded query field was indexed")
		}
	}
	for _, tt := range ix.Types {
		if tt.Name == "Invoice" {
			t.Error("excluded type was indexed")
		}
	}
}

func TestRebuildReusesOperationVectors(t *testing.T) {
	intro := categorizedIntrospection()
	e := &fakeEmbedder{model: "fake-1"}
	first, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	before := atomic.LoadInt32(&e.calls)

	second, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{Previous: first})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := atomic.LoadInt32(&e.calls) - before; got != 0 {
		t.Errorf("unchanged schema should cost 0 embed calls, got %d", got)
	}
	if len(second.Queries) != len(first.Queries) || len(second.Mutations) != len(first.Mutations) {
		t.Error("categories lost on rebuild")
	}
}

func TestSaveLoadRoundTripsCategories(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), categorizedIntrospection(), e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadEmbeddingIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Queries) != len(ix.Queries) || len(loaded.Mutations) != len(ix.Mutations) {
		t.Fatalf("categories not persisted: %d queries, %d mutations", len(loaded.Queries), len(loaded.Mutations))
	}

	loaded.SetEmbedder(e)
	results, err := loaded.Search(context.Background(), "user", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results.Queries) == 0 || len(results.Mutations) == 0 {
		t.Error("loaded index should still rank operations")
	}
}

// A v1 index written before categories existed has no queries/mutations keys;
// it must still load and search.
func TestLoadIndexWithoutOperationCategories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	raw, _ := json.Marshal(map[string]interface{}{
		"version": 1,
		"model":   "fake-1",
		"dim":     2,
		"types": []TypeEmbedding{
			{Name: "User", Kind: "OBJECT", SDL: "type User", TextHash: "sha256:x", Embedding: []float32{1, 0}},
		},
	})
	if err := writeFileString(path, string(raw)); err != nil {
		t.Fatal(err)
	}

	ix, err := LoadEmbeddingIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ix.SetEmbedder(&fakeEmbedder{model: "fake-1"})
	results, err := ix.Search(context.Background(), "user", 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results.Types) != 1 {
		t.Errorf("types = %+v", results.Types)
	}
	if len(results.Queries) != 0 || len(results.Mutations) != 0 {
		t.Error("legacy index should yield no operation matches")
	}
}

func TestOperationsOnlyIndexLoads(t *testing.T) {
	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), categorizedIntrospection(), e, EmbeddingIndexOptions{
		Include: []string{"user", "createUser"},
		Kinds:   []string{"ENUM"}, // matches no type, so Types ends up empty
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(ix.Types) != 0 {
		t.Fatalf("expected no types, got %+v", typeNames(ix))
	}
	if ix.Dim == 0 {
		t.Error("dim should be inferred from operation vectors")
	}

	path := filepath.Join(t.TempDir(), "ops.json")
	if err := ix.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := LoadEmbeddingIndex(path); err != nil {
		t.Fatalf("an index with only operations must load: %v", err)
	}
}

func TestSchemaWithoutMutationRoot(t *testing.T) {
	intro := testIntrospection(
		objectType("User", "a user", "id"),
		rootType("Query", rootField("user", "fetch a user", "User", "id")),
	)
	schema := intro["data"].(map[string]interface{})["__schema"].(map[string]interface{})
	schema["queryType"] = map[string]interface{}{"name": "Query"}

	e := &fakeEmbedder{model: "fake-1"}
	ix, err := BuildEmbeddingIndex(context.Background(), intro, e, EmbeddingIndexOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(ix.Queries) != 1 {
		t.Errorf("queries = %+v", ix.Queries)
	}
	if len(ix.Mutations) != 0 {
		t.Errorf("mutations should be empty, got %+v", ix.Mutations)
	}
}
