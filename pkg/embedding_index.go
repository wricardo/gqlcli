package gqlcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EmbeddingIndexVersion is the schema version written to index files. Search
// rejects files from a future version rather than misreading them.
const EmbeddingIndexVersion = 1

// operation buckets, used internally while building an index to keep query
// fields, mutation fields and types in separate reuse/caching namespaces.
type opBucket int

const (
	bucketType opBucket = iota
	bucketQuery
	bucketMutation
)

func (b opBucket) label() string {
	switch b {
	case bucketQuery:
		return "query"
	case bucketMutation:
		return "mutation"
	default:
		return "type"
	}
}

// TypeEmbedding is one GraphQL type in an embedding index: its SDL, the hash of
// the text that was embedded, and the vector.
type TypeEmbedding struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
	// SDL is the compact type definition, ready to paste into an LLM prompt.
	SDL string `json:"sdl"`
	// TextHash is sha256 of the exact text embedded. A rebuild reuses the
	// vector when the hash is unchanged.
	TextHash  string    `json:"text_hash"`
	Embedding []float32 `json:"embedding"`
}

// OperationEmbedding is one field of the Query or Mutation root type: its call
// signature, the hash of the text that was embedded, and the vector.
type OperationEmbedding struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ReturnType is the field's GraphQL return type, e.g. "[User!]!".
	ReturnType string `json:"return_type"`
	// SDL is the field's call signature, e.g. "createUser(input: CreateUserInput!): User!".
	SDL       string    `json:"sdl"`
	TextHash  string    `json:"text_hash"`
	Embedding []float32 `json:"embedding"`
}

// EmbeddingIndex is a searchable set of GraphQL embeddings, persisted as JSON.
// Entries are grouped into three categories: Queries and Mutations (fields of
// the respective root types — the schema's entry points) and Types (every
// other GraphQL type). Build one with BuildEmbeddingIndex, load one with
// LoadEmbeddingIndex.
type EmbeddingIndex struct {
	Version int    `json:"version"`
	Env     string `json:"env,omitempty"`
	// Endpoint is the GraphQL URL the schema was introspected from.
	Endpoint string `json:"endpoint,omitempty"`
	// EmbeddingEndpoint and Model identify the vector space. Comparing a query
	// embedded by a different model against these vectors is meaningless, so
	// Search refuses it.
	EmbeddingEndpoint string    `json:"embedding_endpoint,omitempty"`
	Model             string    `json:"model"`
	Dim               int       `json:"dim"`
	CreatedAt         time.Time `json:"created_at"`
	// Queries and Mutations hold the root-level entry points, kept separate
	// from Types because "find me the operation for X" and "find me the type
	// for X" are different questions with different candidate pools.
	Queries   []OperationEmbedding `json:"queries,omitempty"`
	Mutations []OperationEmbedding `json:"mutations,omitempty"`
	Types     []TypeEmbedding      `json:"types"`

	embedder Embedder
}

// TypeMatch is one type search hit. Score is cosine similarity in [-1, 1];
// higher is closer.
type TypeMatch struct {
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Description string  `json:"description,omitempty"`
	SDL         string  `json:"sdl"`
	Score       float64 `json:"score"`
}

// OperationMatch is one query/mutation field search hit.
type OperationMatch struct {
	Name        string  `json:"name"`
	ReturnType  string  `json:"return_type"`
	Description string  `json:"description,omitempty"`
	SDL         string  `json:"sdl"`
	Score       float64 `json:"score"`
}

// SearchResults groups a search's hits by category, each independently ranked
// and truncated to the requested topN.
type SearchResults struct {
	Queries   []OperationMatch `json:"queries,omitempty"`
	Mutations []OperationMatch `json:"mutations,omitempty"`
	Types     []TypeMatch      `json:"types,omitempty"`
}

