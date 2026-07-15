package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/engine"
	"github.com/Du-vy/TDropFarmer/internal/twitch"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
	"github.com/Du-vy/TDropFarmer/internal/twitch/inventory"
)

func TestFormatEventMessage(t *testing.T) {
	app := &App{
		config: config.Config{},
	}

	// Test EventWatchStart
	msg := app.formatEventMessage(engine.Event{
		Type:     engine.EventWatchStart,
		Streamer: "streamer1",
	})
	expected := "▶ Started watching **streamer1**"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	// Test EventWatchStop
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventWatchStop,
		Streamer: "streamer1",
	})
	expected = "■ Stopped watching **streamer1**"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	// Test EventBonusClaimed
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventBonusClaimed,
		Streamer: "streamer1",
		Payload: channelpoints.ClaimResult{
			Points:        50,
			StreamerLogin: "streamer1",
		},
	})
	expected = "💰 Claimed community bonus of **50** points from **streamer1**!"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	// Test EventDropClaimed
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventDropClaimed,
		Streamer: "streamer1",
		Payload: inventory.Drop{
			Name:       "Super Drop",
			CampaignID: "camp-123",
		},
	})
	expected = "🎁 Reclamado Drop: **Super Drop** de campaña **camp-123**!"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	// Test EventChatMention
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventChatMention,
		Streamer: "streamer1",
		Payload:  "[anotheruser]: hello @myuser",
	})
	expected = "💬 Mención detectada en el chat de **streamer1**:\n[anotheruser]: hello @myuser"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}
}

type mockInventoryGQLClient struct {
	dashboardResponse []byte
	dashboardErr      error
	detailResponses   map[string][]byte
}

func (m mockInventoryGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	if req.OperationName == "ViewerDropsDashboard" {
		if m.dashboardErr != nil {
			return gql.Response{}, m.dashboardErr
		}
		return gql.Response{Data: m.dashboardResponse}, nil
	}
	if req.OperationName == "DropCampaignDetails" {
		id, _ := req.Variables["dropID"].(string)
		return gql.Response{Data: m.detailResponses[id]}, nil
	}
	return gql.Response{}, nil
}

type claimFlowGQLClient struct {
	inventoryResponse []byte
	claimResponse     []byte
}

type campaignScanCountingClient struct {
	dashboardCalls int
}

func (c *campaignScanCountingClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	switch req.OperationName {
	case "Inventory":
		return gql.Response{Data: []byte(`{"currentUser":{"inventory":{"dropCampaignsInProgress":[]}}}`)}, nil
	case "ViewerDropsDashboard":
		c.dashboardCalls++
		return gql.Response{Data: []byte(`{"currentUser":{"dropCampaigns":[]}}`)}, nil
	default:
		return gql.Response{}, nil
	}
}

func (m claimFlowGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	switch req.OperationName {
	case "Inventory":
		return gql.Response{Data: m.inventoryResponse}, nil
	case "DropsPage_ClaimDropRewards":
		return gql.Response{Data: m.claimResponse}, nil
	default:
		return gql.Response{}, nil
	}
}

func TestCheckAndClaimDropsRemovesClaimedGameBeforeSorting(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				FallbackAllCampaigns: true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := engine.New(app.config, nil, logger)
	invClient := inventory.Client{Client: claimFlowGQLClient{
		inventoryResponse: []byte(`{"currentUser":{"inventory":{"dropCampaignsInProgress":[
			{
				"id":"7ds-campaign",
				"name":"7DS Origin 2nd Drops",
				"game":{"name":"The Seven Deadly Sins: Origin"},
				"timeBasedDrops":[{
					"id":"7ds-drop",
					"name":"Regular Hero Draw Ticket x 1",
					"requiredMinutesWatched":120,
					"self":{"currentMinutesWatched":120,"hasPreconditionsMet":true,"dropInstanceID":"instance-7ds","isClaimed":false}
				}]
			},
			{
				"id":"rematch-campaign",
				"name":"Rematch Nations Cup",
				"game":{"name":"REMATCH"},
				"timeBasedDrops":[{
					"id":"rematch-drop",
					"name":"Rematch Nations Cup",
					"requiredMinutesWatched":30,
					"self":{"currentMinutesWatched":3,"hasPreconditionsMet":true,"dropInstanceID":null,"isClaimed":false}
				}]
			}
		]}}}`),
		claimResponse: []byte(`{"claimDropRewards":{"status":"ELIGIBLE_FOR_ALL"}}`),
	}}

	app.checkAndClaimDrops(context.Background(), eng, invClient)

	if len(app.activeGames) != 1 || app.activeGames[0] != "REMATCH" {
		t.Fatalf("expected only REMATCH after claiming final 7DS drop, got %v", app.activeGames)
	}
	if len(app.lastDrops) == 0 || !app.lastDrops[0].IsClaimed || app.lastDrops[0].IsClaimable || app.lastDrops[0].IsEarnable {
		t.Fatalf("expected claimed drop to be reflected in lastDrops, got %+v", app.lastDrops)
	}
}

