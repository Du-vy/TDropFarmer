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
	err := notifier.Send(context.Background(), WebhookPayload{Content: "hello world"})
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

func TestDiscordNotifierSendEmbed(t *testing.T) {
	var receivedPayload WebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := NewDiscord(server.URL)
	payload := WebhookPayload{
		Embeds: []Embed{
			{
				Title: "New Drop Claimed!",
				Color: 9520895,
				Fields: []EmbedField{
					{Name: "Game", Value: "Overwatch 2", Inline: true},
					{Name: "Item", Value: "Sombra Skin", Inline: true},
				},
				Thumbnail: &EmbedMedia{URL: "https://example.com/game.png"},
				Image:     &EmbedMedia{URL: "https://example.com/reward.png"},
			},
		},
	}
	err := notifier.Send(context.Background(), payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(receivedPayload.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(receivedPayload.Embeds))
	}

	emb := receivedPayload.Embeds[0]
	if emb.Title != "New Drop Claimed!" {
		t.Errorf("expected title 'New Drop Claimed!', got %q", emb.Title)
	}
	if emb.Color != 9520895 {
		t.Errorf("expected color 9520895, got %d", emb.Color)
	}
	if emb.Thumbnail == nil || emb.Thumbnail.URL != "https://example.com/game.png" {
		t.Errorf("expected thumbnail URL, got %+v", emb.Thumbnail)
	}
	if emb.Image == nil || emb.Image.URL != "https://example.com/reward.png" {
		t.Errorf("expected image URL, got %+v", emb.Image)
	}
}

func TestDiscordNotifierSendError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	notifier := NewDiscord(server.URL)
	err := notifier.Send(context.Background(), WebhookPayload{Content: "test error"})
	if err == nil {
		t.Fatalf("expected error from non-2xx status, got nil")
	}
}

func TestDiscordNotifierEmptyURL(t *testing.T) {
	notifier := NewDiscord("")
	err := notifier.Send(context.Background(), WebhookPayload{Content: "empty url"})
	if err == nil {
		t.Fatalf("expected error from empty URL, got nil")
	}
}
