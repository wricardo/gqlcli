package gqlcli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func writeFileString(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestNewVenuEmbedderDefaultsAndOverrides(t *testing.T) {
	t.Setenv("VENU_API_KEY", "from-env")
	t.Setenv("VENU_URL", "")
	t.Setenv("VENU_EMBEDDING_MODEL", "")

	e, err := NewVenuEmbedder()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Endpoint() != DefaultEmbeddingBaseURL+"/embeddings" {
		t.Errorf("endpoint = %q", e.Endpoint())
	}
	if e.Model() != DefaultEmbeddingModel {
		t.Errorf("model = %q", e.Model())
	}
	if e.apiKey != "from-env" {
		t.Errorf("api key = %q, want from-env", e.apiKey)
	}

	e, err = NewVenuEmbedder(
		WithEmbeddingBaseURL("https://example.test/api/v1/"),
		WithEmbeddingAPIKey("explicit"),
		WithEmbeddingModel("nomic-embed-text-v1"),
	)
	if err != nil {
		t.Fatalf("new with options: %v", err)
	}
	if e.Endpoint() != "https://example.test/api/v1/embeddings" {
		t.Errorf("endpoint = %q", e.Endpoint())
	}
	if e.Model() != "nomic-embed-text-v1" || e.apiKey != "explicit" {
		t.Errorf("options not applied: %+v", e)
	}

	// Empty option values leave the defaults in place.
	if _, err := NewVenuEmbedder(WithEmbeddingBaseURL(""), WithEmbeddingModel(""), WithEmbeddingHTTPClient(nil)); err != nil {
		t.Fatalf("empty options should be ignored: %v", err)
	}
}

func TestNewVenuEmbedderEnvOverrides(t *testing.T) {
	t.Setenv("VENU_API_KEY", "k")
	t.Setenv("VENU_URL", "https://env.test/api/v1")
	t.Setenv("VENU_EMBEDDING_MODEL", "env-model")

	e, err := NewVenuEmbedder()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Endpoint() != "https://env.test/api/v1/embeddings" {
		t.Errorf("endpoint = %q", e.Endpoint())
	}
	if e.Model() != "env-model" {
		t.Errorf("model = %q", e.Model())
	}
}

func TestNewVenuEmbedderRequiresAPIKey(t *testing.T) {
	t.Setenv("VENU_API_KEY", "")
	if _, err := NewVenuEmbedder(); err == nil {
		t.Fatal("expected error when no API key is configured")
	}
}

func TestVenuEmbedderEmbed(t *testing.T) {
	var gotBody map[string]string
	var gotKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":[0.6,0.8],"dimensions":2,"model":"nomic-embed-text-v1.5"}`))
	}))
	defer srv.Close()

	e, err := NewVenuEmbedder(WithEmbeddingBaseURL(srv.URL), WithEmbeddingAPIKey("secret"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vec) != 2 || vec[0] != 0.6 {
		t.Errorf("unexpected vector %v", vec)
	}
	if gotKey != "secret" {
		t.Errorf("X-API-Key = %q", gotKey)
	}
	// The API takes "text", not "input" — a regression here silently breaks indexing.
	if gotBody["text"] != nomicDocumentPrefix+"hello world" || gotBody["model"] != DefaultEmbeddingModel {
		t.Errorf("unexpected request body %+v", gotBody)
	}
}

func TestVenuEmbedderErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"validation error", 400, `{"answer":"'text' field is required and must be a non-empty string","code":"VALIDATION_ERROR"}`, "VALIDATION_ERROR"},
		{"unparseable", 502, `<html>bad gateway</html>`, "unparseable response"},
		{"http error", 500, `{}`, "HTTP 500"},
		{"no embedding", 200, `{"dimensions":0}`, "no embedding"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			e, err := NewVenuEmbedder(WithEmbeddingBaseURL(srv.URL), WithEmbeddingAPIKey("k"))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			_, err = e.Embed(context.Background(), "text")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestVenuEmbedderRejectsEmptyText(t *testing.T) {
	e, err := NewVenuEmbedder(WithEmbeddingAPIKey("k"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := e.Embed(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestTruncateForError(t *testing.T) {
	if got := truncateForError("  short  "); got != "short" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", 400)
	got := truncateForError(long)
	if len(got) != 303 || !strings.HasSuffix(got, "...") {
		t.Errorf("unexpected truncation: len=%d", len(got))
	}
}

func TestVenuEmbedderNomicTaskPrefixes(t *testing.T) {
	var texts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		texts = append(texts, body["text"])
		_, _ = w.Write([]byte(`{"embedding":[1,0],"dimensions":2}`))
	}))
	defer srv.Close()

	e, err := NewVenuEmbedder(WithEmbeddingBaseURL(srv.URL), WithEmbeddingAPIKey("k"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := e.Embed(context.Background(), "doc"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := e.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("embed query: %v", err)
	}
	if texts[0] != nomicDocumentPrefix+"doc" || texts[1] != nomicQueryPrefix+"q" {
		t.Fatalf("nomic prefixes not applied: %q", texts)
	}

	// A non-nomic model gets no prefixes.
	texts = nil
	plain, err := NewVenuEmbedder(WithEmbeddingBaseURL(srv.URL), WithEmbeddingAPIKey("k"), WithEmbeddingModel("text-embedding-3-small"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := plain.Embed(context.Background(), "doc"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if texts[0] != "doc" {
		t.Errorf("unexpected prefix for non-nomic model: %q", texts[0])
	}

	// Explicit prefixes win, including explicitly empty ones.
	texts = nil
	custom, err := NewVenuEmbedder(WithEmbeddingBaseURL(srv.URL), WithEmbeddingAPIKey("k"), WithEmbeddingTaskPrefixes("D: ", ""))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := custom.Embed(context.Background(), "doc"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := custom.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("embed query: %v", err)
	}
	if texts[0] != "D: doc" || texts[1] != "q" {
		t.Errorf("custom prefixes not applied: %q", texts)
	}
}
