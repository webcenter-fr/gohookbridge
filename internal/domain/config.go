package domain

type GlobalConfig struct {
	Server   ServerConfig         `json:"server"   yaml:"server"`
	Defaults DefaultChannelConfig `json:"defaults" yaml:"defaults"`
}

// ServerConfig holds the global server settings. The yaml tags mirror the
// JSON ones so that bootstrap.yaml files use the same snake_case keys as the
// API and the documentation (yaml.v3 otherwise lowercases the Go field name,
// which silently produced a zero-valued configuration).
type ServerConfig struct {
	MaxBodySize        int    `json:"max_body_size"        yaml:"max_body_size"`
	BehindReverseProxy bool   `json:"behind_reverse_proxy" yaml:"behind_reverse_proxy"`
	CORSOrigin         string `json:"cors_origin"          yaml:"cors_origin"`
	Footer             string `json:"footer"               yaml:"footer"`
	SessionSecret      string `json:"session_secret"       yaml:"session_secret"`

	RateLimitEnabled       bool `json:"rate_limit_enabled"        yaml:"rate_limit_enabled"`
	RateLimitRequests      int  `json:"rate_limit_requests"       yaml:"rate_limit_requests"`
	RateLimitWindowSeconds int  `json:"rate_limit_window_seconds" yaml:"rate_limit_window_seconds"`

	BanEnabled           bool `json:"ban_enabled"             yaml:"ban_enabled"`
	BanMaxUniqueFailures int  `json:"ban_max_unique_failures" yaml:"ban_max_unique_failures"`
	BanWindowSeconds     int  `json:"ban_window_seconds"      yaml:"ban_window_seconds"`
	BanDurationSeconds   int  `json:"ban_duration_seconds"    yaml:"ban_duration_seconds"`
}

type DefaultChannelConfig struct {
	WebhookSecret     string   `json:"webhook_secret"      yaml:"webhook_secret"`
	AllowedIPs        []string `json:"allowed_ips"         yaml:"allowed_ips"`
	MessageTTLSeconds int      `json:"message_ttl_seconds" yaml:"message_ttl_seconds"`
}

// DefaultGlobalConfig returns the built-in defaults applied when the Raft FSM
// holds no global configuration yet.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Server: ServerConfig{
			MaxBodySize:        26214400,
			BehindReverseProxy: false,
			CORSOrigin:         "*",

			RateLimitEnabled:       false,
			RateLimitRequests:      100,
			RateLimitWindowSeconds: 60,

			BanEnabled:           false,
			BanMaxUniqueFailures: 5,
			BanWindowSeconds:     300,
			BanDurationSeconds:   3600,
		},
		Defaults: DefaultChannelConfig{},
	}
}

// ResolveChannelConfig merges a channel with the global defaults: it applies
// legacy migrations and fills unset channel fields from the global config.
func ResolveChannelConfig(p *Channel, global *GlobalConfig) *Channel {
	resolved := *p
	MigrateChannel(&resolved)
	if resolved.AccessMode == "" {
		resolved.AccessMode = "public"
	}
	if resolved.MaxBodySize == 0 {
		resolved.MaxBodySize = global.Server.MaxBodySize
	}
	if resolved.WebhookSecret == "" {
		resolved.WebhookSecret = global.Defaults.WebhookSecret
	}
	if len(resolved.AllowedIPs) == 0 {
		resolved.AllowedIPs = global.Defaults.AllowedIPs
	}
	if resolved.MessageTTLSeconds == 0 && global.Defaults.MessageTTLSeconds > 0 {
		resolved.MessageTTLSeconds = global.Defaults.MessageTTLSeconds
	}
	return &resolved
}
