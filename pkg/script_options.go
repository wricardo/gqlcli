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
	// Type is the operation kind the document actually declares —
	// OperationQuery, OperationMutation or OperationSubscription — not the
	// gql helper the script happened to call. It falls back to the helper's
	// own kind only when the document cannot be parsed.
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

// WithReadOnly rejects every mutation the script dispatches. The kind is taken
// from the parsed document, not from the helper the script called, so
// gql.query("mutation { ... }") is blocked too. A document that cannot be
// parsed is refused rather than sent, since an unclassifiable operation is
// exactly what a read-only policy must not wave through.
//
// The rejection surfaces in JavaScript as a catchable throw.
func WithReadOnly(readOnly bool) ScriptOption {
	return func(c *scriptConfig) { c.readOnly = readOnly }
}

// WithOnRequest registers a callback invoked for every operation a script
// attempts, before any policy check. Operations later blocked by WithReadOnly
// or WithApprover are reported here too; use WithOnResponse for the outcome.
//
// Within one run the callback is invoked from a single goroutine — gql.each
// interleaves its workers inside one JavaScript runtime rather than spreading
// them across goroutines — so it needs no locking to observe a single run.
// Concurrent RunSource calls on a shared runner do run in parallel, so a
// callback accumulating state across runs must guard it.
func WithOnRequest(fn func(RequestInfo)) ScriptOption {
	return func(c *scriptConfig) { c.onRequest = fn }
}

// WithOnResponse registers a callback invoked after each attempted operation
// settles, with the result or the error that blocked or failed it. It carries
// the same threading guarantee as WithOnRequest.
func WithOnResponse(fn func(RequestInfo, map[string]interface{}, error)) ScriptOption {
	return func(c *scriptConfig) { c.onResponse = fn }
}

// WithApprover gates each operation after WithReadOnly and the operation
// budget. Returning a non-nil error aborts that one operation; the script may
// catch it and continue. Use it to prompt a human or apply a host policy.
//
// It runs on the goroutine blocked inside RunSource, so an approver that waits
// on a human holds that script still while it waits — and only that script.
func WithApprover(fn func(context.Context, RequestInfo) error) ScriptOption {
	return func(c *scriptConfig) { c.approver = fn }
}

// LimitedWriter passes at most a fixed number of bytes through to an
// underlying writer and discards the rest, reporting whether anything was
// dropped. Build one with LimitWriter.
type LimitedWriter struct {
	w         io.Writer
	limit     int64
	written   int64
	truncated bool
}

// LimitWriter caps what a script can write through WithStdout/WithStderr.
//
// Capping at the writer, rather than trimming the buffer afterwards, is what
// actually bounds memory: a script that logs inside a loop produces its whole
// run's worth of output before anyone gets to trim it. A limit of zero or less
// discards everything.
//
// It is not safe for concurrent use, which suits its purpose — script console
// output arrives on the runtime's single goroutine.
func LimitWriter(w io.Writer, limit int64) *LimitedWriter {
	return &LimitedWriter{w: w, limit: limit}
}

// Write implements io.Writer. Short writes are never reported to the script:
// it returns len(p) even when part of p was dropped, since a discarded log line
// is not a script error.
func (l *LimitedWriter) Write(p []byte) (int, error) {
	room := l.limit - l.written
	if room <= 0 {
		l.truncated = true
		return len(p), nil
	}
	chunk := p
	if int64(len(chunk)) > room {
		chunk = chunk[:room]
		l.truncated = true
	}
	n, err := l.w.Write(chunk)
	l.written += int64(n)
	if err != nil {
		return n, err
	}
	return len(p), nil
}

// Truncated reports whether any output was discarded.
func (l *LimitedWriter) Truncated() bool { return l.truncated }

// Written reports how many bytes reached the underlying writer.
func (l *LimitedWriter) Written() int64 { return l.written }

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
