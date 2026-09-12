package gqlcli

import (
	"context"
	"fmt"
	"os"

	"github.com/dop251/goja"
)

// ScriptRunner executes JavaScript automation against a GraphQL client.
type ScriptRunner struct {
	client Client
}

func NewScriptRunner(client Client) *ScriptRunner {
	return &ScriptRunner{client: client}
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
	rt := goja.New()
	rt.Set("console", map[string]func(...interface{}){
		"log":   func(args ...interface{}) { fmt.Fprintln(os.Stdout, args...) },
		"error": func(args ...interface{}) { fmt.Fprintln(os.Stderr, args...) },
	})

	gqlObj := rt.NewObject()
	if err := gqlObj.Set("query", func(call goja.FunctionCall) goja.Value {
		query, vars, opName, err := parseOperationArgs(call)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		result, err := r.client.Execute(ctx, ExecutionModeHTTP, QueryOptions{
			Query:         query,
			Variables:     vars,
			OperationName: opName,
		})
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return rt.ToValue(result)
	}); err != nil {
		return nil, fmt.Errorf("failed to register gql.query: %w", err)
	}
	if err := gqlObj.Set("mutation", func(call goja.FunctionCall) goja.Value {
		mutation, vars, opName, err := parseOperationArgs(call)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		result, err := r.client.ExecuteMutation(ctx, ExecutionModeHTTP, MutationOptions{
			Mutation:      mutation,
			Variables:     vars,
			OperationName: opName,
		})
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return rt.ToValue(result)
	}); err != nil {
		return nil, fmt.Errorf("failed to register gql.mutation: %w", err)
	}
	if err := gqlObj.Set("request", func(call goja.FunctionCall) goja.Value {
		opType, operation, vars, opName, err := parseRequestArgs(call)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		if opType == "mutation" {
			result, err := r.client.ExecuteMutation(ctx, ExecutionModeHTTP, MutationOptions{
				Mutation:      operation,
				Variables:     vars,
				OperationName: opName,
			})
			if err != nil {
				panic(rt.NewGoError(err))
			}
			return rt.ToValue(result)
		}
		result, err := r.client.Execute(ctx, ExecutionModeHTTP, QueryOptions{
			Query:         operation,
			Variables:     vars,
			OperationName: opName,
		})
		if err != nil {
			panic(rt.NewGoError(err))
		}
		return rt.ToValue(result)
	}); err != nil {
		return nil, fmt.Errorf("failed to register gql.request: %w", err)
	}
	if err := rt.Set("gql", gqlObj); err != nil {
		return nil, fmt.Errorf("failed to expose gql object: %w", err)
	}

	if _, err := rt.RunString(scriptHelpersSource); err != nil {
		return nil, fmt.Errorf("failed to register script helpers: %w", err)
	}

	if _, err := rt.RunScript(sourceName, source); err != nil {
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
