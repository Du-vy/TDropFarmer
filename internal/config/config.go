package config

type Config struct {
	Account       AccountConfig      `json:"account"`
	Auth          AuthConfig         `json:"auth"`
	Watch         WatchConfig        `json:"watch"`
	Features      FeatureConfig      `json:"features"`
	Predictions   PredictionConfig   `json:"predictions"`
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
	MaxChannels int      `json:"max_channels"`
	Priorities  []string `json:"priorities"`
	TickSeconds int      `json:"tick_seconds"`
}

type FeatureConfig struct {
	ClaimBonuses *bool `json:"claim_bonuses"`
	ClaimDrops   *bool `json:"claim_drops"`
	FollowRaids  *bool `json:"follow_raids"`
	Predictions  *bool `json:"predictions"`
	DryRun       *bool `json:"dry_run"`
	Chat         *bool `json:"chat"`
}

func (f FeatureConfig) ClaimBonusesEnabled() bool { return boolDefault(f.ClaimBonuses, true) }
func (f FeatureConfig) ClaimDropsEnabled() bool   { return boolDefault(f.ClaimDrops, false) }
func (f FeatureConfig) FollowRaidsEnabled() bool  { return boolDefault(f.FollowRaids, false) }
func (f FeatureConfig) PredictionsEnabled() bool  { return boolDefault(f.Predictions, false) }
func (f FeatureConfig) DryRunEnabled() bool       { return boolDefault(f.DryRun, false) }
func (f FeatureConfig) ChatEnabled() bool         { return boolDefault(f.Chat, false) }

type PredictionConfig struct {
	Strategy        string           `json:"strategy"`
	Percentage      int              `json:"percentage"`
	PercentageGap   int              `json:"percentage_gap"`
	MaxPoints       int              `json:"max_points"`
	MinimumPoints   int              `json:"minimum_points"`
	DelayMode       string           `json:"delay_mode"`
	DelaySeconds    int              `json:"delay_seconds"`
	StealthMode     bool             `json:"stealth_mode"`
	FilterCondition *FilterCondition `json:"filter_condition"`
}

type FilterCondition struct {
	By    string  `json:"by"`
	Where string  `json:"where"`
	Value float64 `json:"value"`
}

type StreamerConfig struct {
	Login              string            `json:"login"`
	ClaimDrops         *bool             `json:"claim_drops,omitempty"`
	FollowRaids        *bool             `json:"follow_raids,omitempty"`
	Predictions        *bool             `json:"predictions,omitempty"`
	Chat               *bool             `json:"chat,omitempty"`
	PredictionSettings *PredictionConfig `json:"prediction_settings,omitempty"`
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
