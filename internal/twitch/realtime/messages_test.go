package realtime

import "testing"

func TestParseWelcomeMessage(t *testing.T) {
	message, err := ParseMessage([]byte(`{
    "metadata": {
      "message_id": "msg-1",
      "message_type": "session_welcome",
      "message_timestamp": "2026-06-24T00:00:00Z"
    },
    "payload": {
      "session": {
        "id": "session-1",
        "status": "connected",
        "connected_at": "2026-06-24T00:00:00Z",
        "keepalive_timeout_seconds": 10
      }
    }
  }`))
	if err != nil {
		t.Fatalf("ParseMessage returned error: %v", err)
	}
	if message.Metadata.MessageType != MessageTypeSessionWelcome {
		t.Fatalf("message type = %q, want session_welcome", message.Metadata.MessageType)
	}

	payload, err := DecodePayload[SessionWelcomePayload](message)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}
	if payload.Session.ID != "session-1" {
		t.Fatalf("session id = %q, want session-1", payload.Session.ID)
	}
}

func TestParseMessageRequiresType(t *testing.T) {
	if _, err := ParseMessage([]byte(`{"metadata": {}, "payload": {}}`)); err == nil {
		t.Fatalf("ParseMessage returned nil error, want missing type error")
	}
}
