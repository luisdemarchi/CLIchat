package hostclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubscribeStateAcceptsLargeInstanceEvents(t *testing.T) {
	largeMessage := strings.Repeat("x", 2*1024*1024)
	payload := map[string]any{
		"kind": "instance_updated",
		"payload": map[string]any{
			"kind": "instance_updated",
			"id":   "session-1",
			"payload": map[string]any{
				"id":         "session-1",
				"providerId": "codex",
				"origin":     "internal",
				"title":      "Large transcript",
				"status":     "idle",
				"messages": []map[string]string{{
					"id":        "message-1",
					"role":      "assistant",
					"text":      largeMessage,
					"createdAt": "2026-05-20T00:00:00Z",
				}},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/state/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: instance_updated\ndata: %s\n\n", encoded)
	}))
	defer server.Close()

	client := New(server.URL)
	seen := false
	err = client.SubscribeState(context.Background(), func(event StateEvent) {
		seen = true
		if event.Kind != "instance_updated" {
			t.Fatalf("kind = %q, want instance_updated", event.Kind)
		}
		if len(event.Payload) < len(largeMessage) {
			t.Fatalf("payload length = %d, want at least %d", len(event.Payload), len(largeMessage))
		}
	})
	if err != nil {
		t.Fatalf("SubscribeState returned error: %v", err)
	}
	if !seen {
		t.Fatal("expected state event callback")
	}
}
