package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
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
	// rotatedStreamerCooldowns is guarded by streamersMu.
	rotatedStreamerCooldowns map[string]time.Time
	notifier                 *notify.DiscordNotifier
	watchCreditMu            sync.Mutex
	watchCreditProgress      map[string]int
	lastWatchCreditAt        time.Time
	watchCreditAlerted       bool
	auxiliaryWatchDisabled   bool
	auxiliaryWatchCancel     context.CancelFunc
}

type dropProgressStallState struct {
	streamer       string
	progress       map[string]int
	unchangedPolls int
}

type watchTelemetrySender interface {
	SendMinuteWatched(context.Context, domain.Streamer, string) error
	SendPresence(context.Context, domain.Streamer, string) error
}

type watchTelemetryState struct {
	primarySession          string
	primarySuccessfulPulses int
	nextPrimaryPulse        time.Time
	auxiliaryLogin          string
	auxiliarySession        string
	auxiliaryLeaseStarted   time.Time
	nextAuxiliaryPulse      time.Time
	auxiliaryFailures       int
	lastAuxiliaryWatch      map[string]time.Time
	auxiliaryBackoff        map[string]time.Time
}

type gameStreamDiscoverer interface {
	GetLiveStreamsForCampaigns(ctx context.Context, gameName string, campaignIDs []string, limit int) ([]domain.Streamer, error)
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

	// Keep a stall-rotated streamer out of discovery long enough that the next
	// discovery pass picks someone else instead of re-adding them minutes later.
	stallRotationCooldown = 90 * time.Minute

	// How long the account may go without a single credited watch minute, while
	// actively watching a drop streamer, before the watchdog raises an alert.
	watchCreditStallThreshold = time.Hour
	watchTelemetryTick        = 5 * time.Second
	auxiliaryPulseOffset      = 30 * time.Second
	primaryPulseSafetyMargin  = 5 * time.Second
	minimumAuxiliaryWindow    = 10 * time.Second
	primaryPulsesBeforeAux    = 2
	maxAuxiliaryFailures      = 3
	auxiliaryFailureBackoff   = 15 * time.Minute
	channelPointsMaxBackoff   = 15 * time.Minute
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
	initialCampaignsLoaded := false
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
			initialCampaignsLoaded = true
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
	initialEvents, initialContextErr := a.loadChannelPointEvents(ctx, contextLoader, streamers)
	if initialContextErr != nil && !errors.Is(initialContextErr, context.Canceled) {
		a.logger.Warn("initial channel points load failed", slog.String("error", initialContextErr.Error()))
	}

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

	var notifier *notify.DiscordNotifier
	if a.config.Notifications.Discord.Enabled {
		notifier = notify.NewDiscord(a.config.Notifications.Discord.WebhookURL)
		a.logger.Info("discord notifier configured")
	}
	// Set before the goroutines below start: the watch-credit watchdog inside
	// pollDrops reads it.
	a.notifier = notifier

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
			a.pollDrops(ctx, eng, inventoryClient, initialCampaignsLoaded)
		}()
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
					campaignName, campaignKey, gameImageURL, dropList := a.farmingCampaignForGame(s.GameName)
					if campaignKey != "" {
						campaignChanged := a.setCurrentFarmingCampaign(campaignKey)
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

func (a *App) pollDrops(ctx context.Context, eng *engine.Engine, invClient inventory.Client, initialCampaignsLoaded bool) {
	// Inventory and claims still run immediately, but the expensive campaign
	// scan was already completed synchronously before initial discovery.
	a.checkAndClaimDropsWithCampaignRefresh(ctx, eng, invClient, !initialCampaignsLoaded)

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
	a.checkAndClaimDropsWithCampaignRefresh(ctx, eng, invClient, true)
}

func (a *App) checkAndClaimDropsWithCampaignRefresh(ctx context.Context, eng *engine.Engine, invClient inventory.Client, refreshActiveGames bool) {
	drops, err := invClient.GetInventory(ctx)
	if err != nil {
		a.logger.Warn("fetch drops inventory failed", slog.String("error", err.Error()))
		return
	}

	a.checkWatchCreditWatchdog(eng, drops)

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
				refreshActiveGames = true
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
	if !refreshActiveGames {
		return
	}

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

func (a *App) activeCampaignIDsForGame(gameName string) []string {
	a.dropsMu.RLock()
	defer a.dropsMu.RUnlock()
	return collectActiveCampaignIDsForGame(gameName, a.lastDrops, a.lastActiveCampaignDrops)
}

func collectActiveCampaignIDsForGame(gameName string, dropSets ...[]inventory.Drop) []string {
	seen := make(map[string]bool)
	var campaignIDs []string
	for _, drops := range dropSets {
		for _, drop := range drops {
			if !strings.EqualFold(drop.GameName, gameName) || drop.CampaignID == "" || drop.IsClaimed || !drop.IsEarnable || drop.RequiredMinutes <= 0 || seen[drop.CampaignID] {
				continue
			}
			seen[drop.CampaignID] = true
			campaignIDs = append(campaignIDs, drop.CampaignID)
		}
	}
	sort.Strings(campaignIDs)
	return campaignIDs
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

func (a *App) markStreamerRotationCooldown(login string) {
	now := time.Now()
	a.streamersMu.Lock()
	defer a.streamersMu.Unlock()

	if a.rotatedStreamerCooldowns == nil {
		a.rotatedStreamerCooldowns = make(map[string]time.Time)
	}
	for key, until := range a.rotatedStreamerCooldowns {
		if now.After(until) {
			delete(a.rotatedStreamerCooldowns, key)
		}
	}
	a.rotatedStreamerCooldowns[strings.ToLower(login)] = now.Add(stallRotationCooldown)
}

func (a *App) isStreamerOnRotationCooldown(login string) bool {
	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()
	until, ok := a.rotatedStreamerCooldowns[strings.ToLower(login)]
	return ok && time.Now().Before(until)
}

func (a *App) rotateStalledDropStreamerIfNeeded(eng *engine.Engine, drops []inventory.Drop) {
	streamer, game := a.activeDynamicDropStreamer(eng)
	if streamer == "" || game == "" {
		return
	}

	// An empty snapshot is tracked like frozen progress: a watched game that
	// never enters the inventory (no minute ever credited) must rotate too,
	// otherwise restricted or non-crediting campaigns are farmed forever.
	progress := dropProgressSnapshot(drops, game)
	key := gameKey(game)
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
			slog.Bool("game_missing_from_inventory", len(progress) == 0),
		)
	}
	delete(a.dropProgressStalls, key)
	a.disableAuxiliaryWatch("drop_progress_stalled")

	a.markStreamerRotationCooldown(streamer)

	if !a.removeDynamicStreamer(streamer) {
		return
	}

	eng.SendEvent(engine.Event{
		Type:    engine.EventUpdateStreamers,
		Payload: a.getCombinedStreamers(),
		Time:    time.Now().UTC(),
	})
}

// checkWatchCreditWatchdog tracks whether Twitch is still crediting watch
// time at all. Progress on any drop (or a campaign newly entering the
// inventory) counts as credit. If nothing has been credited for
// watchCreditStallThreshold while a drop streamer is actively watched, the
// telemetry is being accepted but ignored, which an HTTP success alone cannot
// detect.
func (a *App) checkWatchCreditWatchdog(eng *engine.Engine, drops []inventory.Drop) {
	watchedStreamer := a.activeDropStreamer(eng)

	progress := make(map[string]int, len(drops))
	for _, drop := range drops {
		if drop.IsClaimed {
			continue
		}
		key := drop.ID
		if key == "" {
			key = drop.CampaignID + "\x00" + drop.Name
		}
		progress[key] = drop.CurrentMinutes
	}

	now := time.Now()

	a.watchCreditMu.Lock()
	defer a.watchCreditMu.Unlock()

	// The first poll only establishes the baseline.
	credited := a.watchCreditProgress == nil
	if !credited {
		for key, minutes := range progress {
			previous, tracked := a.watchCreditProgress[key]
			if !tracked || minutes > previous {
				credited = true
				break
			}
		}
	}
	a.watchCreditProgress = progress

	// While nothing is being watched no credit is expected, so the clock only
	// runs when a drop streamer is active.
	if credited || watchedStreamer == nil {
		if credited && a.watchCreditAlerted && a.logger != nil {
			a.logger.Info("drop watch credit resumed")
		}
		a.lastWatchCreditAt = now
		a.watchCreditAlerted = false
		return
	}

	stalledFor := now.Sub(a.lastWatchCreditAt)
	if stalledFor < watchCreditStallThreshold {
		return
	}

	if a.logger != nil {
		a.logger.Warn("no watch minutes credited while actively watching; Twitch may have stopped counting this session",
			slog.Duration("stalled_for", stalledFor.Round(time.Minute)),
			slog.String("streamer", watchedStreamer.Login),
			slog.String("game", watchedStreamer.GameName),
		)
	}

	firstAlert := !a.watchCreditAlerted
	a.watchCreditAlerted = true
	a.auxiliaryWatchDisabled = true
	if a.auxiliaryWatchCancel != nil {
		a.auxiliaryWatchCancel()
		a.auxiliaryWatchCancel = nil
	}
	if !firstAlert || a.notifier == nil {
		return
	}

	streamerName := watchedStreamer.DisplayName
	if streamerName == "" {
		streamerName = watchedStreamer.Login
	}
	payload := notify.WebhookPayload{
		Embeds: []notify.Embed{
			{
				Title:       "Watch Credit Stalled!",
				Description: fmt.Sprintf("No drop minutes have been credited for **%s** while actively watching. Twitch may have stopped counting this session's watch time — consider restarting the bot.", stalledFor.Round(time.Minute)),
				Color:       15158332, // Red (#E74C3C)
				Fields: []notify.EmbedField{
					{
						Name:   "Streamer",
						Value:  streamerName,
						Inline: true,
					},
					{
						Name:   "Game",
						Value:  watchedStreamer.GameName,
						Inline: true,
					},
				},
				Footer: &notify.EmbedFooter{
					Text: "TDropFarmer Bot - by Duvy",
				},
				Timestamp: now.UTC().Format(time.RFC3339),
			},
		},
	}
	go func() {
		if err := a.notifier.Send(context.Background(), payload); err != nil && a.logger != nil {
			a.logger.Warn("discord notification failed", slog.String("error", err.Error()))
		}
	}()
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

func (a *App) farmingCampaignForGame(gameName string) (campaignName, campaignKey, gameImageURL string, dropList []string) {
	a.dropsMu.RLock()
	defer a.dropsMu.RUnlock()

	campaignIDs := collectActiveCampaignIDsForGame(gameName, a.lastDrops, a.lastActiveCampaignDrops)
	if len(campaignIDs) == 0 {
		return "", "", "", nil
	}
	campaignKey = strings.Join(campaignIDs, "\x00")

	activeCampaigns := make(map[string]bool, len(campaignIDs))
	for _, campaignID := range campaignIDs {
		activeCampaigns[campaignID] = true
	}

	type campaignDetails struct {
		name         string
		gameImageURL string
	}
	detailsByID := make(map[string]campaignDetails, len(campaignIDs))
	collectDetails := func(drops []inventory.Drop) {
		for _, drop := range drops {
			if !strings.EqualFold(drop.GameName, gameName) || !activeCampaigns[drop.CampaignID] {
				continue
			}
			details := detailsByID[drop.CampaignID]
			if details.name == "" {
				details.name = drop.CampaignName
			}
			if details.gameImageURL == "" {
				details.gameImageURL = drop.GameImageURL
			}
			detailsByID[drop.CampaignID] = details
		}
	}
	collectDetails(a.lastDrops)
	collectDetails(a.lastActiveCampaignDrops)

	campaignNames := make([]string, 0, len(campaignIDs))
	for _, campaignID := range campaignIDs {
		details := detailsByID[campaignID]
		name := details.name
		if name == "" {
			name = campaignID
		}
		campaignNames = append(campaignNames, name)
		if gameImageURL == "" {
			gameImageURL = details.gameImageURL
		}
	}
	campaignName = strings.Join(campaignNames, ", ")

	seenDrops := make(map[string]bool)
	appendDrops := func(drops []inventory.Drop) {
		for _, drop := range drops {
			if !strings.EqualFold(drop.GameName, gameName) || !activeCampaigns[drop.CampaignID] {
				continue
			}
			dropKey := drop.ID
			if dropKey == "" {
				dropKey = drop.CampaignID + "\x00" + drop.Name
			}
			if seenDrops[dropKey] {
				continue
			}
			seenDrops[dropKey] = true
			status := fmt.Sprintf("%d/%d min", drop.CurrentMinutes, drop.RequiredMinutes)
			if drop.IsClaimed {
				status = "Claimed"
			} else if drop.IsClaimable {
				status = "Claimable"
			}
			dropList = append(dropList, fmt.Sprintf("• **%s** (%s)", drop.Name, status))
		}
	}
	appendDrops(a.lastDrops)
	appendDrops(a.lastActiveCampaignDrops)

	return campaignName, campaignKey, gameImageURL, dropList
}

func (a *App) discoverGamesStreamers(ctx context.Context, client gameStreamDiscoverer, stickyGameName string) ([]domain.Streamer, error) {
	seen := make(map[string]bool)
	var lastErr error

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
		campaignIDs := a.activeCampaignIDsForGame(game)
		streamers, err := client.GetLiveStreamsForCampaigns(ctx, game, campaignIDs, 3)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if gql.IsPersistedQueryNotFound(err) {
				return nil, fmt.Errorf("game discovery persisted query unavailable: %w", err)
			}
			a.logger.Warn("discover game streams failed", slog.String("game", game), slog.String("error", err.Error()))
			lastErr = err
			continue
		}
		if len(streamers) == 0 {
			continue
		}

		var targetStreamers []domain.Streamer
		for _, s := range streamers {
			if seen[s.Login] || a.isStreamerOnRotationCooldown(s.Login) {
				continue
			}
			seen[s.Login] = true
			targetStreamers = append(targetStreamers, s)
		}
		if len(targetStreamers) == 0 {
			// Every candidate was filtered (static overlap or stall-rotation
			// cooldown); move on to the next game instead of farming nothing.
			a.logger.Debug("all discovered streams filtered for game", slog.String("game", game))
			continue
		}

		a.logger.Info("game discovery selected target",
			slog.String("game", game),
			slog.Int("count", len(targetStreamers)),
		)
		return targetStreamers, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("game stream discovery incomplete: %w", lastErr)
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

			currentOnline := indexStreamsByUserID(streams)
			notReturned := make([]string, 0)

			a.streamersMu.Lock()
			for _, s := range streamers {
				wasOnline := online[s.Login]
				streamInfo, isOnline := currentOnline[s.ID]
				online[s.Login] = isOnline
				if !isOnline {
					notReturned = append(notReturned, s.Login)
				}
				if isOnline && !strings.EqualFold(streamInfo.UserLogin, s.Login) {
					a.logger.Debug("stream login differs from resolved login; matched by user ID",
						slog.String("resolved_login", s.Login),
						slog.String("stream_login", streamInfo.UserLogin),
						slog.String("user_id", s.ID),
					)
				}

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
			a.logger.Debug("stream status poll completed",
				slog.Int("requested", len(streamers)),
				slog.Int("online", len(streams)),
				slog.Any("not_returned", notReturned),
			)
		}
	}
}

