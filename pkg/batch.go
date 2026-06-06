package gqlcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/itchyny/gojq"
	"github.com/urfave/cli/v2"
)

// BatchRequest is a single operation in a batch, with an optional jq filter.
type BatchRequest struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
	JQ            string                 `json:"jq,omitempty"`
}

// executeBatchHTTP sends a JSON array of operations to the server's batch
// transport and returns the raw response body. If the server supports the
// Batch transport, it processes all operations in a single round-trip.
func (c *HTTPClient) executeBatchHTTP(ctx context.Context, requests []BatchRequest) ([]json.RawMessage, error) {
	if c.config.URL == "" {
		return nil, fmt.Errorf("GraphQL URL is not configured")
	}

	body, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch: %w", err)
	}

	resp, err := c.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		Post(c.config.URL)

	if err != nil {
		return nil, fmt.Errorf("batch request failed: %w", err)
	}

	var results []json.RawMessage
	if err := json.Unmarshal(resp.Body(), &results); err != nil {
		return nil, fmt.Errorf("failed to parse batch response: %w\nBody: %s", err, string(resp.Body()))
	}

	return results, nil
}

// executeNDJSON sends operations as newline-delimited JSON to the server's
// NDJSON transport and returns one raw response per operation.
func (c *HTTPClient) executeNDJSON(ctx context.Context, requests []BatchRequest) ([]json.RawMessage, error) {
	if c.config.URL == "" {
		return nil, fmt.Errorf("GraphQL URL is not configured")
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, req := range requests {
		if err := enc.Encode(req); err != nil {
			return nil, fmt.Errorf("failed to encode ndjson line: %w", err)
		}
	}

	resp, err := c.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/x-ndjson").
		SetBody(buf.Bytes()).
		Post(c.config.URL)

	if err != nil {
		return nil, fmt.Errorf("ndjson request failed: %w", err)
	}

	var results []json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(resp.Body()))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		results = append(results, json.RawMessage(append([]byte(nil), line...)))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading ndjson response: %w", err)
	}

	return results, nil
}

// applyJQ runs a jq expression on a JSON value and returns the filtered
// results. If jqExpr is empty, returns the input unchanged.
func applyJQ(raw json.RawMessage, jqExpr string) ([]json.RawMessage, error) {
	if jqExpr == "" {
		return []json.RawMessage{raw}, nil
	}

	query, err := gojq.Parse(jqExpr)
	if err != nil {
		return nil, fmt.Errorf("jq parse error: %w", err)
	}

	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("jq compile error: %w", err)
	}

	var input interface{}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal for jq: %w", err)
	}

	var results []json.RawMessage
	iter := code.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return nil, fmt.Errorf("jq error: %w", err)
		}
		out, err := gojq.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("jq marshal error: %w", err)
		}
		results = append(results, out)
	}

	return results, nil
}