func TestInitialDropCheckReusesLoadedCampaigns(t *testing.T) {
	cfg := config.Config{
		Watch: config.WatchConfig{
			FallbackAllCampaigns: true,
			AutoStartCampaigns:   true,
		},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := &App{config: cfg, logger: logger}
	eng := engine.New(cfg, nil, logger)
	client := &campaignScanCountingClient{}
	invClient := inventory.Client{Client: client, UserID: "viewer"}

	app.checkAndClaimDropsWithCampaignRefresh(context.Background(), eng, invClient, false)
	if client.dashboardCalls != 0 {
		t.Fatalf("initial drop check repeated campaign scan %d times", client.dashboardCalls)
	}

	app.checkAndClaimDrops(context.Background(), eng, invClient)
	if client.dashboardCalls != 1 {
		t.Fatalf("periodic drop check campaign scans = %d, want 1", client.dashboardCalls)
	}
}

func TestSortActiveGames(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames:        []string{"Corepunk", "Overwatch"},
				FallbackAllCampaigns: true,
				AutoStartCampaigns:   true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
	}

	// Mock active campaign games: "Overwatch", "THE FINALS", "Minecraft"
	mockCampaignsJSON := `{
		"currentUser": {
			"dropCampaigns": [
				{"id": "overwatch", "status": "ACTIVE", "game": {"displayName": "Overwatch"}},
				{"id": "the-finals", "status": "ACTIVE", "game": {"displayName": "THE FINALS"}},
				{"id": "minecraft", "status": "ACTIVE", "game": {"displayName": "Minecraft"}},
				{"id": "marvel", "status": "ACTIVE", "game": {"displayName": "Marvel Rivals"}}
			]
		}
	}`

	earnableDetail := func(id, game string) []byte {
		return []byte(`{
			"user": {
				"dropCampaign": {
					"id": "` + id + `",
					"status": "ACTIVE",
					"self": {"isAccountConnected": true},
					"game": {"displayName": "` + game + `"},
					"timeBasedDrops": [
						{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
					]
				}
			}
		}`)
	}
	completedDetail := func(id, game string) []byte {
		return []byte(`{
			"user": {
				"dropCampaign": {
					"id": "` + id + `",
					"status": "ACTIVE",
					"self": {"isAccountConnected": true},
					"game": {"displayName": "` + game + `"},
					"timeBasedDrops": [
						{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": true}}
					]
				}
			}
		}`)
	}
	mockClient := mockInventoryGQLClient{
		dashboardResponse: []byte(mockCampaignsJSON),
		detailResponses: map[string][]byte{
			"overwatch":  earnableDetail("overwatch", "Overwatch"),
			"the-finals": completedDetail("the-finals", "THE FINALS"),
			"minecraft":  earnableDetail("minecraft", "Minecraft"),
			"marvel":     completedDetail("marvel", "Marvel Rivals"),
		},
	}
	invClient := inventory.Client{Client: mockClient, UserID: "805921782"}

	// 1. Case where FallbackAllCampaigns is true
	drops := []inventory.Drop{
		// Game: THE FINALS (Not Priority, in progress)
		{GameName: "THE FINALS", IsEarnable: true},
		// Game: Corepunk (Priority, in progress)
		{GameName: "Corepunk", IsEarnable: true},
	}

	sorted := app.sortActiveGames(context.Background(), invClient, drops, "")
	// Expected Order:
	// Priority games in progress: "Corepunk"
	// Priority games available (active campaign but not in progress): "Overwatch"
	// Other games in progress: "THE FINALS"
	// Other games available (active campaign but not in progress): "Minecraft"
	expected := []string{"Corepunk", "Overwatch", "THE FINALS", "Minecraft"}
	if len(sorted) != len(expected) {
		t.Fatalf("expected len %d, got %d (sorted = %v)", len(expected), len(sorted), sorted)
	}
	for i, game := range expected {
		if sorted[i] != game {
			t.Errorf("at index %d: expected %q, got %q", i, game, sorted[i])
		}
	}

	// 2. Case where FallbackAllCampaigns is false
	app.config.Watch.FallbackAllCampaigns = false
	sorted = app.sortActiveGames(context.Background(), invClient, drops, "")
	// Expected Order:
	// Only Priority games (in progress first, then available, then remaining configured):
	// "Corepunk", "Overwatch"
	expected = []string{"Corepunk", "Overwatch"}
	if len(sorted) != len(expected) {
		t.Fatalf("expected len %d, got %d (sorted = %v)", len(expected), len(sorted), sorted)
	}
	for i, game := range expected {
		if sorted[i] != game {
			t.Errorf("at index %d: expected %q, got %q", i, game, sorted[i])
		}
	}

	// 3. Case where a priority game is fully completed (has drops, but all are claimed)
	app.config.Watch.PriorityGames = []string{"Corepunk", "Overwatch", "THE FINALS"}
	app.config.Watch.FallbackAllCampaigns = false
	drops = []inventory.Drop{
		// Game: THE FINALS (Priority, but fully claimed/completed)
		{GameName: "THE FINALS", IsEarnable: false, IsClaimed: true},
		// Game: Corepunk (Priority, in progress and earnable)
		{GameName: "Corepunk", IsEarnable: true, IsClaimed: false},
	}
	sorted = app.sortActiveGames(context.Background(), invClient, drops, "")
	// Expected: "Corepunk" (in progress) and "Overwatch" (available).
	// "THE FINALS" should be excluded because all of its drops are claimed/completed.
	expected = []string{"Corepunk", "Overwatch"}
	if len(sorted) != len(expected) {
		t.Fatalf("expected len %d, got %d (sorted = %v)", len(expected), len(sorted), sorted)
	}
	for i, game := range expected {
		if sorted[i] != game {
			t.Errorf("at index %d: expected %q, got %q", i, game, sorted[i])
		}
	}

	// 4. Case where FallbackAllCampaigns is true and a game has all drops claimed in inventory
	app.config.Watch.PriorityGames = []string{"Corepunk", "Overwatch"}
	app.config.Watch.FallbackAllCampaigns = true
	drops = []inventory.Drop{
		{GameName: "THE FINALS", IsEarnable: false, IsClaimed: true},
		{GameName: "Corepunk", IsEarnable: true, IsClaimed: false},
	}
	sorted = app.sortActiveGames(context.Background(), invClient, drops, "")
	for _, game := range sorted {
		if strings.EqualFold(game, "THE FINALS") {
			t.Errorf("THE FINALS should be excluded when all drops are claimed, got sorted = %v", sorted)
		}
	}
}

func TestSortActiveGamesPrioritizesConnectedOverUnconnected(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames:        []string{"ConnectedPriority", "UnconnectedPriority"},
				FallbackAllCampaigns: true,
				AutoStartCampaigns:   true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
	}

	mockCampaignsJSON := `{
		"currentUser": {
			"dropCampaigns": [
				{"id": "connected-priority", "status": "ACTIVE", "game": {"displayName": "ConnectedPriority"}},
				{"id": "unconnected-priority", "status": "ACTIVE", "game": {"displayName": "UnconnectedPriority"}},
				{"id": "connected-other", "status": "ACTIVE", "game": {"displayName": "ConnectedOther"}},
				{"id": "unconnected-other", "status": "ACTIVE", "game": {"displayName": "UnconnectedOther"}}
			]
		}
	}`

	detail := func(id, game string, connected bool) []byte {
		connStr := "false"
		if connected {
			connStr = "true"
		}
		return []byte(`{
			"user": {
				"dropCampaign": {
					"id": "` + id + `",
					"status": "ACTIVE",
					"self": {"isAccountConnected": ` + connStr + `},
					"game": {"displayName": "` + game + `"},
					"timeBasedDrops": [
						{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
					]
				}
			}
		}`)
	}

	mockClient := mockInventoryGQLClient{
		dashboardResponse: []byte(mockCampaignsJSON),
		detailResponses: map[string][]byte{
			"connected-priority":   detail("connected-priority", "ConnectedPriority", true),
			"unconnected-priority": detail("unconnected-priority", "UnconnectedPriority", false),
			"connected-other":      detail("connected-other", "ConnectedOther", true),
			"unconnected-other":    detail("unconnected-other", "UnconnectedOther", false),
		},
	}
	invClient := inventory.Client{Client: mockClient, UserID: "805921782"}

	sorted := app.sortActiveGames(context.Background(), invClient, nil, "")
	// Expected Order:
	// 1. Priority connected: "ConnectedPriority"
	// 2. Priority unconnected: "UnconnectedPriority"
	// 3. Other connected: "ConnectedOther"
	// 4. Other unconnected: "UnconnectedOther"
	expected := []string{"ConnectedPriority", "UnconnectedPriority", "ConnectedOther", "UnconnectedOther"}
	if len(sorted) != len(expected) {
		t.Fatalf("expected len %d, got %d (sorted = %v)", len(expected), len(sorted), sorted)
	}
	for i, game := range expected {
		if sorted[i] != game {
			t.Errorf("at index %d: expected %q, got %q", i, game, sorted[i])
		}
	}
}

