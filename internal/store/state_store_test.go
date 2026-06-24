package store

import (
	"errors"
	"testing"
	"time"
)

func TestStateStoreRecordPointGain(t *testing.T) {
	store := NewStateStore(t.TempDir())
	gainTime := time.Now().UTC().Round(time.Second)

	if err := store.RecordPointGain(PointGain{
		Login:     "streamer",
		ChannelID: "1234",
		Amount:    50,
		Reason:    "bonus_claim",
		Time:      gainTime,
	}); err != nil {
		t.Fatalf("RecordPointGain returned error: %v", err)
	}

	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	streamer := state.Streamers["streamer"]
	if streamer.PointsGained != 50 {
		t.Fatalf("points gained = %d, want 50", streamer.PointsGained)
	}
	if streamer.BonusClaims != 1 {
		t.Fatalf("bonus claims = %d, want 1", streamer.BonusClaims)
	}
	if !streamer.LastPointGainAt.Equal(gainTime) {
		t.Fatalf("last point gain = %s, want %s", streamer.LastPointGainAt, gainTime)
	}
}

func TestStateStoreMissing(t *testing.T) {
	store := NewStateStore(t.TempDir())
	if _, err := store.Load(); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("Load error = %v, want ErrStateNotFound", err)
	}
}
