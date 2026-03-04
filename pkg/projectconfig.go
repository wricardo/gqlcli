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
	Default      string               `json:"default"`
	Environments map[string]EnvConfig `json:"environments"`
}

// EnvConfig holds per-environment connection settings.
type EnvConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
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
