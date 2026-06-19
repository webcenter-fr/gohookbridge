package store

import (
	"encoding/json"
	"regexp"

	"github.com/go-playground/validator/v10"
)

type Channel struct {
	ID                string   `json:"id" validate:"required,min=1,max=64,channelid"`
	Description       string   `json:"description,omitempty" validate:"max=500"`
	WebhookSecret     string   `json:"webhook_secret,omitempty"`
	AllowedIPs        []string `json:"allowed_ips,omitempty"`
	MaxBodySize       int      `json:"max_body_size,omitempty"`
	MessageTTLSeconds int      `json:"message_ttl_seconds,omitempty"`
	EncryptionMode        string   `json:"encryption_mode,omitempty"`
	EncryptionKey         string   `json:"encryption_key,omitempty"`
	EncryptionPublicKey   string   `json:"encryption_public_key,omitempty"`
	EncryptionPrivateKey  string   `json:"encryption_private_key,omitempty"`
	EncryptionPubKeys     []string `json:"encryption_public_keys,omitempty"`

	// Deprecated: use WebhookSecret instead
	WebhookSignatures []string `json:"webhook_signatures,omitempty"`
	// Deprecated: use EncryptionMode instead
	EncryptionEnabled bool `json:"encryption_enabled,omitempty"`
}

var validate *validator.Validate

func init() {
	validate = validator.New()
	validate.RegisterValidation("channelid", func(fl validator.FieldLevel) bool {
		return regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`).MatchString(fl.Field().String())
	})
}

type GlobalConfig struct {
	Server   ServerConfig          `json:"server"`
	Defaults DefaultChannelConfig  `json:"defaults"`
}

type ServerConfig struct {
	MaxBodySize   int    `json:"max_body_size"`
	BehindReverseProxy    bool   `json:"behind_reverse_proxy"`
	CORSOrigin    string `json:"cors_origin"`
	Footer        string `json:"footer"`
	SessionSecret string `json:"session_secret"`
}

type GlobalConfigResponse struct {
	Server   ServerConfigResponse `json:"server"`
	Defaults DefaultChannelConfig `json:"defaults"`
}

type ServerConfigResponse struct {
	MaxBodySize   int    `json:"max_body_size"`
	BehindReverseProxy    bool   `json:"behind_reverse_proxy"`
	CORSOrigin    string `json:"cors_origin"`
	Footer        string `json:"footer"`
	SessionSecret string `json:"session_secret"`
}

type DefaultChannelConfig struct {
	WebhookSecret     string   `json:"webhook_secret"`
	AllowedIPs        []string `json:"allowed_ips"`
	MessageTTLSeconds int      `json:"message_ttl_seconds"`
}

type User struct {
	ID           string   `json:"id" validate:"required,min=1,max=128"`
	Username     string   `json:"username" validate:"required,min=1,max=128"`
	PasswordHash string   `json:"password_hash,omitempty"`
	OIDCSubjects []string `json:"oidc_subjects,omitempty"`
	Roles        []string `json:"roles"`
	Channels     []string `json:"channels"`
}

type Role struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type Permission string

const (
	PermAll          Permission = "*"
	PermGlobalRead   Permission = "global:read"
	PermGlobalWrite  Permission = "global:write"
	PermUsersRead    Permission = "users:read"
	PermUsersWrite   Permission = "users:write"
	PermRBACRead     Permission = "rbac:read"
	PermRBACWrite    Permission = "rbac:write"
	PermChannelRead  Permission = "channel:read"
	PermChannelWrite Permission = "channel:write"
	PermChannelView  Permission = "channel:view"
)

var DefaultRoles = []Role{
	{Name: "admin", Permissions: []string{"*"}},
	{Name: "channel_admin", Permissions: []string{"channel:write", "channel:read"}},
	{Name: "channel_viewer", Permissions: []string{"channel:read"}},
}

type OIDCProvider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	IssuerURL    string   `json:"issuer_url"`
	Scopes       []string `json:"scopes"`
}

type ClientCursor struct {
	Channel         string `json:"channel"`
	ClientID        string `json:"client_id"`
	LastTimestampMs int64  `json:"last_timestamp_ms"`
}

type UserBinding struct {
	UserID   string   `json:"user_id" validate:"required"`
	Roles    []string `json:"roles"`
	Channels []string `json:"channels"`
}

func defaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Server: ServerConfig{
			MaxBodySize: 26214400,
			BehindReverseProxy:  false,
			CORSOrigin:  "*",
		},
		Defaults: DefaultChannelConfig{},
	}
}

func resolveChannelConfig(p *Channel, global *GlobalConfig) *Channel {
	resolved := *p
	migrateChannel(&resolved)
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

func migrateChannel(p *Channel) {
	if p.WebhookSecret == "" && len(p.WebhookSignatures) > 0 {
		p.WebhookSecret = p.WebhookSignatures[0]
	}
	if p.EncryptionMode == "" && p.EncryptionEnabled {
		p.EncryptionMode = "e2e"
	}
	if p.EncryptionMode == "provider_side" {
		p.EncryptionMode = "e2e"
	}
}

func MigrateChannelForTest(p *Channel) {
	migrateChannel(p)
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}