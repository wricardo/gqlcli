package gqlcli

import "context"

// Config holds the CLI configuration
type Config struct {
	URL    string // GraphQL endpoint URL (default: http://localhost:8080/graphql)
	Format string // Output format: json, table, compact, toon, llm (default: json)
	Pretty bool   // Pretty-print JSON output

	// Authentication
	Token string // Bearer token for requests
	Auth  AuthConfig

	// HTTP client settings
	Timeout int  // Request timeout in seconds (default: 30)
	Debug   bool // Enable debug logging (logs requests/responses)
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Enabled bool
	Type    string // bearer, api-key
	Token   string
}

// QueryOptions holds options for query execution
type QueryOptions struct {
	Query         string                 // GraphQL query string
	Variables     map[string]interface{} // Query variables
	OperationName string                 // Named operation to execute
}

// MutationOptions holds options for mutation execution
type MutationOptions struct {
	Mutation      string                 // GraphQL mutation string
	Variables     map[string]interface{} // Mutation variables
	OperationName string                 // Named operation to execute
	Input         interface{}            // Input object (auto-wrapped as {"input": {...}})
}

// GraphQLRequest is the standard GraphQL request format
type GraphQLRequest struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
}

// GraphQLResponse is the standard GraphQL response format
type GraphQLResponse struct {
	Data   map[string]interface{} `json:"data,omitempty"`
	Errors []GraphQLError         `json:"errors,omitempty"`
}

// GraphQLError represents a GraphQL error
type GraphQLError struct {
	Message    string                 `json:"message"`
	Path       []interface{}          `json:"path,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// ExecutionMode determines how the query is executed
type ExecutionMode int

const (
	ExecutionModeInline ExecutionMode = iota
	ExecutionModeHTTP
)

// Client executes GraphQL operations
type Client interface {
	Execute(ctx context.Context, mode ExecutionMode, opts QueryOptions) (map[string]interface{}, error)
	ExecuteMutation(ctx context.Context, mode ExecutionMode, opts MutationOptions) (map[string]interface{}, error)
	Introspect(ctx context.Context) (map[string]interface{}, error)
}

// Formatter formats query results
type Formatter interface {
	Format(data map[string]interface{}) (string, error)
	Name() string
}

// FormatterRegistry manages available formatters
type FormatterRegistry interface {
	Register(name string, formatter Formatter) error
	Get(name string) (Formatter, error)
	List() []string
}
