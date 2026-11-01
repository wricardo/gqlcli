package gqlcli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestExpandFragments_AppendsMissingDeclaration(t *testing.T) {
	document := `query GetUser { user(id: "1") { ...UserFields } }`
	declarations := map[string]string{
		"UserFields": `fragment UserFields on User { id name }`,
		"Unused":     `fragment Unused on User { email }`,
	}

	got, err := ExpandFragments(document, declarations)
	if err != nil {
		t.Fatalf("ExpandFragments() error = %v", err)
	}
	if !strings.Contains(got, `fragment UserFields on User { id name }`) {
		t.Fatalf("expanded document missing UserFields declaration:\n%s", got)
	}
	if strings.Contains(got, "fragment Unused") {
		t.Fatalf("unused declaration was appended:\n%s", got)
	}
	if !strings.HasPrefix(got, document) {
		t.Fatalf("original document was rewritten:\n%s", got)
	}
}

func TestExpandFragments_LeavesDocumentUntouchedWhenFragmentsAreDefined(t *testing.T) {
	document := `query GetUser { user(id: "1") { ...UserFields } }
fragment UserFields on User { id name }`
	declarations := map[string]string{
		"UserFields": `fragment UserFields on User { id email }`,
	}

	got, err := ExpandFragments(document, declarations)
	if err != nil {
		t.Fatalf("ExpandFragments() error = %v", err)
	}
	if got != document {
		t.Fatalf("document changed even though its fragment was defined:\n%s", got)
	}
}

func TestExpandFragments_ResolvesNestedDeclarations(t *testing.T) {
	document := `query GetUser { user(id: "1") { ...UserFields } }`
	declarations := map[string]string{
		"UserFields":    `fragment UserFields on User { id profile { ...ProfileFields } }`,
		"ProfileFields": `fragment ProfileFields on Profile { bio avatarUrl }`,
	}

	got, err := ExpandFragments(document, declarations)
	if err != nil {
		t.Fatalf("ExpandFragments() error = %v", err)
	}
	for _, want := range []string{
		`fragment UserFields on User { id profile { ...ProfileFields } }`,
		`fragment ProfileFields on Profile { bio avatarUrl }`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded document missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "fragment ProfileFields") != 1 {
		t.Fatalf("nested declaration appended more than once:\n%s", got)
	}
}

func TestExpandFragments_MissingDeclarationIsAnError(t *testing.T) {
	document := `query GetUser { user(id: "1") { ...UserFields } }`

	_, err := ExpandFragments(document, map[string]string{
		"Other": `fragment Other on User { id }`,
	})
	if err == nil || !strings.Contains(err.Error(), "UserFields") {
		t.Fatalf("ExpandFragments() error = %v, want one naming UserFields", err)
	}
}

func TestExpandFragments_InvalidDeclarationsAreReported(t *testing.T) {
	document := `query GetUser { user(id: "1") { ...UserFields } }`

	tests := map[string]map[string]string{
		"empty": {
			"UserFields": "  ",
		},
		"not a fragment": {
			"UserFields": `query UserFields { user { id } }`,
		},
		"name mismatch": {
			"UserFields": `fragment ProfileFields on Profile { id }`,
		},
		"syntax error": {
			"UserFields": `fragment UserFields on User { id`,
		},
	}
	for name, declarations := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ExpandFragments(document, declarations); err == nil {
				t.Fatalf("ExpandFragments() error = nil, want declaration error")
			}
		})
	}
}

func TestExpandFragments_NoDeclarationsIsPassThrough(t *testing.T) {
	document := `query GetUser { user(id: "1") { id } }`

	got, err := ExpandFragments(document, nil)
	if err != nil {
		t.Fatalf("ExpandFragments() error = %v", err)
	}
	if got != document {
		t.Fatalf("got %q, want %q", got, document)
	}
}

// cliBuilderWithConfig builds a CLI app whose .gqlcli.json content is injected
// directly, so tests do not depend on the working directory.
func cliBuilderWithConfig(t *testing.T, cfg *ProjectConfig) *cli.App {
	t.Helper()
	builder := NewCLIBuilder(&Config{Format: "json", Timeout: 5})
	builder.projectConfig = cfg
	app := &cli.App{Name: "gqlcli"}
	builder.RegisterCommands(app)
	return app
}

func TestQueryCommand_ExpandsDeclaredFragmentsEndToEnd(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user":{"id":"1"}}}`))
	}))
	defer server.Close()

	app := cliBuilderWithConfig(t, &ProjectConfig{
		Fragments: map[string]string{
			"UserFields": `fragment UserFields on User { id name }`,
		},
		Operations: map[string]NamedOperation{
			"get-user": {
				Type:  "query",
				Query: `query GetUser { user(id: "1") { ...UserFields } }`,
			},
		},
	})

	args := ReorderOSArgs([]string{
		"gqlcli", "query", "--op", "get-user", "--url", server.URL,
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}

	query, _ := gotBody["query"].(string)
	if !strings.Contains(query, "fragment UserFields on User { id name }") {
		t.Fatalf("request query did not include declared fragment:\n%s", query)
	}
}

func TestBatchCommand_ExpandsDeclaredFragmentsEndToEnd(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody += string(body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"data":{"user":{"id":"1"}}}` + "\n"))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "ops.ndjson")
	if err := os.WriteFile(path, []byte(`{"query":"query A { user(id: \"1\") { ...UserFields } }"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write ops file: %v", err)
	}

	app := cliBuilderWithConfig(t, &ProjectConfig{
		Fragments: map[string]string{
			"UserFields": `fragment UserFields on User { id name }`,
		},
	})

	args := ReorderOSArgs([]string{
		"gqlcli", "batch", "--file", path, "--url", server.URL,
	}, app.Commands)
	if err := app.Run(args); err != nil {
		t.Fatalf("app.Run() error = %v", err)
	}

	if !strings.Contains(gotBody, "fragment UserFields on User") {
		t.Fatalf("batch request did not include declared fragment:\n%s", gotBody)
	}
}
