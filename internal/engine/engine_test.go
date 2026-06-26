package engine

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
)

func TestEngineReschedule(t *testing.T) {
	cfg := config.Config{
		Watch: config.WatchConfig{
			Priorities:  []string{"order"},
			TickSeconds: 1,
		},
	}
	resolved := []domain.Streamer{
		{Login: "a", ID: "1", DisplayName: "A"},
		{Login: "b", ID: "2", DisplayName: "B"},
	}
	eng := New(cfg, resolved, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = eng.Run(ctx)
}

func TestEngineSendEventStreak(t *testing.T) {
	cfg := config.Config{
		Watch: config.WatchConfig{
			Priorities:  []string{"streak", "order"},
			TickSeconds: 5,
		},
	}
	resolved := []domain.Streamer{
		{Login: "a", ID: "1", DisplayName: "A"},
	}
	eng := New(cfg, resolved, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		eng.SendEvent(Event{
			Type:     EventStreak,
			Streamer: "a",
			Time:     time.Now(),
		})
	}()

	_ = eng.Run(ctx)
}

func TestEngineRecordsPointGain(t *testing.T) {
	recorder := &recordingPointRecorder{}
	eng := New(config.Config{}, []domain.Streamer{{Login: "a", ID: "1"}}, slog.Default(), WithPointRecorder(recorder))

	eng.handleEvent(context.Background(), Event{
		Type:     EventPoints,
		Streamer: "a",
		Payload:  int64(25),
		Time:     time.Now(),
	})

	if eng.streamers[0].Points != 25 {
		t.Fatalf("points = %d, want 25", eng.streamers[0].Points)
	}
	if len(recorder.gains) != 1 {
		t.Fatalf("recorded gains = %d, want 1", len(recorder.gains))
	}
	if recorder.gains[0].Amount != 25 {
		t.Fatalf("recorded amount = %d, want 25", recorder.gains[0].Amount)
	}
}

func TestEngineClaimsBonusAndRecordsPoints(t *testing.T) {
	claimer := &fakeBonusClaimer{result: channelpoints.ClaimResult{ClaimID: "claim-1", Claimed: true, Points: 50}}
	recorder := &recordingPointRecorder{}
	eng := New(config.Config{}, []domain.Streamer{{Login: "a", ID: "1"}}, slog.Default(), WithBonusClaimer(claimer), WithPointRecorder(recorder))

	eng.handleEvent(context.Background(), Event{
		Type:      EventBonusAvailable,
		Streamer:  "a",
		ChannelID: "1",
		Payload: channelpoints.ClaimableBonus{
			ClaimID:       "claim-1",
			ChannelID:     "1",
			StreamerLogin: "a",
			Points:        50,
		},
		Time: time.Now(),
	})

	if !claimer.called {
		t.Fatalf("claimer was not called")
	}
	if eng.streamers[0].Points != 50 {
		t.Fatalf("points = %d, want 50", eng.streamers[0].Points)
	}
	if len(recorder.gains) != 1 {
		t.Fatalf("recorded gains = %d, want 1", len(recorder.gains))
	}
	if recorder.gains[0].Reason != "bonus_claim" {
		t.Fatalf("reason = %q, want bonus_claim", recorder.gains[0].Reason)
	}
}

func TestEngineDryRunBonusDoesNotClaimOrRecord(t *testing.T) {
	claimer := &fakeBonusClaimer{result: channelpoints.ClaimResult{ClaimID: "claim-1", Claimed: true, Points: 50}}
	recorder := &recordingPointRecorder{}
	cfg := config.Config{Features: config.FeatureConfig{DryRun: config.Bool(true)}}
	eng := New(cfg, []domain.Streamer{{Login: "a", ID: "1"}}, slog.Default(), WithBonusClaimer(claimer), WithPointRecorder(recorder))

	eng.handleEvent(context.Background(), Event{
		Type:      EventBonusAvailable,
		Streamer:  "a",
		ChannelID: "1",
		Payload: channelpoints.ClaimableBonus{
			ClaimID:       "claim-1",
			ChannelID:     "1",
			StreamerLogin: "a",
			Points:        50,
		},
		Time: time.Now(),
	})

	if claimer.called {
		t.Fatalf("claimer was called in dry-run")
	}
	if len(recorder.gains) != 0 {
		t.Fatalf("recorded gains = %d, want 0", len(recorder.gains))
	}
	select {
	case event := <-eng.Events():
		result, ok := event.Payload.(channelpoints.ClaimResult)
		if !ok || !result.DryRun {
			t.Fatalf("event payload = %#v, want dry-run claim result", event.Payload)
		}
	default:
		t.Fatalf("expected dry-run bonus event")
	}
}

type recordingPointRecorder struct {
	gains []store.PointGain
}

func (r *recordingPointRecorder) RecordPointGain(gain store.PointGain) error {
	r.gains = append(r.gains, gain)
	return nil
}

type fakeBonusClaimer struct {
	called bool
	result channelpoints.ClaimResult
	err    error
}

func (f *fakeBonusClaimer) ClaimBonus(context.Context, channelpoints.ClaimableBonus) (channelpoints.ClaimResult, error) {
	f.called = true
	return f.result, f.err
}
