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

func TestScriptCommand_SaveAndRunByOp_InlineSource(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	tmp := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir tmp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})

	cfg := &Config{URL: server.URL, Timeout: 5, Strict: true}
	builder := NewCLIBuilder(cfg)
	app := cli.NewApp()
	builder.RegisterCommands(app)

	source := `async function run(gql, input) {
  await gql.query("query { ok }");
  return { tenantId: input.tenantId, concurrency: input.concurrency };
}`

	if err := RunApp(app, []string{
		"gqlcli", "script", "save",
		"--name", "tenant-job",
		"--source", source,
		"--defaults", `{"tenantId":"acme","concurrency":2}`,
		"--description", "test job",
	}); err != nil {
		t.Fatalf("script save failed: %v", err)
	}

	content, err := os.ReadFile(".gqlcli.json")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(content), `"scripts"`) || !strings.Contains(string(content), `"tenant-job"`) {
		t.Fatalf("saved config missing scripts entry: %s", string(content))
	}
	if !strings.Contains(string(content), `"source"`) {
		t.Fatalf("saved config missing inline source: %s", string(content))
	}

	var out bytes.Buffer
	app.Writer = &out
	if err := RunApp(app, []string{
		"gqlcli", "script",
		"--url", server.URL,
		"--op", "tenant-job",
		"--arg", `{"concurrency":5}`,
	}); err != nil {
		t.Fatalf("script --op failed: %v", err)
	}

	if calls == 0 {
		t.Fatalf("expected GraphQL call from script")
	}
	if !strings.Contains(out.String(), `"tenantId": "acme"`) {
		t.Fatalf("output = %q, want default tenantId", out.String())
	}
	if !strings.Contains(out.String(), `"concurrency": 5`) {
		t.Fatalf("output = %q, want overridden concurrency", out.String())
	}
}
