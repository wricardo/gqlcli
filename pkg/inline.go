package gqlcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// InlineExecutor runs GraphQL operations in-process against a schema without an HTTP server.
// Create one with NewInlineExecutor; use Execute to run operations.
type InlineExecutor struct {
	srv    *handler.Server
	enrich func(context.Context) context.Context
}

// inlineConfig holds options for NewInlineExecutor.
type inlineConfig struct {
	enrich      func(context.Context) context.Context
	schemaHints bool
}

// Option configures an InlineExecutor.
type Option func(*inlineConfig)

// WithContextEnricher sets a function called before each Execute to enrich the context.
// Use it to inject dataloaders, auth, or other request-scoped values.
func WithContextEnricher(fn func(context.Context) context.Context) Option {
	return func(o *inlineConfig) { o.enrich = fn }
}

// WithSchemaHints enables schemaHint extensions on GraphQL validation errors.
// When a field, argument, or type is not found, the error will include a
// schemaHint extension containing a compact SDL description of the referenced type.
func WithSchemaHints() Option {
	return func(o *inlineConfig) { o.schemaHints = true }
}

// NewInlineExecutor creates an InlineExecutor that runs GraphQL operations in-process.
// No HTTP server is required — operations execute directly against the schema.
func NewInlineExecutor(schema graphql.ExecutableSchema, opts ...Option) *InlineExecutor {
	cfg := &inlineConfig{}
	for _, o := range opts {
		o(cfg)
	}

	srv := handler.NewDefaultServer(schema)

	if cfg.schemaHints {
		d := newSchemaHintDescriber(srv)
		srv.SetErrorPresenter(makeSchemaHintPresenter(d))
	}

	return &InlineExecutor{srv: srv, enrich: cfg.enrich}
}

// Execute runs a GraphQL query or mutation and returns the raw JSON response.
func (e *InlineExecutor) Execute(ctx context.Context, query string, variables map[string]interface{}) (json.RawMessage, error) {
	if e.enrich != nil {
		ctx = e.enrich(ctx)
	}

	body := map[string]interface{}{"query": query}
	if variables != nil {
		body["variables"] = variables
	}
	reqJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/graphql", bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := &inlineRecorder{body: &bytes.Buffer{}, header: make(http.Header)}
	e.srv.ServeHTTP(rr, req)

	return rr.body.Bytes(), nil
}

// ExecuteFunc returns Execute as a plain function value.
// Convenient for passing to builders or adapters that expect this signature.
func (e *InlineExecutor) ExecuteFunc() func(context.Context, string, map[string]interface{}) (json.RawMessage, error) {
	return e.Execute
}

// MaxSchemaHintChars is the maximum number of characters returned in a schemaHint extension.
// Hints longer than this are truncated with a trailing "..." marker.
const MaxSchemaHintChars = 10000

// truncateHint caps hint to MaxSchemaHintChars characters.
func truncateHint(hint string) string {
	if len(hint) <= MaxSchemaHintChars {
		return hint
	}
	return hint[:MaxSchemaHintChars] + "..."
}

// --- schema hint error presenter ---

func makeSchemaHintPresenter(d *Describer) graphql.ErrorPresenterFunc {
	return func(ctx context.Context, err error) *gqlerror.Error {
		gqlErr, ok := err.(*gqlerror.Error)
		if !ok {
			gqlErr = &gqlerror.Error{Message: err.Error()}
		}

		target := extractHintTargetFromErrorMsg(gqlErr.Message)

		if target.typeName != "" {
			var hint string
			var hintErr error

			if target.fieldFilter != "" {
				hint, hintErr = d.DescribeWithFieldFilter(ctx, target.typeName, target.fieldFilter)
			}

			if hint == "" {
				hint, hintErr = d.Describe(ctx, target.typeName)
			}

			if hintErr == nil && hint != "" {
				if gqlErr.Extensions == nil {
					gqlErr.Extensions = map[string]interface{}{}
				}
				gqlErr.Extensions["schemaHint"] = truncateHint(hint)
			}
		}
		return gqlErr
	}
}

// --- internal HTTP recorder ---

type inlineRecorder struct {
	body   *bytes.Buffer
	header http.Header
	status int
}

func (r *inlineRecorder) Header() http.Header         { return r.header }
func (r *inlineRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *inlineRecorder) WriteHeader(s int)           { r.status = s }
