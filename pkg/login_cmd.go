package gqlcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

// GetLoginCommand returns the login command for HTTP-mode environments.
func (b *CLIBuilder) GetLoginCommand() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "Authenticate and save token to .gqlcli.json",
		Description: "Runs a login mutation against the environment's URL, extracts a token\n" +
			"from the response, and writes it as an Authorization header in .gqlcli.json.\n\n" +
			"The mutation and token-path can be provided inline or stored in .gqlcli.json\n" +
			"under the environment's 'login' key for future re-authentication.\n\n" +
			"Examples:\n" +
			"  gqlcli login --env prod \\\n" +
			"    --mutation 'mutation Login($email: String!, $password: String!) { login(email: $email, password: $password) { token } }' \\\n" +
			"    --variables '{\"email\":\"you@example.com\",\"password\":\"secret\"}' \\\n" +
			"    --token-path login.token\n\n" +
			"  # After storing login config in .gqlcli.json:\n" +
			"  gqlcli login --env prod --variables '{\"email\":\"you@example.com\",\"password\":\"secret\"}'\n\n" +
			"AUTO RE-LOGIN\n" +
			"  --save-creds also persists --variables (e.g. email/password) under\n" +
			"  environments.<env>.login.credentials, in plaintext, in .gqlcli.json.\n" +
			"  With credentials saved, every command that talks to this environment\n" +
			"  checks the saved token's JWT \"exp\" claim before the request; once it has\n" +
			"  expired, it re-runs this login mutation automatically and persists the\n" +
			"  fresh token — no manual 'gqlcli login' needed. Only opt in on a machine\n" +
			"  you trust with the plaintext credentials.\n\n" +
			"  gqlcli login --env prod --mutation '...' --variables '{\"email\":\"...\",\"password\":\"...\"}' \\\n" +
			"    --token-path login.token --save-creds",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "env",
				Usage:    "Environment name from .gqlcli.json",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "mutation",
				Usage: "Login mutation (GraphQL string); can be stored in .gqlcli.json login.mutation",
			},
			&cli.StringFlag{
				Name:    "variables",
				Aliases: []string{"v"},
				Usage:   "Variables as JSON, e.g. '{\"email\":\"...\",\"password\":\"...\"}'",
			},
			&cli.StringFlag{
				Name:  "token-path",
				Usage: "Dot-path into response data where the token lives (e.g. login.token); can be stored in .gqlcli.json login.token_path",
			},
			&cli.StringFlag{
				Name:  "header",
				Usage: "Header name to write the token into",
				Value: "Authorization",
			},
			&cli.StringFlag{
				Name:  "prefix",
				Usage: "Value prefix prepended to the token",
				Value: "Bearer",
			},
			&cli.BoolFlag{
				Name:  "save-creds",
				Usage: "Persist --variables to .gqlcli.json (plaintext) so an expired JWT auto re-logs in on future commands",
			},
			insecureFlag(),
		},
		Action: func(c *cli.Context) error {
			envName := c.String("env")

			cfg, err := LoadProjectConfig()
			if err != nil {
				return err
			}
			if cfg == nil {
				return fmt.Errorf(".gqlcli.json not found; run 'gqlcli config init' first")
			}
			env, ok := cfg.Environments[envName]
			if !ok {
				return fmt.Errorf("environment %q not found in .gqlcli.json", envName)
			}

			// Resolve mutation string
			mutation := c.String("mutation")
			if mutation == "" && env.Login != nil {
				mutation = env.Login.Mutation
			}
			if mutation == "" {
				return fmt.Errorf("--mutation is required (or store it in .gqlcli.json under environments.%s.login.mutation)", envName)
			}

			// Resolve token path
			tokenPath := c.String("token-path")
			if tokenPath == "" && env.Login != nil {
				tokenPath = env.Login.TokenPath
			}
			if tokenPath == "" {
				return fmt.Errorf("--token-path is required (or store it in .gqlcli.json under environments.%s.login.token_path)", envName)
			}

			// Parse variables
			var variables map[string]interface{}
			if v := c.String("variables"); v != "" {
				if err := json.Unmarshal([]byte(v), &variables); err != nil {
					return fmt.Errorf("invalid --variables JSON: %w", err)
				}
			}

			// Execute mutation
			client := NewHTTPClient(&Config{URL: env.URL, Insecure: c.Bool("insecure")})
			result, err := client.ExecuteMutation(context.Background(), ExecutionModeHTTP, MutationOptions{
				Mutation:  mutation,
				Variables: variables,
			})
			if err != nil {
				return fmt.Errorf("login mutation failed: %w", err)
			}

			// Extract token from response using dot-path into data
			token, err := extractByPath(result["data"], tokenPath)
			if err != nil {
				return fmt.Errorf("extracting token at %q: %w", tokenPath, err)
			}

			// Write token into env headers
			headerName := c.String("header")
			prefix := c.String("prefix")
			headerValue := token
			if prefix != "" {
				headerValue = prefix + " " + token
			}
			if env.Headers == nil {
				env.Headers = make(map[string]string)
			}
			env.Headers[headerName] = headerValue

			// Persist login config for future re-authentication
			if env.Login == nil {
				env.Login = &EnvLoginConfig{}
			}
			if c.IsSet("mutation") {
				env.Login.Mutation = mutation
			}
			if c.IsSet("token-path") {
				env.Login.TokenPath = tokenPath
			}

			if c.Bool("save-creds") {
				env.Login.Credentials = variables
				env.Login.HeaderName = headerName
				env.Login.HeaderPrefix = prefix
			}

			cfg.Environments[envName] = env
			if err := saveProjectConfig(cfg); err != nil {
				return err
			}

			fmt.Printf("logged in; %s header saved to environment %q\n", headerName, envName)
			if c.Bool("save-creds") {
				fmt.Println("credentials saved to .gqlcli.json (plaintext) for automatic re-login on expiry")
			}
			return nil
		},
	}
}

