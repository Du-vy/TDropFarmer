package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/auth"
	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/engine"
	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/twitch"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
	"github.com/Du-vy/TDropFarmer/internal/twitch/playback"
	"github.com/Du-vy/TDropFarmer/internal/twitch/predictions"
	"github.com/Du-vy/TDropFarmer/internal/twitch/realtime"
)

type App struct {
	config     config.Config
	logger     *slog.Logger
	tokenStore auth.TokenStore
}

func New(cfg config.Config, logger *slog.Logger, tokenStore auth.TokenStore) *App {
	return &App{config: cfg, logger: logger, tokenStore: tokenStore}
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info("starting tdropfarmer",
		slog.String("username", a.config.Account.Username),
		slog.Int("streamers", len(a.config.Streamers)),
		slog.Bool("dry_run", a.config.Features.DryRunEnabled()),
	)

	flow := auth.DeviceFlow{
		ClientID: a.config.Auth.ClientID,
		Scopes:   a.config.Auth.Scopes,
		Store:    a.tokenStore,
	}
	token, validation, err := flow.ValidToken(ctx)
	if err != nil {
		if errors.Is(err, store.ErrTokenNotFound) {
			return fmt.Errorf("no token found; run `tdropfarmer login --config <path>` first")
		}
		return fmt.Errorf("validate token: %w", err)
	}
	a.logger.Info("authenticated",
		slog.String("login", validation.Login),
		slog.String("user_id", validation.UserID),
		slog.Int("expires_in", validation.ExpiresIn),
	)

	helixClient := twitch.Client{
		ClientID:    a.config.Auth.ClientID,
		AccessToken: token.AccessToken,
	}
	streamers, err := helixClient.ResolveStreamers(ctx, streamerLogins(a.config.Streamers))
	if err != nil {
		return fmt.Errorf("resolve streamers: %w", err)
	}
	a.logger.Info("resolved streamers", slog.Int("count", len(streamers)))
	for _, streamer := range streamers {
		a.logger.Debug("resolved streamer",
			slog.String("login", streamer.Login),
			slog.String("id", streamer.ID),
			slog.String("display_name", streamer.DisplayName),
		)
	}

	gqlClient := gql.Client{
		ClientID:    a.config.Auth.ClientID,
		AccessToken: token.AccessToken,
	}

	contextLoader := channelpoints.ContextLoader{Client: gqlClient}
	initialEvents := a.loadChannelPointEvents(ctx, contextLoader, streamers)

	eng := engine.New(a.config, streamers, a.logger,
		engine.WithPointRecorder(store.NewStateStore(a.config.Storage.Path)),
		engine.WithBonusClaimer(channelpoints.GraphQLBonusClaimer{Client: gqlClient}),
	)
	eng.SetPredictionHandler(engine.NewPredictionAdapter(
		a.config,
		&predictions.PredictionClaimer{Client: gqlClient},
		a.logger,
		func(login string) int64 { return eng.PointsForStreamer(login) },
	))

	for _, event := range initialEvents {
		eng.SendEvent(event)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.pollOnlineStatus(ctx, eng, &helixClient, streamers)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.pollChannelPoints(ctx, eng, contextLoader, streamers)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.runMinuteWatched(ctx, eng, gqlClient, streamers)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.runEventSub(ctx, eng, &helixClient, gqlClient, streamers, &contextLoader)
	}()

	go func() {
		for event := range eng.Events() {
			a.logger.Debug("engine output event",
				slog.String("type", string(event.Type)),
				slog.String("streamer", event.Streamer),
			)
		}
	}()

	a.logger.Info("starting engine")
	runErr := eng.Run(ctx)

	wg.Wait()
	return runErr
}

