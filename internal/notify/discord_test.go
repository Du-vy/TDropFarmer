package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscordNotifierSend(t *testing.T) {
	var receivedContent string
	var contentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")

		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
			receivedContent = payload.Content
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := NewDiscord(server.URL)
	err := notifier.Send(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
	}

	if receivedContent != "hello world" {
		t.Errorf("expected receivedContent %q, got %q", "hello world", receivedContent)
	}
}

func TestDiscordNotifierSendError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	notifier := NewDiscord(server.URL)
	err := notifier.Send(context.Background(), "test error")
	if err == nil {
		t.Fatalf("expected error from non-2xx status, got nil")
	}
}

func TestDiscordNotifierEmptyURL(t *testing.T) {
	notifier := NewDiscord("")
	err := notifier.Send(context.Background(), "empty url")
	if err == nil {
		t.Fatalf("expected error from empty URL, got nil")
	}
}
