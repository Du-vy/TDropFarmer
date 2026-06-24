package realtime

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	MessageTypeSessionWelcome   = "session_welcome"
	MessageTypeSessionKeepalive = "session_keepalive"
	MessageTypeNotification     = "notification"
	MessageTypeSessionReconnect = "session_reconnect"
	MessageTypeRevocation       = "revocation"
)

type Message struct {
	Metadata Metadata        `json:"metadata"`
	Payload  json.RawMessage `json:"payload"`
}

type Metadata struct {
	MessageID        string    `json:"message_id"`
	MessageType      string    `json:"message_type"`
	MessageTimestamp time.Time `json:"message_timestamp"`
	SubscriptionType string    `json:"subscription_type,omitempty"`
	SubscriptionVer  string    `json:"subscription_version,omitempty"`
}

type SessionWelcomePayload struct {
	Session Session `json:"session"`
}

type SessionReconnectPayload struct {
	Session Session `json:"session"`
}

type Session struct {
	ID                      string    `json:"id"`
	Status                  string    `json:"status"`
	ConnectedAt             time.Time `json:"connected_at"`
	KeepaliveTimeoutSeconds int       `json:"keepalive_timeout_seconds"`
	ReconnectURL            string    `json:"reconnect_url"`
}

type NotificationPayload struct {
	Subscription Subscription    `json:"subscription"`
	Event        json.RawMessage `json:"event"`
}

type RevocationPayload struct {
	Subscription Subscription `json:"subscription"`
}

type Subscription struct {
	ID        string            `json:"id,omitempty"`
	Status    string            `json:"status,omitempty"`
	Type      string            `json:"type"`
	Version   string            `json:"version"`
	Condition map[string]string `json:"condition"`
	Transport Transport         `json:"transport"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	Cost      int               `json:"cost,omitempty"`
}

type Transport struct {
	Method    string `json:"method"`
	SessionID string `json:"session_id,omitempty"`
}

func ParseMessage(data []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Message{}, fmt.Errorf("decode eventsub message: %w", err)
	}
	if message.Metadata.MessageType == "" {
		return Message{}, fmt.Errorf("eventsub message missing metadata.message_type")
	}
	return message, nil
}

func DecodePayload[T any](message Message) (T, error) {
	var payload T
	if len(message.Payload) == 0 {
		return payload, fmt.Errorf("eventsub message payload is empty")
	}
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return payload, fmt.Errorf("decode eventsub payload: %w", err)
	}
	return payload, nil
}
