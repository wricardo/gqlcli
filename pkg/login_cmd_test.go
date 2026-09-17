package gqlcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signedJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": exp.Unix(),
	})
	signed, err := tok.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("signing test JWT: %v", err)
	}
	return signed
}

func TestAutoReloginIfExpired_RefreshesExpiredJWTAndPersists(t *testing.T) {
	t.Chdir(t.TempDir())

	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"login":{"token":"` + signedJWT(t, time.Now().Add(time.Hour)) + `"}}}`))
	}))
	defer server.Close()

	expired := signedJWT(t, time.Now().Add(-time.Hour))

	b := &CLIBuilder{
		config: &Config{},
		projectConfig: &ProjectConfig{
			Environments: map[string]EnvConfig{
				"prod": {
					URL:     server.URL,
					Headers: map[string]string{"Authorization": "Bearer " + expired},
					Login: &EnvLoginConfig{
						Mutation:     "mutation Login($email:String!){ login(email:$email){ token } }",
						TokenPath:    "login.token",
						Credentials:  map[string]interface{}{"email": "you@example.com"},
						HeaderName:   "Authorization",
						HeaderPrefix: "Bearer",
					},
				},
			},
		},
	}
	env := b.projectConfig.Environments["prod"]
	headers := map[string]string{"Authorization": "Bearer " + expired}

	if err := b.autoReloginIfExpired("prod", env, headers, false); err != nil {
		t.Fatalf("autoReloginIfExpired: %v", err)
	}

	if loginCalls != 1 {
		t.Fatalf("expected 1 login mutation call, got %d", loginCalls)
	}
	if headers["Authorization"] == "Bearer "+expired {
		t.Fatalf("expected headers to be updated with a fresh token, still has the expired one")
	}

	// Persisted to .gqlcli.json too.
	data, err := os.ReadFile(filepath.Join(".", ".gqlcli.json"))
	if err != nil {
		t.Fatalf("reading persisted config: %v", err)
	}
	var persisted ProjectConfig
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshaling persisted config: %v", err)
	}
	if persisted.Environments["prod"].Headers["Authorization"] != headers["Authorization"] {
		t.Fatalf("persisted token does not match refreshed in-memory token")
	}
}

func TestAutoReloginIfExpired_NoOpWhenTokenStillValid(t *testing.T) {
	t.Chdir(t.TempDir())

	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"login":{"token":"should-not-be-called"}}}`))
	}))
	defer server.Close()

	valid := signedJWT(t, time.Now().Add(time.Hour))

	b := &CLIBuilder{config: &Config{}, projectConfig: &ProjectConfig{Environments: map[string]EnvConfig{}}}
	env := EnvConfig{
		URL: server.URL,
		Login: &EnvLoginConfig{
			Mutation:     "mutation { login { token } }",
			TokenPath:    "login.token",
			Credentials:  map[string]interface{}{"email": "you@example.com"},
			HeaderName:   "Authorization",
			HeaderPrefix: "Bearer",
		},
	}
	headers := map[string]string{"Authorization": "Bearer " + valid}

	if err := b.autoReloginIfExpired("prod", env, headers, false); err != nil {
		t.Fatalf("autoReloginIfExpired: %v", err)
	}
	if loginCalls != 0 {
		t.Fatalf("expected no login mutation call for a still-valid token, got %d", loginCalls)
	}
	if headers["Authorization"] != "Bearer "+valid {
		t.Fatalf("headers should be untouched when token is still valid")
	}
}

func TestAutoReloginIfExpired_BootstrapsWhenTokenIsMissing(t *testing.T) {
	t.Chdir(t.TempDir())

	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"login":{"token":"` + signedJWT(t, time.Now().Add(time.Hour)) + `"}}}`))
	}))
	defer server.Close()

	b := &CLIBuilder{config: &Config{}, projectConfig: &ProjectConfig{Environments: map[string]EnvConfig{
		"prod": {URL: server.URL},
	}}}
	env := EnvConfig{
		URL: server.URL,
		Login: &EnvLoginConfig{
			Mutation:     "mutation Login($email:String!){ login(email:$email){ token } }",
			TokenPath:    "login.token",
			Credentials:  map[string]interface{}{"email": "you@example.com"},
			HeaderName:   "Authorization",
			HeaderPrefix: "Bearer",
		},
	}
	headers := map[string]string{} // no Authorization header at all — first run

	if err := b.autoReloginIfExpired("prod", env, headers, false); err != nil {
		t.Fatalf("autoReloginIfExpired: %v", err)
	}
	if loginCalls != 1 {
		t.Fatalf("expected 1 login mutation call to bootstrap a missing token, got %d", loginCalls)
	}
	if headers["Authorization"] == "" {
		t.Fatalf("expected headers to be populated with a fresh token")
	}
}

func TestAutoReloginIfExpired_NoOpWithoutSavedCredentials(t *testing.T) {
	t.Chdir(t.TempDir())

	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"login":{"token":"unused"}}}`))
	}))
	defer server.Close()

	expired := signedJWT(t, time.Now().Add(-time.Hour))

	b := &CLIBuilder{config: &Config{}, projectConfig: &ProjectConfig{Environments: map[string]EnvConfig{}}}
	env := EnvConfig{
		URL: server.URL,
		Login: &EnvLoginConfig{
			Mutation:  "mutation { login { token } }",
			TokenPath: "login.token",
			// No Credentials saved (login ran without --save-creds).
		},
	}
	headers := map[string]string{"Authorization": "Bearer " + expired}

	if err := b.autoReloginIfExpired("prod", env, headers, false); err != nil {
		t.Fatalf("autoReloginIfExpired: %v", err)
	}
	if loginCalls != 0 {
		t.Fatalf("expected no login mutation call without saved credentials, got %d", loginCalls)
	}
	if headers["Authorization"] != "Bearer "+expired {
		t.Fatalf("headers should be untouched without saved credentials")
	}
}

func TestAutoReloginIfExpired_NoOpForNonJWTToken(t *testing.T) {
	t.Chdir(t.TempDir())

	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loginCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"login":{"token":"unused"}}}`))
	}))
	defer server.Close()

	b := &CLIBuilder{config: &Config{}, projectConfig: &ProjectConfig{Environments: map[string]EnvConfig{}}}
	env := EnvConfig{
		URL: server.URL,
		Login: &EnvLoginConfig{
			Mutation:     "mutation { login { token } }",
			TokenPath:    "login.token",
			Credentials:  map[string]interface{}{"email": "you@example.com"},
			HeaderName:   "Authorization",
			HeaderPrefix: "Bearer",
		},
	}
	headers := map[string]string{"Authorization": "Bearer opaque-session-token"}

	if err := b.autoReloginIfExpired("prod", env, headers, false); err != nil {
		t.Fatalf("autoReloginIfExpired: %v", err)
	}
	if loginCalls != 0 {
		t.Fatalf("expected no login mutation call for a non-JWT token, got %d", loginCalls)
	}
	if headers["Authorization"] != "Bearer opaque-session-token" {
		t.Fatalf("headers should be untouched for a non-JWT token")
	}
}
