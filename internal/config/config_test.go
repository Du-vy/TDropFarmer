package config

import (
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.json")
	if _, err := Load(path); err != nil {
		t.Fatalf("Load(%q) returned error: %v", path, err)
	}
}

func TestDefaultsAndNormalize(t *testing.T) {
	cfg := Config{
		Account: AccountConfig{Username: " My_Twitch_User "},
		Auth:    AuthConfig{ClientID: "example-client-id"},
		Streamers: []StreamerConfig{
			{Login: " Streamer_One "},
		},
	}

	ApplyDefaults(&cfg)
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if cfg.Account.Username != "my_twitch_user" {
		t.Fatalf("username = %q, want normalized login", cfg.Account.Username)
	}
	if cfg.Streamers[0].Login != "streamer_one" {
		t.Fatalf("streamer login = %q, want normalized login", cfg.Streamers[0].Login)
	}
	if cfg.Watch.MaxChannels != 2 {
		t.Fatalf("max channels = %d, want 2", cfg.Watch.MaxChannels)
	}
	if !cfg.Features.ClaimBonusesEnabled() {
		t.Fatalf("claim bonuses default should be enabled")
	}
	if cfg.Features.PredictionsEnabled() {
		t.Fatalf("predictions default should be disabled")
	}
}

func TestRejectsPostMVPFeatures(t *testing.T) {
	cfg := Config{
		Account: AccountConfig{Username: "my_user"},
		Auth:    AuthConfig{ClientID: "example-client-id"},
		Features: FeatureConfig{
			ClaimDrops:  Bool(true),
			FollowRaids: Bool(true),
		},
	}

	ApplyDefaults(&cfg)
	Normalize(&cfg)

	if err := Validate(cfg); err == nil {
		t.Fatalf("Validate returned nil, want post-MVP feature errors")
	}
}

func TestRejectsInvalidFixedOutcomeStrategy(t *testing.T) {
	cfg := Config{
		Account: AccountConfig{Username: "my_user"},
		Auth:    AuthConfig{ClientID: "example-client-id"},
		Predictions: PredictionConfig{
			Strategy: "fixed_outcome_11",
		},
	}

	ApplyDefaults(&cfg)
	Normalize(&cfg)

	if err := Validate(cfg); err == nil {
		t.Fatalf("Validate returned nil, want invalid prediction strategy error")
	}
}