func indexStreamsByUserID(streams []twitch.StreamInfo) map[string]twitch.StreamInfo {
	indexed := make(map[string]twitch.StreamInfo, len(streams))
	for _, stream := range streams {
		indexed[stream.UserID] = stream
	}
	return indexed
}

func (a *App) runMinuteWatched(ctx context.Context, eng *engine.Engine, gqlClient gql.Client) {
	// Use the same Client ID and auth token as the rest of the app.
	// The Browser Client ID requires Client-Integrity tokens that we don't
	// generate, so we use the Android App Client ID which works without them.
	fetcher := playback.TokenFetcher{Client: gqlClient}
	watcher := playback.NewWatcher(fetcher)
	spadeEndpoint, err := playback.DiscoverSpadeURL(ctx)
	if err != nil {
		spadeEndpoint = playback.DefaultSpadeURL
		a.logger.Warn("spade endpoint discovery failed; using fallback", slog.String("error", err.Error()))
	}
	watcher.SetSpadeURL(spadeEndpoint)

	a.logger.Info("watch telemetry configured",
		slog.String("transport", "spade_direct"),
		slog.String("endpoint", spadeEndpoint),
		slog.Bool("auxiliary_watch", a.config.Watch.AuxiliaryWatch),
	)

	state := watchTelemetryState{
		lastAuxiliaryWatch: make(map[string]time.Time),
		auxiliaryBackoff:   make(map[string]time.Time),
	}
	ticker := time.NewTicker(watchTelemetryTick)
	defer ticker.Stop()

	for {
		a.processWatchTelemetry(ctx, eng, watcher, &state, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) processWatchTelemetry(ctx context.Context, eng *engine.Engine, sender watchTelemetrySender, state *watchTelemetryState, now time.Time) {
	primary := a.activeDropStreamer(eng)
	primarySession := streamerSession(primary)
	if primarySession != state.primarySession {
		a.pauseAuxiliaryWatch(state, now, "primary_changed")
		state.primarySession = primarySession
		state.primarySuccessfulPulses = 0
		state.nextPrimaryPulse = now
		if primary != nil && a.logger != nil {
			a.logger.Info("drop watch session establishing",
				slog.String("streamer", primary.Login),
				slog.String("game", primary.GameName),
				slog.String("broadcast_id", primary.BroadcastID),
			)
		}
	}

	if primary != nil && !now.Before(state.nextPrimaryPulse) {
		state.nextPrimaryPulse = now.Add(randomDuration(55*time.Second, 65*time.Second))
		if err := sender.SendMinuteWatched(ctx, *primary, a.userID); err != nil {
			state.primarySuccessfulPulses = 0
			a.pauseAuxiliaryWatch(state, now, "primary_failed")
			a.logger.Warn("minute watched failed",
				slog.String("streamer", primary.Login),
				slog.String("error", err.Error()),
			)
			return
		}
		state.primarySuccessfulPulses++
		if !state.nextAuxiliaryPulse.IsZero() && state.nextAuxiliaryPulse.Before(now.Add(auxiliaryPulseOffset)) {
			state.nextAuxiliaryPulse = now.Add(auxiliaryPulseOffset)
		}
		a.logger.Debug("minute watched sent",
			slog.String("streamer", primary.Login),
			slog.String("game", primary.GameName),
			slog.Int("session_pulses", state.primarySuccessfulPulses),
		)
		return
	}

	if !a.config.Watch.AuxiliaryWatch || a.isAuxiliaryWatchDisabled() || primary == nil || state.primarySuccessfulPulses < primaryPulsesBeforeAux {
		a.pauseAuxiliaryWatch(state, now, "not_safe")
		return
	}

	lease := time.Duration(a.config.Watch.AuxiliaryLeaseMinutes) * time.Minute
	current := a.staticStreamerSnapshot(state.auxiliaryLogin)
	if current == nil || streamerSession(current) != state.auxiliarySession || strings.EqualFold(current.Login, primary.Login) || now.Sub(state.auxiliaryLeaseStarted) >= lease {
		a.pauseAuxiliaryWatch(state, now, "lease_or_stream_changed")
		current = nil
	}

	if current == nil {
		current = a.selectAuxiliaryStreamer(primary.Login, state.lastAuxiliaryWatch, state.auxiliaryBackoff, now)
		if current == nil {
			return
		}
		state.auxiliaryLogin = current.Login
		state.auxiliarySession = streamerSession(current)
		state.auxiliaryLeaseStarted = now
		state.nextAuxiliaryPulse = now.Add(auxiliaryPulseOffset)
		state.auxiliaryFailures = 0
		a.logger.Info("auxiliary watch selected",
			slog.String("streamer", current.Login),
			slog.String("game", current.GameName),
			slog.Duration("lease", lease),
		)
		return
	}

	if now.Before(state.nextAuxiliaryPulse) {
		return
	}
	requestDeadline := state.nextPrimaryPulse.Add(-primaryPulseSafetyMargin)
	if requestDeadline.Sub(now) < minimumAuxiliaryWindow {
		return
	}
	requestCtx, finishRequest, ok := a.beginAuxiliaryRequest(ctx, requestDeadline)
	if !ok {
		a.pauseAuxiliaryWatch(state, now, "disabled")
		return
	}
	defer finishRequest()
	state.nextAuxiliaryPulse = now.Add(randomDuration(55*time.Second, 65*time.Second))
	if err := sender.SendPresence(requestCtx, *current, a.userID); err != nil {
		state.auxiliaryFailures++
		a.logger.Warn("auxiliary presence failed",
			slog.String("streamer", current.Login),
			slog.Int("consecutive_failures", state.auxiliaryFailures),
			slog.String("error", err.Error()),
		)
		if state.auxiliaryFailures >= maxAuxiliaryFailures {
			if state.auxiliaryBackoff == nil {
				state.auxiliaryBackoff = make(map[string]time.Time)
			}
			state.auxiliaryBackoff[current.Login] = now.Add(auxiliaryFailureBackoff)
			a.pauseAuxiliaryWatch(state, now, "repeated_failures")
		}
		return
	}
	state.auxiliaryFailures = 0
	a.logger.Debug("auxiliary presence sent",
		slog.String("streamer", current.Login),
		slog.String("game", current.GameName),
	)
}

func (a *App) pauseAuxiliaryWatch(state *watchTelemetryState, now time.Time, reason string) {
	if state.auxiliaryLogin == "" {
		return
	}
	if state.lastAuxiliaryWatch == nil {
		state.lastAuxiliaryWatch = make(map[string]time.Time)
	}
	state.lastAuxiliaryWatch[state.auxiliaryLogin] = now
	if a.logger != nil {
		a.logger.Info("auxiliary watch paused",
			slog.String("streamer", state.auxiliaryLogin),
			slog.String("reason", reason),
		)
	}
	state.auxiliaryLogin = ""
	state.auxiliarySession = ""
	state.auxiliaryLeaseStarted = time.Time{}
	state.nextAuxiliaryPulse = time.Time{}
	state.auxiliaryFailures = 0
}

func streamerSession(streamer *domain.Streamer) string {
	if streamer == nil {
		return ""
	}
	return strings.ToLower(streamer.Login) + "\x00" + streamer.BroadcastID + "\x00" + streamer.GameID
}

func (a *App) staticStreamerSnapshot(login string) *domain.Streamer {
	if login == "" {
		return nil
	}
	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()
	for _, streamer := range a.staticStreamers {
		if strings.EqualFold(streamer.Login, login) && streamer.BroadcastID != "" {
			copy := streamer
			return &copy
		}
	}
	return nil
}

func (a *App) selectAuxiliaryStreamer(primaryLogin string, lastWatched, backoff map[string]time.Time, now time.Time) *domain.Streamer {
	activeGames := a.activeGamesSnapshot()
	isActiveDropGame := func(gameName string) bool {
		for _, activeGame := range activeGames {
			if strings.EqualFold(gameName, activeGame) {
				return true
			}
		}
		return false
	}

	a.streamersMu.RLock()
	defer a.streamersMu.RUnlock()

	var selected *domain.Streamer
	for _, streamer := range a.staticStreamers {
		if streamer.BroadcastID == "" || streamer.GameName == "" || strings.EqualFold(streamer.Login, primaryLogin) || isActiveDropGame(streamer.GameName) {
			continue
		}
		if until, blocked := backoff[streamer.Login]; blocked && now.Before(until) {
			continue
		}
		if selected != nil {
			selectedAt, selectedSeen := lastWatched[selected.Login]
			candidateAt, candidateSeen := lastWatched[streamer.Login]
			if !selectedSeen && !candidateSeen {
				continue
			}
			if !selectedSeen && candidateSeen {
				continue
			}
			if candidateSeen && !candidateAt.Before(selectedAt) {
				continue
			}
		}
		copy := streamer
		selected = &copy
	}
	return selected
}

func (a *App) isAuxiliaryWatchDisabled() bool {
	a.watchCreditMu.Lock()
	defer a.watchCreditMu.Unlock()
	return a.auxiliaryWatchDisabled
}

func (a *App) disableAuxiliaryWatch(reason string) {
	a.watchCreditMu.Lock()
	alreadyDisabled := a.auxiliaryWatchDisabled
	a.auxiliaryWatchDisabled = true
	cancel := a.auxiliaryWatchCancel
	a.auxiliaryWatchCancel = nil
	a.watchCreditMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !alreadyDisabled && a.logger != nil {
		a.logger.Warn("auxiliary watch disabled for process lifetime", slog.String("reason", reason))
	}
}

func (a *App) beginAuxiliaryRequest(ctx context.Context, deadline time.Time) (context.Context, func(), bool) {
	a.watchCreditMu.Lock()
	defer a.watchCreditMu.Unlock()
	if a.auxiliaryWatchDisabled {
		return nil, func() {}, false
	}

	requestCtx, cancel := context.WithDeadline(ctx, deadline)
	a.auxiliaryWatchCancel = cancel
	finish := func() {
		cancel()
		a.watchCreditMu.Lock()
		a.auxiliaryWatchCancel = nil
		a.watchCreditMu.Unlock()
	}
	return requestCtx, finish, true
}

func (a *App) pollChannelPoints(ctx context.Context, eng *engine.Engine, loader channelpoints.ContextLoader) {
	baseInterval := time.Duration(a.config.Watch.TickSeconds) * time.Second
	if baseInterval < time.Minute {
		baseInterval = time.Minute
	}
	retryInterval := baseInterval
	maxBackoff := channelPointsMaxBackoff
	if maxBackoff < baseInterval {
		maxBackoff = baseInterval
	}

	for {
		nextInterval := randomDuration(retryInterval-10*time.Second, retryInterval+10*time.Second)
		timer := time.NewTimer(nextInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			streamers := a.getStreamersToPoll(eng)
			events, err := a.loadChannelPointEvents(ctx, loader, streamers)
			for _, event := range events {
				eng.SendEvent(event)
			}
			if err == nil {
				retryInterval = baseInterval
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if gql.IsPersistedQueryNotFound(err) {
				retryInterval *= 2
				if retryInterval > maxBackoff {
					retryInterval = maxBackoff
				}
				a.logger.Warn("channel points query unavailable; backing off",
					slog.String("error", err.Error()),
					slog.Duration("retry_in", retryInterval),
				)
				continue
			}
			retryInterval = baseInterval
			a.logger.Warn("channel points polling failed", slog.String("error", err.Error()))
		}
	}
}

func (a *App) loadChannelPointEvents(ctx context.Context, loader channelpoints.ContextLoader, streamers []domain.Streamer) ([]engine.Event, error) {
	events := make([]engine.Event, 0, len(streamers)*2)
	for i, streamer := range streamers {
		if i > 0 {
			select {
			case <-ctx.Done():
				return events, ctx.Err()
			case <-time.After(randomDuration(1*time.Second, 3*time.Second)):
			}
		}

		pointsContext, err := loader.Load(ctx, streamer.Login, streamer.ID)
		if err != nil {
			if gql.IsPersistedQueryNotFound(err) {
				return events, fmt.Errorf("load channel points context for %s: %w", streamer.Login, err)
			}
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
	return events, nil
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
			}
			if !drop.IsClaimed && drop.IsEarnable {
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
	availableGames := make(map[string]bool)

	if a.config.Watch.AutoStartCampaigns {
		availableConnected, availableUnconnected, allDrops, err := invClient.GetActiveCampaignGames(ctx)
		if err != nil {
			previous := a.activeGamesSnapshot()
			if len(previous) > 0 {
				a.logger.Warn("fetch active campaign games failed; keeping previous active games", slog.String("error", err.Error()))
				return previous
			}
			a.logger.Warn("fetch active campaign games failed; using in-progress inventory", slog.String("error", err.Error()))
		} else {
			a.dropsMu.Lock()
			a.lastActiveCampaignDrops = allDrops
			a.dropsMu.Unlock()
			for _, drop := range allDrops {
				if drop.GameName != "" && drop.IsEarnable && !drop.IsClaimed && drop.RequiredMinutes > 0 {
					availableGames[gameKey(drop.GameName)] = true
				}
			}
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
			if hasAnyDrops[pgKey] && !hasUnclaimed[pgKey] && !availableGames[pgKey] {
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
		if hasAnyDrops[key] && !hasUnclaimed[key] && !availableGames[key] {
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