func TestSortActiveGamesKeepsCurrentCampaignWithinSamePriorityBucket(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames:        []string{"Warframe"},
				FallbackAllCampaigns: true,
				AutoStartCampaigns:   true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	invClient := inventory.Client{Client: mockInventoryGQLClient{
		dashboardResponse: []byte(`{"currentUser":{"dropCampaigns":[]}}`),
	}, UserID: "805921782"}
	drops := []inventory.Drop{
		{GameName: "Sea of Thieves", CampaignID: "sea-campaign", IsEarnable: true, IsClaimed: false},
		{GameName: "Black Desert", CampaignID: "black-desert-campaign", IsEarnable: true, IsClaimed: false},
	}

	sorted := app.sortActiveGames(context.Background(), invClient, drops, "Black Desert")

	if len(sorted) < 2 {
		t.Fatalf("expected at least 2 games, got %v", sorted)
	}
	if sorted[0] != "Black Desert" || sorted[1] != "Sea of Thieves" {
		t.Fatalf("expected current campaign to stay first in same bucket, got %v", sorted)
	}
}

func TestSortActiveGamesPriorityCampaignPreemptsCurrentNonPriorityCampaign(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames:        []string{"Warframe"},
				FallbackAllCampaigns: true,
				AutoStartCampaigns:   true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	invClient := inventory.Client{Client: mockInventoryGQLClient{
		dashboardResponse: []byte(`{"currentUser":{"dropCampaigns":[]}}`),
	}, UserID: "805921782"}
	drops := []inventory.Drop{
		{GameName: "Sea of Thieves", CampaignID: "sea-campaign", IsEarnable: true, IsClaimed: false},
		{GameName: "Black Desert", CampaignID: "black-desert-campaign", IsEarnable: true, IsClaimed: false},
		{GameName: "Warframe", CampaignID: "warframe-campaign", IsEarnable: true, IsClaimed: false},
	}

	sorted := app.sortActiveGames(context.Background(), invClient, drops, "Black Desert")

	if len(sorted) == 0 || sorted[0] != "Warframe" {
		t.Fatalf("expected priority campaign to preempt current non-priority campaign, got %v", sorted)
	}
}

func TestSortActiveGamesKeepsPreviousCatalogWhenDashboardFails(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				FallbackAllCampaigns: true,
				AutoStartCampaigns:   true,
			},
			Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
		},
		activeGames: []string{"Existing Game", "Another Game"},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	invClient := inventory.Client{Client: mockInventoryGQLClient{
		dashboardErr: gql.Error{Message: "PersistedQueryNotFound"},
	}}

	got := app.sortActiveGames(context.Background(), invClient, []inventory.Drop{
		{GameName: "Partial Inventory Game", IsEarnable: true},
	}, "")

	if len(got) != 2 || got[0] != "Existing Game" || got[1] != "Another Game" {
		t.Fatalf("expected previous active games to be preserved, got %v", got)
	}
}

type fakeGameDiscoverer struct {
	calls             []string
	campaignIDsByGame map[string][]string
	streamers         map[string][]domain.Streamer
	errorsByGame      map[string]error
}

func TestSortActiveGamesKeepsNewCampaignForCompletedSameGame(t *testing.T) {
	app := &App{config: config.Config{
		Watch: config.WatchConfig{
			PriorityGames:        []string{"Marvel Rivals"},
			FallbackAllCampaigns: true,
			AutoStartCampaigns:   true,
		},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}}
	client := mockInventoryGQLClient{
		dashboardResponse: []byte(`{"currentUser":{"dropCampaigns":[{"id":"season-9","status":"ACTIVE","game":{"displayName":"Marvel Rivals"}}]}}`),
		detailResponses: map[string][]byte{
			"season-9": []byte(`{"user":{"dropCampaign":{"id":"season-9","name":"Season 9 Twitch Drops","status":"ACTIVE","self":{"isAccountConnected":true},"game":{"displayName":"Marvel Rivals"},"timeBasedDrops":[{"id":"reward","requiredMinutesWatched":30,"requiredSubs":0,"self":{"hasPreconditionsMet":true,"isClaimed":false}}]}}}`),
		},
	}
	drops := []inventory.Drop{{GameName: "Marvel Rivals", CampaignID: "season-8", IsClaimed: true}}

	got := app.sortActiveGames(context.Background(), inventory.Client{Client: client, UserID: "viewer"}, drops, "")
	if len(got) != 1 || got[0] != "Marvel Rivals" {
		t.Fatalf("expected active same-game campaign to survive completed inventory campaign, got %v", got)
	}
}

func TestSortActiveGamesDoesNotLetUnearnableSameGameCampaignBlockNewCampaign(t *testing.T) {
	app := &App{config: config.Config{
		Watch: config.WatchConfig{
			PriorityGames:        []string{"Marvel Rivals"},
			FallbackAllCampaigns: true,
			AutoStartCampaigns:   true,
		},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}}
	client := mockInventoryGQLClient{
		dashboardResponse: []byte(`{"currentUser":{"dropCampaigns":[{"id":"season-9","status":"ACTIVE","game":{"displayName":"Marvel Rivals"}}]}}`),
		detailResponses: map[string][]byte{
			"season-9": []byte(`{"user":{"dropCampaign":{"id":"season-9","name":"Season 9 Twitch Drops","status":"ACTIVE","self":{"isAccountConnected":true},"game":{"displayName":"Marvel Rivals"},"timeBasedDrops":[{"id":"reward","requiredMinutesWatched":30,"requiredSubs":0,"self":{"hasPreconditionsMet":true,"isClaimed":false}}]}}}`),
		},
	}
	drops := []inventory.Drop{{GameName: "Marvel Rivals", CampaignID: "jubilee", IsClaimed: false, IsEarnable: false}}

	got := app.sortActiveGames(context.Background(), inventory.Client{Client: client, UserID: "viewer"}, drops, "")
	if len(got) != 1 || got[0] != "Marvel Rivals" {
		t.Fatalf("expected new watch campaign despite unearnable same-game campaign, got %v", got)
	}
}

