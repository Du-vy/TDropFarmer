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
	if !cfg.Features.ClaimBonusesEnabled() {
		t.Fatalf("claim bonuses default should be enabled")
	}
	if cfg.Watch.AuxiliaryWatch {
		t.Fatal("auxiliary watch must be opt-in")
	}
	if cfg.Watch.AuxiliaryLeaseMinutes != 16 {
		t.Fatalf("auxiliary lease = %d, want 16", cfg.Watch.AuxiliaryLeaseMinutes)
	}
}

func TestAuthFallbackDefaults(t *testing.T) {
	cfg := Config{
		Account: AccountConfig{Username: "my_user"},
	}

	ApplyDefaults(&cfg)
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate failed with default TV auth credentials: %v", err)
	}

	if cfg.Auth.ClientID != "kd1unb4b3q4t58fwlpcbzcbnm76a8fp" {
		t.Fatalf("expected ClientID 'kd1unb4b3q4t58fwlpcbzcbnm76a8fp', got %q", cfg.Auth.ClientID)
	}

	if len(cfg.Auth.Scopes) == 0 {
		t.Fatalf("expected default scopes to be populated")
	}
}

func TestValidateProxyURL(t *testing.T) {
	base := Config{
		Account: AccountConfig{Username: "my_user"},
		Auth:    AuthConfig{ClientID: "example-client-id"},
		Watch:   WatchConfig{TickSeconds: 20},
		Storage: StorageConfig{Path: "./data"},
		Logging: LoggingConfig{Level: "info", Format: "text"},
	}

	cfg := base
	cfg.Network.ProxyURL = "socks5://warp:1080"
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate rejected valid socks5 proxy url: %v", err)
	}

	cfg = base
	cfg.Network.ProxyURL = "socks5h://warp:1080"
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate rejected valid socks5h proxy url: %v", err)
	}

	cfg = base
	cfg.Network.ProxyURL = "http://proxy:8080"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected http proxy url to fail validation")
	}
}

func TestNormalizeTrimsProxyURL(t *testing.T) {
	cfg := Config{
		Account: AccountConfig{Username: "my_user"},
		Network: NetworkConfig{ProxyURL: " socks5://warp:1080 "},
	}
	Normalize(&cfg)

	if cfg.Network.ProxyURL != "socks5://warp:1080" {
		t.Fatalf("proxy url = %q, want trimmed value", cfg.Network.ProxyURL)
	}
}

func TestValidateAuxiliaryWatchLease(t *testing.T) {
	cfg := Config{
		Account: AccountConfig{Username: "my_user"},
		Auth:    AuthConfig{ClientID: "example-client-id"},
		Watch: WatchConfig{
			TickSeconds:           20,
			AuxiliaryWatch:        true,
			AuxiliaryLeaseMinutes: 9,
		},
		Storage: StorageConfig{Path: "./data"},
		Logging: LoggingConfig{Level: "info", Format: "text"},
	}

	if err := Validate(cfg); err == nil {
		t.Fatal("expected auxiliary lease shorter than 10 minutes to fail validation")
	}
}
