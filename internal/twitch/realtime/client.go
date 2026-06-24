package realtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

const defaultWebSocketURL = "wss://eventsub.wss.twitch.tv/ws"

type Handler interface {
	Welcome(context.Context, SessionWelcomePayload) error
	Keepalive(context.Context, Message) error
	Notification(context.Context, NotificationPayload) error
	Reconnect(context.Context, SessionReconnectPayload) error
	Revocation(context.Context, RevocationPayload) error
}

type HandlerFunc struct {
	OnWelcome      func(context.Context, SessionWelcomePayload) error
	OnKeepalive    func(context.Context, Message) error
	OnNotification func(context.Context, NotificationPayload) error
	OnReconnect    func(context.Context, SessionReconnectPayload) error
	OnRevocation   func(context.Context, RevocationPayload) error
}

func (h HandlerFunc) Welcome(ctx context.Context, payload SessionWelcomePayload) error {
	if h.OnWelcome == nil {
		return nil
	}
	return h.OnWelcome(ctx, payload)
}

func (h HandlerFunc) Keepalive(ctx context.Context, message Message) error {
	if h.OnKeepalive == nil {
		return nil
	}
	return h.OnKeepalive(ctx, message)
}

func (h HandlerFunc) Notification(ctx context.Context, payload NotificationPayload) error {
	if h.OnNotification == nil {
		return nil
	}
	return h.OnNotification(ctx, payload)
}

func (h HandlerFunc) Reconnect(ctx context.Context, payload SessionReconnectPayload) error {
	if h.OnReconnect == nil {
		return nil
	}
	return h.OnReconnect(ctx, payload)
}

func (h HandlerFunc) Revocation(ctx context.Context, payload RevocationPayload) error {
	if h.OnRevocation == nil {
		return nil
	}
	return h.OnRevocation(ctx, payload)
}

func (c Client) Listen(ctx context.Context, handler Handler, logger *slog.Logger) error {
	if handler == nil {
		return fmt.Errorf("eventsub handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return c.listenURL(ctx, c.webSocketURL(), handler, logger)
}

func (c Client) listenURL(ctx context.Context, endpoint string, handler Handler, logger *slog.Logger) error {
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("connect eventsub websocket: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "shutdown")

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read eventsub websocket: %w", err)
		}

		message, err := ParseMessage(data)
		if err != nil {
			return err
		}

		switch message.Metadata.MessageType {
		case MessageTypeSessionWelcome:
			payload, err := DecodePayload[SessionWelcomePayload](message)
			if err != nil {
				return err
			}
			if err := handler.Welcome(ctx, payload); err != nil {
				return err
			}
		case MessageTypeSessionKeepalive:
			if err := handler.Keepalive(ctx, message); err != nil {
				return err
			}
		case MessageTypeNotification:
			payload, err := DecodePayload[NotificationPayload](message)
			if err != nil {
				return err
			}
			if err := handler.Notification(ctx, payload); err != nil {
				return err
			}
		case MessageTypeSessionReconnect:
			payload, err := DecodePayload[SessionReconnectPayload](message)
			if err != nil {
				return err
			}
			if err := handler.Reconnect(ctx, payload); err != nil {
				return err
			}
			if payload.Session.ReconnectURL != "" {
				logger.Info("eventsub reconnect requested")
				return c.listenURL(ctx, payload.Session.ReconnectURL, handler, logger)
			}
		case MessageTypeRevocation:
			payload, err := DecodePayload[RevocationPayload](message)
			if err != nil {
				return err
			}
			if err := handler.Revocation(ctx, payload); err != nil {
				return err
			}
		default:
			logger.Warn("unknown eventsub message type",
				slog.String("message_type", message.Metadata.MessageType),
				slog.String("message_id", message.Metadata.MessageID),
			)
		}
	}
}

func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<uint(attempt-1)) * time.Second
}
