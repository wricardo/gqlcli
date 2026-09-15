package gqlcli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dop251/goja"
)

// ScriptRunner executes JavaScript automation against a GraphQL client.
type ScriptRunner struct {
	client Client
	cfg    scriptConfig
}

// NewScriptRunner builds a runner. With no options it writes console output to
// the process stdio and imposes no policy. Hosts embedding the runner should at
// minimum set WithStdout/WithStderr and run under a cancellable context.
func NewScriptRunner(client Client, opts ...ScriptOption) *ScriptRunner {
	cfg := defaultScriptConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &ScriptRunner{client: client, cfg: cfg}
}

// scriptRun holds the state of a single RunSource call. The operation counter
// is per-run, and every gql.* helper funnels through dispatch so policy and
// observability live in one place.
type scriptRun struct {
	runner *ScriptRunner
	rt     *goja.Runtime
	ctx    context.Context
	ops    int
}

// value dispatches info and converts the outcome into a JS value, throwing a
// catchable error into the script when the operation is blocked or fails.
func (s *scriptRun) value(info RequestInfo) goja.Value {
	result, err := s.dispatch(info)
	if err != nil {
		panic(s.rt.NewGoError(err))
	}
	return s.rt.ToValue(result)
}

func (s *scriptRun) dispatch(info RequestInfo) (map[string]interface{}, error) {
	// Callbacks see a copy of the variables so a hook cannot alter what is sent.
	observed := info
	observed.Variables = copyVariables(info.Variables)

	cfg := &s.runner.cfg
	if cfg.onRequest != nil {
		cfg.onRequest(observed)
	}

	result, err := s.gatedExecute(info, observed)

	if cfg.onResponse != nil {
		cfg.onResponse(observed, result, err)
	}
	return result, err
}

func (s *scriptRun) gatedExecute(send, observed RequestInfo) (map[string]interface{}, error) {
	cfg := &s.runner.cfg

	s.ops++
	if cfg.maxOperations > 0 && s.ops > cfg.maxOperations {
		return nil, fmt.Errorf("%w: limit is %d", ErrOperationBudget, cfg.maxOperations)
	}
	if cfg.readOnly && send.Type == "mutation" {
		return nil, ErrReadOnly
	}
	if cfg.approver != nil {
		if err := cfg.approver(s.ctx, observed); err != nil {
			return nil, fmt.Errorf("operation not approved: %w", err)
		}
	}

	if send.Type == "mutation" {
		return s.runner.client.ExecuteMutation(s.ctx, ExecutionModeHTTP, MutationOptions{
			Mutation:      send.Operation,
			Variables:     send.Variables,
			OperationName: send.OperationName,
		})
	}
	return s.runner.client.Execute(s.ctx, ExecutionModeHTTP, QueryOptions{
		Query:         send.Operation,
		Variables:     send.Variables,
		OperationName: send.OperationName,
	})
}

// classifyInterrupt reports a cancellation-flavored error for err, or nil if
// err was not caused by the run's context ending. It covers both an interrupted
// VM and a context error surfaced through an in-flight GraphQL call.
func classifyInterrupt(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if cause, ok := interrupted.Value().(error); ok && cause != nil {
			return fmt.Errorf("%w: %w", ErrScriptInterrupted, cause)
		}
		return ErrScriptInterrupted
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrScriptInterrupted, ctxErr)
	}
	return nil
}

// RunFile executes a JavaScript file and calls fnName(gql, input).
func (r *ScriptRunner) RunFile(ctx context.Context, filePath, fnName string, input interface{}) (interface{}, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script file: %w", err)
	}
	return r.RunSource(ctx, filePath, string(src), fnName, input)
}

