package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateWebSocketSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Client-Id"); got != "client-id" {
			t.Fatalf("Client-Id = %q, want client-id", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want Bearer access-token", got)
		}
		var request SubscriptionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Transport.Method != "websocket" || request.Transport.SessionID != "session-1" {
			t.Fatalf("transport = %#v, want websocket/session-1", request.Transport)
		}
		writeJSON(t, w, SubscriptionResponse{
			Data: []Subscription{{ID: "sub-1", Type: request.Type, Version: request.Version, Condition: request.Condition, Transport: request.Transport}},
		})
	}))
	defer server.Close()

	client := Client{ClientID: "client-id", AccessToken: "access-token", EventSubEndpoint: server.URL}
	response, err := client.CreateWebSocketSubscription(context.Background(), "session-1", SubscriptionRequest{
		Type:      "stream.online",
		Version:   "1",
		Condition: map[string]string{"broadcaster_user_id": "1234"},
	})
	if err != nil {
		t.Fatalf("CreateWebSocketSubscription returned error: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "sub-1" {
		t.Fatalf("response = %#v, want subscription sub-1", response)
	}
}

func TestCreateWebSocketSubscriptionRequiresSession(t *testing.T) {
	client := Client{ClientID: "client-id", AccessToken: "access-token"}
	_, err := client.CreateWebSocketSubscription(context.Background(), "", SubscriptionRequest{Type: "stream.online", Version: "1", Condition: map[string]string{"broadcaster_user_id": "1234"}})
	if err == nil {
		t.Fatalf("CreateWebSocketSubscription returned nil error, want session error")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