// GetBatchCommand returns the batch subcommand for CLIBuilder (HTTP mode).
func (b *CLIBuilder) GetBatchCommand() *cli.Command {
	return &cli.Command{
		Name:    "batch",
		Aliases: []string{"b"},
		Usage:   "Execute multiple GraphQL operations in one request",
		Description: "Send multiple queries/mutations in a single HTTP request.\n\n" +
			"Reads operations from stdin (one JSON object per line, NDJSON format) or\n" +
			"from a JSON array file.\n\n" +
			"Modes:\n" +
			"  --ndjson (default)  Send as application/x-ndjson (streaming, one response line per operation)\n" +
			"  --array             Send as JSON array (returns a JSON array of responses)\n\n" +
			"SERVER-SIDE JQ\n" +
			"  Add a \"jq\" field to any operation object. The server applies the expression\n" +
			"  to the full {\"data\":...} response before returning it. This reduces payload\n" +
			"  size and avoids round-tripping data you don't need.\n\n" +
			"  The jq expression receives the full response envelope, so always start from .data:\n" +
			"    .data                                            strip to data object\n" +
			"    .data.users[].name                              pluck a field from every item\n" +
			"    .data.users[] | select(.active)                 filter an array\n" +
			"    .data.users | length                            aggregate (count)\n" +
			"    .data.logs[] | select(.message | test(\"err\"))  regex match\n\n" +
			"  When jq produces multiple values, each becomes a separate response line/element.\n\n" +
			"CLIENT-SIDE JQ\n" +
			"  --jq EXPR   Apply jq to every response after the server returns it.\n" +
			"  Useful when the server does not support server-side jq.\n\n" +
			"Examples:\n" +
			"  # Pipe NDJSON from stdin\n" +
			"  echo '{\"query\":\"{ users { id } }\"}' | gqlcli batch\n\n" +
			"  # Per-operation server-side jq\n" +
			"  printf '{\"query\":\"{ users { id name } }\",\"jq\":\".data.users[].name\"}\\n" +
			"{\"query\":\"{ posts { id } }\",\"jq\":\".data.posts | length\"}\\n' | gqlcli batch\n\n" +
			"  # From a JSON array file (array transport)\n" +
			"  gqlcli batch --array --file operations.json\n\n" +
			"  # Client-side jq on all responses\n" +
			"  echo '{\"query\":\"{ users { id name } }\"}' | gqlcli batch --jq '.data'",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "url",
				Aliases: []string{"u"},
				Usage:   "GraphQL endpoint URL (env: GRAPHQL_URL)",
				Value:   b.config.URL,
				EnvVars: []string{"GRAPHQL_URL"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "Enable debug mode",
				Value:   b.config.Debug,
			},
			insecureFlag(),
			&cli.IntFlag{Name: "timeout", Usage: "Request timeout in seconds (default: 30)", Value: b.config.Timeout},
			&cli.IntFlag{Name: "retry", Usage: "Retry count for transient failures (connection errors, 408, 429, 5xx; default: 0)", Value: b.config.RetryCount},
			&cli.DurationFlag{Name: "retry-delay", Usage: "Delay between retries (e.g. 500ms, 2s; default: 1s when --retry > 0)", Value: b.config.RetryDelay},
			&cli.BoolFlag{Name: "fail-on-graphql-errors", Usage: "Exit non-zero when any response.errors is present", Value: b.config.FailOnGraphQLErrors},
			&cli.BoolFlag{
				Name:  "ndjson",
				Usage: "Use NDJSON transport (default)",
				Value: true,
			},
			&cli.BoolFlag{
				Name:  "array",
				Usage: "Use JSON array batch transport",
			},
			&cli.StringFlag{
				Name:  "file",
				Usage: "Read operations from file instead of stdin",
			},
			&cli.StringFlag{
				Name:  "jq",
				Usage: "Apply jq expression to each response (client-side)",
			},
			&cli.StringFlag{
				Name:  "env",
				Usage: "Environment from .gqlcli.json",
			},
			headerFlag(),
		},
		Action: func(c *cli.Context) error {
			if err := b.applyEnvConfig(c); err != nil {
				return err
			}
			httpClient := NewHTTPClient(b.config)

			// Read input
			var input io.Reader
			if filePath := c.String("file"); filePath != "" {
				f, err := os.Open(filePath)
				if err != nil {
					return fmt.Errorf("failed to open file: %w", err)
				}
				defer f.Close()
				input = f
			} else {
				// Check if stdin has data
				stat, _ := os.Stdin.Stat()
				if (stat.Mode() & os.ModeCharDevice) != 0 {
					return fmt.Errorf("no input: pipe NDJSON to stdin or use --file")
				}
				input = os.Stdin
			}

			useArray := c.Bool("array")
			clientJQ := c.String("jq")

			if useArray {
				return executeBatchArray(c, httpClient, input, clientJQ)
			}
			return executeBatchNDJSON(c, httpClient, input, clientJQ)
		},
	}
}

func executeBatchNDJSON(c *cli.Context, client *HTTPClient, input io.Reader, clientJQ string) error {
	// Read all lines into batch requests
	var requests []BatchRequest
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req BatchRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return fmt.Errorf("invalid JSON on line: %w\nLine: %s", err, string(line))
		}
		requests = append(requests, req)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading input: %w", err)
	}

	if len(requests) == 0 {
		return nil
	}

	results, err := client.executeNDJSON(context.Background(), requests)
	if err != nil {
		return err
	}

	return outputBatchResults(results, requests, clientJQ, c.Bool("fail-on-graphql-errors"))
}

func executeBatchArray(c *cli.Context, client *HTTPClient, input io.Reader, clientJQ string) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	data = bytes.TrimSpace(data)

	var requests []BatchRequest

	// Detect format: JSON array or NDJSON
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &requests); err != nil {
			return fmt.Errorf("invalid JSON array: %w", err)
		}
	} else {
		// Parse as NDJSON into array
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var req BatchRequest
			if err := json.Unmarshal(line, &req); err != nil {
				return fmt.Errorf("invalid JSON: %w", err)
			}
			requests = append(requests, req)
		}
	}

	if len(requests) == 0 {
		fmt.Println("[]")
		return nil
	}

	results, err := client.executeBatchHTTP(context.Background(), requests)
	if err != nil {
		return err
	}

	return outputBatchResults(results, requests, clientJQ, c.Bool("fail-on-graphql-errors"))
}

