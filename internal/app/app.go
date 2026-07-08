package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"math/rand/v2"

	"github.com/Du-vy/TDropFarmer/internal/auth"
	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/engine"
	"github.com/Du-vy/TDropFarmer/internal/notify"
	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/twitch"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
	"github.com/Du-vy/TDropFarmer/internal/twitch/chat"
	"github.com/Du-vy/TDropFarmer/internal/twitch/discovery"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
	"github.com/Du-vy/TDropFarmer/internal/twitch/inventory"
	"github.com/Du-vy/TDropFarmer/internal/twitch/playback"
)

type App struct {
	config                  config.Config
	logger                  *slog.Logger
	tokenStore              auth.TokenStore
	chatMu                  sync.Mutex
	chatCancels             map[string]context.CancelFunc
	streamersMu             sync.RWMutex
	staticStreamers         []domain.Streamer
	dynamicStreamers        []domain.Streamer
	activeGamesMu           sync.RWMutex
	activeGames             []string
	userID                  string
	dropsMu                 sync.RWMutex
	lastDrops               []inventory.Drop
	lastActiveCampaignDrops []inventory.Drop
	currentFarmingCampaign  string
	dropProgressStalls      map[string]dropProgressStallState
}

type dropProgressStallState struct {
	streamer       string
	progress       map[string]int
	unchangedPolls int
}

type gameStreamDiscoverer interface {
	GetLiveStreams(ctx context.Context, gameName string, limit int) ([]domain.Streamer, error)
}

func randomDuration(min, max time.Duration) time.Duration {
	if min >= max {
		return min
	}
	diff := max - min
	return min + time.Duration(rand.Int64N(int64(diff)))
}

const (
	targetGameDiscoveryRefreshInterval = 30 * time.Minute
	dropProgressStallUnchangedPolls    = 2
)

func New(cfg config.Config, logger *slog.Logger, tokenStore auth.TokenStore) *App {
	return &App{
		config:      cfg,
		logger:      logger,
		tokenStore:  tokenStore,
		chatCancels: make(map[string]context.CancelFunc),
	}
}

