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

// EmbeddingIndex is a searchable set of GraphQL type embeddings, persisted as
// JSON. Build one with BuildEmbeddingIndex, load one with LoadEmbeddingIndex.
type EmbeddingIndex struct {
	Version int    `json:"version"`
	Env     string `json:"env,omitempty"`
	// Endpoint is the GraphQL URL the types were introspected from.
	Endpoint string `json:"endpoint,omitempty"`
	// EmbeddingEndpoint and Model identify the vector space. Comparing a query
	// embedded by a different model against these vectors is meaningless, so
	// Search refuses it.
	EmbeddingEndpoint string          `json:"embedding_endpoint,omitempty"`
	Model             string          `json:"model"`
	Dim               int             `json:"dim"`
	CreatedAt         time.Time       `json:"created_at"`
	Types             []TypeEmbedding `json:"types"`

	embedder Embedder
}

// TypeMatch is one search hit. Score is cosine similarity in [-1, 1]; higher is
// closer.
type TypeMatch struct {
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Description string  `json:"description,omitempty"`
	SDL         string  `json:"sdl"`
	Score       float64 `json:"score"`
}

// EmbeddingIndexOptions controls BuildEmbeddingIndex.
type EmbeddingIndexOptions struct {
	// Kinds restricts indexing to these type kinds (OBJECT, INPUT_OBJECT,
	// ENUM, INTERFACE, UNION, SCALAR). Empty indexes every kind.
	Kinds []string
	// Include keeps only type names matching one of these globs, when non-empty.
	Include []string
	// Exclude drops type names matching any of these globs. Applied after Include.
	Exclude []string
	// ShowArgs includes field argument signatures in the indexed SDL. Worth
	// leaving on: arguments carry most of the meaning of the Query root.
	ShowArgs bool
	// MaxChars truncates the embedded text. Zero uses DefaultEmbeddingMaxChars.
	MaxChars int
	// Concurrency is the number of in-flight embedding requests. Zero uses 4.
	Concurrency int
	// Previous is an existing index whose vectors are reused for types whose
	// text hash is unchanged, so a rebuild only pays for what moved.
	Previous *EmbeddingIndex
	// Force re-embeds every type even when Previous has a matching hash.
	Force bool
	// Env and Endpoint are recorded in the index for mismatch warnings.
	Env      string
	Endpoint string
	// Progress, when set, is called as each type completes. Calls are
	// serialized, so it needs no locking of its own.
	Progress func(done, total int, name string, reused bool)
}

// BuildEmbeddingIndex embeds the types in a GraphQL introspection response.
// introspection is the full envelope returned by Client.Introspect (the map
// with a "data" key).
func BuildEmbeddingIndex(ctx context.Context, introspection map[string]interface{}, e Embedder, opts EmbeddingIndexOptions) (*EmbeddingIndex, error) {
	if e == nil {
		return nil, fmt.Errorf("embedding index: embedder is nil")
	}

	typesList, err := introspectionTypes(introspection)
	if err != nil {
		return nil, err
	}

	entries, err := selectIndexTypes(typesList, opts)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("embedding index: no types matched the filters")
	}

	reusable := map[string]TypeEmbedding{}
	if opts.Previous != nil && !opts.Force {
		for _, prev := range opts.Previous.Types {
			if opts.Previous.Model == e.Model() {
				reusable[prev.Name+"\x00"+prev.TextHash] = prev
			}
		}
	}

	concurrency := opts.Concurrency
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
			if prev, ok := reusable[entry.Name+"\x00"+entry.TextHash]; ok && len(prev.Embedding) > 0 {
				entry.Embedding = prev.Embedding
				reused = true
			} else {
				vec, err := e.Embed(ctx, entry.embedText)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("embedding type %s: %w", entry.Name, err)
					}
					mu.Unlock()
					return
				}
				entry.Embedding = vec
			}

			mu.Lock()
			done++
			if opts.Progress != nil {
				opts.Progress(done, len(entries), entry.Name, reused)
			}
			mu.Unlock()
		}
	}

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go worker()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
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

	ix.Types = make([]TypeEmbedding, 0, len(entries))
	for _, entry := range entries {
		ix.Types = append(ix.Types, entry.TypeEmbedding)
	}
	sort.Slice(ix.Types, func(i, j int) bool { return ix.Types[i].Name < ix.Types[j].Name })

	if len(ix.Types) > 0 {
		ix.Dim = len(ix.Types[0].Embedding)
	}
	ix.normalize()
	return ix, nil
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

