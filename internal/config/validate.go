package config

import (
	"errors"
	"fmt"
	"regexp"
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
	if cfg.Watch.TickSeconds < 5 {
		errs = append(errs, fmt.Errorf("watch.tick_seconds must be at least 5"))
	}
	if cfg.Watch.MaxCampaigns < 1 {
		errs = append(errs, fmt.Errorf("watch.max_campaigns must be at least 1"))
	}

	for _, priority := range cfg.Watch.Priorities {
		if !validPriority(priority) {
			errs = append(errs, fmt.Errorf("watch.priorities contains unsupported value %q", priority))
		}
	}
	for i, game := range cfg.Watch.Games {
		if strings.TrimSpace(game) == "" {
			errs = append(errs, fmt.Errorf("watch.games[%d] must not be empty", i))
		}
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

