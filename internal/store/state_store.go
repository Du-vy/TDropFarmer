package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrStateNotFound = errors.New("state not found")

type RuntimeState struct {
	UpdatedAt time.Time                       `json:"updated_at"`
	Streamers map[string]StreamerRuntimeState `json:"streamers"`
}

type StreamerRuntimeState struct {
	Login            string    `json:"login"`
	ChannelID        string    `json:"channel_id"`
	PointsGained     int64     `json:"points_gained"`
	BonusClaims      int       `json:"bonus_claims"`
	LastPointGainAt  time.Time `json:"last_point_gain_at,omitempty"`
	LastBonusClaimAt time.Time `json:"last_bonus_claim_at,omitempty"`
}

type PointGain struct {
	Login     string
	ChannelID string
	Amount    int64
	Reason    string
	Time      time.Time
}

type StateStore struct {
	path string
}

func NewStateStore(dataDir string) StateStore {
	return StateStore{path: filepath.Join(dataDir, "state.json")}
}

func (s StateStore) Load() (RuntimeState, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RuntimeState{}, ErrStateNotFound
		}
		return RuntimeState{}, fmt.Errorf("read state file: %w", err)
	}

	var state RuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return RuntimeState{}, fmt.Errorf("parse state file: %w", err)
	}
	if state.Streamers == nil {
		state.Streamers = make(map[string]StreamerRuntimeState)
	}
	return state, nil
}

func (s StateStore) Save(state RuntimeState) error {
	if state.Streamers == nil {
		state.Streamers = make(map[string]StreamerRuntimeState)
	}
	state.UpdatedAt = time.Now().UTC()
	return writeJSONAtomic(s.path, state)
}

func (s StateStore) RecordPointGain(gain PointGain) error {
	if gain.Login == "" {
		return fmt.Errorf("point gain login must not be empty")
	}
	if gain.Amount == 0 {
		return nil
	}
	if gain.Time.IsZero() {
		gain.Time = time.Now().UTC()
	}

	state, err := s.Load()
	if err != nil {
		if !errors.Is(err, ErrStateNotFound) {
			return err
		}
		state = RuntimeState{Streamers: make(map[string]StreamerRuntimeState)}
	}

	streamer := state.Streamers[gain.Login]
	streamer.Login = gain.Login
	streamer.ChannelID = gain.ChannelID
	streamer.PointsGained += gain.Amount
	streamer.LastPointGainAt = gain.Time.UTC()
	if gain.Reason == "bonus_claim" {
		streamer.BonusClaims++
		streamer.LastBonusClaimAt = gain.Time.UTC()
	}
	state.Streamers[gain.Login] = streamer

	return s.Save(state)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}

	if err := replaceFile(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
