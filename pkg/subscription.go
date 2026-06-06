package gqlcli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const graphqlTransportWSProtocol = "graphql-transport-ws"

// SubscriptionOptions holds options for GraphQL subscription execution.
type SubscriptionOptions struct {
	Subscription  string                 // GraphQL subscription string
	Variables     map[string]interface{} // Subscription variables
	OperationName string                 // Named operation to execute
}

// SubscriptionEvent is one stream event emitted by Subscribe.
type SubscriptionEvent struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

type graphQLWSMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type graphQLWSPayload struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
}

// Subscribe executes a GraphQL subscription using the GraphQL over WebSocket
// protocol (graphql-transport-ws). It calls emit for each server event until the
// server sends complete, the context is cancelled, or an error occurs.
func (c *HTTPClient) Subscribe(ctx context.Context, opts SubscriptionOptions, emit func(SubscriptionEvent) error) error {
	if c.config.URL == "" {
		return fmt.Errorf("GraphQL URL is not configured")
	}

	wsURL, err := websocketURL(c.config.URL)
	if err != nil {
		return err
	}

	header := http.Header{}
	for k, v := range c.config.Headers {
		header.Set(k, v)
	}
	if c.config.Token != "" {
		header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.Token))
	}

	dialer := websocket.Dialer{
		Subprotocols:     []string{graphqlTransportWSProtocol},
		HandshakeTimeout: 10 * time.Second,
	}
	if c.config.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]interface{}{"type": "connection_init"}); err != nil {
		return fmt.Errorf("send connection_init: %w", err)
	}

	for {
		var msg graphQLWSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("read connection_ack: %w", err)
		}
		switch msg.Type {
		case "connection_ack":
			goto acked
		case "ping":
			_ = conn.WriteJSON(map[string]interface{}{"type": "pong"})
		case "pong":
			continue
		case "connection_error", "error":
			var payload interface{}
			_ = json.Unmarshal(msg.Payload, &payload)
			return fmt.Errorf("subscription connection error: %v", payload)
		default:
			return fmt.Errorf("expected connection_ack, got %q", msg.Type)
		}
	}

acked:
	payload := graphQLWSPayload{Query: opts.Subscription, Variables: opts.Variables, OperationName: opts.OperationName}
	if err := conn.WriteJSON(map[string]interface{}{"id": "1", "type": "subscribe", "payload": payload}); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for {
			var msg graphQLWSMessage
			if err := conn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}

			switch msg.Type {
			case "next", "error":
				var payload interface{}
				if len(msg.Payload) > 0 {
					if err := json.Unmarshal(msg.Payload, &payload); err != nil {
						errCh <- fmt.Errorf("decode %s payload: %w", msg.Type, err)
						return
					}
				}
				if err := emit(SubscriptionEvent{Type: msg.Type, Payload: payload}); err != nil {
					errCh <- err
					return
				}
			case "complete":
				if err := emit(SubscriptionEvent{Type: "complete"}); err != nil {
					errCh <- err
					return
				}
				errCh <- nil
				return
			case "ping":
				_ = conn.WriteJSON(map[string]interface{}{"type": "pong"})
			case "pong":
				continue
			default:
				errCh <- fmt.Errorf("unexpected subscription message type %q", msg.Type)
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		_ = conn.WriteJSON(map[string]interface{}{"id": "1", "type": "complete"})
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client cancelled"))
		return nil
	case err := <-errCh:
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == nil {
			return nil
		}
		return fmt.Errorf("subscription stream failed: %w", err)
	}
}

func websocketURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already a WebSocket URL
	default:
		return "", fmt.Errorf("URL must start with http://, https://, ws://, or wss://")
	}
	return u.String(), nil
}
