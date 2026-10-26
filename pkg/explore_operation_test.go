package gqlcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExploreSelectsNamedOperation(t *testing.T) {
	app, out, requests := validateCLI(t)
	path := filepath.Join(t.TempDir(), "target.graphql")
	if err := os.WriteFile(path, []byte(validatorSDL), 0600); err != nil {
		t.Fatal(err)
	}
	doc := `query A { type(name:"Query") { name } } query B { type(name:"User") { name } }`
	if err := app.Run([]string{"gqlcli", "explore", "--schema-file", path, "--format", "json", "--operation", "B", doc}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name":"User"`) {
		t.Fatalf("named operation not selected: %s", out.String())
	}
	if *requests != 0 {
		t.Fatalf("explorer made %d remote requests", *requests)
	}
}
