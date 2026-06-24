package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestListenDispatchesWelcomeAndNotification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatalf("accept websocket: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		writeWS(t, conn, `{
      "metadata": {"message_id":"1", "message_type":"session_welcome", "message_timestamp":"2026-06-24T00:00:00Z"},
      "payload": {"session": {"id":"session-1", "status":"connected", "connected_at":"2026-06-24T00:00:00Z", "keepalive_timeout_seconds":10}}
    }`)
		writeWS(t, conn, `{
      "metadata": {"message_id":"2", "message_type":"notification", "message_timestamp":"2026-06-24T00:00:01Z", "subscription_type":"stream.online", "subscription_version":"1"},
      "payload": {"subscription": {"id":"sub-1", "type":"stream.online", "version":"1", "condition":{"broadcaster_user_id":"1234"}, "transport":{"method":"websocket", "session_id":"session-1"}}, "event": {"broadcaster_user_id":"1234"}}
    }`)
	}))
	defer server.Close()

	var welcomed atomic.Bool
	var notified atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := Client{WebSocketURL: "ws" + strings.TrimPrefix(server.URL, "http")}
	err := client.Listen(ctx, HandlerFunc{
		OnWelcome: func(ctx context.Context, payload SessionWelcomePayload) error {
			if payload.Session.ID != "session-1" {
				t.Fatalf("session id = %q, want session-1", payload.Session.ID)
			}
			welcomed.Store(true)
			return nil
		},
		OnNotification: func(ctx context.Context, payload NotificationPayload) error {
			if payload.Subscription.ID != "sub-1" {
				t.Fatalf("subscription id = %q, want sub-1", payload.Subscription.ID)
			}
			notified.Store(true)
			cancel()
			return context.Canceled
		},
	}, slog.Default())
	if err == nil {
		t.Fatalf("Listen returned nil error, want cancellation from handler")
	}
	if !welcomed.Load() || !notified.Load() {
		t.Fatalf("welcome=%v notification=%v, want both true", welcomed.Load(), notified.Load())
	}
}

func TestBackoff(t *testing.T) {
	if got := Backoff(1); got != time.Second {
		t.Fatalf("Backoff(1) = %s, want 1s", got)
	}
	if got := Backoff(10); got != 32*time.Second {
		t.Fatalf("Backoff(10) = %s, want 32s", got)
	}
}

func writeWS(t *testing.T, conn *websocket.Conn, message string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
		t.Fatalf("write websocket: %v", err)
	}
}