// RunSource executes JavaScript source and calls fnName(gql, input).
// The gql argument exposes helpers: query, mutation, request, each.
func (r *ScriptRunner) RunSource(ctx context.Context, sourceName, source, fnName string, input interface{}) (interface{}, error) {
	if r.cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.timeout)
		defer cancel()
	}

	rt := goja.New()

	// Interrupt the VM itself when the context ends; without this a script that
	// never terminates hangs the caller regardless of any deadline. The runtime
	// is per-run, so a late interrupt cannot affect a subsequent run.
	if ctx.Done() != nil {
		finished := make(chan struct{})
		defer close(finished)
		go func() {
			select {
			case <-ctx.Done():
				rt.Interrupt(ctx.Err())
			case <-finished:
			}
		}()
	}

	run := &scriptRun{runner: r, rt: rt, ctx: ctx}

	stdout, stderr := r.cfg.stdout, r.cfg.stderr
	rt.Set("console", map[string]func(...interface{}){
		"log":   func(args ...interface{}) { fmt.Fprintln(stdout, args...) },
		"error": func(args ...interface{}) { fmt.Fprintln(stderr, args...) },
	})

	gqlObj := rt.NewObject()
	if err := gqlObj.Set("query", func(call goja.FunctionCall) goja.Value {
		query, vars, opName, err := parseOperationArgs(call)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return run.value(RequestInfo{
			Type:          "query",
			Operation:     query,
			Variables:     vars,
			OperationName: opName,
		})
	}); err != nil {
		return nil, fmt.Errorf("failed to register gql.query: %w", err)
	}
	if err := gqlObj.Set("mutation", func(call goja.FunctionCall) goja.Value {
		mutation, vars, opName, err := parseOperationArgs(call)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return run.value(RequestInfo{
			Type:          "mutation",
			Operation:     mutation,
			Variables:     vars,
			OperationName: opName,
		})
	}); err != nil {
		return nil, fmt.Errorf("failed to register gql.mutation: %w", err)
	}
	if err := gqlObj.Set("request", func(call goja.FunctionCall) goja.Value {
		opType, operation, vars, opName, err := parseRequestArgs(call)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return run.value(RequestInfo{
			Type:          opType,
			Operation:     operation,
			Variables:     vars,
			OperationName: opName,
		})
	}); err != nil {
		return nil, fmt.Errorf("failed to register gql.request: %w", err)
	}
	if err := rt.Set("gql", gqlObj); err != nil {
		return nil, fmt.Errorf("failed to expose gql object: %w", err)
	}

	if _, err := rt.RunString(scriptHelpersSource); err != nil {
		if interrupted := classifyInterrupt(ctx, err); interrupted != nil {
			return nil, interrupted
		}
		return nil, fmt.Errorf("failed to register script helpers: %w", err)
	}

	if _, err := rt.RunScript(sourceName, source); err != nil {
		if interrupted := classifyInterrupt(ctx, err); interrupted != nil {
			return nil, interrupted
		}
		return nil, fmt.Errorf("failed to evaluate script: %w", err)
	}

	fnValue := rt.Get(fnName)
	fn, ok := goja.AssertFunction(fnValue)
	if !ok {
		return nil, fmt.Errorf("script function %q not found", fnName)
	}

	inputValue := goja.Undefined()
	if input != nil {
		inputValue = rt.ToValue(input)
	}

	result, err := fn(goja.Undefined(), rt.Get("gql"), inputValue)
	if err != nil {
		if interrupted := classifyInterrupt(ctx, err); interrupted != nil {
			return nil, interrupted
		}
		return nil, fmt.Errorf("script %s() failed: %w", fnName, err)
	}
	if goja.IsUndefined(result) || goja.IsNull(result) {
		return nil, nil
	}

	if p, ok := exportedPromise(result); ok {
		switch p.State() {
		case goja.PromiseStateFulfilled:
			return exportJSValue(p.Result()), nil
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("script %s() failed: %s", fnName, jsValueString(p.Result()))
		default:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("%w: %w", ErrScriptInterrupted, ctxErr)
			}
			return nil, fmt.Errorf("script %s() returned a pending Promise; only in-runtime async workflows are supported", fnName)
		}
	}
	return exportJSValue(result), nil
}

func exportedPromise(v goja.Value) (*goja.Promise, bool) {
	exported := v.Export()
	p, ok := exported.(*goja.Promise)
	return p, ok
}

func exportJSValue(v goja.Value) interface{} {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.Export()
}

func jsValueString(v goja.Value) string {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return "unknown error"
	}
	return v.String()
}