// EmbeddingIndexOptions controls BuildEmbeddingIndex.
type EmbeddingIndexOptions struct {
	// Kinds restricts the Types category to these type kinds (OBJECT,
	// INPUT_OBJECT, ENUM, INTERFACE, UNION, SCALAR). Empty indexes every kind.
	// Does not affect the Queries/Mutations categories.
	Kinds []string
	// Include keeps only names (of types, query fields, and mutation fields)
	// matching one of these globs, when non-empty.
	Include []string
	// Exclude drops names matching any of these globs. Applied after Include.
	Exclude []string
	// ShowArgs includes field argument signatures in an indexed type's SDL.
	// Query and mutation field signatures always include their arguments,
	// regardless of this setting — arguments are the call signature.
	ShowArgs bool
	// SkipQueries excludes the Query root's fields from the index.
	SkipQueries bool
	// SkipMutations excludes the Mutation root's fields from the index.
	SkipMutations bool
	// MaxChars truncates the embedded text. Zero uses DefaultEmbeddingMaxChars.
	MaxChars int
	// Concurrency is the number of in-flight embedding requests. Zero uses 4.
	Concurrency int
	// Previous is an existing index whose vectors are reused for entries whose
	// text hash is unchanged, so a rebuild only pays for what moved.
	Previous *EmbeddingIndex
	// Force re-embeds every entry even when Previous has a matching hash.
	Force bool
	// Env and Endpoint are recorded in the index for mismatch warnings.
	Env      string
	Endpoint string
	// Progress, when set, is called as each entry completes. Calls are
	// serialized, so it needs no locking of its own.
	Progress func(done, total int, name string, reused bool)
}

// buildEntry is the unit of work for the embedding worker pool: one type or
// one query/mutation field, tagged with which category it belongs to.
type buildEntry struct {
	bucket      opBucket
	name        string
	returnKey   string // GraphQL kind for a type, return type for an operation field
	description string
	sdl         string
	textHash    string
	embedText   string
	embedding   []float32
}

func (e buildEntry) reuseKey() string {
	return fmt.Sprintf("%d\x00%s\x00%s", e.bucket, e.name, e.textHash)
}

// BuildEmbeddingIndex embeds the types and root-level operations in a GraphQL
// introspection response. introspection is the full envelope returned by
// Client.Introspect (the map with a "data" key).
func BuildEmbeddingIndex(ctx context.Context, introspection map[string]interface{}, e Embedder, opts EmbeddingIndexOptions) (*EmbeddingIndex, error) {
	if e == nil {
		return nil, fmt.Errorf("embedding index: embedder is nil")
	}

	typesList, queryType, mutationType, err := introspectionSchema(introspection)
	if err != nil {
		return nil, err
	}

	var entries []buildEntry
	typeEntries, err := selectIndexTypes(typesList, opts)
	if err != nil {
		return nil, err
	}
	entries = append(entries, typeEntries...)

	if !opts.SkipQueries && queryType != "" {
		queryEntries, err := selectOperationFields(typesList, queryType, bucketQuery, opts)
		if err != nil {
			return nil, err
		}
		entries = append(entries, queryEntries...)
	}
	if !opts.SkipMutations && mutationType != "" {
		mutationEntries, err := selectOperationFields(typesList, mutationType, bucketMutation, opts)
		if err != nil {
			return nil, err
		}
		entries = append(entries, mutationEntries...)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("embedding index: no types or operations matched the filters")
	}

	reusable := map[string]buildEntry{}
	if opts.Previous != nil && !opts.Force && opts.Previous.Model == e.Model() {
		for _, prev := range opts.Previous.Types {
			key := buildEntry{bucket: bucketType, name: prev.Name, textHash: prev.TextHash}.reuseKey()
			reusable[key] = buildEntry{embedding: prev.Embedding}
		}
		for _, prev := range opts.Previous.Queries {
			key := buildEntry{bucket: bucketQuery, name: prev.Name, textHash: prev.TextHash}.reuseKey()
			reusable[key] = buildEntry{embedding: prev.Embedding}
		}
		for _, prev := range opts.Previous.Mutations {
			key := buildEntry{bucket: bucketMutation, name: prev.Name, textHash: prev.TextHash}.reuseKey()
			reusable[key] = buildEntry{embedding: prev.Embedding}
		}
	}

	if err := embedEntries(ctx, entries, e, reusable, opts.Concurrency, opts.Progress); err != nil {
		return nil, err
	}

	ix := &EmbeddingIndex{
		Version:   EmbeddingIndexVersion,
		Env:       opts.Env,
		Endpoint:  opts.Endpoint,
		Model:     e.Model(),
		CreatedAt: time.Now().UTC(),
		embedder:  e,
	}
	if ve, ok := e.(*VenuEmbedder); ok {
		ix.EmbeddingEndpoint = ve.Endpoint()
	}

	for _, entry := range entries {
		switch entry.bucket {
		case bucketQuery:
			ix.Queries = append(ix.Queries, OperationEmbedding{
				Name: entry.name, Description: entry.description, ReturnType: entry.returnKey,
				SDL: entry.sdl, TextHash: entry.textHash, Embedding: entry.embedding,
			})
		case bucketMutation:
			ix.Mutations = append(ix.Mutations, OperationEmbedding{
				Name: entry.name, Description: entry.description, ReturnType: entry.returnKey,
				SDL: entry.sdl, TextHash: entry.textHash, Embedding: entry.embedding,
			})
		default:
			ix.Types = append(ix.Types, TypeEmbedding{
				Name: entry.name, Kind: entry.returnKey, Description: entry.description,
				SDL: entry.sdl, TextHash: entry.textHash, Embedding: entry.embedding,
			})
		}
	}
	sort.Slice(ix.Types, func(i, j int) bool { return ix.Types[i].Name < ix.Types[j].Name })
	sort.Slice(ix.Queries, func(i, j int) bool { return ix.Queries[i].Name < ix.Queries[j].Name })
	sort.Slice(ix.Mutations, func(i, j int) bool { return ix.Mutations[i].Name < ix.Mutations[j].Name })

	for _, entry := range entries {
		if len(entry.embedding) > 0 {
			ix.Dim = len(entry.embedding)
			break
		}
	}
	ix.normalize()
	return ix, nil
}

