package app

import (
	"context"
	"log/slog"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/engine"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
	"github.com/Du-vy/TDropFarmer/internal/twitch/realtime"
)

type eventSubBridge struct {
	engine       *engine.Engine
	streamers    []domain.Streamer
	pointsClient *channelpoints.ContextLoader
	logger       *slog.Logger
	sessionID    string
	onSession    func(string)
}

func (b *eventSubBridge) Welcome(ctx context.Context, payload realtime.SessionWelcomePayload) error {
	b.logger.Info("eventsub connected",
		slog.String("session_id", payload.Session.ID),
		slog.Int("keepalive", payload.Session.KeepaliveTimeoutSeconds),
	)
	b.sessionID = payload.Session.ID
	if b.onSession != nil {
		b.onSession(payload.Session.ID)
	}
	return nil
}

func (b *eventSubBridge) Keepalive(_ context.Context, _ realtime.Message) error {
	return nil
}

func (b *eventSubBridge) Notification(_ context.Context, payload realtime.NotificationPayload) error {
	b.logger.Debug("eventsub notification",
		slog.String("type", payload.Subscription.Type),
	)
	return nil
}

func (b *eventSubBridge) Reconnect(_ context.Context, payload realtime.SessionReconnectPayload) error {
	b.logger.Info("eventsub reconnect requested")
	return nil
}

func (b *eventSubBridge) Revocation(_ context.Context, payload realtime.RevocationPayload) error {
	b.logger.Info("eventsub subscription revoked",
		slog.String("type", payload.Subscription.Type),
		slog.String("id", payload.Subscription.ID),
	)
	return nil
}