// GetLogoutCommand returns the logout command, which clears the auth header from an env.
func (b *CLIBuilder) GetLogoutCommand() *cli.Command {
	return &cli.Command{
		Name:  "logout",
		Usage: "Remove the saved auth token from an environment in .gqlcli.json",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "env",
				Usage:    "Environment name from .gqlcli.json",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "header",
				Usage: "Header name to remove",
				Value: "Authorization",
			},
		},
		Action: func(c *cli.Context) error {
			envName := c.String("env")

			cfg, err := LoadProjectConfig()
			if err != nil {
				return err
			}
			if cfg == nil {
				return fmt.Errorf(".gqlcli.json not found")
			}
			env, ok := cfg.Environments[envName]
			if !ok {
				return fmt.Errorf("environment %q not found in .gqlcli.json", envName)
			}

			headerName := c.String("header")
			if _, exists := env.Headers[headerName]; !exists {
				fmt.Printf("no %s header set on environment %q\n", headerName, envName)
				return nil
			}
			delete(env.Headers, headerName)
			if len(env.Headers) == 0 {
				env.Headers = nil
			}

			cfg.Environments[envName] = env
			if err := saveProjectConfig(cfg); err != nil {
				return err
			}

			fmt.Printf("%s header removed from environment %q\n", headerName, envName)
			return nil
		},
	}
}

// autoReloginIfExpired checks the environment's saved bearer token for JWT
// expiry — or its absence — and, when `login --save-creds` has stashed a
// mutation + credentials for this env, re-authenticates and updates headers
// in place plus persists the fresh token to .gqlcli.json.
//
// It is a deliberate no-op — never an error — whenever there is nothing safe
// to act on: no Login config, no saved Credentials, a token that isn't a
// parseable JWT, or a JWT with no numeric "exp" claim (in both cases the
// existing header, even if present, is left alone since its shape can't be
// verified). A missing/empty token under the configured header IS treated as
// needing login, since --save-creds also persists HeaderName/HeaderPrefix,
// which only exist to make that bootstrap case unambiguous. Only an actual
// re-login attempt that fails surfaces an error, since at that point the
// caller already knows the token is stale or missing.
func (b *CLIBuilder) autoReloginIfExpired(envName string, env EnvConfig, headers map[string]string, insecure bool) error {
	if env.Login == nil || env.Login.Mutation == "" || env.Login.TokenPath == "" || len(env.Login.Credentials) == 0 {
		return nil
	}

	headerName := env.Login.HeaderName
	if headerName == "" {
		headerName = "Authorization"
	}
	current := headers[headerName]

	if current != "" {
		token := current
		if env.Login.HeaderPrefix != "" {
			token = strings.TrimPrefix(current, env.Login.HeaderPrefix+" ")
		}

		claims, err := (&TokenStore{}).ParseClaims(token)
		if err != nil {
			return nil
		}
		expRaw, ok := claims.Raw["exp"]
		if !ok {
			return nil
		}
		expSeconds, ok := expRaw.(float64)
		if !ok {
			return nil
		}
		if time.Now().Before(time.Unix(int64(expSeconds), 0)) {
			return nil
		}
	}

	client := NewHTTPClient(&Config{URL: env.URL, Insecure: insecure})
	result, err := client.ExecuteMutation(context.Background(), ExecutionModeHTTP, MutationOptions{
		Mutation:  env.Login.Mutation,
		Variables: env.Login.Credentials,
	})
	if err != nil {
		return fmt.Errorf("auto re-login for environment %q failed: %w", envName, err)
	}

	newToken, err := extractByPath(result["data"], env.Login.TokenPath)
	if err != nil {
		return fmt.Errorf("auto re-login for environment %q: extracting token at %q: %w", envName, env.Login.TokenPath, err)
	}

	newValue := newToken
	if env.Login.HeaderPrefix != "" {
		newValue = env.Login.HeaderPrefix + " " + newToken
	}
	headers[headerName] = newValue

	if b.projectConfig != nil {
		persisted := b.projectConfig.Environments[envName]
		if persisted.Headers == nil {
			persisted.Headers = make(map[string]string)
		}
		persisted.Headers[headerName] = newValue
		b.projectConfig.Environments[envName] = persisted
		if err := saveProjectConfig(b.projectConfig); err != nil {
			fmt.Fprintf(os.Stderr, "warning: auto re-login succeeded but failed to persist new token: %v\n", err)
		}
	}

	reason := "expired"
	if current == "" {
		reason = "missing"
	}
	fmt.Fprintf(os.Stderr, "token for environment %q %s; re-authenticated automatically\n", envName, reason)
	return nil
}

// extractByPath walks a dot-separated path through nested maps and returns the
// string value at the leaf. E.g. path "login.token" on {"login":{"token":"abc"}}
// returns "abc".
func extractByPath(data interface{}, path string) (string, error) {
	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("expected object at %q, got %T", part, current)
		}
		current, ok = m[part]
		if !ok {
			return "", fmt.Errorf("key %q not found", part)
		}
	}
	s, ok := current.(string)
	if !ok {
		return "", fmt.Errorf("value at path is %T, not a string", current)
	}
	return s, nil
}
