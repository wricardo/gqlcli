package gqlcli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

const validatorSDL = `
type Query {
  user(id: ID!): User
  me: User
}
type User {
  id: ID!
  name: String!
}
`

func testValidator(t *testing.T) *SchemaValidator {
	t.Helper()
	v, err := NewSchemaValidatorFromSDL(validatorSDL)
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromSDL: %v", err)
	}
	return v
}

// introspectionServer answers the introspection query with the result gqlgen
// would produce for schemaSDL, and records how many requests it received.
func introspectionServer(t *testing.T, schemaSDL string) (*httptest.Server, *int) {
	t.Helper()
	schema := gqlparser.MustLoadSchema(&ast.Source{Name: "server", Input: schemaSDL})
	payload, err := json.Marshal(introspectSchema(schema))
	if err != nil {
		t.Fatalf("marshal introspection: %v", err)
	}

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		query, _ := body["query"].(string)
		if !strings.Contains(query, "__schema") {
			t.Errorf("server received a non-introspection operation: %s", query)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func TestValidateReportsPositionAndRule(t *testing.T) {
	result := testValidator(t).Validate("query Broken {\n  me {\n    nope\n  }\n}")

	if result.Valid {
		t.Fatal("Valid = true, want false for an unknown field")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(result.Errors), result.Errors)
	}

	err := result.Errors[0]
	if err.Line != 3 || err.Column != 5 {
		t.Errorf("position = %d:%d, want 3:5", err.Line, err.Column)
	}
	if !strings.Contains(err.Message, "nope") {
		t.Errorf("message = %q, want it to name the unknown field", err.Message)
	}
	if err.Rule == "" {
		t.Error("Rule is empty, want the failing validation rule")
	}
	if !strings.HasPrefix(err.String(), "3:5: ") {
		t.Errorf("String() = %q, want a line:column prefix", err.String())
	}
}

func TestValidateReportsOperations(t *testing.T) {
	result := testValidator(t).Validate("query A { me { id } }\nquery B { me { name } }")

	if !result.Valid {
		t.Fatalf("Valid = false, want true: %v", result.Errors)
	}
	if got := strings.Join(result.OperationNames, ","); got != "A,B" {
		t.Errorf("OperationNames = %v, want [A B]", result.OperationNames)
	}
	if got := strings.Join(result.OperationKinds, ","); got != "query,query" {
		t.Errorf("OperationKinds = %v, want [query query]", result.OperationKinds)
	}
}

// A syntax error leaves nothing to report operations from, but must still
// produce a positioned error rather than an empty result.
func TestValidateHandlesSyntaxError(t *testing.T) {
	result := testValidator(t).Validate("{ me { id ")

	if result.Valid {
		t.Fatal("Valid = true, want false for a truncated document")
	}
	if len(result.Errors) == 0 {
		t.Fatal("no errors reported for a truncated document")
	}
	if len(result.OperationNames) != 0 {
		t.Errorf("OperationNames = %v, want none for an unparseable document", result.OperationNames)
	}
}

// Variable values are not part of the document, so a document declaring
// required variables is valid on its own.
func TestValidateIgnoresVariableValues(t *testing.T) {
	result := testValidator(t).Validate("query ($id: ID!) { user(id: $id) { name } }")
	if !result.Valid {
		t.Errorf("Valid = false, want true: %v", result.Errors)
	}
}

func TestValidationResultMap(t *testing.T) {
	valid := testValidator(t).Validate("{ me { id } }").Map()
	if valid["valid"] != true {
		t.Errorf("valid map = %v, want valid true", valid)
	}
	if _, ok := valid["errors"]; ok {
		t.Errorf("valid map carries an errors key: %v", valid)
	}

	invalid := testValidator(t).Validate("{ nope }").Map()
	if invalid["valid"] != false {
		t.Errorf("invalid map = %v, want valid false", invalid)
	}
	errs, ok := invalid["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Fatalf("invalid map errors = %v, want a non-empty list", invalid["errors"])
	}
	first, ok := errs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("error entry = %T, want a map", errs[0])
	}
	for _, key := range []string{"message", "line", "column"} {
		if _, ok := first[key]; !ok {
			t.Errorf("error entry missing %q: %v", key, first)
		}
	}

	// The map has to survive a trip through the output formatters.
	formatter, err := NewFormatterRegistry().Get("json")
	if err != nil {
		t.Fatalf("get json formatter: %v", err)
	}
	out, err := formatter.Format(invalid)
	if err != nil {
		t.Fatalf("format validation result: %v", err)
	}
	if !strings.Contains(out, `"valid"`) {
		t.Errorf("formatted result = %s, want a valid key", out)
	}
}