func (a *App) pollOnlineStatus(ctx context.Context, eng *engine.Engine, client *twitch.Client, streamers []domain.Streamer) {
	userIDs := make([]string, 0, len(streamers))
	for _, s := range streamers {
		userIDs = append(userIDs, s.ID)
	}

	interval := 60 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	online := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			streams, err := client.GetStreams(ctx, userIDs)
			if err != nil {
				a.logger.Warn("check streams failed", slog.String("error", err.Error()))
				continue
			}
			currentOnline := make(map[string]bool)
			for _, stream := range streams {
				currentOnline[stream.UserLogin] = true
			}
			for _, s := range streamers {
				wasOnline := online[s.Login]
				isOnline := currentOnline[s.Login]
				online[s.Login] = isOnline

				if isOnline && !wasOnline {
					eng.SendEvent(engine.Event{
						Type:      engine.EventOnline,
						Streamer:  s.Login,
						ChannelID: s.ID,
						Payload:   nil,
						Time:      time.Now(),
					})
				}
				if !isOnline && wasOnline {
					eng.SendEvent(engine.Event{
						Type:      engine.EventOffline,
						Streamer:  s.Login,
						ChannelID: s.ID,
						Payload:   nil,
						Time:      time.Now(),
					})
				}
			}
		}
	}
}

func (a *App) runMinuteWatched(ctx context.Context, eng *engine.Engine, gqlClient gql.Client, streamers []domain.Streamer) {
	fetcher := playback.TokenFetcher{Client: gqlClient}
	watcher := playback.NewWatcher(fetcher)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			active := eng.ActiveStreamers()
			for _, login := range active {
				s := findStreamer(streamers, login)
				if s == nil {
					continue
				}
				if err := watcher.SendMinuteWatched(ctx, *s); err != nil {
					a.logger.Warn("minute watched failed",
						slog.String("streamer", login),
						slog.String("error", err.Error()),
					)
				}
			}
		}
	}
}

func (a *App) pollChannelPoints(ctx context.Context, eng *engine.Engine, loader channelpoints.ContextLoader, streamers []domain.Streamer) {
	interval := time.Duration(a.config.Watch.TickSeconds) * time.Second
	if interval < time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, event := range a.loadChannelPointEvents(ctx, loader, streamers) {
				eng.SendEvent(event)
			}
		}
	}
}

func (a *App) loadChannelPointEvents(ctx context.Context, loader channelpoints.ContextLoader, streamers []domain.Streamer) []engine.Event {
	events := make([]engine.Event, 0, len(streamers)*2)
	for _, streamer := range streamers {
		pointsContext, err := loader.Load(ctx, streamer.Login, streamer.ID)
		if err != nil {
			a.logger.Warn("load channel points context failed",
				slog.String("streamer", streamer.Login),
				slog.String("error", err.Error()),
			)
			continue
		}
		events = append(events, engine.Event{
			Type:      engine.EventBalance,
			Streamer:  streamer.Login,
			ChannelID: streamer.ID,
			Payload:   pointsContext.Balance,
		})
		if pointsContext.AvailableClaim != nil {
			events = append(events, engine.Event{
				Type:      engine.EventBonusAvailable,
				Streamer:  streamer.Login,
				ChannelID: streamer.ID,
				Payload:   *pointsContext.AvailableClaim,
			})
		}
	}
	return events
}

func findStreamer(streamers []domain.Streamer, login string) *domain.Streamer {
	for i := range streamers {
		if streamers[i].Login == login {
			return &streamers[i]
		}
	}
	return nil
}

func streamerLogins(streamers []config.StreamerConfig) []string {
	logins := make([]string, 0, len(streamers))
	for _, streamer := range streamers {
		logins = append(logins, streamer.Login)
	}
	return logins
}

func (a *App) runEventSub(ctx context.Context, eng *engine.Engine, helixClient *twitch.Client, gqlClient gql.Client, streamers []domain.Streamer, contextLoader *channelpoints.ContextLoader) {
	bridge := &eventSubBridge{
		engine:       eng,
		streamers:    streamers,
		pointsClient: contextLoader,
		logger:       a.logger,
		onSession: func(sessionID string) {
		},
	}

	realtimeClient := realtime.Client{
		ClientID:    a.config.Auth.ClientID,
		AccessToken: helixClient.AccessToken,
	}

	for {
		err := realtimeClient.Listen(ctx, bridge, a.logger)
		if err != nil {
			a.logger.Warn("eventsub listener exited", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}