// embedEntries fills in entries[i].embedding, reusing cached vectors where
// available and calling e.Embed concurrently otherwise.
func embedEntries(ctx context.Context, entries []buildEntry, e Embedder, reusable map[string]buildEntry, concurrency int, progress func(done, total int, name string, reused bool)) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	if concurrency > len(entries) {
		concurrency = len(entries)
	}

	var (
		mu       sync.Mutex
		done     int
		firstErr error
		wg       sync.WaitGroup
		next     int
	)

	worker := func() {
		defer wg.Done()
		for {
			mu.Lock()
			if firstErr != nil || next >= len(entries) {
				mu.Unlock()
				return
			}
			i := next
			next++
			mu.Unlock()

			entry := &entries[i]
			reused := false
			if prev, ok := reusable[entry.reuseKey()]; ok && len(prev.embedding) > 0 {
				entry.embedding = prev.embedding
				reused = true
			} else {
				vec, err := e.Embed(ctx, entry.embedText)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("embedding %s %s: %w", entry.bucket.label(), entry.name, err)
					}
					mu.Unlock()
					return
				}
				entry.embedding = vec
			}

			mu.Lock()
			done++
			if progress != nil {
				progress(done, len(entries), entry.name, reused)
			}
			mu.Unlock()
		}
	}

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go worker()
	}
	wg.Wait()
	return firstErr
}

// BuildEmbeddingIndexFromClient introspects through client and builds an index
// from the result.
func BuildEmbeddingIndexFromClient(ctx context.Context, client Client, e Embedder, opts EmbeddingIndexOptions) (*EmbeddingIndex, error) {
	if client == nil {
		return nil, fmt.Errorf("embedding index: client is nil")
	}
	introspection, err := client.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	return BuildEmbeddingIndex(ctx, introspection, e, opts)
}

func selectIndexTypes(typesList []interface{}, opts EmbeddingIndexOptions) ([]buildEntry, error) {
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultEmbeddingMaxChars
	}

	kinds := map[string]bool{}
	for _, k := range opts.Kinds {
		if k != "" {
			kinds[strings.ToUpper(strings.TrimSpace(k))] = true
		}
	}

	var entries []buildEntry
	for _, raw := range typesList {
		tm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		if name == "" || strings.HasPrefix(name, "__") {
			continue
		}
		kind, _ := tm["kind"].(string)
		if len(kinds) > 0 && !kinds[strings.ToUpper(kind)] {
			continue
		}

		keep, err := matchesNameGlobs(name, opts.Include, opts.Exclude)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}

		description, _ := tm["description"].(string)
		sdl := strings.TrimRight(FormatTypeSDL(tm, opts.ShowArgs, false), "\n")
		// Keep the kind in the text: "input_object" vs "object" is real signal
		// separating CreateUserInput from User.
		text := buildEmbedText(strings.ToLower(kind)+" type", name, description, sdl, maxChars)

		entries = append(entries, buildEntry{
			bucket:      bucketType,
			name:        name,
			returnKey:   kind,
			description: description,
			sdl:         sdl,
			textHash:    textHash(text),
			embedText:   text,
		})
	}
	return entries, nil
}

