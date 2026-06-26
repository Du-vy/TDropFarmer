package app

import (
	"context"
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/engine"
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
	detailResponses   map[string][]byte
}

func (m mockInventoryGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	if req.OperationName == "ViewerDropsDashboard" {
		return gql.Response{Data: m.dashboardResponse}, nil
	}
	if req.OperationName == "DropCampaignDetails" {
		id, _ := req.Variables["dropID"].(string)
		return gql.Response{Data: m.detailResponses[id]}, nil
	}
	return gql.Response{}, nil
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
			"the-finals": earnableDetail("the-finals", "THE FINALS"),
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

	sorted := app.sortActiveGames(context.Background(), invClient, drops)
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
	sorted = app.sortActiveGames(context.Background(), invClient, drops)
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
	sorted = app.sortActiveGames(context.Background(), invClient, drops)
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
}