func (f *fakeGameDiscoverer) GetLiveStreamsForCampaigns(ctx context.Context, gameName string, campaignIDs []string, limit int) ([]domain.Streamer, error) {
	f.calls = append(f.calls, gameName)
	if f.campaignIDsByGame == nil {
		f.campaignIDsByGame = make(map[string][]string)
	}
	f.campaignIDsByGame[gameName] = append([]string(nil), campaignIDs...)
	if err := f.errorsByGame[gameName]; err != nil {
		return nil, err
	}
	return f.streamers[gameName], nil
}

func TestDiscoverGamesStreamersStopsAfterPersistedQueryFailure(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch:    config.WatchConfig{FallbackAllCampaigns: true},
			Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
		},
		activeGames: []string{"First Game", "Second Game"},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	discoverer := &fakeGameDiscoverer{errorsByGame: map[string]error{
		"First Game": gql.Error{Message: "PersistedQueryNotFound"},
	}}

	_, err := app.discoverGamesStreamers(context.Background(), discoverer, "")
	if !gql.IsPersistedQueryNotFound(err) {
		t.Fatalf("expected persisted query error, got %v", err)
	}
	if len(discoverer.calls) != 1 || discoverer.calls[0] != "First Game" {
		t.Fatalf("expected discovery to stop after first operation-wide failure, got calls %v", discoverer.calls)
	}
}

type countingErrorGQLClient struct {
	calls int
	err   error
}

func (c *countingErrorGQLClient) Do(context.Context, gql.Request) (gql.Response, error) {
	c.calls++
	return gql.Response{}, c.err
}