// indexEntry is a TypeEmbedding plus the text that gets embedded for it.
type indexEntry struct {
	TypeEmbedding
	embedText string
}

func selectIndexTypes(typesList []interface{}, opts EmbeddingIndexOptions) ([]indexEntry, error) {
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

	var entries []indexEntry
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
		text := buildEmbedText(name, kind, description, sdl, maxChars)

		sum := sha256.Sum256([]byte(text))
		entries = append(entries, indexEntry{
			TypeEmbedding: TypeEmbedding{
				Name:        name,
				Kind:        kind,
				Description: description,
				SDL:         sdl,
				TextHash:    "sha256:" + hex.EncodeToString(sum[:]),
			},
			embedText: text,
		})
	}
	return entries, nil
}

// buildEmbedText assembles the text embedded for a type: name and kind first so
// they survive truncation, then the description, then the SDL body.
func buildEmbedText(name, kind, description, sdl string, maxChars int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GraphQL %s type: %s\n", strings.ToLower(kind), name)
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

func introspectionTypes(introspection map[string]interface{}) ([]interface{}, error) {
	data, ok := introspection["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid introspection response: missing data")
	}
	schema, ok := data["__schema"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid introspection response: missing __schema")
	}
	typesList, ok := schema["types"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid introspection response: missing types")
	}
	return typesList, nil
}

// normalize scales every vector to unit length so Search can use a dot product.
// Vectors already unit length (as Venu returns) are left untouched.
func (ix *EmbeddingIndex) normalize() {
	for i := range ix.Types {
		normalizeVector(ix.Types[i].Embedding)
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

// Search embeds query and returns the topN closest types, best first. It fails
// if the attached embedder's model differs from the one that built the index —
// scores across vector spaces are meaningless.
func (ix *EmbeddingIndex) Search(ctx context.Context, query string, topN int) ([]TypeMatch, error) {
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

// SearchVector ranks the index against an already-computed query vector. No
// network calls; useful when the caller embeds queries itself or reuses one
// vector across several indexes.
func (ix *EmbeddingIndex) SearchVector(vec []float32, topN int) ([]TypeMatch, error) {
	if len(vec) == 0 {
		return nil, fmt.Errorf("embedding search: query vector is empty")
	}
	if ix.Dim > 0 && len(vec) != ix.Dim {
		return nil, fmt.Errorf("embedding search: query vector has %d dimensions, index has %d", len(vec), ix.Dim)
	}
	if topN <= 0 {
		topN = 5
	}

	query := make([]float32, len(vec))
	copy(query, vec)
	normalizeVector(query)

	matches := make([]TypeMatch, 0, len(ix.Types))
	for _, t := range ix.Types {
		if len(t.Embedding) != len(query) {
			continue
		}
		var dot float64
		for i := range query {
			dot += float64(query[i]) * float64(t.Embedding[i])
		}
		matches = append(matches, TypeMatch{
			Name:        t.Name,
			Kind:        t.Kind,
			Description: t.Description,
			SDL:         t.SDL,
			Score:       dot,
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
	return matches, nil
}

// LoadEmbeddingIndex reads an index file written by SaveEmbeddingIndex and
// normalizes its vectors for search.
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
	if len(ix.Types) == 0 {
		return nil, fmt.Errorf("embedding index %s contains no types", path)
	}
	if ix.Dim == 0 {
		ix.Dim = len(ix.Types[0].Embedding)
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
