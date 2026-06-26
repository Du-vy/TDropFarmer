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
}

type AccountConfig struct {
	Username string `json:"username"`
}

type AuthConfig struct {
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

type WatchConfig struct {
	MaxChannels  int      `json:"max_channels"`
	Priorities   []string `json:"priorities"`
	TickSeconds  int      `json:"tick_seconds"`
	Games        []string `json:"games"`
	AllCampaigns bool     `json:"all_campaigns"`
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