func TestLoadChannelPointEventsStopsAfterPersistedQueryFailure(t *testing.T) {
	client := &countingErrorGQLClient{err: gql.Error{Message: "PersistedQueryNotFound"}}
	app := &App{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	streamers := []domain.Streamer{
		{ID: "1", Login: "one"},
		{ID: "2", Login: "two"},
		{ID: "3", Login: "three"},
	}

	events, err := app.loadChannelPointEvents(context.Background(), channelpoints.ContextLoader{Client: client}, streamers)
	if !gql.IsPersistedQueryNotFound(err) {
		t.Fatalf("expected persisted query error, got %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no channel point events, got %v", events)
	}
	if client.calls != 1 {
		t.Fatalf("expected one request for an operation-wide failure, got %d", client.calls)
	}
}

func TestDiscoverGamesStreamersStopsAtFirstGameWithStreams(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				FallbackAllCampaigns: true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		activeGames: []string{"No Streams", "Target Game", "Later Game"},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	discoverer := &fakeGameDiscoverer{
		streamers: map[string][]domain.Streamer{
			"Target Game": {
				{ID: "1", Login: "target_one", GameName: "Target Game"},
				{ID: "2", Login: "target_two", GameName: "Target Game"},
			},
			"Later Game": {
				{ID: "3", Login: "later_one", GameName: "Later Game"},
			},
		},
	}

	streamers, err := app.discoverGamesStreamers(context.Background(), discoverer, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCalls := []string{"No Streams", "Target Game"}
	if len(discoverer.calls) != len(expectedCalls) {
		t.Fatalf("expected calls %v, got %v", expectedCalls, discoverer.calls)
	}
	for i, expected := range expectedCalls {
		if discoverer.calls[i] != expected {
			t.Fatalf("expected calls %v, got %v", expectedCalls, discoverer.calls)
		}
	}

	if len(streamers) != 2 {
		t.Fatalf("expected 2 streamers, got %d (%v)", len(streamers), streamers)
	}
	for _, streamer := range streamers {
		if streamer.GameName != "Target Game" {
			t.Fatalf("expected only target game streamers, got %v", streamers)
		}
	}
}

func TestDiscoverGamesStreamersPassesWatchCampaignIDs(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch:    config.WatchConfig{FallbackAllCampaigns: true},
			Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
		},
		activeGames: []string{"Marvel Rivals"},
		lastDrops: []inventory.Drop{
			{GameName: "Marvel Rivals", CampaignID: "jubilee", RequiredMinutes: 0, IsEarnable: false},
		},
		lastActiveCampaignDrops: []inventory.Drop{
			{GameName: "Marvel Rivals", CampaignID: "season-9", RequiredMinutes: 30, IsEarnable: true},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	discoverer := &fakeGameDiscoverer{streamers: map[string][]domain.Streamer{
		"Marvel Rivals": {{ID: "1", Login: "season_nine", GameName: "Marvel Rivals"}},
	}}

	_, err := app.discoverGamesStreamers(context.Background(), discoverer, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := discoverer.campaignIDsByGame["Marvel Rivals"]
	if len(got) != 1 || got[0] != "season-9" {
		t.Fatalf("expected only watch campaign ID, got %v", got)
	}
}

func TestFarmingCampaignForGameIsStableAcrossDropOrder(t *testing.T) {
	drops := []inventory.Drop{
		{ID: "radiant-drop", Name: "Radiant Wilds Chest", CampaignID: "radiant", CampaignName: "Radiant Wilds", GameName: "Albion Online", RequiredMinutes: 180, IsEarnable: true},
		{ID: "noble-drop", Name: "Noble Community Chest", CampaignID: "noble", CampaignName: "AOCP Knight", GameName: "Albion Online", RequiredMinutes: 240, IsEarnable: true},
	}
	app := &App{lastDrops: drops}

	campaignName, campaignKey, _, dropList := app.farmingCampaignForGame("albion online")
	if campaignKey != "noble\x00radiant" {
		t.Fatalf("expected stable campaign key, got %q", campaignKey)
	}
	if campaignName != "AOCP Knight, Radiant Wilds" {
		t.Fatalf("expected deterministic campaign names, got %q", campaignName)
	}
	if len(dropList) != 2 {
		t.Fatalf("expected both active drops, got %v", dropList)
	}
	if !app.setCurrentFarmingCampaign(campaignKey) {
		t.Fatal("expected first campaign set to be treated as changed")
	}

	app.lastDrops = []inventory.Drop{drops[1], drops[0]}
	_, reorderedKey, _, _ := app.farmingCampaignForGame("Albion Online")
	if reorderedKey != campaignKey {
		t.Fatalf("expected reordering not to change campaign key: %q != %q", reorderedKey, campaignKey)
	}
	if app.setCurrentFarmingCampaign(reorderedKey) {
		t.Fatal("expected reordering not to trigger a campaign change")
	}
}

func TestFarmingCampaignForGameDetectsActiveCampaignSetChange(t *testing.T) {
	app := &App{lastDrops: []inventory.Drop{
		{ID: "a-drop", Name: "A Drop", CampaignID: "campaign-a", GameName: "Game", RequiredMinutes: 30, IsEarnable: true},
		{ID: "b-drop", Name: "B Drop", CampaignID: "campaign-b", GameName: "Game", RequiredMinutes: 60, IsEarnable: true},
	}}

	_, initialKey, _, _ := app.farmingCampaignForGame("Game")
	if !app.setCurrentFarmingCampaign(initialKey) {
		t.Fatal("expected initial campaign set to be treated as changed")
	}

	app.lastDrops[0].IsClaimed = true
	app.lastDrops[0].IsEarnable = false
	_, updatedKey, _, _ := app.farmingCampaignForGame("Game")
	if updatedKey != "campaign-b" {
		t.Fatalf("expected only remaining active campaign, got %q", updatedKey)
	}
	if !app.setCurrentFarmingCampaign(updatedKey) {
		t.Fatal("expected a real active campaign set change to be detected")
	}
}

func TestFarmingCampaignForGameIgnoresCompletedInventoryCampaign(t *testing.T) {
	app := &App{
		lastDrops: []inventory.Drop{
			{ID: "old-drop", Name: "Old Drop", CampaignID: "old", CampaignName: "Old Campaign", GameName: "Game", RequiredMinutes: 30, IsClaimed: true},
		},
		lastActiveCampaignDrops: []inventory.Drop{
			{ID: "new-drop", Name: "New Drop", CampaignID: "new", CampaignName: "New Campaign", GameName: "Game", GameImageURL: "https://example.com/game.png", RequiredMinutes: 60, IsEarnable: true},
		},
	}

	campaignName, campaignKey, imageURL, dropList := app.farmingCampaignForGame("Game")
	if campaignKey != "new" || campaignName != "New Campaign" {
		t.Fatalf("expected only new active campaign, got name=%q key=%q", campaignName, campaignKey)
	}
	if imageURL != "https://example.com/game.png" {
		t.Fatalf("expected active campaign image, got %q", imageURL)
	}
	if len(dropList) != 1 || !strings.Contains(dropList[0], "New Drop") {
		t.Fatalf("expected only the new campaign drop, got %v", dropList)
	}
}

func TestDiscoverGamesStreamersKeepsStickyGameWithinSameBucket(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				FallbackAllCampaigns: true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		activeGames: []string{"Black Desert", "Sea of Thieves"},
		lastDrops: []inventory.Drop{
			{GameName: "Black Desert", IsEarnable: true, IsClaimed: false},
			{GameName: "Sea of Thieves", IsEarnable: true, IsClaimed: false},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	discoverer := &fakeGameDiscoverer{
		streamers: map[string][]domain.Streamer{
			"Black Desert": {
				{ID: "1", Login: "black_desert_one", GameName: "Black Desert"},
			},
			"Sea of Thieves": {
				{ID: "2", Login: "sea_one", GameName: "Sea of Thieves"},
			},
		},
	}

	streamers, err := app.discoverGamesStreamers(context.Background(), discoverer, "Sea of Thieves")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCalls := []string{"Sea of Thieves"}
	if len(discoverer.calls) != len(expectedCalls) || discoverer.calls[0] != expectedCalls[0] {
		t.Fatalf("expected calls %v, got %v", expectedCalls, discoverer.calls)
	}
	if len(streamers) != 1 || streamers[0].GameName != "Sea of Thieves" {
		t.Fatalf("expected Sea of Thieves streamers, got %v", streamers)
	}
}

func TestDiscoverGamesStreamersTriesHigherBucketBeforeStickyGame(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames:        []string{"Warframe"},
				FallbackAllCampaigns: true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		activeGames: []string{"Warframe", "Black Desert", "Sea of Thieves"},
		lastDrops: []inventory.Drop{
			{GameName: "Warframe", IsEarnable: true, IsClaimed: false},
			{GameName: "Black Desert", IsEarnable: true, IsClaimed: false},
			{GameName: "Sea of Thieves", IsEarnable: true, IsClaimed: false},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	discoverer := &fakeGameDiscoverer{
		streamers: map[string][]domain.Streamer{
			"Black Desert": {
				{ID: "1", Login: "black_desert_one", GameName: "Black Desert"},
			},
			"Sea of Thieves": {
				{ID: "2", Login: "sea_one", GameName: "Sea of Thieves"},
			},
		},
	}

	streamers, err := app.discoverGamesStreamers(context.Background(), discoverer, "Sea of Thieves")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCalls := []string{"Warframe", "Sea of Thieves"}
	if len(discoverer.calls) != len(expectedCalls) {
		t.Fatalf("expected calls %v, got %v", expectedCalls, discoverer.calls)
	}
	for i, expected := range expectedCalls {
		if discoverer.calls[i] != expected {
			t.Fatalf("expected calls %v, got %v", expectedCalls, discoverer.calls)
		}
	}
	if len(streamers) != 1 || streamers[0].GameName != "Sea of Thieves" {
		t.Fatalf("expected Sea of Thieves streamers after higher bucket has none, got %v", streamers)
	}
}

func TestCanKeepCurrentFarmingGameAllowsSameBucket(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames: []string{"Warframe"},
			},
		},
		lastDrops: []inventory.Drop{
			{GameName: "Black Desert", IsEarnable: true, IsClaimed: false},
			{GameName: "Sea of Thieves", IsEarnable: true, IsClaimed: false},
		},
	}

	if !app.canKeepCurrentFarmingGame([]string{"Black Desert", "Sea of Thieves"}, "Sea of Thieves") {
		t.Fatal("expected current game to stay when only same-bucket games are ahead")
	}
}

func TestCanKeepCurrentFarmingGameRejectsHigherBucket(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				PriorityGames: []string{"Warframe"},
			},
		},
		lastDrops: []inventory.Drop{
			{GameName: "Warframe", IsEarnable: true, IsClaimed: false},
			{GameName: "Sea of Thieves", IsEarnable: true, IsClaimed: false},
		},
	}

	if app.canKeepCurrentFarmingGame([]string{"Warframe", "Sea of Thieves"}, "Sea of Thieves") {
		t.Fatal("expected higher-priority bucket to preempt current game")
	}
}

func TestActiveGamesSignatureNormalizesGames(t *testing.T) {
	got := activeGamesSignature([]string{" Fortnite ", "Warhammer 40,000: Darktide"})
	want := "fortnite\x00warhammer 40,000: darktide"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestTopGamesReturnsCopyWithLimit(t *testing.T) {
	games := []string{"Fortnite", "Darktide", "2XKO"}
	top := topGames(games, 2)

	if len(top) != 2 || top[0] != "Fortnite" || top[1] != "Darktide" {
		t.Fatalf("unexpected top games: %v", top)
	}

	top[0] = "Changed"
	if games[0] != "Fortnite" {
		t.Fatalf("topGames should return a copy, original changed to %v", games)
	}
}

func TestActiveDropStreamerPrefersDynamicCandidate(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)

	streamer := app.activeDropStreamer(eng)
	if streamer == nil {
		t.Fatal("expected active drop streamer")
	}
	if streamer.Login != "dynamic" {
		t.Fatalf("expected dynamic streamer, got %q", streamer.Login)
	}
}

