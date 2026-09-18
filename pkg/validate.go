package gqlcli

import (
	"context"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// ValidationError is one problem found in a document.
type ValidationError struct {
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Path    string `json:"path,omitempty"`
}

// String renders the error as "line:column: message", omitting the position
// when the error carries none.
func (e ValidationError) String() string {
	var b strings.Builder
	if e.Line > 0 {
		fmt.Fprintf(&b, "%d:%d: ", e.Line, e.Column)
	}
	if e.Path != "" {
		fmt.Fprintf(&b, "%s: ", e.Path)
	}
	b.WriteString(e.Message)
	return b.String()
}

// ValidationResult is the verdict on a single document.
type ValidationResult struct {
	Valid bool `json:"valid"`

	Errors []ValidationError `json:"errors,omitempty"`

	// OperationNames holds the name of each operation in the document, with an
	// empty string for an anonymous one. Both it and OperationKinds are empty
	// when the document could not be parsed.
	OperationNames []string `json:"operationNames,omitempty"`
	OperationKinds []string `json:"operationKinds,omitempty"`
}

// String renders one error per line.
func (r *ValidationResult) String() string {
	if r.Valid {
		return "valid"
	}
	lines := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		lines = append(lines, e.String())
	}
	return strings.Join(lines, "\n")
}

// Map renders the result in the shape the output formatters consume, so
// validation output can be piped through --format and --jq like any response.
func (r *ValidationResult) Map() map[string]interface{} {
	errs := make([]interface{}, 0, len(r.Errors))
	for _, e := range r.Errors {
		em := map[string]interface{}{"message": e.Message}
		if e.Line > 0 {
			em["line"] = e.Line
			em["column"] = e.Column
		}
		if e.Rule != "" {
			em["rule"] = e.Rule
		}
		if e.Path != "" {
			em["path"] = e.Path
		}
		errs = append(errs, em)
	}

	out := map[string]interface{}{"valid": r.Valid}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	if len(r.OperationNames) > 0 {
		names := make([]interface{}, 0, len(r.OperationNames))
		kinds := make([]interface{}, 0, len(r.OperationKinds))
		for _, n := range r.OperationNames {
			names = append(names, n)
		}
		for _, k := range r.OperationKinds {
			kinds = append(kinds, k)
		}
		out["operationNames"] = names
		out["operationKinds"] = kinds
	}
	return out
}

// SchemaValidator checks documents against a schema without executing them.
//
// Building one costs a single introspection round trip, so reuse it across
// documents. It holds no mutable state and is safe to share between
// goroutines.
type SchemaValidator struct {
	schema *ast.Schema
	sdl    string
}

// NewSchemaValidator validates against an already-parsed schema. Inline
// callers can pass InlineExecutor.Schema() and skip introspection entirely.
func NewSchemaValidator(schema *ast.Schema) *SchemaValidator {
	return &SchemaValidator{schema: schema}
}

// NewSchemaValidatorFromSDL validates against a schema read from SDL — a file
// on disk, or the output of SDL() saved from an earlier introspection.
func NewSchemaValidatorFromSDL(sdl string) (*SchemaValidator, error) {
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: sdl})
	if err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}
	return &SchemaValidator{schema: schema, sdl: sdl}, nil
}

// NewSchemaValidatorFromIntrospection rebuilds the schema from an
// introspection result, accepting either a response envelope or a bare
// __schema map.
func NewSchemaValidatorFromIntrospection(introspection map[string]interface{}) (*SchemaValidator, error) {
	sdl, err := SchemaSDL(introspection)
	if err != nil {
		return nil, err
	}
	v, err := NewSchemaValidatorFromSDL(sdl)
	if err != nil {
		return nil, fmt.Errorf("rebuild schema from introspection: %w", err)
	}
	return v, nil
}

// NewSchemaValidatorFromClient introspects through client and rebuilds the
// schema. It works over either transport, since both the HTTP and inline
// clients satisfy Client.
func NewSchemaValidatorFromClient(ctx context.Context, client Client) (*SchemaValidator, error) {
	if client == nil {
		return nil, fmt.Errorf("schema validator: client is nil")
	}
	introspection, err := client.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	return NewSchemaValidatorFromIntrospection(introspection)
}

// NewSchemaValidatorFromConfig introspects the endpoint described by cfg,
// which carries the token, headers, timeout and TLS settings the request
// needs.
func NewSchemaValidatorFromConfig(ctx context.Context, cfg *Config) (*SchemaValidator, error) {
	if cfg == nil {
		return nil, fmt.Errorf("schema validator: config is nil")
	}
	return NewSchemaValidatorFromClient(ctx, NewHTTPClient(cfg))
}

// NewSchemaValidatorFromURL introspects a public endpoint, for callers who
// have nothing but its URL. Use NewSchemaValidatorFromConfig when the endpoint
// needs auth.
func NewSchemaValidatorFromURL(ctx context.Context, url string) (*SchemaValidator, error) {
	return NewSchemaValidatorFromConfig(ctx, &Config{URL: url})
}

// Schema returns the schema documents are validated against.
func (v *SchemaValidator) Schema() *ast.Schema { return v.schema }

// SDL returns the SDL the validator was built from, empty when it was built
// from an already-parsed schema. Persist it to rebuild the validator later
// without reaching the server again.
func (v *SchemaValidator) SDL() string { return v.sdl }

// Validate parses document and checks it against the schema without executing
// it. Variable values are not part of the document, so they are not checked
// here; a document declaring required variables validates on its own.
func (v *SchemaValidator) Validate(document string) *ValidationResult {
	if v == nil || v.schema == nil {
		return &ValidationResult{
			Errors: []ValidationError{{Message: "no schema available to validate against"}},
		}
	}

	doc, errs := gqlparser.LoadQueryWithRules(v.schema, document, nil)
	result := &ValidationResult{Valid: len(errs) == 0}
	for _, err := range errs {
		result.Errors = append(result.Errors, validationErrorFrom(err))
	}
	if doc != nil {
		for _, op := range doc.Operations {
			result.OperationNames = append(result.OperationNames, op.Name)
			result.OperationKinds = append(result.OperationKinds, string(op.Operation))
		}
	}
	return result
}

func validationErrorFrom(err *gqlerror.Error) ValidationError {
	out := ValidationError{Message: err.Message, Rule: err.Rule}
	if len(err.Locations) > 0 {
		out.Line = err.Locations[0].Line
		out.Column = err.Locations[0].Column
	}
	// Variable and coercion errors carry a path instead of a position.
	if len(err.Path) > 0 {
		out.Path = err.Path.String()
	}
	return out
}
