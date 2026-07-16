package config

type Config struct {
	Account       AccountConfig      `json:"account"`
	Auth          AuthConfig         `json:"auth"`
	Watch         WatchConfig        `json:"watch"`
	Features      FeatureConfig      `json:"features"`
	Streamers     []StreamerConfig   `json:"streamers"`
	Storage       StorageConfig      `json:"storage"`
	Logging       LoggingConfig      `json:"logging"`
	Notifications NotificationConfig `json:"notifications"`
	Network       NetworkConfig      `json:"network"`
}

type AccountConfig struct {
	Username string `json:"username"`
}

type AuthConfig struct {
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

type WatchConfig struct {
	TickSeconds           int      `json:"tick_seconds"`
	PriorityGames         []string `json:"priority_games"`
	FallbackAllCampaigns  bool     `json:"fallback_all_campaigns"`
	AutoStartCampaigns    bool     `json:"auto_start_campaigns"`
	IgnoredGames          []string `json:"ignored_games"`
	AuxiliaryWatch        bool     `json:"auxiliary_watch"`
	AuxiliaryLeaseMinutes int      `json:"auxiliary_lease_minutes"`
}

type FeatureConfig struct {
	ClaimBonuses *bool `json:"claim_bonuses"`
	ClaimDrops   *bool `json:"claim_drops"`
	DryRun       *bool `json:"dry_run"`
	Chat         *bool `json:"chat"`
}

func (f FeatureConfig) ClaimBonusesEnabled() bool { return boolDefault(f.ClaimBonuses, true) }
func (f FeatureConfig) ClaimDropsEnabled() bool   { return boolDefault(f.ClaimDrops, false) }
func (f FeatureConfig) DryRunEnabled() bool       { return boolDefault(f.DryRun, false) }
func (f FeatureConfig) ChatEnabled() bool         { return boolDefault(f.Chat, false) }

type StreamerConfig struct {
	Login      string `json:"login"`
	ClaimDrops *bool  `json:"claim_drops,omitempty"`
	Chat       *bool  `json:"chat,omitempty"`
}

type StorageConfig struct {
	Path string `json:"path"`
}

type NetworkConfig struct {
	// ProxyURL, when set, routes all Twitch traffic (GQL, Helix, auth,
	// playback telemetry, and IRC chat) through a SOCKS5 proxy, e.g.
	// "socks5://warp:1080". Empty means direct connection. The Discord
	// webhook notifier is never proxied.
	ProxyURL string `json:"proxy_url"`
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
	File   string `json:"file"`
}

type NotificationConfig struct {
	Discord DiscordConfig `json:"discord"`
	Webhook WebhookConfig `json:"webhook"`
}

type DiscordConfig struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
}

type WebhookConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Method  string `json:"method"`
}

func Bool(value bool) *bool {
	return &value
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