func TestIndexStreamsByUserIDIgnoresCanonicalLoginDifferences(t *testing.T) {
	streams := []twitch.StreamInfo{
		{UserID: "123", UserLogin: "renamed_channel", ID: "broadcast-1"},
	}

	indexed := indexStreamsByUserID(streams)
	stream, ok := indexed["123"]
	if !ok {
		t.Fatal("expected stream to be indexed by stable user ID")
	}
	if stream.UserLogin != "renamed_channel" || stream.ID != "broadcast-1" {
		t.Fatalf("unexpected indexed stream: %+v", stream)
	}
	if _, ok := indexed["old_channel_name"]; ok {
		t.Fatal("stream index must not depend on login")
	}
}

func TestRotateStalledDropStreamerRemovesDynamicCandidate(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	drops := []inventory.Drop{
		{
			ID:              "drop-1",
			GameName:        "Delta Force",
			RequiredMinutes: 240,
			CurrentMinutes:  197,
			IsEarnable:      true,
		},
	}

	app.rotateStalledDropStreamerIfNeeded(eng, drops)
	app.rotateStalledDropStreamerIfNeeded(eng, drops)
	if len(app.dynamicStreamers) != 1 {
		t.Fatalf("dynamic streamer removed too early: %v", app.dynamicStreamers)
	}

	app.rotateStalledDropStreamerIfNeeded(eng, drops)
	if len(app.dynamicStreamers) != 0 {
		t.Fatalf("expected stalled dynamic streamer to be removed, got %v", app.dynamicStreamers)
	}
}

func TestRotateStalledDropStreamerRotatesWhenGameMissingFromInventory(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	// No inventory entries for the watched game: the campaign never credited
	// a single minute, so it never entered DropCampaignsInProgress.
	drops := []inventory.Drop{
		{
			ID:              "drop-other",
			GameName:        "Other Game",
			RequiredMinutes: 240,
			CurrentMinutes:  197,
			IsEarnable:      true,
		},
	}

	app.rotateStalledDropStreamerIfNeeded(eng, drops)
	app.rotateStalledDropStreamerIfNeeded(eng, drops)
	if len(app.dynamicStreamers) != 1 {
		t.Fatalf("dynamic streamer removed too early: %v", app.dynamicStreamers)
	}

	app.rotateStalledDropStreamerIfNeeded(eng, drops)
	if len(app.dynamicStreamers) != 0 {
		t.Fatalf("expected non-crediting dynamic streamer to be removed, got %v", app.dynamicStreamers)
	}
	if !app.isStreamerOnRotationCooldown("dynamic") {
		t.Fatal("expected rotated streamer to be on cooldown")
	}
	if !app.isAuxiliaryWatchDisabled() {
		t.Fatal("expected stalled drop progress to disable auxiliary watch")
	}
}

func TestDiscoverGamesStreamersSkipsStreamersOnRotationCooldown(t *testing.T) {
	app := &App{
		config: config.Config{
			Watch: config.WatchConfig{
				FallbackAllCampaigns: true,
			},
			Features: config.FeatureConfig{
				ClaimDrops: config.Bool(true),
			},
		},
		activeGames: []string{"Target Game", "Later Game"},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	app.markStreamerRotationCooldown("target_one")
	app.markStreamerRotationCooldown("target_two")

	discoverer := &fakeGameDiscoverer{
		streamers: map[string][]domain.Streamer{
			"Target Game": {
				{ID: "1", Login: "target_one", GameName: "Target Game"},
				{ID: "2", Login: "target_two", GameName: "Target Game"},
			},
			"Later Game": {
				{ID: "3", Login: "later_one", GameName: "Later Game"},
			},
		},
	}

	streamers, err := app.discoverGamesStreamers(context.Background(), discoverer, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(streamers) != 1 || streamers[0].Login != "later_one" {
		t.Fatalf("expected discovery to skip cooled-down streamers and pick later_one, got %v", streamers)
	}
}

func TestCheckWatchCreditWatchdogAlertsAndRecovers(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	drops := []inventory.Drop{
		{
			ID:              "drop-1",
			GameName:        "Delta Force",
			RequiredMinutes: 240,
			CurrentMinutes:  100,
			IsEarnable:      true,
		},
	}

	// First poll establishes the baseline; no alert.
	app.checkWatchCreditWatchdog(eng, drops)
	if app.watchCreditAlerted {
		t.Fatal("watchdog alerted on baseline poll")
	}

	// Frozen progress but still under the threshold: no alert yet.
	app.checkWatchCreditWatchdog(eng, drops)
	if app.watchCreditAlerted {
		t.Fatal("watchdog alerted before threshold elapsed")
	}

	// Frozen progress past the threshold: alert.
	app.watchCreditMu.Lock()
	app.lastWatchCreditAt = time.Now().Add(-watchCreditStallThreshold - time.Minute)
	app.watchCreditMu.Unlock()
	app.checkWatchCreditWatchdog(eng, drops)
	if !app.watchCreditAlerted {
		t.Fatal("watchdog did not alert after threshold elapsed")
	}
	if !app.auxiliaryWatchDisabled {
		t.Fatal("watchdog did not disable auxiliary watch")
	}

	// Any credited minute clears the alert.
	drops[0].CurrentMinutes = 101
	app.checkWatchCreditWatchdog(eng, drops)
	if app.watchCreditAlerted {
		t.Fatal("watchdog alert not cleared after credit resumed")
	}
}

type fakeWatchTelemetrySender struct {
	calls       []string
	primaryErr  error
	presenceErr error
}

type blockingPresenceSender struct {
	started chan struct{}
}

func (b *blockingPresenceSender) SendMinuteWatched(ctx context.Context, streamer domain.Streamer, userID string) error {
	return nil
}

func (b *blockingPresenceSender) SendPresence(ctx context.Context, streamer domain.Streamer, userID string) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeWatchTelemetrySender) SendMinuteWatched(ctx context.Context, streamer domain.Streamer, userID string) error {
	f.calls = append(f.calls, "primary:"+streamer.Login)
	return f.primaryErr
}

func (f *fakeWatchTelemetrySender) SendPresence(ctx context.Context, streamer domain.Streamer, userID string) error {
	f.calls = append(f.calls, "auxiliary:"+streamer.Login)
	return f.presenceErr
}

func TestWatchTelemetryEstablishesPrimaryBeforeAuxiliary(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	app.config.Watch.AuxiliaryWatch = true
	app.config.Watch.AuxiliaryLeaseMinutes = 16
	app.staticStreamers[0].BroadcastID = "stream-1"
	app.staticStreamers[0].GameID = "other-game"
	app.staticStreamers[0].GameName = "Other Game"

	sender := &fakeWatchTelemetrySender{}
	state := watchTelemetryState{lastAuxiliaryWatch: make(map[string]time.Time)}
	started := time.Now()

	app.processWatchTelemetry(context.Background(), eng, sender, &state, started)
	if state.primarySuccessfulPulses != 1 || state.auxiliaryLogin != "" {
		t.Fatalf("after first primary pulse: %+v", state)
	}

	app.processWatchTelemetry(context.Background(), eng, sender, &state, started.Add(65*time.Second))
	if state.primarySuccessfulPulses != 2 || state.auxiliaryLogin != "" {
		t.Fatalf("after second primary pulse: %+v", state)
	}

	app.processWatchTelemetry(context.Background(), eng, sender, &state, started.Add(70*time.Second))
	if state.auxiliaryLogin != "static" {
		t.Fatalf("expected static auxiliary after handshake, got %+v", state)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("auxiliary sent before offset: %v", sender.calls)
	}

	app.processWatchTelemetry(context.Background(), eng, sender, &state, started.Add(101*time.Second))
	want := []string{"primary:dynamic", "primary:dynamic", "auxiliary:static"}
	if len(sender.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", sender.calls, want)
	}
	for i := range want {
		if sender.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", sender.calls, want)
		}
	}
}

