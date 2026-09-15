package gqlcli

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// ErrScriptInterrupted reports that a script was stopped by context cancellation
// or a WithTimeout deadline rather than by throwing. Hosts embedding
// ScriptRunner use errors.Is to tell a runaway script apart from a script bug.
var ErrScriptInterrupted = errors.New("script interrupted")

// ErrReadOnly is returned to the script when WithReadOnly(true) blocks a mutation.
var ErrReadOnly = errors.New("mutations are disabled: script runner is read-only")

// ErrOperationBudget is returned to the script when WithMaxOperations is exceeded.
var ErrOperationBudget = errors.New("script exceeded its GraphQL operation budget")

// RequestInfo describes a single GraphQL operation a script is attempting.
type RequestInfo struct {
	// Type is "query" or "mutation".
	Type string
	// Operation is the GraphQL document text.
	Operation string
	// OperationName is the operation name, if the script supplied one.
	OperationName string
	// Variables are the operation variables. Callbacks receive a copy, so
	// mutating this map does not change what is sent.
	Variables map[string]interface{}
}

// ScriptOption configures a ScriptRunner.
type ScriptOption func(*scriptConfig)

type scriptConfig struct {
	stdout        io.Writer
	stderr        io.Writer
	timeout       time.Duration
	maxOperations int
	readOnly      bool
	onRequest     func(RequestInfo)
	onResponse    func(RequestInfo, map[string]interface{}, error)
	approver      func(context.Context, RequestInfo) error
}

func defaultScriptConfig() scriptConfig {
	return scriptConfig{stdout: os.Stdout, stderr: os.Stderr}
}

// WithStdout redirects script console.log output. Defaults to os.Stdout.
// Hosts that reserve their own stdout for structured output must set this.
func WithStdout(w io.Writer) ScriptOption {
	return func(c *scriptConfig) {
		if w != nil {
			c.stdout = w
		}
	}
}

// WithStderr redirects script console.error output. Defaults to os.Stderr.
func WithStderr(w io.Writer) ScriptOption {
	return func(c *scriptConfig) {
		if w != nil {
			c.stderr = w
		}
	}
}

// WithTimeout bounds a single run. It derives a deadline from the context
// passed to RunSource/RunFile; cancelling that context still works on its own.
func WithTimeout(d time.Duration) ScriptOption {
	return func(c *scriptConfig) { c.timeout = d }
}

// WithMaxOperations caps how many GraphQL operations one run may attempt.
// Operations blocked by WithReadOnly or WithApprover count against the budget,
// so a script cannot spin indefinitely against a rejecting policy.
// Zero or negative means unlimited.
func WithMaxOperations(n int) ScriptOption {
	return func(c *scriptConfig) { c.maxOperations = n }
}

// WithReadOnly rejects every mutation the script dispatches, including those
// issued through gql.request. The rejection surfaces in JavaScript as a
// catchable throw.
func WithReadOnly(readOnly bool) ScriptOption {
	return func(c *scriptConfig) { c.readOnly = readOnly }
}

// WithOnRequest registers a callback invoked for every operation a script
// attempts, before any policy check. Operations later blocked by WithReadOnly
// or WithApprover are reported here too; use WithOnResponse for the outcome.
func WithOnRequest(fn func(RequestInfo)) ScriptOption {
	return func(c *scriptConfig) { c.onRequest = fn }
}

// WithOnResponse registers a callback invoked after each attempted operation
// settles, with the result or the error that blocked or failed it.
func WithOnResponse(fn func(RequestInfo, map[string]interface{}, error)) ScriptOption {
	return func(c *scriptConfig) { c.onResponse = fn }
}

// WithApprover gates each operation after WithReadOnly and the operation
// budget. Returning a non-nil error aborts that one operation; the script may
// catch it and continue. Use it to prompt a human or apply a host policy.
func WithApprover(fn func(context.Context, RequestInfo) error) ScriptOption {
	return func(c *scriptConfig) { c.approver = fn }
}

func copyVariables(vars map[string]interface{}) map[string]interface{} {
	if vars == nil {
		return nil
	}
	out := make(map[string]interface{}, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}