const scriptHelpersSource = `
(() => {
  gql.each = async function each(items, worker, options) {
    if (!Array.isArray(items)) {
      throw new Error("gql.each: items must be an array")
    }
    if (typeof worker !== "function") {
      throw new Error("gql.each: worker must be a function")
    }

    options = options || {}
    const parsedConcurrency = Number(options.concurrency)
    const concurrency = Number.isFinite(parsedConcurrency) && parsedConcurrency > 0
      ? Math.max(1, Math.floor(parsedConcurrency))
      : 1
    const stopOnError = options.stopOnError !== false

    let nextIndex = 0
    let success = 0
    let failed = 0
    let aborted = false
    const errors = []

    async function runner() {
      while (true) {
        if (aborted) {
          return
        }
        const idx = nextIndex
        nextIndex += 1
        if (idx >= items.length) {
          return
        }

        const item = items[idx]
        try {
          await worker(item, idx)
          success += 1
        } catch (err) {
          failed += 1
          errors.push({ index: idx, error: String(err) })

          if (typeof options.onError === "function") {
            try {
              await options.onError(err, item, idx)
            } catch (_) {
              // ignore onError failures
            }
          }

          if (stopOnError) {
            aborted = true
            throw err
          }
        }
      }
    }

    const workerCount = Math.min(concurrency, items.length)
    const workers = []
    for (let i = 0; i < workerCount; i++) {
      workers.push(runner())
    }

    if (stopOnError) {
      await Promise.all(workers)
    } else {
      await Promise.allSettled(workers)
    }

    return {
      total: items.length,
      success,
      failed,
      errors,
    }
  }
})()
`

func parseOperationArgs(call goja.FunctionCall) (operation string, variables map[string]interface{}, operationName string, err error) {
	if len(call.Arguments) == 0 {
		return "", nil, "", fmt.Errorf("operation string is required")
	}
	operation, ok := call.Arguments[0].Export().(string)
	if !ok || operation == "" {
		return "", nil, "", fmt.Errorf("operation must be a non-empty string")
	}

	if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) && !goja.IsNull(call.Arguments[1]) {
		raw := call.Arguments[1].Export()
		vars, ok := raw.(map[string]interface{})
		if !ok {
			return "", nil, "", fmt.Errorf("variables must be an object")
		}
		variables = vars
	}

	if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
		opName, ok := call.Arguments[2].Export().(string)
		if !ok {
			return "", nil, "", fmt.Errorf("operationName must be a string")
		}
		operationName = opName
	}

	return operation, variables, operationName, nil
}

func parseRequestArgs(call goja.FunctionCall) (opType, operation string, variables map[string]interface{}, operationName string, err error) {
	if len(call.Arguments) == 0 {
		return "", "", nil, "", fmt.Errorf("request requires an argument")
	}

	if _, ok := call.Arguments[0].Export().(string); ok {
		op, vars, opName, err := parseOperationArgs(call)
		if err != nil {
			return "", "", nil, "", err
		}
		return "query", op, vars, opName, nil
	}

	raw, ok := call.Arguments[0].Export().(map[string]interface{})
	if !ok {
		return "", "", nil, "", fmt.Errorf("request argument must be a string or an object")
	}

	opType = "query"
	if t, ok := raw["type"].(string); ok && t != "" {
		opType = t
	}
	if opType != "query" && opType != "mutation" {
		return "", "", nil, "", fmt.Errorf("request type must be 'query' or 'mutation'")
	}

	if q, ok := raw["query"].(string); ok && q != "" {
		operation = q
	}
	if m, ok := raw["mutation"].(string); ok && m != "" {
		opType = "mutation"
		operation = m
	}
	if operation == "" {
		return "", "", nil, "", fmt.Errorf("request object must include query or mutation")
	}

	if v, exists := raw["variables"]; exists && v != nil {
		vm, ok := v.(map[string]interface{})
		if !ok {
			return "", "", nil, "", fmt.Errorf("request.variables must be an object")
		}
		variables = vm
	}

	if opName, ok := raw["operationName"].(string); ok {
		operationName = opName
	}

	return opType, operation, variables, operationName, nil
}
