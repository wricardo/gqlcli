package gqlcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExploreCommandLocalExecutionAndErrors(t *testing.T) {
	app, out, requests := validateCLI(t)
	path := filepath.Join(t.TempDir(), "target.graphql")
	if err := os.WriteFile(path, []byte(validatorSDL), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		document string
		vars     string
		want     string
		fails    bool
	}{
		{`query($c:String!) { element(coordinate:$c) { coordinate ... on SchemaField { type { signature } } } }`, `{"c":"User.name"}`, `"coordinate":"User.name"`, false},
		{`{ type(name:"User") { name fields { name } } }`, "", `"name":"User"`, false},
		{`query A { overview { typeCount } } query B { type(name:"User") { name } }`, "", `"errors"`, true},
		{`{ __schema { queryType { name } } }`, "", `"name":"Query"`, false},
		{`{ missing }`, "", `"errors"`, true},
		{`{ element(coordinate: }`, "", `"errors"`, true},
	}
	for _, tc := range cases {
		out.Reset()
		args := []string{"gqlcli", "explore", "--schema-file", path, "--format", "json", "--strict"}
		if tc.vars != "" {
			args = append(args, "--variables", tc.vars)
		}
		args = append(args, tc.document)
		code := exitCode(t, app.Run(args))
		if (code != 0) != tc.fails {
			t.Errorf("explore %q: exit = %d; response = %s", tc.document, code, out.String())
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("explore %q: missing %s in %s", tc.document, tc.want, out.String())
		}
		var response map[string]interface{}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Errorf("not a GraphQL JSON response: %v: %s", err, out.String())
		}
	}
	if *requests != 0 {
		t.Fatalf("offline explorer executed %d remote operations", *requests)
	}
}

func TestExploreCommandUsesCacheWithoutRemoteExecution(t *testing.T) {
	app, out, requests := validateCLI(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	query := `{ overview { queryCount queryRoot { name } } }`
	args := []string{"gqlcli", "explore", "--schema-cache-ttl", "10m", "--format", "json", query}
	if err := app.Run(args); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"queryCount"`) {
		t.Fatalf("unexpected response: %s", out.String())
	}
	initial := *requests
	if initial != 1 {
		t.Fatalf("initial introspection requests = %d, want 1", initial)
	}
	out.Reset()
	offlineArgs := []string{"gqlcli", "explore", "--schema-cache-ttl", "10m", "--offline", "--format", "json", query}
	if err := app.Run(offlineArgs); err != nil {
		t.Fatal(err)
	}
	if *requests != initial {
		t.Fatalf("offline explorer sent %d unexpected requests", *requests-initial)
	}
}

func TestExplorePrintSchemaField(t *testing.T) {
	app, out, requests := validateCLI(t)
	if err := app.Run([]string{"gqlcli", "explore", "--print-schema"}); err != nil {
		t.Fatal(err)
	}
	full := out.String()
	if full != ExplorerSDL() {
		t.Fatalf("bare --print-schema changed its output")
	}

	for _, tc := range []struct {
		field string
		want  []string
	}{
		{"operations", []string{"operations(kind: OperationKind", "type Operation", "type SchemaField", "type TypeRef", "enum OperationKind"}},
		{"paths", []string{"paths(from: String!", "type SchemaPathConnection", "type SchemaPath", "type PathSegment", "type SchemaEdge", "interface SchemaElement", "enum PathTraversal"}},
		{"reachableFields", []string{"reachableFields(matching: String!", "type ReachableFieldConnection", "type ReachableField", "type SchemaPathConnection", "type PathSegment", "type SchemaEdge"}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out.Reset()
			if err := app.Run([]string{"gqlcli", "explore", "--print-schema-field", tc.field}); err != nil {
				t.Fatal(err)
			}
			scoped := out.String()
			if len(scoped) >= len(full)*85/100 {
				t.Errorf("scoped schema is not substantially smaller: %d vs %d bytes", len(scoped), len(full))
			}
			for _, want := range tc.want {
				if !strings.Contains(scoped, want) {
					t.Errorf("schema for %s missing %q", tc.field, want)
				}
			}
			if strings.Contains(scoped, "\n  overview:") || strings.Contains(scoped, "\n  validate(") {
				t.Error("scoped output includes unrelated root fields")
			}
		})
	}
	out.Reset()
	err := app.Run([]string{"gqlcli", "explore", "--print-schema-field", "notAField"})
	if err == nil || !strings.Contains(err.Error(), "unknown explorer root field") || !strings.Contains(err.Error(), "reachableFields") {
		t.Errorf("unexpected invalid scope error: %v", err)
	}
	out.Reset()
	err = app.Run([]string{"gqlcli", "explore", "--print-schema-field", ""})
	if err == nil || !strings.Contains(err.Error(), "unknown explorer root field") {
		t.Errorf("unexpected empty scope error: %v", err)
	}
	if *requests != 0 {
		t.Errorf("printing explorer schema sent %d target requests", *requests)
	}
}
