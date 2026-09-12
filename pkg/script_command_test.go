package gqlcli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestScriptCommand_DisableInactiveUsersWorkflow(t *testing.T) {
	var disabledIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		query, _ := req["query"].(string)

		switch {
		case strings.Contains(query, "users"):
			_, _ = w.Write([]byte(`{"data":{"users":[{"id":"1","active":true},{"id":"2","active":false},{"id":"3","active":false}]}}`))
		case strings.Contains(query, "disableUser"):
			vars, _ := req["variables"].(map[string]interface{})
			id, _ := vars["id"].(string)
			disabledIDs = append(disabledIDs, id)
			_, _ = w.Write([]byte(`{"data":{"disableUser":{"ok":true}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer server.Close()

	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "disable_users.js")
	script := `function run(gql) {
  const res = gql.query("query { users { id active } }");
  let disabled = 0;
  for (const u of res.data.users) {
    if (!u.active) {
      gql.mutation("mutation Disable($id: ID!) { disableUser(id: $id) { ok } }", { id: u.id });
      disabled++;
    }
  }
  return { disabled: disabled };
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := &Config{URL: server.URL, Timeout: 5, Strict: true}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	builder.RegisterCommands(app)

	var out bytes.Buffer
	app.Writer = &out

	if err := RunApp(app, []string{"gqlcli", "script", "--url", server.URL, "--file", scriptPath}); err != nil {
		t.Fatalf("script command failed: %v", err)
	}

	if len(disabledIDs) != 2 {
		t.Fatalf("disabled mutation calls = %d, want 2 (ids=%v)", len(disabledIDs), disabledIDs)
	}
	if !strings.Contains(out.String(), `"disabled": 2`) {
		t.Fatalf("output = %q, want disabled count", out.String())
	}
}

func TestScriptCommand_AsyncAwaitSupported(t *testing.T) {
	var disabledIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		query, _ := req["query"].(string)

		switch {
		case strings.Contains(query, "users"):
			_, _ = w.Write([]byte(`{"data":{"users":[{"id":"1","active":true},{"id":"2","active":false}]}}`))
		case strings.Contains(query, "disableUser"):
			vars, _ := req["variables"].(map[string]interface{})
			id, _ := vars["id"].(string)
			disabledIDs = append(disabledIDs, id)
			_, _ = w.Write([]byte(`{"data":{"disableUser":{"ok":true}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer server.Close()

	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "async_disable_users.js")
	script := `async function run(gql) {
  const res = await gql.query("query { users { id active } }");
  for (const u of res.data.users) {
    if (!u.active) {
      await gql.mutation("mutation Disable($id: ID!) { disableUser(id: $id) { ok } }", { id: u.id });
    }
  }
  return { disabled: 1 };
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := &Config{URL: server.URL, Timeout: 5, Strict: true}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	builder.RegisterCommands(app)

	var out bytes.Buffer
	app.Writer = &out

	if err := RunApp(app, []string{"gqlcli", "script", "--url", server.URL, "--file", scriptPath}); err != nil {
		t.Fatalf("script command failed: %v", err)
	}

	if len(disabledIDs) != 1 || disabledIDs[0] != "2" {
		t.Fatalf("disabled mutation calls = %v, want [2]", disabledIDs)
	}
	if !strings.Contains(out.String(), `"disabled": 1`) {
		t.Fatalf("output = %q, want disabled count", out.String())
	}
}

func TestScriptCommand_ArgIsPassedToScript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "arg_script.js")
	script := `function run(gql, input) {
  if (!input || input.tenantId !== "acme") {
    throw new Error("missing tenantId");
  }
  return { tenantId: input.tenantId };
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := &Config{URL: server.URL, Timeout: 5}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	builder.RegisterCommands(app)

	var out bytes.Buffer
	app.Writer = &out

	if err := RunApp(app, []string{"gqlcli", "script", "--url", server.URL, "--file", scriptPath, "--arg", `{"tenantId":"acme"}`}); err != nil {
		t.Fatalf("script command failed: %v", err)
	}

	if !strings.Contains(out.String(), `"tenantId": "acme"`) {
		t.Fatalf("output = %q, want tenantId", out.String())
	}
}

func TestScriptCommand_EachHelper(t *testing.T) {
	var disabledIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		query, _ := req["query"].(string)

		switch {
		case strings.Contains(query, "users"):
			_, _ = w.Write([]byte(`{"data":{"users":[{"id":"1","active":false},{"id":"2","active":false},{"id":"3","active":true}]}}`))
		case strings.Contains(query, "disableUser"):
			vars, _ := req["variables"].(map[string]interface{})
			id, _ := vars["id"].(string)
			disabledIDs = append(disabledIDs, id)
			_, _ = w.Write([]byte(`{"data":{"disableUser":{"ok":true}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	defer server.Close()

	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "each_disable_users.js")
	script := `async function run(gql) {
  const res = await gql.query("query { users { id active } }");
  const inactive = res.data.users.filter((u) => !u.active);
  const summary = await gql.each(inactive, async (u) => {
    await gql.mutation("mutation Disable($id: ID!) { disableUser(id: $id) { ok } }", { id: u.id });
  }, { concurrency: 3, stopOnError: false });
  return summary;
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := &Config{URL: server.URL, Timeout: 5, Strict: true}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	builder.RegisterCommands(app)

	var out bytes.Buffer
	app.Writer = &out

	if err := RunApp(app, []string{"gqlcli", "script", "--url", server.URL, "--file", scriptPath}); err != nil {
		t.Fatalf("script command failed: %v", err)
	}

	if len(disabledIDs) != 2 {
		t.Fatalf("disabled mutation calls = %d, want 2 (ids=%v)", len(disabledIDs), disabledIDs)
	}
	if !strings.Contains(out.String(), `"total": 2`) || !strings.Contains(out.String(), `"success": 2`) {
		t.Fatalf("output = %q, want gql.each summary", out.String())
	}
}