func (a *App) Run(ctx context.Context) error {
	banner := "\n\x1b[1;36m============================================================\x1b[0m\n" +
		"\x1b[1;33m              T D R O P   F A R M E R\x1b[0m\n" +
		"    Twitch Drops & Channel Points Farmer Bot in Go\n" +
		"\x1b[1;36m============================================================\x1b[0m\n\n"
	fmt.Print(banner)

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
	a.userID = validation.UserID

	// TODO: the access token is captured once here and shared by every client
	// for the lifetime of the process. If it expires or is revoked mid-run,
	// all API calls fail until restart. A shared refreshing token source would
	// fix this but requires re-plumbing the twitch/gql/chat clients.
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

	a.staticStreamers = streamers

	gqlClient := gql.Client{
		ClientID:    a.config.Auth.ClientID,
		AccessToken: token.AccessToken,
	}

	// Load initial active games from inventory or active campaigns if drops are enabled
	var initialActiveGames []string
	if a.config.Features.ClaimDropsEnabled() {
		inventoryClient := inventory.Client{Client: gqlClient, UserID: a.userID, Logger: a.logger, IgnoredGames: a.config.Watch.IgnoredGames}
		drops, errInv := inventoryClient.GetInventory(ctx)
		if errInv != nil {
			a.logger.Warn("initial inventory fetch failed", slog.String("error", errInv.Error()))
		} else {
			a.dropsMu.Lock()
			a.lastDrops = drops
			a.dropsMu.Unlock()
			initialActiveGames = a.sortActiveGames(ctx, inventoryClient, drops, "")
		}
		a.activeGamesMu.Lock()
		a.activeGames = initialActiveGames
		a.activeGamesMu.Unlock()
	}

	hasGamesConfigured := len(a.config.Watch.PriorityGames) > 0
	useFallbackAllCampaigns := a.config.Watch.FallbackAllCampaigns && a.config.Features.ClaimDropsEnabled()

	if hasGamesConfigured || useFallbackAllCampaigns {
		discClient := discovery.Client{Client: gqlClient, Logger: a.logger}
		a.logger.Info("performing initial target game discovery")
		discovered, err := a.discoverGamesStreamers(ctx, discClient, "")
		if err != nil {
			a.logger.Warn("initial games discovery failed", slog.String("error", err.Error()))
		} else {
			a.dynamicStreamers = discovered
			a.logger.Info("discovered dynamic streamers", slog.Int("count", len(discovered)))
		}
	}

	combinedStreamers := a.getCombinedStreamers()
	contextLoader := channelpoints.ContextLoader{Client: gqlClient}
	initialEvents := a.loadChannelPointEvents(ctx, contextLoader, streamers)

	eng := engine.New(a.config, combinedStreamers, a.logger,
		engine.WithPointRecorder(store.NewStateStore(a.config.Storage.Path)),
		engine.WithBonusClaimer(channelpoints.GraphQLBonusClaimer{Client: gqlClient}),
	)

	for _, event := range initialEvents {
		eng.SendEvent(event)
	}

	if len(initialActiveGames) > 0 {
		eng.SendEvent(engine.Event{
			Type:    engine.EventActiveGames,
			Payload: initialActiveGames,
			Time:    time.Now().UTC(),
		})
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.pollOnlineStatus(ctx, eng, &helixClient)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.pollChannelPoints(ctx, eng, contextLoader)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.runMinuteWatched(ctx, eng, gqlClient)
	}()

	if len(a.config.Watch.PriorityGames) > 0 || useFallbackAllCampaigns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.pollGameStreams(ctx, eng, gqlClient)
		}()
	}

	if a.config.Features.ClaimDropsEnabled() {
		inventoryClient := inventory.Client{Client: gqlClient, UserID: a.userID, Logger: a.logger, IgnoredGames: a.config.Watch.IgnoredGames}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.pollDrops(ctx, eng, inventoryClient)
		}()
	}

	var notifier *notify.DiscordNotifier
	if a.config.Notifications.Discord.Enabled {
		notifier = notify.NewDiscord(a.config.Notifications.Discord.WebhookURL)
		a.logger.Info("discord notifier configured")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range eng.Events() {
			a.logger.Debug("engine output event",
				slog.String("type", string(event.Type)),
				slog.String("streamer", event.Streamer),
			)

			if event.Type == engine.EventWatchStart {
				if a.shouldJoinChat(event.Streamer) {
					a.startChat(ctx, eng, event.Streamer, token.AccessToken)
				}

				s := a.findCombinedStreamer(event.Streamer)
				if s != nil && s.GameName != "" {
					campaignName, campaignID, gameImageURL, dropList := a.farmingCampaignForGame(s.GameName)
					if campaignID != "" {
						campaignChanged := a.setCurrentFarmingCampaign(campaignID)
						if notifier != nil && campaignChanged {

							var description string
							if len(dropList) > 0 {
								description = strings.Join(dropList, "\n")
							} else {
								description = "No drops found or campaign already completed."
							}

							payload := notify.WebhookPayload{
								Embeds: []notify.Embed{
									{
										Title:       "Started Farming Campaign!",
										Description: description,
										Color:       3447003, // Slate Blue (#3498DB)
										Author: &notify.EmbedAuthor{
											Name: s.GameName,
										},
										Fields: []notify.EmbedField{
											{
												Name:   "Game",
												Value:  s.GameName,
												Inline: true,
											},
											{
												Name:   "Campaign",
												Value:  campaignName,
												Inline: true,
											},
											{
												Name:   "Streamer",
												Value:  s.DisplayName,
												Inline: true,
											},
										},
										Footer: &notify.EmbedFooter{
											Text: "TDropFarmer Bot - by Duvy",
										},
										Timestamp: time.Now().UTC().Format(time.RFC3339),
									},
								},
							}
							if gameImageURL != "" {
								payload.Embeds[0].Author.IconURL = gameImageURL
								payload.Embeds[0].Thumbnail = &notify.EmbedMedia{URL: gameImageURL}
							}

							go func() {
								if err := notifier.Send(context.Background(), payload); err != nil {
									a.logger.Warn("discord notification failed", slog.String("error", err.Error()))
								}
							}()
						}
					}
				}
			}
			if event.Type == engine.EventWatchStop {
				a.stopChat(event.Streamer)
			}

			if notifier != nil {
				if event.Type == engine.EventDropClaimed {
					if d, ok := event.Payload.(inventory.Drop); ok {
						payload := notify.WebhookPayload{
							Embeds: []notify.Embed{
								{
									Title: "New Drop Claimed!",
									Color: 9520895, // Twitch Purple (#9146FF)
									Author: &notify.EmbedAuthor{
										Name: d.GameName,
									},
									Fields: []notify.EmbedField{
										{
											Name:   "Game",
											Value:  d.GameName,
											Inline: true,
										},
										{
											Name:   "Item",
											Value:  d.Name,
											Inline: true,
										},
									},
									Footer: &notify.EmbedFooter{
										Text: "TDropFarmer Bot - by Duvy",
									},
									Timestamp: time.Now().UTC().Format(time.RFC3339),
								},
							},
						}
						if d.GameImageURL != "" {
							payload.Embeds[0].Author.IconURL = d.GameImageURL
						}
						if d.ImageURL != "" {
							payload.Embeds[0].Thumbnail = &notify.EmbedMedia{URL: d.ImageURL}
						}

						go func() {
							if err := notifier.Send(context.Background(), payload); err != nil {
								a.logger.Warn("discord notification failed", slog.String("error", err.Error()))
							}
						}()
						continue
					}
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

	for {
		nextInterval := randomDuration(12*time.Minute, 18*time.Minute)
		timer := time.NewTimer(nextInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
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

	activeGamesMap := make(map[string]bool)
	for i := range drops {
		drop := drops[i]
		if !drop.IsClaimed && (drop.IsEarnable || drop.IsClaimable) {
			campaign := drop.CampaignName
			if campaign == "" {
				campaign = drop.CampaignID
			}
			a.logger.Info("drop progress update",
				slog.String("campaign", campaign),
				slog.String("game", drop.GameName),
				slog.String("name", drop.Name),
				slog.Int("current", drop.CurrentMinutes),
				slog.Int("required", drop.RequiredMinutes),
			)
			if drop.IsEarnable && drop.GameName != "" {
				activeGamesMap[drop.GameName] = true
			}
		}

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
				drops[i].IsClaimed = true
				drops[i].IsClaimable = false
				drops[i].IsEarnable = false
				drop = drops[i]

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

	a.rotateStalledDropStreamerIfNeeded(eng, drops)

	a.dropsMu.Lock()
	a.lastDrops = drops
	a.dropsMu.Unlock()

	activeGames := a.sortActiveGames(ctx, invClient, drops, a.activeDynamicDropGame(eng))

	a.activeGamesMu.Lock()
	a.activeGames = activeGames
	a.activeGamesMu.Unlock()

	a.logger.Info("active games updated",
		slog.Int("count", len(activeGames)),
		slog.Any("top", topGames(activeGames, 5)),
	)

	eng.SendEvent(engine.Event{
		Type:    engine.EventActiveGames,
		Payload: activeGames,
		Time:    time.Now().UTC(),
	})
}

func (a *App) formatEventMessage(event engine.Event) string {
	switch event.Type {
	case engine.EventWatchStart:
		return fmt.Sprintf("▶ Started watching **%s**", event.Streamer)
	case engine.EventWatchStop:
		return fmt.Sprintf("■ Stopped watching **%s**", event.Streamer)
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

func (a *App) getCombinedStreamers() []domain.Streamer {
	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()
	combined := make([]domain.Streamer, 0, len(a.staticStreamers)+len(a.dynamicStreamers))
	combined = append(combined, a.staticStreamers...)
	combined = append(combined, a.dynamicStreamers...)
	return combined
}

func (a *App) activeGamesSnapshot() []string {
	a.activeGamesMu.RLock()
	defer a.activeGamesMu.RUnlock()
	snapshot := make([]string, len(a.activeGames))
	copy(snapshot, a.activeGames)
	return snapshot
}

func activeGamesSignature(games []string) string {
	keys := make([]string, 0, len(games))
	for _, game := range games {
		keys = append(keys, gameKey(game))
	}
	return strings.Join(keys, "\x00")
}

func topGames(games []string, limit int) []string {
	if limit > len(games) {
		limit = len(games)
	}
	top := make([]string, limit)
	copy(top, games[:limit])
	return top
}

func (a *App) activeDynamicDropGame(eng *engine.Engine) string {
	_, game := a.activeDynamicDropStreamer(eng)
	return game
}

func (a *App) activeDynamicDropStreamer(eng *engine.Engine) (string, string) {
	active := eng.ActiveStreamers()
	if len(active) == 0 {
		return "", ""
	}

	activeLogins := make(map[string]bool, len(active))
	for _, login := range active {
		activeLogins[login] = true
	}

	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()
	for _, streamer := range a.dynamicStreamers {
		if activeLogins[streamer.Login] && streamer.GameName != "" {
			return streamer.Login, streamer.GameName
		}
	}
	return "", ""
}

func (a *App) activeDropStreamer(eng *engine.Engine) *domain.Streamer {
	active := eng.ActiveStreamers()
	if len(active) == 0 {
		return nil
	}

	activeLogins := make(map[string]bool, len(active))
	for _, login := range active {
		activeLogins[login] = true
	}

	activeGames := a.activeGamesSnapshot()
	isActiveGame := func(gameName string) bool {
		for _, activeGame := range activeGames {
			if strings.EqualFold(gameName, activeGame) {
				return true
			}
		}
		return false
	}

	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()

	for _, streamer := range a.dynamicStreamers {
		if activeLogins[streamer.Login] && isActiveGame(streamer.GameName) {
			selected := streamer
			return &selected
		}
	}
	for _, streamer := range a.staticStreamers {
		if activeLogins[streamer.Login] && isActiveGame(streamer.GameName) {
			selected := streamer
			return &selected
		}
	}
	return nil
}

func (a *App) getStreamersToPoll(eng *engine.Engine) []domain.Streamer {
	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()

	polled := make([]domain.Streamer, 0, len(a.staticStreamers))
	polled = append(polled, a.staticStreamers...)

	activeLogins := eng.ActiveStreamers()
	activeMap := make(map[string]bool, len(activeLogins))
	for _, login := range activeLogins {
		activeMap[login] = true
	}

	for _, s := range a.dynamicStreamers {
		if activeMap[s.Login] {
			polled = append(polled, s)
		}
	}

	return polled
}

func (a *App) findCombinedStreamer(login string) *domain.Streamer {
	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()
	for i := range a.staticStreamers {
		if a.staticStreamers[i].Login == login {
			return &a.staticStreamers[i]
		}
	}
	for i := range a.dynamicStreamers {
		if a.dynamicStreamers[i].Login == login {
			return &a.dynamicStreamers[i]
		}
	}
	return nil
}

func (a *App) removeDynamicStreamer(login string) bool {
	a.streamersMu.Lock()
	defer a.streamersMu.Unlock()

	for i := range a.dynamicStreamers {
		if strings.EqualFold(a.dynamicStreamers[i].Login, login) {
			a.dynamicStreamers = append(a.dynamicStreamers[:i], a.dynamicStreamers[i+1:]...)
			return true
		}
	}
	return false
}

func (a *App) rotateStalledDropStreamerIfNeeded(eng *engine.Engine, drops []inventory.Drop) {
	streamer, game := a.activeDynamicDropStreamer(eng)
	if streamer == "" || game == "" {
		return
	}

	progress := dropProgressSnapshot(drops, game)
	key := gameKey(game)
	if len(progress) == 0 {
		delete(a.dropProgressStalls, key)
		return
	}
	if a.dropProgressStalls == nil {
		a.dropProgressStalls = make(map[string]dropProgressStallState)
	}

	state, ok := a.dropProgressStalls[key]
	if !ok || state.streamer != streamer || dropProgressSnapshotAdvanced(state.progress, progress) || !dropProgressSnapshotEqual(state.progress, progress) {
		a.dropProgressStalls[key] = dropProgressStallState{streamer: streamer, progress: progress}
		return
	}

	state.unchangedPolls++
	if state.unchangedPolls < dropProgressStallUnchangedPolls {
		state.progress = progress
		a.dropProgressStalls[key] = state
		return
	}

	if a.logger != nil {
		a.logger.Warn("drop progress stalled; rotating streamer",
			slog.String("streamer", streamer),
			slog.String("game", game),
			slog.Int("unchanged_polls", state.unchangedPolls),
		)
	}
	delete(a.dropProgressStalls, key)

	if !a.removeDynamicStreamer(streamer) {
		return
	}

	eng.SendEvent(engine.Event{
		Type:    engine.EventUpdateStreamers,
		Payload: a.getCombinedStreamers(),
		Time:    time.Now().UTC(),
	})
}

func dropProgressSnapshot(drops []inventory.Drop, gameName string) map[string]int {
	progress := make(map[string]int)
	for _, drop := range drops {
		if !strings.EqualFold(drop.GameName, gameName) || drop.IsClaimed || !drop.IsEarnable {
			continue
		}
		if drop.RequiredMinutes > 0 && drop.CurrentMinutes >= drop.RequiredMinutes {
			continue
		}

		key := drop.ID
		if key == "" {
			key = drop.CampaignID + "\x00" + drop.Name
		}
		progress[key] = drop.CurrentMinutes
	}
	return progress
}

func dropProgressSnapshotAdvanced(previous, current map[string]int) bool {
	for key, currentMinutes := range current {
		if currentMinutes > previous[key] {
			return true
		}
	}
	return false
}

func dropProgressSnapshotEqual(previous, current map[string]int) bool {
	if len(previous) != len(current) {
		return false
	}
	for key, currentMinutes := range current {
		if previous[key] != currentMinutes {
			return false
		}
	}
	return true
}

func (a *App) setCurrentFarmingCampaign(campaignID string) bool {
	a.activeGamesMu.Lock()
	defer a.activeGamesMu.Unlock()
	if a.currentFarmingCampaign == campaignID {
		return false
	}
	a.currentFarmingCampaign = campaignID
	return true
}

func (a *App) currentFarmingCampaignSnapshot() string {
	a.activeGamesMu.RLock()
	defer a.activeGamesMu.RUnlock()
	return a.currentFarmingCampaign
}

func (a *App) farmingCampaignForGame(gameName string) (campaignName, campaignID, gameImageURL string, dropList []string) {
	a.dropsMu.RLock()
	defer a.dropsMu.RUnlock()

	seenDrops := make(map[string]bool)
	for _, d := range a.lastDrops {
		if !strings.EqualFold(d.GameName, gameName) {
			continue
		}
		if campaignName == "" {
			campaignName = d.CampaignName
			if campaignName == "" {
				campaignName = d.CampaignID
			}
			campaignID = d.CampaignID
			gameImageURL = d.GameImageURL
		}
		if seenDrops[d.ID] {
			continue
		}
		seenDrops[d.ID] = true
		status := fmt.Sprintf("%d/%d min", d.CurrentMinutes, d.RequiredMinutes)
		if d.IsClaimed {
			status = "Claimed"
		} else if d.IsClaimable {
			status = "Claimable"
		}
		dropList = append(dropList, fmt.Sprintf("• **%s** (%s)", d.Name, status))
	}

	if campaignID != "" {
		return campaignName, campaignID, gameImageURL, dropList
	}

	for _, d := range a.lastActiveCampaignDrops {
		if !strings.EqualFold(d.GameName, gameName) {
			continue
		}
		if campaignName == "" {
			campaignName = d.CampaignName
			if campaignName == "" {
				campaignName = d.CampaignID
			}
			campaignID = d.CampaignID
			gameImageURL = d.GameImageURL
		}
		if seenDrops[d.ID] {
			continue
		}
		seenDrops[d.ID] = true
		status := fmt.Sprintf("%d/%d min", d.CurrentMinutes, d.RequiredMinutes)
		if d.IsClaimed {
			status = "Claimed"
		} else if d.IsClaimable {
			status = "Claimable"
		}
		dropList = append(dropList, fmt.Sprintf("• **%s** (%s)", d.Name, status))
	}

	return campaignName, campaignID, gameImageURL, dropList
}

func (a *App) discoverGamesStreamers(ctx context.Context, client gameStreamDiscoverer, stickyGameName string) ([]domain.Streamer, error) {
	seen := make(map[string]bool)

	a.streamersMu.RLock()
	for _, s := range a.staticStreamers {
		seen[s.Login] = true
	}
	a.streamersMu.RUnlock()

	var gamesToDiscover []string
	useFallbackAllCampaigns := a.config.Watch.FallbackAllCampaigns && a.config.Features.ClaimDropsEnabled()
	if useFallbackAllCampaigns {
		a.activeGamesMu.RLock()
		gamesToDiscover = make([]string, len(a.activeGames))
		copy(gamesToDiscover, a.activeGames)
		a.activeGamesMu.RUnlock()
		gamesToDiscover = a.stabilizeActiveGamesByRank(gamesToDiscover, stickyGameName)
	} else {
		gamesToDiscover = a.config.Watch.PriorityGames
	}

	for i, game := range gamesToDiscover {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(randomDuration(500*time.Millisecond, 1500*time.Millisecond)):
			}
		}

		a.logger.Debug("discovering live streams for game", slog.String("game", game))
		streamers, err := client.GetLiveStreams(ctx, game, 3)
		if err != nil {
			a.logger.Warn("discover game streams failed", slog.String("game", game), slog.String("error", err.Error()))
			continue
		}
		if len(streamers) == 0 {
			continue
		}

		var targetStreamers []domain.Streamer
		for _, s := range streamers {
			if !seen[s.Login] {
				seen[s.Login] = true
				targetStreamers = append(targetStreamers, s)
			}
		}

		a.logger.Info("game discovery selected target",
			slog.String("game", game),
			slog.Int("count", len(targetStreamers)),
		)
		return targetStreamers, nil
	}
	return nil, nil
}

func (a *App) pollGameStreams(ctx context.Context, eng *engine.Engine, gqlClient gql.Client) {
	discClient := discovery.Client{Client: gqlClient, Logger: a.logger}
	lastDiscoveryAt := time.Now()

	for {
		nextInterval := randomDuration(4*time.Minute, 6*time.Minute)
		timer := time.NewTimer(nextInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			activeGames := a.activeGamesSnapshot()
			stickyGameName := a.activeDynamicDropGame(eng)
			if stickyGameName != "" && a.canKeepCurrentFarmingGame(activeGames, stickyGameName) && time.Since(lastDiscoveryAt) < targetGameDiscoveryRefreshInterval {
				a.logger.Debug("skipping target game discovery; active dynamic streamer is still valid")
				continue
			}

			a.logger.Info("polling target game discovery")
			discovered, err := a.discoverGamesStreamers(ctx, discClient, stickyGameName)
			if err != nil {
				a.logger.Warn("games discovery failed", slog.String("error", err.Error()))
				continue
			}
			lastDiscoveryAt = time.Now()

			a.streamersMu.Lock()
			a.dynamicStreamers = discovered
			a.streamersMu.Unlock()

			a.logger.Info("target game discovery completed", slog.Int("count", len(discovered)))

			combined := a.getCombinedStreamers()
			eng.SendEvent(engine.Event{
				Type:    engine.EventUpdateStreamers,
				Payload: combined,
				Time:    time.Now().UTC(),
			})
		}
	}
}

func (a *App) pollOnlineStatus(ctx context.Context, eng *engine.Engine, client *twitch.Client) {
	online := make(map[string]bool)

	for {
		nextInterval := randomDuration(50*time.Second, 70*time.Second)
		timer := time.NewTimer(nextInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			streamers := a.getCombinedStreamers()
			if len(streamers) == 0 {
				continue
			}
			userIDs := make([]string, 0, len(streamers))
			for _, s := range streamers {
				userIDs = append(userIDs, s.ID)
			}

			streams, err := client.GetAllStreams(ctx, userIDs)
			if err != nil {
				// Keep the previous online state rather than treating a failed
				// poll as everyone going offline.
				a.logger.Warn("check streams failed; keeping previous online status", slog.String("error", err.Error()))
				continue
			}

			currentOnline := make(map[string]twitch.StreamInfo)
			for _, stream := range streams {
				currentOnline[stream.UserLogin] = stream
			}

			a.streamersMu.Lock()
			for _, s := range streamers {
				wasOnline := online[s.Login]
				streamInfo, isOnline := currentOnline[s.Login]
				online[s.Login] = isOnline

				// Update BroadcastID inside protected slices
				for i := range a.staticStreamers {
					if a.staticStreamers[i].Login == s.Login {
						if isOnline {
							a.staticStreamers[i].BroadcastID = streamInfo.ID
							a.staticStreamers[i].GameID = streamInfo.GameID
							a.staticStreamers[i].GameName = streamInfo.GameName
							a.staticStreamers[i].Title = streamInfo.Title
						} else {
							a.staticStreamers[i].BroadcastID = ""
							a.staticStreamers[i].GameID = ""
						}
					}
				}
				for i := range a.dynamicStreamers {
					if a.dynamicStreamers[i].Login == s.Login {
						if isOnline {
							a.dynamicStreamers[i].BroadcastID = streamInfo.ID
							a.dynamicStreamers[i].GameID = streamInfo.GameID
							a.dynamicStreamers[i].GameName = streamInfo.GameName
							a.dynamicStreamers[i].Title = streamInfo.Title
						} else {
							a.dynamicStreamers[i].BroadcastID = ""
							a.dynamicStreamers[i].GameID = ""
						}
					}
				}

				if isOnline && !wasOnline {
					eng.SendEvent(engine.Event{
						Type:      engine.EventOnline,
						Streamer:  s.Login,
						ChannelID: s.ID,
						Payload:   streamInfo,
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
			a.streamersMu.Unlock()
		}
	}
}

func (a *App) runMinuteWatched(ctx context.Context, eng *engine.Engine, gqlClient gql.Client) {
	// Use the same Client ID and auth token as the rest of the app.
	// The Browser Client ID requires Client-Integrity tokens that we don't
	// generate, so we use the Android App Client ID which works without them.
	fetcher := playback.TokenFetcher{Client: gqlClient}
	watcher := playback.NewWatcher(fetcher)

	a.logger.Info("watch telemetry configured", slog.String("transport", "graphql_send_spade_events"))

	for {
		nextInterval := randomDuration(55*time.Second, 65*time.Second)
		timer := time.NewTimer(nextInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s := a.activeDropStreamer(eng)
			if s == nil {
				continue
			}
			if err := watcher.SendMinuteWatched(ctx, *s, a.userID); err != nil {
				a.logger.Warn("minute watched failed",
					slog.String("streamer", s.Login),
					slog.String("error", err.Error()),
				)
				continue
			} else {
				a.logger.Debug("minute watched sent",
					slog.String("streamer", s.Login),
					slog.String("game", s.GameName),
				)
			}
		}
	}
}

func (a *App) pollChannelPoints(ctx context.Context, eng *engine.Engine, loader channelpoints.ContextLoader) {
	baseInterval := time.Duration(a.config.Watch.TickSeconds) * time.Second
	if baseInterval < time.Minute {
		baseInterval = time.Minute
	}

	for {
		nextInterval := randomDuration(baseInterval-10*time.Second, baseInterval+10*time.Second)
		timer := time.NewTimer(nextInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			streamers := a.getStreamersToPoll(eng)
			for _, event := range a.loadChannelPointEvents(ctx, loader, streamers) {
				eng.SendEvent(event)
			}
		}
	}
}

func (a *App) loadChannelPointEvents(ctx context.Context, loader channelpoints.ContextLoader, streamers []domain.Streamer) []engine.Event {
	events := make([]engine.Event, 0, len(streamers)*2)
	for i, streamer := range streamers {
		if i > 0 {
			select {
			case <-ctx.Done():
				return events
			case <-time.After(randomDuration(1*time.Second, 3*time.Second)):
			}
		}

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

func (a *App) isPriorityGame(gameName string) bool {
	for _, pg := range a.config.Watch.PriorityGames {
		if strings.EqualFold(pg, gameName) {
			return true
		}
	}
	return false
}

func gameKey(gameName string) string {
	return strings.ToLower(strings.TrimSpace(gameName))
}

const (
	activeGameRankPriorityInProgress = iota
	activeGameRankPriorityAvailable
	activeGameRankOtherInProgress
	activeGameRankOtherAvailable
)

func appendActiveGame(games *[]string, ranks map[string]int, gameName string, rank int) {
	if gameName == "" {
		return
	}
	key := gameKey(gameName)
	*games = append(*games, gameName)
	ranks[key] = rank
}

func (a *App) stabilizeCurrentFarmingGame(sortedGames []string, ranks map[string]int, stickyGameName string) []string {
	stickyKey := gameKey(stickyGameName)
	if stickyKey == "" || len(sortedGames) < 2 {
		return sortedGames
	}

	currentIndex := -1
	currentRank := 0
	for i, game := range sortedGames {
		key := gameKey(game)
		if key == stickyKey {
			currentIndex = i
			currentRank = ranks[key]
			break
		}
	}
	if currentIndex <= 0 {
		return sortedGames
	}

	insertIndex := 0
	for insertIndex < len(sortedGames) {
		key := gameKey(sortedGames[insertIndex])
		if ranks[key] >= currentRank {
			break
		}
		insertIndex++
	}
	if insertIndex == currentIndex {
		return sortedGames
	}

	reordered := make([]string, 0, len(sortedGames))
	reordered = append(reordered, sortedGames[:insertIndex]...)
	reordered = append(reordered, sortedGames[currentIndex])
	reordered = append(reordered, sortedGames[insertIndex:currentIndex]...)
	reordered = append(reordered, sortedGames[currentIndex+1:]...)
	return reordered
}

func (a *App) activeGameRanksSnapshot(games []string) map[string]int {
	inProgress := make(map[string]bool)
	a.dropsMu.RLock()
	for _, drop := range a.lastDrops {
		if drop.GameName == "" || !drop.IsEarnable || drop.IsClaimed {
			continue
		}
		inProgress[gameKey(drop.GameName)] = true
	}
	a.dropsMu.RUnlock()

	ranks := make(map[string]int, len(games))
	for _, game := range games {
		key := gameKey(game)
		if key == "" {
			continue
		}
		if a.isPriorityGame(game) {
			if inProgress[key] {
				ranks[key] = activeGameRankPriorityInProgress
			} else {
				ranks[key] = activeGameRankPriorityAvailable
			}
			continue
		}
		if inProgress[key] {
			ranks[key] = activeGameRankOtherInProgress
		} else {
			ranks[key] = activeGameRankOtherAvailable
		}
	}
	return ranks
}

func (a *App) stabilizeActiveGamesByRank(games []string, stickyGameName string) []string {
	return a.stabilizeCurrentFarmingGame(games, a.activeGameRanksSnapshot(games), stickyGameName)
}

func (a *App) canKeepCurrentFarmingGame(games []string, stickyGameName string) bool {
	stickyKey := gameKey(stickyGameName)
	if stickyKey == "" {
		return false
	}

	ranks := a.activeGameRanksSnapshot(games)
	stickyRank, ok := ranks[stickyKey]
	if !ok {
		return false
	}

	for _, game := range games {
		key := gameKey(game)
		rank, ok := ranks[key]
		if ok && rank < stickyRank {
			return false
		}
	}
	return true
}

func (a *App) sortActiveGames(ctx context.Context, invClient inventory.Client, drops []inventory.Drop, stickyGameName string) []string {
	// Categorize in-progress games
	var priorityInProgress []string
	var otherInProgress []string
	inProgressMap := make(map[string]bool)
	addedInProgress := make(map[string]bool)

	// Keep track of games that have drops and games that have unclaimed drops
	hasAnyDrops := make(map[string]bool)
	hasUnclaimed := make(map[string]bool)

	for _, drop := range drops {
		if drop.GameName != "" {
			key := gameKey(drop.GameName)
			hasAnyDrops[key] = true
			if !drop.IsClaimed {
				hasUnclaimed[key] = true
				inProgressMap[key] = true
			}
		}
	}

	for _, drop := range drops {
		if drop.IsEarnable && drop.GameName != "" {
			key := gameKey(drop.GameName)
			if !addedInProgress[key] {
				addedInProgress[key] = true
				if a.isPriorityGame(drop.GameName) {
					priorityInProgress = append(priorityInProgress, drop.GameName)
				} else {
					otherInProgress = append(otherInProgress, drop.GameName)
				}
			}
		}
	}

	// Categorize available games
	var priorityConnectedAvailable []string
	var priorityUnconnectedAvailable []string
	var otherConnectedAvailable []string
	var otherUnconnectedAvailable []string

	if a.config.Watch.AutoStartCampaigns {
		availableConnected, availableUnconnected, allDrops, err := invClient.GetActiveCampaignGames(ctx)
		if err != nil {
			a.logger.Warn("fetch active campaign games failed", slog.String("error", err.Error()))
		} else {
			a.dropsMu.Lock()
			a.lastActiveCampaignDrops = allDrops
			a.dropsMu.Unlock()
			seenConnected := make(map[string]bool)
			for _, game := range availableConnected {
				if game == "" || inProgressMap[gameKey(game)] {
					continue
				}
				seenConnected[gameKey(game)] = true
				if a.isPriorityGame(game) {
					priorityConnectedAvailable = append(priorityConnectedAvailable, game)
				} else {
					otherConnectedAvailable = append(otherConnectedAvailable, game)
				}
			}
			for _, game := range availableUnconnected {
				key := gameKey(game)
				if game == "" || inProgressMap[key] || seenConnected[key] {
					continue
				}
				if a.isPriorityGame(game) {
					priorityUnconnectedAvailable = append(priorityUnconnectedAvailable, game)
				} else {
					otherUnconnectedAvailable = append(otherUnconnectedAvailable, game)
				}
			}
		}
	}

	var sortedGames []string
	gameRanks := make(map[string]int)
	useFallbackAllCampaigns := a.config.Watch.FallbackAllCampaigns && a.config.Features.ClaimDropsEnabled()

	if useFallbackAllCampaigns {
		for _, game := range priorityInProgress {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankPriorityInProgress)
		}
		for _, game := range priorityConnectedAvailable {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankPriorityAvailable)
		}
		for _, game := range priorityUnconnectedAvailable {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankPriorityAvailable)
		}
		for _, game := range otherInProgress {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankOtherInProgress)
		}
		for _, game := range otherConnectedAvailable {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankOtherAvailable)
		}
		for _, game := range otherUnconnectedAvailable {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankOtherAvailable)
		}
	} else {
		for _, game := range priorityInProgress {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankPriorityInProgress)
		}
		for _, game := range priorityConnectedAvailable {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankPriorityAvailable)
		}
		for _, game := range priorityUnconnectedAvailable {
			appendActiveGame(&sortedGames, gameRanks, game, activeGameRankPriorityAvailable)
		}
		for _, pg := range a.config.Watch.PriorityGames {
			pgKey := gameKey(pg)
			if hasAnyDrops[pgKey] && !hasUnclaimed[pgKey] {
				continue
			}

			found := false
			for _, g := range sortedGames {
				if strings.EqualFold(g, pg) {
					found = true
					break
				}
			}
			if !found {
				appendActiveGame(&sortedGames, gameRanks, pg, activeGameRankPriorityAvailable)
			}
		}
	}

	filtered := sortedGames[:0]
	for _, game := range sortedGames {
		key := gameKey(game)
		if hasAnyDrops[key] && !hasUnclaimed[key] {
			if a.logger != nil {
				a.logger.Debug("excluding game with all drops claimed",
					slog.String("game", game),
				)
			}
			continue
		}
		filtered = append(filtered, game)
	}
	sortedGames = filtered
	sortedGames = a.stabilizeCurrentFarmingGame(sortedGames, gameRanks, stickyGameName)

	return sortedGames
}
