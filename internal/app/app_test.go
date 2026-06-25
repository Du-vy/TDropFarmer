package app

import (
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/engine"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
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
