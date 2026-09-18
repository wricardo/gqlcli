package gqlcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestExecuteBatchChunksSplitsRequestsInOrder(t *testing.T) {
	requests := []BatchRequest{{Query: "one"}, {Query: "two"}, {Query: "three"}, {Query: "four"}, {Query: "five"}}
	var executed, output [][]string

	err := executeBatchChunks(requests, 2, func(chunk []BatchRequest) ([]json.RawMessage, error) {
		queries := make([]string, len(chunk))
		results := make([]json.RawMessage, len(chunk))
		for i, request := range chunk {
			queries[i] = request.Query
			results[i] = json.RawMessage(`{"data":{"ok":true}}`)
		}
		executed = append(executed, queries)
		return results, nil
	}, func(_ []json.RawMessage, chunk []BatchRequest) error {
		queries := make([]string, len(chunk))
		for i, request := range chunk {
			queries[i] = request.Query
		}
		output = append(output, queries)
		return nil
	})
	if err != nil {
		t.Fatalf("executeBatchChunks() error = %v", err)
	}

	want := [][]string{{"one", "two"}, {"three", "four"}, {"five"}}
	if !reflect.DeepEqual(executed, want) {
		t.Fatalf("executed = %#v, want %#v", executed, want)
	}
	if !reflect.DeepEqual(output, want) {
		t.Fatalf("output = %#v, want %#v", output, want)
	}
}

func TestExecuteBatchChunksZeroSendsOneRequest(t *testing.T) {
	requests := []BatchRequest{{Query: "one"}, {Query: "two"}}
	requestsSent := 0

	err := executeBatchChunks(requests, 0, func(chunk []BatchRequest) ([]json.RawMessage, error) {
		requestsSent++
		if len(chunk) != len(requests) {
			t.Fatalf("chunk length = %d, want %d", len(chunk), len(requests))
		}
		return nil, nil
	}, func([]json.RawMessage, []BatchRequest) error { return nil })
	if err != nil {
		t.Fatalf("executeBatchChunks() error = %v", err)
	}
	if requestsSent != 1 {
		t.Fatalf("requests sent = %d, want 1", requestsSent)
	}
}

func TestBatchCommandChunksNDJSONRequests(t *testing.T) {
	var received [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/x-ndjson" {
			t.Errorf("Content-Type = %q, want application/x-ndjson", got)
		}
		var queries []string
		scanner := bufio.NewScanner(r.Body)
		for scanner.Scan() {
			var request BatchRequest
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				t.Errorf("unmarshal request: %v", err)
				return
			}
			queries = append(queries, request.Query)
			_, _ = w.Write([]byte(`{"data":{"ok":true}}` + "\n"))
		}
		if err := scanner.Err(); err != nil {
			t.Errorf("read request: %v", err)
		}
		received = append(received, queries)
	}))
	defer server.Close()

	input := t.TempDir() + "/operations.ndjson"
	if err := os.WriteFile(input, []byte("{\"query\":\"one\"}\n{\"query\":\"two\"}\n{\"query\":\"three\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	builder := NewCLIBuilder(&Config{URL: server.URL, Timeout: 5})
	app := &cli.App{Name: "gqlcli", Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	builder.RegisterCommands(app)
	if err := app.Run([]string{"gqlcli", "batch", "--file", input, "--batch-size", "2"}); err != nil {
		t.Fatalf("batch command error = %v", err)
	}

	want := [][]string{{"one", "two"}, {"three"}}
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("received = %#v, want %#v", received, want)
	}
}