// outputBatchResults applies client-side jq (if any) and prints results
// as NDJSON (one line per value).
func outputBatchResults(results []json.RawMessage, requests []BatchRequest, clientJQ string, failOnGraphQLErrors bool) error {
	for i, raw := range results {
		// Apply client-side jq if provided (overrides per-request jq for
		// output since server already applied per-request jq).
		jqExpr := clientJQ
		if jqExpr == "" && i < len(requests) {
			// If server didn't handle jq (e.g. standard POST fallback),
			// apply per-request jq client-side.
			// Check if response looks like it was already filtered by
			// looking for the data envelope.
			if requests[i].JQ != "" {
				var check map[string]interface{}
				if json.Unmarshal(raw, &check) == nil {
					if _, hasData := check["data"]; hasData {
						// Server didn't apply jq (still has data envelope)
						jqExpr = requests[i].JQ
					}
				}
			}
		}

		if failOnGraphQLErrors && hasGraphQLErrors(raw) {
			fmt.Println(string(raw))
			return cli.Exit("", 1)
		}

		filtered, err := applyJQ(raw, jqExpr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jq error on result %d: %v\n", i, err)
			// Print unfiltered on error
			fmt.Println(string(raw))
			continue
		}

		for _, item := range filtered {
			fmt.Println(string(item))
		}
	}
	return nil
}

func hasGraphQLErrors(raw json.RawMessage) bool {
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	errs, ok := result["errors"].([]interface{})
	return ok && len(errs) > 0
}

// batchCommand returns the batch subcommand for InlineCommandSet.
func (cs *InlineCommandSet) batchCommand() *cli.Command {
	return &cli.Command{
		Name:    "batch",
		Aliases: []string{"b"},
		Usage:   "Execute multiple GraphQL operations",
		Description: "Execute multiple queries/mutations from stdin (NDJSON format).\n\n" +
			"Each line is a JSON object with \"query\" (required) and optional\n" +
			"\"variables\", \"operationName\", and \"jq\" fields.\n\n" +
			"The \"jq\" field filters the response using jq syntax (client-side).\n\n" +
			"Examples:\n" +
			"  echo '{\"query\":\"{ books { title } }\"}' | myapp batch\n" +
			"  printf '{\"query\":\"{ books { title } }\",\"jq\":\".data.books[].title\"}\\n' | myapp batch",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "jq", Usage: "Apply jq expression to each response"},
			&cli.StringFlag{Name: "file", Usage: "Read operations from file instead of stdin"},
		},
		Action: func(c *cli.Context) error {
			var input io.Reader
			if filePath := c.String("file"); filePath != "" {
				f, err := os.Open(filePath)
				if err != nil {
					return fmt.Errorf("failed to open file: %w", err)
				}
				defer f.Close()
				input = f
			} else {
				stat, _ := os.Stdin.Stat()
				if (stat.Mode() & os.ModeCharDevice) != 0 {
					return fmt.Errorf("no input: pipe NDJSON to stdin or use --file")
				}
				input = os.Stdin
			}

			clientJQ := c.String("jq")

			scanner := bufio.NewScanner(input)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

			for scanner.Scan() {
				line := bytes.TrimSpace(scanner.Bytes())
				if len(line) == 0 {
					continue
				}

				var req BatchRequest
				if err := json.Unmarshal(line, &req); err != nil {
					fmt.Fprintf(os.Stderr, "invalid JSON: %v\n", err)
					continue
				}

				raw, err := cs.exec.Execute(context.Background(), req.Query, req.Variables)
				if err != nil {
					fmt.Fprintf(os.Stderr, "execution error: %v\n", err)
					continue
				}

				jqExpr := clientJQ
				if jqExpr == "" {
					jqExpr = req.JQ
				}

				filtered, err := applyJQ(raw, jqExpr)
				if err != nil {
					fmt.Fprintf(os.Stderr, "jq error: %v\n", err)
					fmt.Println(string(raw))
					continue
				}

				for _, item := range filtered {
					fmt.Println(string(item))
				}
			}

			return scanner.Err()
		},
	}
}
