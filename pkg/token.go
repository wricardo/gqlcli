package gqlcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Claims holds JWT token claims parsed from a saved token.
type Claims struct {
	UserID string
	Email  string
	// Raw holds all parsed claims for custom extraction.
	Raw map[string]interface{}
}

// TokenStore persists a JWT token on disk.
// Tokens are stored at {dir}/token with 0600 permissions.
// Create with NewTokenStore (uses ~/.{appName}/token) or NewTokenStoreAt for a custom path.
type TokenStore struct {
	dir string
}

// NewTokenStore creates a TokenStore that stores tokens under ~/.{appName}/.
func NewTokenStore(appName string) *TokenStore {
	home, _ := os.UserHomeDir()
	return &TokenStore{dir: filepath.Join(home, "."+appName)}
}

// NewTokenStoreAt creates a TokenStore that stores tokens in the given directory.
func NewTokenStoreAt(dir string) *TokenStore {
	return &TokenStore{dir: dir}
}

// Save writes the token to disk, creating the directory if needed.
func (s *TokenStore) Save(token string) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("failed to create token directory: %w", err)
	}
	path := filepath.Join(s.dir, "token")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(token)), 0600); err != nil {
		return fmt.Errorf("failed to write token: %w", err)
	}
	return nil
}

// Load reads the token from disk.
// Returns an empty string (not an error) if no token file exists.
func (s *TokenStore) Load() (string, error) {
	path := filepath.Join(s.dir, "token")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Clear deletes the saved token file.
func (s *TokenStore) Clear() error {
	path := filepath.Join(s.dir, "token")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to delete token: %w", err)
	}
	return nil
}

// Exists reports whether a token file is present.
func (s *TokenStore) Exists() bool {
	_, err := os.Stat(filepath.Join(s.dir, "token"))
	return err == nil
}

// ParseClaims parses JWT claims from a token string without validating the signature.
// This is safe for reading tokens that your own application issued and saved.
func (s *TokenStore) ParseClaims(tokenString string) (*Claims, error) {
	tok, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}
	mc, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims format")
	}
	raw := make(map[string]interface{}, len(mc))
	for k, v := range mc {
		raw[k] = v
	}
	userID, _ := mc["user_id"].(string)
	email, _ := mc["email"].(string)
	return &Claims{UserID: userID, Email: email, Raw: raw}, nil
}

// FormatInfo returns a human-readable summary of the current token.
// Returns an empty string if no token is saved or it cannot be parsed.
func (s *TokenStore) FormatInfo() string {
	token, err := s.Load()
	if err != nil || token == "" {
		return ""
	}
	claims, err := s.ParseClaims(token)
	if err != nil {
		return "invalid token"
	}
	if claims.Email != "" {
		return fmt.Sprintf("logged in as %s (id: %s)", claims.Email, claims.UserID)
	}
	return fmt.Sprintf("logged in (id: %s)", claims.UserID)
}

// FormatInfoJSON returns token info as a JSON string.
func (s *TokenStore) FormatInfoJSON() (string, error) {
	token, err := s.Load()
	if err != nil {
		return "", err
	}
	if token == "" {
		return `{"authenticated":false}`, nil
	}
	claims, err := s.ParseClaims(token)
	if err != nil {
		return `{"authenticated":false,"error":"invalid token"}`, nil
	}
	out := map[string]interface{}{
		"authenticated": true,
		"user_id":       claims.UserID,
		"email":         claims.Email,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
