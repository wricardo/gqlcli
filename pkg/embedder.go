package gqlcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Embedding provider defaults. The Venu API is OpenAI-shaped for chat but takes
// a single "text" field for embeddings and returns one already L2-normalized
// vector per call.
const (
	DefaultEmbeddingBaseURL = "https://z-ask.zasperhub.com/api/v1"
	DefaultEmbeddingModel   = "nomic-embed-text-v1.5"

	// DefaultEmbeddingMaxChars caps the text sent per embedding request. Type
	// SDL for a wide object can run long and the remote model truncates
	// silently; truncating here keeps the text_hash honest about what was
	// actually embedded.
	DefaultEmbeddingMaxChars = 8000
)

// nomic task prefixes. The nomic-embed-text family is trained for asymmetric
// retrieval: documents must be embedded as "search_document: <text>" and
// queries as "search_query: <text>". Skipping them measurably degrades ranking.
const (
	nomicDocumentPrefix = "search_document: "
	nomicQueryPrefix    = "search_query: "
)

// QueryEmbedder is implemented by embedders that embed search queries
// differently from indexed documents. EmbeddingIndex.Search uses it when
// available and falls back to Embed otherwise.
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// Embedder turns text into a vector. Implement it to index and search with a
// provider other than Venu; Model is recorded in the index file so a search
// against vectors from a different model can be rejected.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Model() string
}

// VenuEmbedder calls the Venu embeddings endpoint (POST {BaseURL}/embeddings)
// with an X-API-Key header. One request per text — the endpoint rejects arrays.
type VenuEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
	// docPrefix and queryPrefix are prepended to indexed text and query text
	// respectively. Defaulted for nomic models, overridable per embedder.
	docPrefix   string
	queryPrefix string
	prefixesSet bool
}

// VenuEmbedderOption configures NewVenuEmbedder.
type VenuEmbedderOption func(*VenuEmbedder)

// WithEmbeddingBaseURL overrides the API base URL (no trailing /embeddings).
func WithEmbeddingBaseURL(url string) VenuEmbedderOption {
	return func(e *VenuEmbedder) {
		if url != "" {
			e.baseURL = strings.TrimSuffix(url, "/")
		}
	}
}

// WithEmbeddingAPIKey sets the X-API-Key value explicitly instead of reading
// VENU_API_KEY from the environment.
func WithEmbeddingAPIKey(key string) VenuEmbedderOption {
	return func(e *VenuEmbedder) {
		if key != "" {
			e.apiKey = key
		}
	}
}

// WithEmbeddingModel selects the embedding model (e.g. nomic-embed-text-v1.5).
func WithEmbeddingModel(model string) VenuEmbedderOption {
	return func(e *VenuEmbedder) {
		if model != "" {
			e.model = model
		}
	}
}

// WithEmbeddingTaskPrefixes sets the task prefixes prepended to document and
// query text. Pass empty strings to disable prefixing for a model that does not
// want it; the nomic-embed-text defaults apply when this option is not used.
func WithEmbeddingTaskPrefixes(document, query string) VenuEmbedderOption {
	return func(e *VenuEmbedder) {
		e.docPrefix = document
		e.queryPrefix = query
		e.prefixesSet = true
	}
}

// WithEmbeddingHTTPClient supplies the HTTP client used for requests.
func WithEmbeddingHTTPClient(c *http.Client) VenuEmbedderOption {
	return func(e *VenuEmbedder) {
		if c != nil {
			e.http = c
		}
	}
}

// NewVenuEmbedder creates a Venu-backed Embedder. The API key comes from
// WithEmbeddingAPIKey, else VENU_API_KEY; the base URL from
// WithEmbeddingBaseURL, else VENU_URL, else DefaultEmbeddingBaseURL.
func NewVenuEmbedder(opts ...VenuEmbedderOption) (*VenuEmbedder, error) {
	e := &VenuEmbedder{
		baseURL: DefaultEmbeddingBaseURL,
		apiKey:  os.Getenv("VENU_API_KEY"),
		model:   DefaultEmbeddingModel,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	if url := os.Getenv("VENU_URL"); url != "" {
		e.baseURL = strings.TrimSuffix(url, "/")
	}
	if model := os.Getenv("VENU_EMBEDDING_MODEL"); model != "" {
		e.model = model
	}
	for _, opt := range opts {
		opt(e)
	}
	if !e.prefixesSet && strings.Contains(strings.ToLower(e.model), "nomic") {
		e.docPrefix = nomicDocumentPrefix
		e.queryPrefix = nomicQueryPrefix
	}
	if e.apiKey == "" {
		return nil, fmt.Errorf("embedding API key missing: set VENU_API_KEY or pass WithEmbeddingAPIKey")
	}
	return e, nil
}

// Model returns the configured embedding model name.
func (e *VenuEmbedder) Model() string { return e.model }

// Endpoint returns the embeddings URL requests are sent to.
func (e *VenuEmbedder) Endpoint() string { return e.baseURL + "/embeddings" }

type venuEmbeddingResponse struct {
	Embedding  []float32 `json:"embedding"`
	Dimensions int       `json:"dimensions"`
	Model      string    `json:"model"`
	// Error fields returned on validation/auth failures.
	Answer string `json:"answer"`
	Code   string `json:"code"`
}

// Embed returns the vector for indexed text, with the document task prefix
// applied. Text is not truncated here; callers that index long documents should
// truncate to DefaultEmbeddingMaxChars first so the hash they store matches
// what the model saw.
func (e *VenuEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, e.docPrefix, text)
}

// EmbedQuery returns the vector for a search query, with the query task prefix
// applied.
func (e *VenuEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, e.queryPrefix, text)
}

func (e *VenuEmbedder) embed(ctx context.Context, prefix, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embed: text is empty")
	}

	body, err := json.Marshal(map[string]string{"model": e.model, "text": prefix + text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", e.apiKey)

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("embed: reading response: %w", err)
	}

	var parsed venuEmbeddingResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embed: HTTP %d, unparseable response: %s", resp.StatusCode, truncateForError(string(raw)))
	}
	if parsed.Answer != "" && len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("embed: %s (%s)", parsed.Answer, parsed.Code)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("embed: HTTP %d: %s", resp.StatusCode, truncateForError(string(raw)))
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("embed: response contained no embedding")
	}
	return parsed.Embedding, nil
}

func truncateForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
