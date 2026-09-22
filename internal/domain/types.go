package domain

import (
	"regexp"

	"github.com/go-playground/validator/v10"
)

type ChannelAccessToken struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"token_hash"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
}

type Channel struct {
	ID                   string               `json:"id"                               validate:"required,min=1,max=64,channelid"`
	Description          string               `json:"description,omitempty"            validate:"max=500"`
	CreatedBy            string               `json:"created_by,omitempty"`
	WebhookSecret        string               `json:"webhook_secret,omitempty"`
	AllowedIPs           []string             `json:"allowed_ips,omitempty"`
	MaxBodySize          int                  `json:"max_body_size,omitempty"`
	MessageTTLSeconds    int                  `json:"message_ttl_seconds,omitempty"`
	EncryptionMode       string               `json:"encryption_mode,omitempty"`
	EncryptionKey        string               `json:"encryption_key,omitempty"`
	EncryptionPublicKey  string               `json:"encryption_public_key,omitempty"`
	EncryptionPrivateKey string               `json:"encryption_private_key,omitempty"`
	EncryptionPubKeys    []string             `json:"encryption_public_keys,omitempty"`
	AccessMode           string               `json:"access_mode,omitempty"`
	AccessTokens         []ChannelAccessToken `json:"access_tokens,omitempty"`

	// Deprecated: use WebhookSecret instead
	WebhookSignatures []string `json:"webhook_signatures,omitempty"`
	// Deprecated: use EncryptionMode instead
	EncryptionEnabled bool `json:"encryption_enabled,omitempty"`
}

var validate *validator.Validate

//nolint:gochecknoinits
func init() {
	validate = validator.New()
	_ = validate.RegisterValidation("channelid", func(fl validator.FieldLevel) bool {
		return regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`).MatchString(fl.Field().String())
	})
}

type User struct {
	ID           string   `json:"id"                      validate:"required,min=1,max=128"`
	Username     string   `json:"username"                validate:"required,min=1,max=128"`
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
	GroupsClaim  string   `json:"groups_claim"`
}

type RoleMapping struct {
	ID           string `json:"id"`
	Type         string `json:"type"`          // "user" or "group"
	Subject      string `json:"subject"`       // user_id or group_name
	Role         string `json:"role"`          // role name
	ChannelScope string `json:"channel_scope"` // "*" or specific channel ID
}

type ChannelRoleMapping struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Type      string `json:"type"`    // "user" or "group"
	Subject   string `json:"subject"` // user_id or group_name
	Role      string `json:"role"`    // "owner", "write", or "read"
}

type ClientCursor struct {
	Channel         string `json:"channel"`
	ClientID        string `json:"client_id"`
	LastTimestampMs int64  `json:"last_timestamp_ms"`
}

type UserBinding struct {
	UserID   string   `json:"user_id"  validate:"required"`
	Roles    []string `json:"roles"`
	Channels []string `json:"channels"`
}

// MigrateChannel applies in-place migrations to a channel that was persisted
// with a legacy schema: it promotes the deprecated WebhookSignatures field to
// WebhookSecret, maps the deprecated EncryptionEnabled flag to the e2e
// encryption mode, and renames the legacy "provider_side" mode to "e2e".
func MigrateChannel(p *Channel) {
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