// selectOperationFields indexes the fields of a root type (Query or Mutation)
// as individually searchable operations, distinct from the type-level index.
func selectOperationFields(typesList []interface{}, rootTypeName string, bucket opBucket, opts EmbeddingIndexOptions) ([]buildEntry, error) {
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultEmbeddingMaxChars
	}

	var rootFields []interface{}
	for _, raw := range typesList {
		tm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := tm["name"].(string); name == rootTypeName {
			rootFields, _ = tm["fields"].([]interface{})
			break
		}
	}

	var entries []buildEntry
	for _, raw := range rootFields {
		fm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fm["name"].(string)
		if name == "" {
			continue
		}

		keep, err := matchesNameGlobs(name, opts.Include, opts.Exclude)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}

		description, _ := fm["description"].(string)
		returnType := formatTypeRef(fm["type"])
		sdl := strings.TrimSpace(formatSDLField(fm, true, false))
		text := buildEmbedText(bucket.label(), name, description, sdl, maxChars)

		entries = append(entries, buildEntry{
			bucket:      bucket,
			name:        name,
			returnKey:   returnType,
			description: description,
			sdl:         sdl,
			textHash:    textHash(text),
			embedText:   text,
		})
	}
	return entries, nil
}

func textHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// buildEmbedText assembles the text embedded for an entry: kind/category and
// name first so they survive truncation, then the description, then the SDL
// body.
func buildEmbedText(kindLabel, name, description, sdl string, maxChars int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GraphQL %s: %s\n", strings.ToLower(kindLabel), name)
	if description != "" {
		fmt.Fprintf(&b, "Description: %s\n", description)
	}
	b.WriteString(sdl)

	text := b.String()
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars]
	}
	return text
}

