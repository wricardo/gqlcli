package gqlcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func schemaCLI(t *testing.T) (func(args ...string) (string, error), string, *int32) {
	t.Helper()
	srv, schemaHits, _ := cachingIntrospectionServer(t)
	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		out := &bytes.Buffer{}
		builder := NewCLIBuilder(&Config{URL: srv.URL, SchemaCacheDir: dir})
		app := &cli.App{Name: "gqlcli", Writer: out, ErrWriter: &bytes.Buffer{}}
		builder.RegisterCommands(app)
		err := app.Run(append([]string{"gqlcli"}, args...))
		return out.String(), err
	}
	return run, dir, schemaHits
}

func TestSchemaCommand_RefreshStatusClear(t *testing.T) {
	run, dir, schemaHits := schemaCLI(t)
	ttl := []string{"--schema-cache-ttl", "1m"}

	out, err := run(append([]string{"schema", "status"}, ttl...)...)
	if err != nil || !strings.Contains(out, "state: missing") {
		t.Fatalf("status before refresh = %q, %v; want missing", out, err)
	}

	out, err = run(append([]string{"schema", "refresh"}, ttl...)...)
	if err != nil || !strings.Contains(out, "(2 types,") {
		t.Fatalf("refresh = %q, %v", out, err)
	}
	if *schemaHits != 1 {
		t.Fatalf("schema requests after refresh = %d, want 1", *schemaHits)
	}

	if _, err := run(append([]string{"types"}, ttl...)...); err != nil {
		t.Fatalf("types: %v", err)
	}
	if *schemaHits != 1 {
		t.Errorf("types re-introspected after refresh; schema requests = %d, want 1", *schemaHits)
	}

	out, err = run(append([]string{"schema", "status"}, ttl...)...)
	if err != nil || !strings.Contains(out, "state: fresh") {
		t.Fatalf("status after refresh = %q, %v; want fresh", out, err)
	}

	// refresh always re-introspects, even over a fresh entry.
	if _, err := run(append([]string{"schema", "refresh"}, ttl...)...); err != nil {
		t.Fatal(err)
	}
	if *schemaHits != 2 {
		t.Errorf("refresh served from cache; schema requests = %d, want 2", *schemaHits)
	}

	out, err = run(append([]string{"schema", "clear"}, ttl...)...)
	if err != nil || !strings.Contains(out, "removed 1") {
		t.Fatalf("clear = %q, %v", out, err)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "schema-*.json")); len(entries) != 0 {
		t.Errorf("entries left after clear: %v", entries)
	}
}

func TestSchemaCommand_ClearAll(t *testing.T) {
	run, dir, _ := schemaCLI(t)
	other := filepath.Join(dir, "schema-other.json")
	if err := os.WriteFile(other, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("schema", "refresh", "--schema-cache-ttl", "1m"); err != nil {
		t.Fatal(err)
	}
	// --all works even when the cache is disabled for this invocation.
	out, err := run("schema", "clear", "--all", "--schema-cache-ttl", "0")
	if err != nil || !strings.Contains(out, "removed 2") {
		t.Fatalf("clear --all = %q, %v; want 2 removed", out, err)
	}
}

func TestSchemaCommand_RefreshFailsWhenCacheDisabled(t *testing.T) {
	run, _, schemaHits := schemaCLI(t)
	if _, err := run("schema", "refresh", "--schema-cache-ttl", "0"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("refresh with ttl 0: err = %v, want disabled error", err)
	}
	if *schemaHits != 0 {
		t.Errorf("schema requests = %d, want 0", *schemaHits)
	}
}
