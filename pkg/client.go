package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

var (
	reErrOutputField   = regexp.MustCompile(`Cannot query field "[^"]+" on type "([^"]+)"`)
	reErrInputField    = regexp.MustCompile(`Field "[^"]+" is not defined by type "([^"]+)"`)
	reErrUnknownArg    = regexp.MustCompile(`Unknown argument "[^"]+" on field "([^.]+)\.[^"]+"`)
	reErrNeedsSubfield = regexp.MustCompile(`Field "[^"]+" of type "([^"]+)" must have a selection of subfields`)
)

func extractTypeFromErrorMsg(msg string) string {
	for _, re := range []*regexp.Regexp{reErrOutputField, reErrInputField, reErrUnknownArg} {
		if m := re.FindStringSubmatch(msg); len(m) == 2 {
			return m[1]
		}
	}
	if m := reErrNeedsSubfield.FindStringSubmatch(msg); len(m) == 2 {
		return strings.Trim(m[1], "[]!")
	}
	return ""
}

// HTTPClient is a GraphQL client that executes operations via HTTP
type HTTPClient struct {
	config    *Config
	client    *resty.Client
	describer *Describer
}

func (c *HTTPClient) getDescriber() *Describer {
	if c.describer == nil {
		c.describer = NewDescriberFromHTTPClient(c)
	}
	return c.describer
}

// NewHTTPClient creates a new HTTP GraphQL client
func NewHTTPClient(cfg *Config) *HTTPClient {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if cfg.Timeout == 0 {
		timeout = 30 * time.Second
	}

	restClient := resty.New().SetTimeout(timeout)

	// Enable debug mode if configured
	if cfg.Debug {
		restClient.SetDebug(true)
	}

	// Add auth if configured
	if cfg.Token != "" {
		restClient.SetHeader("Authorization", fmt.Sprintf("Bearer %s", cfg.Token))
	}

	return &HTTPClient{
		config: cfg,
		client: restClient,
	}
}

// Execute runs a GraphQL query via HTTP
func (c *HTTPClient) Execute(ctx context.Context, mode ExecutionMode, opts QueryOptions) (map[string]interface{}, error) {
	if mode != ExecutionModeHTTP {
		return nil, fmt.Errorf("HTTP client only supports ExecutionModeHTTP")
	}

	return c.executeOperation(ctx, opts.Query, opts.Variables, opts.OperationName)
}

// ExecuteMutation runs a GraphQL mutation via HTTP
func (c *HTTPClient) ExecuteMutation(ctx context.Context, mode ExecutionMode, opts MutationOptions) (map[string]interface{}, error) {
	if mode != ExecutionModeHTTP {
		return nil, fmt.Errorf("HTTP client only supports ExecutionModeHTTP")
	}

	// Auto-wrap input if provided
	variables := opts.Variables
	if opts.Input != nil {
		if variables == nil {
			variables = make(map[string]interface{})
		}
		variables["input"] = opts.Input
	}

	return c.executeOperation(ctx, opts.Mutation, variables, opts.OperationName)
}

// Introspect queries the GraphQL schema
func (c *HTTPClient) Introspect(ctx context.Context) (map[string]interface{}, error) {
	query := `
		query IntrospectionQuery {
			__schema {
				queryType { name }
				mutationType { name }
				subscriptionType { name }
				types {
					...FullType
				}
			}
		}

		fragment FullType on __Type {
			kind
			name
			description
			fields(includeDeprecated: true) {
				name
				description
				args {
					...InputValue
				}
				type {
					...TypeRef
				}
				isDeprecated
				deprecationReason
			}
			inputFields {
				...InputValue
			}
			interfaces {
				...TypeRef
			}
			enumValues(includeDeprecated: true) {
				name
				description
				isDeprecated
				deprecationReason
			}
			possibleTypes {
				...TypeRef
			}
		}

		fragment InputValue on __InputValue {
			name
			description
			type { ...TypeRef }
			defaultValue
		}

		fragment TypeRef on __Type {
			kind
			name
			ofType {
				kind
				name
				ofType {
					kind
					name
					ofType {
						kind
						name
					}
				}
			}
		}
	`

	return c.executeOperation(ctx, query, nil, "")
}

// executeOperation is the internal method that handles request/response
func (c *HTTPClient) executeOperation(ctx context.Context, query string, variables map[string]interface{}, operationName string) (map[string]interface{}, error) {
	// Validate URL
	if c.config.URL == "" {
		return nil, fmt.Errorf("GraphQL URL is not configured")
	}

	if !strings.HasPrefix(c.config.URL, "http://") && !strings.HasPrefix(c.config.URL, "https://") {
		return nil, fmt.Errorf("URL must start with http:// or https://")
	}

	// Build request
	request := GraphQLRequest{
		Query: query,
	}
	if variables != nil {
		request.Variables = variables
	}
	if operationName != "" {
		request.OperationName = operationName
	}

	// Execute request
	resp, err := c.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(request).
		Post(c.config.URL)

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Parse response
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w\nBody: %s", err, string(resp.Body()))
	}

	// Check for errors in response; enrich with schema hints and return as typed error.
	if rawErrors, ok := result["errors"].([]interface{}); ok {
		c.enrichErrors(ctx, rawErrors)
		return result, &GraphQLResponseError{Response: result, Query: query}
	}

	return result, nil
}

// enrichErrors attaches schemaHint to each error's extensions map when the server
// did not already provide one and the error message references a known type.
func (c *HTTPClient) enrichErrors(ctx context.Context, errors []interface{}) {
	for _, e := range errors {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		msg, _ := em["message"].(string)
		if msg == "" {
			continue
		}

		// Skip if server already attached a hint.
		if ext, ok := em["extensions"].(map[string]interface{}); ok {
			if _, hasHint := ext["schemaHint"]; hasHint {
				continue
			}
		}

		typeName := extractTypeFromErrorMsg(msg)
		if typeName == "" {
			continue
		}
		hint, err := c.getDescriber().Describe(ctx, typeName)
		if err != nil || hint == "" {
			continue
		}

		ext, ok := em["extensions"].(map[string]interface{})
		if !ok {
			ext = make(map[string]interface{})
			em["extensions"] = ext
		}
		ext["schemaHint"] = hint
	}
}

// formatQueryForError formats the query for error display with line numbers
func formatQueryForError(query string) string {
	lines := strings.Split(query, "\n")
	if len(lines) <= 1 {
		if len(query) > 100 {
			return fmt.Sprintf("   %s... (truncated, length: %d chars)", query[:97], len(query))
		}
		return fmt.Sprintf("   %s", query)
	}

	var result []string
	for i, line := range lines {
		lineNum := fmt.Sprintf("%2d", i+1)
		result = append(result, fmt.Sprintf("   %s | %s", lineNum, line))
	}
	return strings.Join(result, "\n")
}
