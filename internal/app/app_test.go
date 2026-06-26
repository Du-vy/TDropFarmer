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

	// Test EventOnline
	msg := app.formatEventMessage(engine.Event{
		Type:     engine.EventOnline,
		Streamer: "streamer1",
	})
	expected := "🟢 Streamer **streamer1** is now ONLINE!"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	// Test EventOffline
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventOffline,
		Streamer: "streamer1",
	})
	expected = "🔴 Streamer **streamer1** is now OFFLINE!"
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
	response []byte
}

func (m mockInventoryGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	return gql.Response{Data: m.response}, nil
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
				{"status": "ACTIVE", "game": {"displayName": "Overwatch"}},
				{"status": "ACTIVE", "game": {"displayName": "THE FINALS"}},
				{"status": "ACTIVE", "game": {"displayName": "Minecraft"}}
			]
		}
	}`

	mockClient := mockInventoryGQLClient{response: []byte(mockCampaignsJSON)}
	invClient := inventory.Client{Client: mockClient}

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
}
