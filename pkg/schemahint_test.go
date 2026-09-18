package gqlcli

import (
	"fmt"
	"strings"
	"testing"
)

func hintFor(t *testing.T, document string) (string, string) {
	t.Helper()
	v, err := NewSchemaValidatorFromSDL(roundTripSDL)
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromSDL: %v", err)
	}
	result := v.Validate(document)
	if result.Valid {
		t.Fatalf("document validated unexpectedly: %s", document)
	}
	if len(result.Errors) == 0 {
		t.Fatalf("no errors for %s", document)
	}
	return result.Errors[0].SchemaHint, result.Errors[0].Message
}

func TestSchemaHintCoversErrorShapes(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     []string
		unwanted []string
	}{
		{
			// Nothing in User resembles "nope", so the filter finds no members
			// and the whole type is shown instead of an empty hint.
			name:     "unknown field falls back to the whole type",
			document: `{ me { nope } }`,
			want:     []string{"type User implements Node & Timestamped", "name: String!", "id: ID!"},
			unwanted: []string{"# Closest matches"},
		},
		{
			name:     "unknown field narrows to similar members",
			document: `{ me { nam } }`,
			want:     []string{"# Closest matches", "name: String!"},
			unwanted: []string{"id: ID!"},
		},
		{
			// The enum shape is not one of the executed path's patterns; it is
			// reached by resolving a quoted name against the schema.
			name:     "bad enum value hints the enum",
			document: `{ me { role(fallback: NOPE) } }`,
			want:     []string{"enum Role", "ADMIN", "GUEST"},
		},
		{
			name:     "unknown argument keeps argument signatures",
			document: `{ me { role(nope: 1) } }`,
			want:     []string{"# Closest matches", "role(fallback: Role! = GUEST): Role!"},
		},
		{
			name:     "unknown input field hints the input type",
			document: `{ node(id: "1") { ... on Team { members(filter: {nope: 1}) { id } } } }`,
			want:     []string{"input MemberFilter", "limit: Int", "role: Role"},
		},
		{
			// The quoted name here is a field of the parent type, so filtering by
			// it would surface unrelated members that share a substring.
			name:     "needs-subfield hints the whole type",
			document: `{ me }`,
			want:     []string{"type User", "id: ID!", "name: String!"},
			unwanted: []string{"# Closest matches"},
		},
		{
			name:     "union has no members to narrow",
			document: `{ search(term: "x") { id } }`,
			want:     []string{"union SearchResult = User | Team"},
			unwanted: []string{"# Closest matches"},
		},
		{
			name:     "fragment on an impossible type hints a named type",
			document: `{ me { ... on Team { id } } }`,
			want:     []string{"type User"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint, message := hintFor(t, tc.document)
			if hint == "" {
				t.Fatalf("no schema hint for %q", message)
			}
			for _, want := range tc.want {
				if !strings.Contains(hint, want) {
					t.Errorf("hint missing %q for %q:\n%s", want, message, hint)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(hint, unwanted) {
					t.Errorf("hint unexpectedly contains %q for %q:\n%s", unwanted, message, hint)
				}
			}
		})
	}
}

// A message naming no schema type must produce no hint rather than a guess.
func TestSchemaHintAbsentWhenNoTypeIsNamed(t *testing.T) {
	for _, document := range []string{
		`{ me { name `,                       // syntax error
		`query ($unused: ID!) { me { id } }`, // unused variable
		`{ me { ... on Nope { id } } }`,      // the named type does not exist
	} {
		hint, message := hintFor(t, document)
		if hint != "" {
			t.Errorf("document %q produced a hint for %q:\n%s", document, message, hint)
		}
	}
}

// Builtin scalars are never worth hinting: "scalar String" tells nobody anything.
func TestSchemaHintSkipsBuiltinScalars(t *testing.T) {
	v, err := NewSchemaValidatorFromSDL(roundTripSDL)
	if err != nil {
		t.Fatal(err)
	}
	if hint := schemaHintFromSchema(v.Schema(), `Expected type "String!", found 1.`); hint != "" {
		t.Errorf("hinted a builtin scalar: %q", hint)
	}
	if hint := schemaHintFromSchema(v.Schema(), `something about "__Type"`); hint != "" {
		t.Errorf("hinted a reserved introspection type: %q", hint)
	}
	if hint := schemaHintFromSchema(nil, `Cannot query field "nope" on type "User".`); hint != "" {
		t.Errorf("hinted without a schema: %q", hint)
	}
}

// The hint belongs at extensions.schemaHint so one jq expression works against
// both a validation result and an executed response.
func TestSchemaHintInResultMap(t *testing.T) {
	v, err := NewSchemaValidatorFromSDL(roundTripSDL)
	if err != nil {
		t.Fatal(err)
	}

	out := v.Validate(`{ me { nope } }`).Map()
	errs, ok := out["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("map has no errors: %v", out)
	}
	first, ok := errs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("error entry = %T, want a map", errs[0])
	}
	extensions, ok := first["extensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("error entry has no extensions: %v", first)
	}
	hint, ok := extensions["schemaHint"].(string)
	if !ok || !strings.Contains(hint, "type User") {
		t.Errorf("extensions.schemaHint = %v, want SDL for User", extensions["schemaHint"])
	}

	// A hintless error must not carry an empty extensions object.
	syntax := v.Validate(`{ me { name `).Map()
	syntaxErrs, _ := syntax["errors"].([]interface{})
	if len(syntaxErrs) == 0 {
		t.Fatal("no errors for a truncated document")
	}
	if entry, ok := syntaxErrs[0].(map[string]interface{}); ok {
		if _, present := entry["extensions"]; present {
			t.Errorf("hintless error carries extensions: %v", entry)
		}
	}
}

// The human rendering indents the hint under its error so multiple errors stay
// readable.
func TestSchemaHintInTextOutput(t *testing.T) {
	v, err := NewSchemaValidatorFromSDL(roundTripSDL)
	if err != nil {
		t.Fatal(err)
	}

	rendered := v.Validate(`{ me { nope } }`).String()
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("rendered result has no hint block:\n%s", rendered)
	}
	if !strings.HasPrefix(lines[0], "1:8: ") {
		t.Errorf("first line = %q, want the positioned error", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  ") {
		t.Errorf("hint line = %q, want it indented", lines[1])
	}
}

func TestSchemaHintIsTruncated(t *testing.T) {
	var sdl strings.Builder
	sdl.WriteString("type Query { me: Huge }\ntype Huge {\n")
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&sdl, "  fieldNumber%d: String\n", i)
	}
	sdl.WriteString("}\n")

	v, err := NewSchemaValidatorFromSDL(sdl.String())
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromSDL: %v", err)
	}

	result := v.Validate(`{ me { nope } }`)
	hint := result.Errors[0].SchemaHint
	if len(hint) > MaxSchemaHintChars+3 {
		t.Errorf("hint is %d chars, want it capped near %d", len(hint), MaxSchemaHintChars)
	}
	if !strings.HasSuffix(hint, "...") {
		t.Error("a truncated hint should end with the ... marker")
	}
}
