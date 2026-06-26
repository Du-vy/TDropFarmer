package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	ApplyDefaults(&cfg)
	Normalize(&cfg)
	if err := Validate(cfg); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", path, err)
	}

	return cfg, nil
}

func ApplyDefaults(cfg *Config) {
	if cfg.Auth.ClientID == "" {
		cfg.Auth.ClientID = "kd1unb4b3q4t58fwlpcbzcbnm76a8fp"
	}
	if len(cfg.Auth.Scopes) == 0 {
		cfg.Auth.Scopes = []string{
			"channel_read",
			"chat:read",
			"user_blocks_edit",
			"user_blocks_read",
			"user_follows_edit",
			"user_read",
		}
	}

	if len(cfg.Watch.Priorities) == 0 {
		cfg.Watch.Priorities = []string{"streak", "order"}
	}
	if cfg.Watch.TickSeconds == 0 {
		cfg.Watch.TickSeconds = 20
	}
	if cfg.Watch.MaxCampaigns == 0 {
		cfg.Watch.MaxCampaigns = 3
	}

	if cfg.Features.ClaimBonuses == nil {
		cfg.Features.ClaimBonuses = Bool(true)
	}
	if cfg.Features.ClaimDrops == nil {
		cfg.Features.ClaimDrops = Bool(false)
	}
	if cfg.Features.DryRun == nil {
		cfg.Features.DryRun = Bool(false)
	}
	if cfg.Features.Chat == nil {
		cfg.Features.Chat = Bool(false)
	}

	if cfg.Storage.Path == "" {
		cfg.Storage.Path = "./data"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "text"
	}
	if cfg.Notifications.Webhook.Method == "" {
		cfg.Notifications.Webhook.Method = "POST"
	}
}

func Normalize(cfg *Config) {
	cfg.Account.Username = normalizeLogin(cfg.Account.Username)
	for i := range cfg.Streamers {
		cfg.Streamers[i].Login = normalizeLogin(cfg.Streamers[i].Login)
	}
	for i := range cfg.Watch.Games {
		cfg.Watch.Games[i] = strings.TrimSpace(cfg.Watch.Games[i])
	}
}