func matchesNameGlobs(name string, include, exclude []string) (bool, error) {
	if len(include) > 0 {
		matched := false
		for _, pattern := range include {
			ok, err := path.Match(pattern, name)
			if err != nil {
				return false, fmt.Errorf("invalid include pattern %q: %w", pattern, err)
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	for _, pattern := range exclude {
		ok, err := path.Match(pattern, name)
		if err != nil {
			return false, fmt.Errorf("invalid exclude pattern %q: %w", pattern, err)
		}
		if ok {
			return false, nil
		}
	}
	return true, nil
}

// introspectionSchema extracts the schema's type list and root type names
// (queryType/mutationType may be empty if the schema has none).
func introspectionSchema(introspection map[string]interface{}) (typesList []interface{}, queryType, mutationType string, err error) {
	data, ok := introspection["data"].(map[string]interface{})
	if !ok {
		return nil, "", "", fmt.Errorf("invalid introspection response: missing data")
	}
	schema, ok := data["__schema"].(map[string]interface{})
	if !ok {
		return nil, "", "", fmt.Errorf("invalid introspection response: missing __schema")
	}
	typesList, ok = schema["types"].([]interface{})
	if !ok {
		return nil, "", "", fmt.Errorf("invalid introspection response: missing types")
	}
	if qt, ok := schema["queryType"].(map[string]interface{}); ok {
		queryType, _ = qt["name"].(string)
	}
	if mt, ok := schema["mutationType"].(map[string]interface{}); ok {
		mutationType, _ = mt["name"].(string)
	}
	return typesList, queryType, mutationType, nil
}

// normalize scales every vector to unit length so Search can use a dot
// product. Vectors already unit length (as Venu returns) are left untouched.
func (ix *EmbeddingIndex) normalize() {
	for i := range ix.Types {
		normalizeVector(ix.Types[i].Embedding)
	}
	for i := range ix.Queries {
		normalizeVector(ix.Queries[i].Embedding)
	}
	for i := range ix.Mutations {
		normalizeVector(ix.Mutations[i].Embedding)
	}
}

func normalizeVector(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	norm := math.Sqrt(sum)
	if math.Abs(norm-1) < 1e-6 {
		return
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
}

// SetEmbedder attaches the embedder used to embed search queries. Required
// before calling Search on a loaded index.
func (ix *EmbeddingIndex) SetEmbedder(e Embedder) { ix.embedder = e }

// Embedder returns the attached embedder, or nil.
func (ix *EmbeddingIndex) Embedder() Embedder { return ix.embedder }

// Search embeds query once and ranks it against all three categories,
// returning up to topN hits per category. It fails if the attached embedder's
// model differs from the one that built the index — scores across vector
// spaces are meaningless.
func (ix *EmbeddingIndex) Search(ctx context.Context, query string, topN int) (*SearchResults, error) {
	if ix.embedder == nil {
		return nil, fmt.Errorf("embedding search: no embedder attached (call SetEmbedder)")
	}
	if ix.Model != "" && ix.embedder.Model() != "" && ix.Model != ix.embedder.Model() {
		return nil, fmt.Errorf("embedding search: index was built with model %q but the embedder uses %q; rebuild the index or switch models", ix.Model, ix.embedder.Model())
	}
	embed := ix.embedder.Embed
	if qe, ok := ix.embedder.(QueryEmbedder); ok {
		embed = qe.EmbedQuery
	}
	vec, err := embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return ix.SearchVector(vec, topN)
}

// SearchVector ranks an already-computed query vector against all three
// categories. No network calls; useful when the caller embeds queries itself
// or reuses one vector across several indexes.
func (ix *EmbeddingIndex) SearchVector(vec []float32, topN int) (*SearchResults, error) {
	query, err := ix.prepareQueryVector(vec)
	if err != nil {
		return nil, err
	}
	if topN <= 0 {
		topN = 5
	}

	return &SearchResults{
		Queries:   rankOperations(ix.Queries, query, topN),
		Mutations: rankOperations(ix.Mutations, query, topN),
		Types:     rankTypes(ix.Types, query, topN),
	}, nil
}

func (ix *EmbeddingIndex) prepareQueryVector(vec []float32) ([]float32, error) {
	if len(vec) == 0 {
		return nil, fmt.Errorf("embedding search: query vector is empty")
	}
	if ix.Dim > 0 && len(vec) != ix.Dim {
		return nil, fmt.Errorf("embedding search: query vector has %d dimensions, index has %d", len(vec), ix.Dim)
	}
	query := make([]float32, len(vec))
	copy(query, vec)
	normalizeVector(query)
	return query, nil
}

func cosine(a, b []float32) (float64, bool) {
	if len(a) != len(b) {
		return 0, false
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot, true
}

func rankTypes(types []TypeEmbedding, query []float32, topN int) []TypeMatch {
	matches := make([]TypeMatch, 0, len(types))
	for _, t := range types {
		dot, ok := cosine(query, t.Embedding)
		if !ok {
			continue
		}
		matches = append(matches, TypeMatch{
			Name: t.Name, Kind: t.Kind, Description: t.Description, SDL: t.SDL, Score: dot,
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) > topN {
		matches = matches[:topN]
	}
	return matches
}

func rankOperations(ops []OperationEmbedding, query []float32, topN int) []OperationMatch {
	matches := make([]OperationMatch, 0, len(ops))
	for _, o := range ops {
		dot, ok := cosine(query, o.Embedding)
		if !ok {
			continue
		}
		matches = append(matches, OperationMatch{
			Name: o.Name, ReturnType: o.ReturnType, Description: o.Description, SDL: o.SDL, Score: dot,
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) > topN {
		matches = matches[:topN]
	}
	return matches
}

// LoadEmbeddingIndex reads an index file written by Save and normalizes its
// vectors for search.
func LoadEmbeddingIndex(path string) (*EmbeddingIndex, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading embedding index %s: %w", path, err)
	}
	var ix EmbeddingIndex
	if err := json.Unmarshal(raw, &ix); err != nil {
		return nil, fmt.Errorf("parsing embedding index %s: %w", path, err)
	}
	if ix.Version > EmbeddingIndexVersion {
		return nil, fmt.Errorf("embedding index %s has version %d, this build understands up to %d", path, ix.Version, EmbeddingIndexVersion)
	}
	if len(ix.Types) == 0 && len(ix.Queries) == 0 && len(ix.Mutations) == 0 {
		return nil, fmt.Errorf("embedding index %s contains no types or operations", path)
	}
	if ix.Dim == 0 {
		switch {
		case len(ix.Types) > 0:
			ix.Dim = len(ix.Types[0].Embedding)
		case len(ix.Queries) > 0:
			ix.Dim = len(ix.Queries[0].Embedding)
		case len(ix.Mutations) > 0:
			ix.Dim = len(ix.Mutations[0].Embedding)
		}
	}
	ix.normalize()
	return &ix, nil
}

// Save writes the index as JSON, creating parent directories as needed.
func (ix *EmbeddingIndex) Save(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// stderrIsTerminal reports whether progress output would land on a terminal.
// Redirected stderr gets no carriage-return progress line, which would
// otherwise litter logs and CI output.
func stderrIsTerminal() bool {
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// EmbeddingIndexPath returns the default index file for an environment:
// .gqlcli-embeddings.json for the unnamed/default env, and
// .gqlcli-embeddings.<env>.json otherwise.
func EmbeddingIndexPath(env string) string {
	env = strings.TrimSpace(env)
	if env == "" {
		return ".gqlcli-embeddings.json"
	}
	return fmt.Sprintf(".gqlcli-embeddings.%s.json", env)
}
