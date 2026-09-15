package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
)

// InlineClient adapts an InlineExecutor to the Client interface, so anything
// built against Client — ScriptRunner most of all — can run in-process against
// a gqlgen schema instead of over HTTP.
type InlineClient struct {
	exec *InlineExecutor
}

// NewInlineClient wraps an InlineExecutor as a Client.
func NewInlineClient(exec *InlineExecutor) *InlineClient {
	return &InlineClient{exec: exec}
}

// Execute runs a query in-process.
//
// The ExecutionMode argument is ignored. It exists to let a caller pick between
// gqlcli's HTTP and in-process transports, and an InlineClient is already one
// of those, so there is nothing left to select.
func (c *InlineClient) Execute(ctx context.Context, _ ExecutionMode, opts QueryOptions) (map[string]interface{}, error) {
	return c.run(ctx, opts.Query, opts.Variables)
}

// ExecuteMutation runs a mutation in-process. As with HTTPClient, a non-nil
// Input is wrapped as {"input": ...}.
func (c *InlineClient) ExecuteMutation(ctx context.Context, _ ExecutionMode, opts MutationOptions) (map[string]interface{}, error) {
	variables := opts.Variables
	if opts.Input != nil {
		if variables == nil {
			variables = make(map[string]interface{})
		}
		variables["input"] = opts.Input
	}
	return c.run(ctx, opts.Mutation, variables)
}

// Introspect runs the full introspection query against the inline schema.
func (c *InlineClient) Introspect(ctx context.Context) (map[string]interface{}, error) {
	return c.run(ctx, FullIntrospectionQuery, nil)
}

// LastResponseMetadata always returns nil: an in-process call has no HTTP
// response to report a status or headers from.
func (c *InlineClient) LastResponseMetadata() *ResponseMetadata { return nil }

// Describer returns a Describer backed by this client's schema.
func (c *InlineClient) Describer() *Describer {
	return NewDescriberFromExecFunc(c.exec.ExecuteFunc())
}

// run returns the whole response envelope, GraphQL errors included, rather than
// failing on them. Script code reads res.errors itself, and a script that can
// see why one call failed can carry on with the rest.
func (c *InlineClient) run(ctx context.Context, operation string, variables map[string]interface{}) (map[string]interface{}, error) {
	raw, err := c.exec.Execute(ctx, operation, variables)
	if err != nil {
		return nil, err
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse inline GraphQL response: %w", err)
	}
	return envelope, nil
}
