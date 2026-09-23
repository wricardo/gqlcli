package gqlcli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultSchemaCacheTTL is how long the CLI reuses a cached introspection
// result before fetching the schema again.
const DefaultSchemaCacheTTL = 10 * time.Minute

// schemaCachePath returns where the introspection result for this client's
// endpoint is cached, or "" when caching is disabled. The file name hashes the
// URL and every header, so endpoints and credentials that may see different
// schemas never share an entry, and no header value is written to disk.
func (c *HTTPClient) schemaCachePath() string {
	if c.config.SchemaCacheTTL <= 0 {
		return ""
	}
	dir := c.config.SchemaCacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(base, "gqlcli")
	}

	h := sha256.New()
	h.Write([]byte(c.config.URL))
	keys := make([]string, 0, len(c.config.Headers))
	for k := range c.config.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte{0})
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(c.config.Headers[k]))
	}
	h.Write([]byte{0})
	h.Write([]byte(c.config.Token))
	return filepath.Join(dir, "schema-"+hex.EncodeToString(h.Sum(nil))[:32]+".json")
}

// schemaCacheFresh reports whether Introspect would be served from disk.
func (c *HTTPClient) schemaCacheFresh() bool {
	path := c.schemaCachePath()
	if path == "" || c.config.RefreshSchema {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < c.config.SchemaCacheTTL
}

func (c *HTTPClient) readSchemaCache() (map[string]interface{}, bool) {
	if !c.schemaCacheFresh() {
		return nil, false
	}
	raw, err := os.ReadFile(c.schemaCachePath())
	if err != nil {
		return nil, false
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil || !hasIntrospectedSchema(result) {
		return nil, false
	}
	return result, true
}

// writeSchemaCache stores result best-effort: a cache that cannot be written
// only costs the next command a network round trip.
func (c *HTTPClient) writeSchemaCache(result map[string]interface{}) {
	path := c.schemaCachePath()
	if path == "" || !hasIntrospectedSchema(result) {
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".schema-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(raw)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	pruneSchemaCache(filepath.Dir(path), c.config.SchemaCacheTTL)
}

// schemaCacheRetention bounds how long expired entries linger. Each token
// rotation yields a new cache key, so without pruning they pile up.
const schemaCacheRetention = 24 * time.Hour

func pruneSchemaCache(dir string, ttl time.Duration) {
	keep := schemaCacheRetention
	if ttl > keep {
		keep = ttl
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "schema-*.json"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && time.Since(info.ModTime()) > keep {
			_ = os.Remove(m)
		}
	}
}

func hasIntrospectedSchema(result map[string]interface{}) bool {
	if errs, ok := result["errors"].([]interface{}); ok && len(errs) > 0 {
		return false
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return false
	}
	schema, ok := data["__schema"].(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = schema["types"].([]interface{})
	return ok
}
