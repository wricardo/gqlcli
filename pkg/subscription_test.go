package gqlcli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func TestHTTPClientSubscribeGraphQLTransportWS(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{graphqlTransportWSProtocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var init graphQLWSMessage
		if err := conn.ReadJSON(&init); err != nil {
			t.Errorf("read init: %v", err)
			return
		}
		if init.Type != "connection_init" {
			t.Errorf("init type = %q, want connection_init", init.Type)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{"type": "connection_ack"}); err != nil {
			t.Errorf("write ack: %v", err)
			return
		}

		var subscribe graphQLWSMessage
		if err := conn.ReadJSON(&subscribe); err != nil {
			t.Errorf("read subscribe: %v", err)
			return
		}
		if subscribe.Type != "subscribe" {
			t.Errorf("subscribe type = %q, want subscribe", subscribe.Type)
			return
		}
		var payload graphQLWSPayload
		if err := json.Unmarshal(subscribe.Payload, &payload); err != nil {
			t.Errorf("decode subscribe payload: %v", err)
			return
		}
		if payload.Query != "subscription Count { count }" {
			t.Errorf("query = %q", payload.Query)
		}
		if payload.OperationName != "Count" {
			t.Errorf("operationName = %q", payload.OperationName)
		}
		if got := payload.Variables["from"]; got != float64(1) {
			t.Errorf("variables[from] = %#v", got)
		}

		_ = conn.WriteJSON(map[string]interface{}{"id": subscribe.ID, "type": "next", "payload": map[string]interface{}{"data": map[string]interface{}{"count": 1}}})
		_ = conn.WriteJSON(map[string]interface{}{"id": subscribe.ID, "type": "next", "payload": map[string]interface{}{"data": map[string]interface{}{"count": 2}}})
		_ = conn.WriteJSON(map[string]interface{}{"id": subscribe.ID, "type": "complete"})
	}))
	defer server.Close()

	client := NewHTTPClient(&Config{URL: server.URL})
	var events []SubscriptionEvent
	err := client.Subscribe(context.Background(), SubscriptionOptions{
		Subscription:  "subscription Count { count }",
		Variables:     map[string]interface{}{"from": 1},
		OperationName: "Count",
	}, func(event SubscriptionEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe returned error: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %#v", len(events), events)
	}
	if events[0].Type != "next" || events[1].Type != "next" || events[2].Type != "complete" {
		t.Fatalf("unexpected event types: %#v", events)
	}
}

func TestWebsocketURL(t *testing.T) {
	tests := map[string]string{
		"http://example.test/graphql":  "ws://example.test/graphql",
		"https://example.test/graphql": "wss://example.test/graphql",
		"ws://example.test/graphql":    "ws://example.test/graphql",
		"wss://example.test/graphql":   "wss://example.test/graphql",
	}
	for input, want := range tests {
		got, err := websocketURL(input)
		if err != nil {
			t.Fatalf("websocketURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("websocketURL(%q) = %q, want %q", input, got, want)
		}
	}
}
