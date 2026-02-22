package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// HTTPClient is a GraphQL client that executes operations via HTTP
type HTTPClient struct {
	config *Config
	client *resty.Client
}

// NewHTTPClient creates a new HTTP GraphQL client
func NewHTTPClient(cfg *Config) *HTTPClient {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if cfg.Timeout == 0 {
		timeout = 30 * time.Second
	}

	restClient := resty.New().SetTimeout(timeout)

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

	// Check for errors in response
	if errors, ok := result["errors"]; ok {
		errorMsg := formatGraphQLErrors(errors)
		queryMsg := fmt.Sprintf("\n\n📝 Query that caused the error:\n%s", formatQueryForError(query))
		return nil, fmt.Errorf("%s%s", errorMsg, queryMsg)
	}

	return result, nil
}

// formatGraphQLErrors formats GraphQL errors for display
func formatGraphQLErrors(errors interface{}) string {
	errorList, ok := errors.([]interface{})
	if !ok {
		return fmt.Sprintf("%v", errors)
	}

	var formattedErrors []string
	for i, err := range errorList {
		errMap, ok := err.(map[string]interface{})
		if !ok {
			formattedErrors = append(formattedErrors, fmt.Sprintf("  %d. %v", i+1, err))
			continue
		}

		var parts []string

		// Get the main error message
		if msg, ok := errMap["message"].(string); ok {
			parts = append(parts, fmt.Sprintf("  ❌ %d. %s", i+1, msg))
		} else {
			parts = append(parts, fmt.Sprintf("  ❌ %d. Unknown error", i+1))
		}

		// Add field path if available
		if path, ok := errMap["path"].([]interface{}); ok && len(path) > 0 {
			var pathStrs []string
			for _, p := range path {
				pathStrs = append(pathStrs, fmt.Sprintf("%v", p))
			}
			parts = append(parts, fmt.Sprintf("     📂 Path: %s", strings.Join(pathStrs, ".")))
		}

		// Add extensions if available
		if extensions, ok := errMap["extensions"].(map[string]interface{}); ok {
			if code, ok := extensions["code"].(string); ok {
				parts = append(parts, fmt.Sprintf("     🏷️  Code: %s", code))
			}
			if positions, ok := extensions["positions"].([]interface{}); ok && len(positions) > 0 {
				parts = append(parts, fmt.Sprintf("     📍 Position: %v", positions[0]))
			}
		}

		formattedErrors = append(formattedErrors, strings.Join(parts, "\n"))
	}

	return "\n🚨 GraphQL Validation/Execution Errors:\n" + strings.Join(formattedErrors, "\n\n")
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
