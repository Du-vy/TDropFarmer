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

	// Test EventPredictionPlaced
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventPredictionPlaced,
		Streamer: "streamer1",
		Payload: engine.PredictionPlacedPayload{
			Title:   "Will they win?",
			Outcome: "Yes",
			Amount:  100,
			DryRun:  true,
		},
	})
	expected = "🔮 Placed prediction on **streamer1**: [Dry Run]\n**Will they win?**\nApuesta: **Yes** (100 puntos)"
	if msg != expected {
		t.Errorf("expected %q, got %q", expected, msg)
	}

	// Test EventPredictionResult
	msg = app.formatEventMessage(engine.Event{
		Type:     engine.EventPredictionResult,
		Streamer: "streamer1",
		Payload: engine.PredictionResultPayload{
			Prediction: engine.PredictionEvent{
				Title: "Will they win?",
			},
			Result: engine.PredictionResultEvent{
				Type:      engine.PredictionWin,
				PointsWon: 200,
			},
		},
	})
	expected = "🏁 Prediction finished on **streamer1**:\n**Will they win?**\nResultado: **WIN** (Puntos ganados: 200)"
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
}
