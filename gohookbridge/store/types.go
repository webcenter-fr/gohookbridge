package store

import "encoding/json"

type Project struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	WebhookSignatures []string `json:"webhook_signatures,omitempty"`
	AllowedIPs        []string `json:"allowed_ips,omitempty"`
	MaxBodySize       int      `json:"max_body_size,omitempty"`
	ReplayToken       string   `json:"replay_token,omitempty"`
	EncryptionEnabled bool     `json:"encryption_enabled,omitempty"`
	EncryptionPubKeys []string `json:"encryption_public_keys,omitempty"`
}

type GlobalConfig struct {
	Server   ServerConfig          `json:"server"`
	Defaults DefaultProjectConfig  `json:"defaults"`
}

type ServerConfig struct {
	MaxBodySize   int    `json:"max_body_size"`
	TrustProxy    bool   `json:"trust_proxy"`
	CORSOrigin    string `json:"cors_origin"`
	Footer        string `json:"footer"`
	SessionSecret string `json:"session_secret"`
}

type GlobalConfigResponse struct {
	Server   ServerConfigResponse `json:"server"`
	Defaults DefaultProjectConfig `json:"defaults"`
}

type ServerConfigResponse struct {
	MaxBodySize   int    `json:"max_body_size"`
	TrustProxy    bool   `json:"trust_proxy"`
	CORSOrigin    string `json:"cors_origin"`
	Footer        string `json:"footer"`
	SessionSecret string `json:"session_secret"`
}

type DefaultProjectConfig struct {
	WebhookSignatures []string `json:"webhook_signatures"`
	AllowedIPs        []string `json:"allowed_ips"`
	ReplayToken       string   `json:"replay_token"`
}

type User struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	PasswordHash string   `json:"password_hash,omitempty"`
	OIDCSubjects []string `json:"oidc_subjects,omitempty"`
	Roles        []string `json:"roles"`
	Projects     []string `json:"projects"`
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
	PermProjectRead  Permission = "project:read"
	PermProjectWrite Permission = "project:write"
	PermProjectView  Permission = "project:view"
)

var DefaultRoles = []Role{
	{Name: "admin", Permissions: []string{"*"}},
	{Name: "project_admin", Permissions: []string{"project:write", "project:read"}},
	{Name: "project_viewer", Permissions: []string{"project:read"}},
}

type OIDCProvider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	IssuerURL    string   `json:"issuer_url"`
	Scopes       []string `json:"scopes"`
}

type UserBinding struct {
	UserID   string   `json:"user_id"`
	Roles    []string `json:"roles"`
	Projects []string `json:"projects"`
}

func defaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Server: ServerConfig{
			MaxBodySize: 26214400,
			TrustProxy:  false,
			CORSOrigin:  "*",
		},
		Defaults: DefaultProjectConfig{},
	}
}

func resolveProjectConfig(p *Project, global *GlobalConfig) *Project {
	resolved := *p
	if resolved.MaxBodySize == 0 {
		resolved.MaxBodySize = global.Server.MaxBodySize
	}
	if len(resolved.WebhookSignatures) == 0 {
		resolved.WebhookSignatures = global.Defaults.WebhookSignatures
	}
	if len(resolved.AllowedIPs) == 0 {
		resolved.AllowedIPs = global.Defaults.AllowedIPs
	}
	if resolved.ReplayToken == "" {
		resolved.ReplayToken = global.Defaults.ReplayToken
	}
	return &resolved
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}