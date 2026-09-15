package gqlcli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestHTTPClientConcurrentUse is the regression test for a data race on
// lastResponseMetadata: it was written unguarded on every operation, so any
// host sharing one client — or one ScriptRunner wrapping it — across goroutines
// raced. Run with -race; without it this passes regardless.
func TestHTTPClientConcurrentUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL, Timeout: 5})

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Execute(context.Background(), ExecutionModeHTTP, QueryOptions{Query: "query { ok }"}); err != nil {
				errs <- err
			}
			// Concurrent readers must not race the writers either.
			_ = client.LastResponseMetadata()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Execute failed: %v", err)
	}
}

func TestScriptRunnerConcurrentReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	runner := NewScriptRunner(NewHTTPClient(&Config{URL: server.URL, Timeout: 5}))

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.RunSource(context.Background(), "c.js",
				`function run(gql){ gql.query("query { ok }"); return 1; }`, "run", nil)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent RunSource failed: %v", err)
	}
}

func TestLimitWriter(t *testing.T) {
	t.Run("passes everything under the limit", func(t *testing.T) {
		var buf bytes.Buffer
		w := LimitWriter(&buf, 100)
		n, err := w.Write([]byte("hello"))
		if err != nil || n != 5 {
			t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
		}
		if buf.String() != "hello" {
			t.Errorf("buffer = %q, want %q", buf.String(), "hello")
		}
		if w.Truncated() {
			t.Error("Truncated() = true, want false")
		}
		if w.Written() != 5 {
			t.Errorf("Written() = %d, want 5", w.Written())
		}
	})

	t.Run("caps a write that straddles the limit", func(t *testing.T) {
		var buf bytes.Buffer
		w := LimitWriter(&buf, 4)
		// The script must not see a short write, or it looks like an I/O error.
		if n, err := w.Write([]byte("hello world")); err != nil || n != 11 {
			t.Fatalf("Write = (%d, %v), want (11, nil)", n, err)
		}
		if buf.String() != "hell" {
			t.Errorf("buffer = %q, want %q", buf.String(), "hell")
		}
		if !w.Truncated() {
			t.Error("Truncated() = false, want true")
		}
	})

	t.Run("discards everything once full", func(t *testing.T) {
		var buf bytes.Buffer
		w := LimitWriter(&buf, 3)
		for i := 0; i < 10; i++ {
			if _, err := w.Write([]byte("abc")); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if buf.String() != "abc" {
			t.Errorf("buffer = %q, want %q", buf.String(), "abc")
		}
		if w.Written() != 3 {
			t.Errorf("Written() = %d, want 3", w.Written())
		}
	})

	t.Run("a zero limit discards everything", func(t *testing.T) {
		var buf bytes.Buffer
		w := LimitWriter(&buf, 0)
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if buf.Len() != 0 {
			t.Errorf("buffer = %q, want empty", buf.String())
		}
		if !w.Truncated() {
			t.Error("Truncated() = false, want true")
		}
	})
}

// A runaway logger is the case LimitWriter exists for: bound the memory during
// the run, rather than trimming a buffer that already grew.
func TestLimitWriterBoundsRunawayScriptLogging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer server.Close()

	var buf bytes.Buffer
	capped := LimitWriter(&buf, 64)
	runner := NewScriptRunner(NewHTTPClient(&Config{URL: server.URL, Timeout: 5}), WithStdout(capped))

	script := `function run() {
  for (let i = 0; i < 5000; i++) { console.log("noisy line number " + i); }
  return "done";
}`
	if _, err := runner.RunSource(context.Background(), "noisy.js", script, "run", nil); err != nil {
		t.Fatalf("RunSource: %v", err)
	}
	if buf.Len() > 64 {
		t.Errorf("captured %d bytes, want at most 64", buf.Len())
	}
	if !capped.Truncated() {
		t.Error("Truncated() = false, want true")
	}
	if !strings.HasPrefix(buf.String(), "noisy line number 0") {
		t.Errorf("buffer = %q, want the earliest output kept", buf.String())
	}
}
