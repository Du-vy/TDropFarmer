package config

import (
	"encoding/json"
	"fmt"
	"os"
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
	if cfg.Watch.MaxChannels == 0 {
		cfg.Watch.MaxChannels = 2
	}
	if len(cfg.Watch.Priorities) == 0 {
		cfg.Watch.Priorities = []string{"streak", "order"}
	}
	if cfg.Watch.TickSeconds == 0 {
		cfg.Watch.TickSeconds = 20
	}

	if cfg.Features.ClaimBonuses == nil {
		cfg.Features.ClaimBonuses = Bool(true)
	}
	if cfg.Features.ClaimDrops == nil {
		cfg.Features.ClaimDrops = Bool(false)
	}
	if cfg.Features.FollowRaids == nil {
		cfg.Features.FollowRaids = Bool(false)
	}
	if cfg.Features.Predictions == nil {
		cfg.Features.Predictions = Bool(false)
	}
	if cfg.Features.DryRun == nil {
		cfg.Features.DryRun = Bool(false)
	}
	if cfg.Features.Chat == nil {
		cfg.Features.Chat = Bool(false)
	}

	if cfg.Predictions.Strategy == "" {
		cfg.Predictions.Strategy = "smart"
	}
	if cfg.Predictions.Percentage == 0 {
		cfg.Predictions.Percentage = 5
	}
	if cfg.Predictions.PercentageGap == 0 {
		cfg.Predictions.PercentageGap = 20
	}
	if cfg.Predictions.MaxPoints == 0 {
		cfg.Predictions.MaxPoints = 50000
	}
	if cfg.Predictions.MinimumPoints == 0 {
		cfg.Predictions.MinimumPoints = 20000
	}
	if cfg.Predictions.DelayMode == "" {
		cfg.Predictions.DelayMode = "from_end"
	}
	if cfg.Predictions.DelaySeconds == 0 {
		cfg.Predictions.DelaySeconds = 6
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
}