func TestNewSchemaValidatorFromClient(t *testing.T) {
	srv, requests := introspectionServer(t, validatorSDL)

	v, err := NewSchemaValidatorFromClient(context.Background(), NewHTTPClient(&Config{URL: srv.URL}))
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromClient: %v", err)
	}
	if *requests != 1 {
		t.Errorf("server saw %d requests, want 1 introspection call", *requests)
	}

	if result := v.Validate("{ me { id } }"); !result.Valid {
		t.Errorf("valid document rejected: %v", result.Errors)
	}
	if result := v.Validate("{ me { nope } }"); result.Valid {
		t.Error("invalid document accepted")
	}

	// Validating more documents must not cost more round trips.
	if *requests != 1 {
		t.Errorf("server saw %d requests, want the schema fetched once", *requests)
	}
}

func TestNewSchemaValidatorFromURL(t *testing.T) {
	srv, _ := introspectionServer(t, validatorSDL)

	v, err := NewSchemaValidatorFromURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromURL: %v", err)
	}
	if result := v.Validate("{ me { id } }"); !result.Valid {
		t.Errorf("valid document rejected: %v", result.Errors)
	}
}

// SDL() is the offline story: persist it once, rebuild with no network.
func TestSchemaValidatorSDLRebuildsOffline(t *testing.T) {
	srv, requests := introspectionServer(t, validatorSDL)

	fetched, err := NewSchemaValidatorFromURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromURL: %v", err)
	}
	sdl := fetched.SDL()
	if sdl == "" {
		t.Fatal("SDL() is empty after introspecting")
	}

	before := *requests
	offline, err := NewSchemaValidatorFromSDL(sdl)
	if err != nil {
		t.Fatalf("NewSchemaValidatorFromSDL with the saved SDL: %v", err)
	}
	if *requests != before {
		t.Errorf("rebuilding from SDL issued %d extra requests, want none", *requests-before)
	}
	if result := offline.Validate("{ me { nope } }"); result.Valid {
		t.Error("offline validator accepted an unknown field")
	}
	if result := offline.Validate("{ me { id name } }"); !result.Valid {
		t.Errorf("offline validator rejected a valid document: %v", result.Errors)
	}
}

func TestSchemaValidatorGuards(t *testing.T) {
	if _, err := NewSchemaValidatorFromClient(context.Background(), nil); err == nil {
		t.Error("NewSchemaValidatorFromClient(nil) succeeded, want an error")
	}
	if _, err := NewSchemaValidatorFromConfig(context.Background(), nil); err == nil {
		t.Error("NewSchemaValidatorFromConfig(nil) succeeded, want an error")
	}
	if _, err := NewSchemaValidatorFromSDL("type Query { broken: }"); err == nil {
		t.Error("NewSchemaValidatorFromSDL with bad SDL succeeded, want an error")
	}

	// A validator with no schema must report that, not panic.
	result := NewSchemaValidator(nil).Validate("{ me }")
	if result.Valid {
		t.Error("a validator with no schema reported a document valid")
	}
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0].Message, "no schema") {
		t.Errorf("errors = %v, want a missing-schema message", result.Errors)
	}
}