func TestWatchTelemetryPausesAuxiliaryWhenPrimarySessionChanges(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	app.config.Watch.AuxiliaryWatch = true
	app.config.Watch.AuxiliaryLeaseMinutes = 16
	app.staticStreamers[0].BroadcastID = "stream-1"

	sender := &fakeWatchTelemetrySender{}
	state := watchTelemetryState{
		primarySession:          "dynamic\x00stream-2\x00",
		primarySuccessfulPulses: 2,
		auxiliaryLogin:          "static",
		auxiliarySession:        "static\x00stream-1\x00",
		auxiliaryLeaseStarted:   time.Now(),
		nextAuxiliaryPulse:      time.Now().Add(time.Minute),
		lastAuxiliaryWatch:      make(map[string]time.Time),
	}
	app.dynamicStreamers[0].BroadcastID = "stream-3"

	app.processWatchTelemetry(context.Background(), eng, sender, &state, time.Now())
	if state.auxiliaryLogin != "" {
		t.Fatalf("auxiliary remained active after primary transition: %+v", state)
	}
	if state.primarySuccessfulPulses != 1 {
		t.Fatalf("new primary session did not restart handshake: %+v", state)
	}
	if len(sender.calls) != 1 || sender.calls[0] != "primary:dynamic" {
		t.Fatalf("unexpected calls after primary transition: %v", sender.calls)
	}
}

func TestWatchTelemetryPausesAuxiliaryWhenPrimaryPulseFails(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	app.config.Watch.AuxiliaryWatch = true
	app.config.Watch.AuxiliaryLeaseMinutes = 16
	app.staticStreamers[0].BroadcastID = "stream-1"
	now := time.Now()
	sender := &fakeWatchTelemetrySender{primaryErr: errors.New("primary unavailable")}
	state := watchTelemetryState{
		primarySession:          "dynamic\x00stream-2\x00",
		primarySuccessfulPulses: 2,
		nextPrimaryPulse:        now,
		auxiliaryLogin:          "static",
		auxiliarySession:        "static\x00stream-1\x00",
		auxiliaryLeaseStarted:   now.Add(-time.Minute),
		nextAuxiliaryPulse:      now.Add(time.Minute),
		lastAuxiliaryWatch:      make(map[string]time.Time),
	}

	app.processWatchTelemetry(context.Background(), eng, sender, &state, now)
	if state.auxiliaryLogin != "" || state.primarySuccessfulPulses != 0 {
		t.Fatalf("primary failure did not reset safe state: %+v", state)
	}
	if len(sender.calls) != 1 || sender.calls[0] != "primary:dynamic" {
		t.Fatalf("unexpected calls after primary failure: %v", sender.calls)
	}
}

func TestSelectAuxiliaryStreamerUsesLeastRecentlyWatched(t *testing.T) {
	app := &App{staticStreamers: []domain.Streamer{
		{Login: "primary", BroadcastID: "broadcast-1", GameName: "Primary Game"},
		{Login: "recent", BroadcastID: "broadcast-2", GameName: "Just Chatting"},
		{Login: "unserved", BroadcastID: "broadcast-3", GameName: "Music"},
	}}
	selected := app.selectAuxiliaryStreamer("primary", map[string]time.Time{
		"recent": time.Now(),
	}, nil, time.Now())
	if selected == nil || selected.Login != "unserved" {
		t.Fatalf("expected unserved auxiliary, got %+v", selected)
	}
}

func TestSelectAuxiliaryStreamerExcludesActiveDropGamesAndBackoff(t *testing.T) {
	now := time.Now()
	app := &App{
		activeGames: []string{"Drop Game"},
		staticStreamers: []domain.Streamer{
			{Login: "drop_candidate", BroadcastID: "broadcast-1", GameName: "Drop Game"},
			{Login: "safe_candidate", BroadcastID: "broadcast-2", GameName: "Music"},
		},
	}
	selected := app.selectAuxiliaryStreamer("primary", nil, nil, now)
	if selected == nil || selected.Login != "safe_candidate" {
		t.Fatalf("expected safe non-drop auxiliary, got %+v", selected)
	}

	selected = app.selectAuxiliaryStreamer("primary", nil, map[string]time.Time{
		"safe_candidate": now.Add(time.Minute),
	}, now)
	if selected != nil {
		t.Fatalf("expected no candidate while safe streamer is in backoff, got %+v", selected)
	}
}

func TestWatchTelemetrySkipsAuxiliaryWithoutSafePrimaryWindow(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	app.config.Watch.AuxiliaryWatch = true
	app.config.Watch.AuxiliaryLeaseMinutes = 16
	app.staticStreamers[0].BroadcastID = "stream-1"
	app.staticStreamers[0].GameName = "Other Game"
	now := time.Now()
	sender := &fakeWatchTelemetrySender{}
	state := watchTelemetryState{
		primarySession:          "dynamic\x00stream-2\x00",
		primarySuccessfulPulses: 2,
		nextPrimaryPulse:        now.Add(primaryPulseSafetyMargin + minimumAuxiliaryWindow - time.Second),
		auxiliaryLogin:          "static",
		auxiliarySession:        "static\x00stream-1\x00",
		auxiliaryLeaseStarted:   now.Add(-time.Minute),
		nextAuxiliaryPulse:      now,
		lastAuxiliaryWatch:      make(map[string]time.Time),
	}

	app.processWatchTelemetry(context.Background(), eng, sender, &state, now)
	if len(sender.calls) != 0 {
		t.Fatalf("auxiliary request started without a safe primary window: %v", sender.calls)
	}
}

