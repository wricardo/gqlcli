package gqlcli

import (
	"encoding/json"
	"fmt"
	"os"
)

// ProjectConfig represents the .gqlcli.json configuration file.
//
// Example:
//
//	{
//	  "default": "local",
//	  "environments": {
//	    "local": {
//	      "url": "http://localhost:8080/graphql",
//	      "headers": { "Authorization": "Bearer dev-token" }
//	    },
//	    "prod": {
//	      "url": "https://api.example.com/graphql",
//	      "headers": { "Authorization": "Bearer prod-token" }
//	    }
//	  }
//	}
type ProjectConfig struct {
	Default      string                     `json:"default"`
	Environments map[string]EnvConfig       `json:"environments"`
	Operations   map[string]NamedOperation  `json:"operations,omitempty"`
}

// NamedOperation is a saved GraphQL query or mutation stored in .gqlcli.json.
// Execute with --op <name> on the query or mutation commands.
type NamedOperation struct {
	Type     string                 `json:"type"`               // "query" or "mutation"
	Query    string                 `json:"query,omitempty"`    // set when Type == "query"
	Mutation string                 `json:"mutation,omitempty"` // set when Type == "mutation"
	Defaults map[string]interface{} `json:"defaults,omitempty"` // default variables; --variables overrides
}

// EnvConfig holds per-environment connection settings.
type EnvConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Login   *EnvLoginConfig   `json:"login,omitempty"`
}

// EnvLoginConfig stores the login mutation and token path for an environment,
// so gqlcli login can re-authenticate without repeating the mutation string.
type EnvLoginConfig struct {
	Mutation  string `json:"mutation"`
	TokenPath string `json:"token_path"`
}

// LoadProjectConfig reads .gqlcli.json from the current directory.
// Returns nil, nil if the file does not exist.
func LoadProjectConfig() (*ProjectConfig, error) {
	data, err := os.ReadFile(".gqlcli.json")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading .gqlcli.json: %w", err)
	}
	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing .gqlcli.json: %w", err)
	}
	return &cfg, nil
}

// Resolve returns the EnvConfig for the given environment name.
// If envName is empty, the default environment is used.
// Returns nil, nil when envName is empty and no default is configured.
func (p *ProjectConfig) Resolve(envName string) (*EnvConfig, error) {
	if envName == "" {
		envName = p.Default
	}
	if envName == "" {
		return nil, nil
	}
	env, ok := p.Environments[envName]
	if !ok {
		return nil, fmt.Errorf("environment %q not found in .gqlcli.json", envName)
	}
	return &env, nil
}
