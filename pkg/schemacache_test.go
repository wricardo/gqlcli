package gqlcli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const cacheTestSchema = `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[
	{"kind":"OBJECT","name":"Query","fields":[{"name":"user","args":[],"type":{"kind":"OBJECT","name":"User"},"isDeprecated":false}]},
	{"kind":"OBJECT","name":"User","fields":[{"name":"id","args":[],"type":{"kind":"SCALAR","name":"ID"},"isDeprecated":false}]}
]}}}`

// cachingIntrospectionServer answers full introspection with cacheTestSchema and
// counts requests by kind.
func cachingIntrospectionServer(t *testing.T) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var schemaHits, typeHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "__schema") {
			atomic.AddInt32(&schemaHits, 1)
			_, _ = w.Write([]byte(cacheTestSchema))
			return
		}
		atomic.AddInt32(&typeHits, 1)
		_, _ = w.Write([]byte(`{"data":{"__type":{"name":"User","kind":"OBJECT","fields":[{"name":"id","args":[],"type":{"kind":"SCALAR","name":"ID"}}]}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &schemaHits, &typeHits
}

func TestIntrospect_ReusesDiskCacheAcrossClients(t *testing.T) {
	srv, schemaHits, _ := cachingIntrospectionServer(t)
	dir := t.TempDir()
	cfg := func() *Config {
		return &Config{URL: srv.URL, SchemaCacheTTL: time.Minute, SchemaCacheDir: dir, Headers: map[string]string{"Authorization": "Bearer a"}}
	}

	for i := 0; i < 3; i++ {
		if _, err := NewHTTPClient(cfg()).Introspect(context.Background()); err != nil {
			t.Fatalf("Introspect #%d: %v", i, err)
		}
	}
	if *schemaHits != 1 {
		t.Errorf("introspected over HTTP %d times, want 1 — later clients should read the cache", *schemaHits)
	}

	other := cfg()
	other.Headers = map[string]string{"Authorization": "Bearer b"}
	if _, err := NewHTTPClient(other).Introspect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *schemaHits != 2 {
		t.Errorf("different credentials hit the cache; schema requests = %d, want 2", *schemaHits)
	}

	refresh := cfg()
	refresh.RefreshSchema = true
	if _, err := NewHTTPClient(refresh).Introspect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *schemaHits != 3 {
		t.Errorf("RefreshSchema did not bypass the cache; schema requests = %d, want 3", *schemaHits)
	}

	entries, _ := filepath.Glob(filepath.Join(dir, "schema-*.json"))
	for _, e := range entries {
		raw, _ := os.ReadFile(e)
		if strings.Contains(string(raw), "Bearer") {
			t.Errorf("cache file %s contains a header value", e)
		}
	}
}

func TestIntrospect_ExpiredCacheRefetches(t *testing.T) {
	srv, schemaHits, _ := cachingIntrospectionServer(t)
	cfg := &Config{URL: srv.URL, SchemaCacheTTL: time.Minute, SchemaCacheDir: t.TempDir()}
	c := NewHTTPClient(cfg)
	if _, err := c.Introspect(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(c.schemaCachePath(), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHTTPClient(cfg).Introspect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *schemaHits != 2 {
		t.Errorf("schema requests = %d, want 2 after the cache expired", *schemaHits)
	}
}

func TestIntrospect_NoCacheByDefault(t *testing.T) {
	srv, schemaHits, _ := cachingIntrospectionServer(t)
	for i := 0; i < 2; i++ {
		if _, err := NewHTTPClient(&Config{URL: srv.URL}).Introspect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if *schemaHits != 2 {
		t.Errorf("schema requests = %d, want 2 — the library must not cache unless asked", *schemaHits)
	}
}

func TestDescriber_ServesDepthZeroFromFreshDiskCache(t *testing.T) {
	srv, schemaHits, typeHits := cachingIntrospectionServer(t)
	cfg := &Config{URL: srv.URL, SchemaCacheTTL: time.Minute, SchemaCacheDir: t.TempDir()}
	if _, err := NewHTTPClient(cfg).Introspect(context.Background()); err != nil {
		t.Fatal(err)
	}

	sdl, err := NewDescriberFromHTTPClient(NewHTTPClient(cfg)).Describe(context.Background(), "User")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(sdl, "id: ID") {
		t.Errorf("SDL = %q, want User.id", sdl)
	}
	if *typeHits != 0 || *schemaHits != 1 {
		t.Errorf("__type requests = %d, __schema requests = %d; want 0 and 1 — a fresh cache should serve describe", *typeHits, *schemaHits)
	}
}

func TestPruneSchemaCache_RemovesOnlyStaleEntries(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "schema-stale.json")
	fresh := filepath.Join(dir, "schema-fresh.json")
	other := filepath.Join(dir, "unrelated.json")
	for _, p := range []string{stale, fresh, other} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{stale, other} {
		_ = os.Chtimes(p, old, old)
	}

	pruneSchemaCache(dir, time.Minute)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale entry survived pruning")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
}