func TestDisableAuxiliaryWatchCancelsInFlightPresence(t *testing.T) {
	app, eng := newActiveDropStreamerTestApp(t)
	app.config.Watch.AuxiliaryWatch = true
	app.config.Watch.AuxiliaryLeaseMinutes = 16
	app.staticStreamers[0].BroadcastID = "stream-1"
	app.staticStreamers[0].GameName = "Other Game"
	now := time.Now()
	sender := &blockingPresenceSender{started: make(chan struct{})}
	state := watchTelemetryState{
		primarySession:          "dynamic\x00stream-2\x00",
		primarySuccessfulPulses: 2,
		nextPrimaryPulse:        now.Add(time.Minute),
		auxiliaryLogin:          "static",
		auxiliarySession:        "static\x00stream-1\x00",
		auxiliaryLeaseStarted:   now.Add(-time.Minute),
		nextAuxiliaryPulse:      now,
		lastAuxiliaryWatch:      make(map[string]time.Time),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.processWatchTelemetry(context.Background(), eng, sender, &state, now)
	}()

	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("auxiliary presence did not start")
	}
	app.disableAuxiliaryWatch("test")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight auxiliary presence was not cancelled")
	}
}

func TestCheckWatchCreditWatchdogIgnoresIdlePeriods(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		Watch:    config.WatchConfig{TickSeconds: 60},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}
	app := &App{config: cfg, logger: logger}
	eng := engine.New(cfg, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = eng.Run(ctx) }()

	drops := []inventory.Drop{
		{
			ID:              "drop-1",
			GameName:        "Delta Force",
			RequiredMinutes: 240,
			CurrentMinutes:  100,
			IsEarnable:      true,
		},
	}

	app.checkWatchCreditWatchdog(eng, drops)
	app.watchCreditMu.Lock()
	app.lastWatchCreditAt = time.Now().Add(-watchCreditStallThreshold - time.Minute)
	app.watchCreditMu.Unlock()

	// No active drop streamer: no credit is expected, so no alert.
	app.checkWatchCreditWatchdog(eng, drops)
	if app.watchCreditAlerted {
		t.Fatal("watchdog alerted while nothing was being watched")
	}
}

func newActiveDropStreamerTestApp(t *testing.T) (*App, *engine.Engine) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		Watch: config.WatchConfig{TickSeconds: 60},
		Features: config.FeatureConfig{
			ClaimDrops: config.Bool(true),
		},
		Streamers: []config.StreamerConfig{{Login: "static"}},
	}
	app := &App{
		config: cfg,
		logger: logger,
		staticStreamers: []domain.Streamer{
			{ID: "1", Login: "static", DisplayName: "Static", GameName: "Delta Force"},
		},
		dynamicStreamers: []domain.Streamer{
			{ID: "2", Login: "dynamic", DisplayName: "Dynamic", GameName: "Delta Force", BroadcastID: "stream-2"},
		},
		activeGames: []string{"Delta Force"},
	}
	eng := engine.New(cfg, app.getCombinedStreamers(), logger)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = eng.Run(ctx) }()

	eng.SendEvent(engine.Event{Type: engine.EventActiveGames, Payload: []string{"Delta Force"}, Time: time.Now().UTC()})
	eng.SendEvent(engine.Event{Type: engine.EventOnline, Streamer: "static", Time: time.Now().UTC()})
	eng.SendEvent(engine.Event{Type: engine.EventOnline, Streamer: "dynamic", Time: time.Now().UTC()})
	waitForActiveStreamers(t, eng, 2)

	return app, eng
}

func waitForActiveStreamers(t *testing.T, eng *engine.Engine, count int) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(eng.ActiveStreamers()) == count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("active streamers = %v, want count %d", eng.ActiveStreamers(), count)
}

func TestSortActiveGamesOrdersOtherGamesByUrgency(t *testing.T) {
	now := time.Now().UTC()
	app := &App{config: config.Config{
		Watch:    config.WatchConfig{FallbackAllCampaigns: true},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}}
	drops := []inventory.Drop{
		{GameName: "Comfortable Game", IsEarnable: true, RequiredMinutes: 60, EndsAt: now.Add(48 * time.Hour)},
		{GameName: "Urgent Game", IsEarnable: true, RequiredMinutes: 60, EndsAt: now.Add(2 * time.Hour)},
		{GameName: "No Deadline Game", IsEarnable: true, RequiredMinutes: 60},
	}

	sorted := app.sortActiveGames(context.Background(), inventory.Client{}, drops, "")

	expected := []string{"Urgent Game", "Comfortable Game", "No Deadline Game"}
	if len(sorted) != len(expected) {
		t.Fatalf("expected len %d, got %d (sorted = %v)", len(expected), len(sorted), sorted)
	}
	for i, game := range expected {
		if sorted[i] != game {
			t.Errorf("at index %d: expected %q, got %q (sorted = %v)", i, game, sorted[i], sorted)
		}
	}
}

func TestSortActiveGamesExcludesUnfinishablePriorityGame(t *testing.T) {
	now := time.Now().UTC()
	app := &App{config: config.Config{
		Watch:    config.WatchConfig{PriorityGames: []string{"Doomed Game", "Healthy Game"}},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}}
	drops := []inventory.Drop{
		// Unclaimed, window still open, but 290 remaining minutes can never
		// fit before a deadline one hour away.
		{GameName: "Doomed Game", IsEarnable: false, RequiredMinutes: 300, CurrentMinutes: 10, EndsAt: now.Add(time.Hour)},
		{GameName: "Healthy Game", IsEarnable: true, RequiredMinutes: 60, EndsAt: now.Add(48 * time.Hour)},
	}

	sorted := app.sortActiveGames(context.Background(), inventory.Client{}, drops, "")

	if len(sorted) != 1 || sorted[0] != "Healthy Game" {
		t.Fatalf("expected only the finishable game, got %v", sorted)
	}
}

func TestSortActiveGamesKeepsPriorityGameWithoutDeadlineInfo(t *testing.T) {
	app := &App{config: config.Config{
		Watch:    config.WatchConfig{PriorityGames: []string{"Mystery Game"}},
		Features: config.FeatureConfig{ClaimDrops: config.Bool(true)},
	}}
	// Unclaimed drop without deadline data must stay farmable (safety net for
	// campaigns where Twitch reports incomplete metadata).
	drops := []inventory.Drop{
		{GameName: "Mystery Game", IsEarnable: false, IsClaimed: false},
	}

	sorted := app.sortActiveGames(context.Background(), inventory.Client{}, drops, "")

	if len(sorted) != 1 || sorted[0] != "Mystery Game" {
		t.Fatalf("expected game without deadline info to be kept, got %v", sorted)
	}
}
