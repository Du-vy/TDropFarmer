package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/auth"
	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/engine"
	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/notify"
	"github.com/Du-vy/TDropFarmer/internal/twitch"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
	"github.com/Du-vy/TDropFarmer/internal/twitch/chat"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
	"github.com/Du-vy/TDropFarmer/internal/twitch/inventory"
	"github.com/Du-vy/TDropFarmer/internal/twitch/playback"
)

type App struct {
	config      config.Config
	logger      *slog.Logger
	tokenStore  auth.TokenStore
	chatMu      sync.Mutex
	chatCancels map[string]context.CancelFunc
}

func New(cfg config.Config, logger *slog.Logger, tokenStore auth.TokenStore) *App {
	return &App{
		config:      cfg,
		logger:      logger,
		tokenStore:  tokenStore,
		chatCancels: make(map[string]context.CancelFunc),
	}
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
			a.logger.Info("no token found; starting device login flow")
		} else {
			a.logger.Warn("existing token could not be used; starting device login flow", slog.String("error", err.Error()))
		}

		token, err = flow.Login(ctx, func(prompt auth.DevicePrompt) {
			fmt.Fprintf(os.Stdout, "Open %s and enter code %s\n", prompt.VerificationURI, prompt.UserCode)
			fmt.Fprintf(os.Stdout, "Code expires in %s\n", prompt.ExpiresIn.Round(0))
		})
		if err != nil {
			return fmt.Errorf("device login: %w", err)
		}

		_, validation, err = flow.ValidToken(ctx)
		if err != nil {
			return fmt.Errorf("validate login token: %w", err)
		}
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



	if a.config.Features.ClaimDropsEnabled() {
		inventoryClient := inventory.Client{Client: gqlClient}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.pollDrops(ctx, eng, inventoryClient)
		}()
	}

	var notifier *notify.DiscordNotifier
	if a.config.Notifications.Discord.Enabled {
		notifier = notify.NewDiscord(a.config.Notifications.Discord.WebhookURL)
		a.logger.Info("discord notifier configured", slog.String("webhook_url", a.config.Notifications.Discord.WebhookURL))
	}

	go func() {
		for event := range eng.Events() {
			a.logger.Debug("engine output event",
				slog.String("type", string(event.Type)),
				slog.String("streamer", event.Streamer),
			)

			if event.Type == engine.EventOnline {
				if a.shouldJoinChat(event.Streamer) {
					a.startChat(ctx, eng, event.Streamer, token.AccessToken)
				}
			}
			if event.Type == engine.EventOffline {
				a.stopChat(event.Streamer)
			}

			if notifier != nil {
				msg := a.formatEventMessage(event)
				if msg != "" {
					go func(message string) {
						if err := notifier.Send(context.Background(), message); err != nil {
							a.logger.Warn("discord notification failed", slog.String("error", err.Error()))
						}
					}(msg)
				}
			}
		}
	}()

	a.logger.Info("starting engine")
	runErr := eng.Run(ctx)

	wg.Wait()
	return runErr
}

func (a *App) pollDrops(ctx context.Context, eng *engine.Engine, invClient inventory.Client) {
	// First check immediately at startup
	a.checkAndClaimDrops(ctx, eng, invClient)

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.checkAndClaimDrops(ctx, eng, invClient)
		}
	}
}

func (a *App) checkAndClaimDrops(ctx context.Context, eng *engine.Engine, invClient inventory.Client) {
	drops, err := invClient.GetInventory(ctx)
	if err != nil {
		a.logger.Warn("fetch drops inventory failed", slog.String("error", err.Error()))
		return
	}

	for _, drop := range drops {
		if drop.IsClaimable {
			a.logger.Info("claiming drop",
				slog.String("id", drop.ID),
				slog.String("name", drop.Name),
				slog.String("instance_id", drop.DropInstanceID),
			)
			if a.config.Features.DryRunEnabled() {
				a.logger.Info("dry-run claim drop",
					slog.String("id", drop.ID),
					slog.String("name", drop.Name),
				)
				eng.SendEvent(engine.Event{
					Type:     engine.EventDropClaimed,
					Streamer: drop.CampaignID,
					Payload:  drop,
					Time:     time.Now().UTC(),
				})
				continue
			}

			success, err := invClient.ClaimDrop(ctx, drop.DropInstanceID)
			if err != nil {
				a.logger.Warn("claim drop failed",
					slog.String("id", drop.ID),
					slog.String("name", drop.Name),
					slog.String("error", err.Error()),
				)
				continue
			}
			if success {
				a.logger.Info("drop claimed successfully",
					slog.String("id", drop.ID),
					slog.String("name", drop.Name),
				)
				eng.SendEvent(engine.Event{
					Type:     engine.EventDropClaimed,
					Streamer: drop.CampaignID,
					Payload:  drop,
					Time:     time.Now().UTC(),
				})
			}
		}
	}
}

func (a *App) formatEventMessage(event engine.Event) string {
	switch event.Type {
	case engine.EventOnline:
		return fmt.Sprintf("🟢 Streamer **%s** is now ONLINE!", event.Streamer)
	case engine.EventOffline:
		return fmt.Sprintf("🔴 Streamer **%s** is now OFFLINE!", event.Streamer)
	case engine.EventBonusClaimed:
		if res, ok := event.Payload.(channelpoints.ClaimResult); ok {
			return fmt.Sprintf("💰 Claimed community bonus of **%d** points from **%s**!", res.Points, res.StreamerLogin)
		}
		return fmt.Sprintf("💰 Claimed community bonus from **%s**!", event.Streamer)
	case engine.EventDropClaimed:
		if d, ok := event.Payload.(inventory.Drop); ok {
			return fmt.Sprintf("🎁 Reclamado Drop: **%s** de campaña **%s**!", d.Name, d.CampaignID)
		}
	case engine.EventChatMention:
		if payloadStr, ok := event.Payload.(string); ok {
			return fmt.Sprintf("💬 Mención detectada en el chat de **%s**:\n%s", event.Streamer, payloadStr)
		}
	}
	return ""
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


func (a *App) startChat(ctx context.Context, eng *engine.Engine, login string, token string) {
	a.chatMu.Lock()
	defer a.chatMu.Unlock()

	if _, exists := a.chatCancels[login]; exists {
		return
	}

	chatCtx, cancel := context.WithCancel(ctx)
	a.chatCancels[login] = cancel

	username := a.config.Account.Username
	client := chat.NewClient(username, token, login, a.logger, func(sender, message string) {
		eng.SendEvent(engine.Event{
			Type:     engine.EventChatMention,
			Streamer: login,
			Payload:  fmt.Sprintf("[%s]: %s", sender, message),
			Time:     time.Now().UTC(),
		})
	})

	go func() {
		defer func() {
			a.chatMu.Lock()
			delete(a.chatCancels, login)
			a.chatMu.Unlock()
		}()
		if err := client.Run(chatCtx); err != nil && err != context.Canceled {
			a.logger.Warn("chat connection error", slog.String("channel", login), slog.String("error", err.Error()))
		}
	}()
}

func (a *App) stopChat(login string) {
	a.chatMu.Lock()
	defer a.chatMu.Unlock()

	if cancel, exists := a.chatCancels[login]; exists {
		cancel()
		delete(a.chatCancels, login)
		a.logger.Info("left chat presence", slog.String("channel", login))
	}
}

func (a *App) shouldJoinChat(login string) bool {
	for _, s := range a.config.Streamers {
		if s.Login == login {
			if s.Chat != nil {
				return *s.Chat
			}
			break
		}
	}
	return a.config.Features.ChatEnabled()
}
