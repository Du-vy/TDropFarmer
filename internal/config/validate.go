package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var loginPattern = regexp.MustCompile(`^[a-z0-9_]{4,25}$`)

func Validate(cfg Config) error {
	var errs []error

	if !validLogin(cfg.Account.Username) {
		errs = append(errs, fmt.Errorf("account.username must be a valid Twitch login"))
	}
	if cfg.Auth.ClientID == "" {
		errs = append(errs, fmt.Errorf("auth.client_id must be set"))
	}
	if cfg.Watch.MaxChannels < 1 || cfg.Watch.MaxChannels > 2 {
		errs = append(errs, fmt.Errorf("watch.max_channels must be between 1 and 2"))
	}
	if cfg.Watch.TickSeconds < 5 {
		errs = append(errs, fmt.Errorf("watch.tick_seconds must be at least 5"))
	}

	for _, priority := range cfg.Watch.Priorities {
		if !validPriority(priority) {
			errs = append(errs, fmt.Errorf("watch.priorities contains unsupported value %q", priority))
		}
	}

	if cfg.Features.FollowRaidsEnabled() {
		errs = append(errs, fmt.Errorf("features.follow_raids is post-MVP and must remain false for now"))
	}

	if err := validatePredictions("predictions", cfg.Predictions); err != nil {
		errs = append(errs, err)
	}

	seen := make(map[string]struct{}, len(cfg.Streamers))
	for i, streamer := range cfg.Streamers {
		field := fmt.Sprintf("streamers[%d]", i)
		if !validLogin(streamer.Login) {
			errs = append(errs, fmt.Errorf("%s.login must be a valid Twitch login", field))
			continue
		}
		if _, ok := seen[streamer.Login]; ok {
			errs = append(errs, fmt.Errorf("%s.login duplicates %q", field, streamer.Login))
		}
		seen[streamer.Login] = struct{}{}

		if streamer.FollowRaids != nil && *streamer.FollowRaids {
			errs = append(errs, fmt.Errorf("%s.follow_raids is post-MVP and must remain false for now", field))
		}
		if streamer.PredictionSettings != nil {
			if err := validatePredictions(field+".prediction_settings", *streamer.PredictionSettings); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if cfg.Storage.Path == "" {
		errs = append(errs, fmt.Errorf("storage.path must be set"))
	}
	if !validLogLevel(cfg.Logging.Level) {
		errs = append(errs, fmt.Errorf("logging.level must be debug, info, warn, or error"))
	}
	if cfg.Logging.Format != "text" && cfg.Logging.Format != "json" {
		errs = append(errs, fmt.Errorf("logging.format must be text or json"))
	}
	if cfg.Notifications.Discord.Enabled && cfg.Notifications.Discord.WebhookURL == "" {
		errs = append(errs, fmt.Errorf("notifications.discord.webhook_url must be set when discord is enabled"))
	}
	if cfg.Notifications.Webhook.Enabled && cfg.Notifications.Webhook.URL == "" {
		errs = append(errs, fmt.Errorf("notifications.webhook.url must be set when webhook is enabled"))
	}

	return errors.Join(errs...)
}

func validatePredictions(field string, cfg PredictionConfig) error {
	var errs []error

	if !validPredictionStrategy(cfg.Strategy) {
		errs = append(errs, fmt.Errorf("%s.strategy is unsupported", field))
	}
	if cfg.Percentage < 1 || cfg.Percentage > 100 {
		errs = append(errs, fmt.Errorf("%s.percentage must be between 1 and 100", field))
	}
	if cfg.PercentageGap < 0 || cfg.PercentageGap > 100 {
		errs = append(errs, fmt.Errorf("%s.percentage_gap must be between 0 and 100", field))
	}
	if cfg.MaxPoints < 1 {
		errs = append(errs, fmt.Errorf("%s.max_points must be greater than 0", field))
	}
	if cfg.MinimumPoints < 0 {
		errs = append(errs, fmt.Errorf("%s.minimum_points must not be negative", field))
	}
	if !validDelayMode(cfg.DelayMode) {
		errs = append(errs, fmt.Errorf("%s.delay_mode is unsupported", field))
	}
	if cfg.DelaySeconds < 0 {
		errs = append(errs, fmt.Errorf("%s.delay_seconds must not be negative", field))
	}
	if cfg.FilterCondition != nil {
		if !validFilterKey(cfg.FilterCondition.By) {
			errs = append(errs, fmt.Errorf("%s.filter_condition.by is unsupported", field))
		}
		if !validFilterOperator(cfg.FilterCondition.Where) {
			errs = append(errs, fmt.Errorf("%s.filter_condition.where is unsupported", field))
		}
	}

	return errors.Join(errs...)
}

func normalizeLogin(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validLogin(value string) bool {
	return loginPattern.MatchString(value)
}

func validPriority(value string) bool {
	switch value {
	case "streak", "order", "points_ascending", "points_descending":
		return true
	default:
		return false
	}
}

func validLogLevel(value string) bool {
	switch value {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validPredictionStrategy(value string) bool {
	if strings.HasPrefix(value, "fixed_outcome_") {
		suffix := strings.TrimPrefix(value, "fixed_outcome_")
		outcome, err := strconv.Atoi(suffix)
		return err == nil && outcome >= 1 && outcome <= 8
	}
	switch value {
	case "most_voted", "high_odds", "percentage", "smart_money", "smart":
		return true
	default:
		return false
	}
}

func validDelayMode(value string) bool {
	switch value {
	case "from_start", "from_end", "percentage":
		return true
	default:
		return false
	}
}

func validFilterKey(value string) bool {
	switch value {
	case "percentage_users", "odds_percentage", "odds", "decision_users", "decision_points", "top_points", "total_users", "total_points":
		return true
	default:
		return false
	}
}

func validFilterOperator(value string) bool {
	switch value {
	case "gt", "gte", "lt", "lte":
		return true
	default:
		return false
	}
}
