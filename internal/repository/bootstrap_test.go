package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
)

func TestLoadBootstrap_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.yaml")
	content := `global:
  server:
    max_body_size: 100
    behind_reverse_proxy: true
    session_secret: "0123456789abcdef0123456789abcdef"
  defaults:
    webhook_secret: global-secret
    allowed_ips: ["10.0.0.0/8"]
    message_ttl_seconds: 3600
users:
  - username: alice
    password: secret123
    roles: [admin]
    channels: ["*"]
channels:
  - id: proj1
`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	cfg, err := LoadBootstrap(path)
	assert.NilError(t, err)

	assert.Assert(t, cfg.Global != nil)
	assert.Equal(t, cfg.Global.Server.MaxBodySize, 100)
	assert.Assert(t, cfg.Global.Server.BehindReverseProxy)
	assert.Equal(t, cfg.Global.Server.SessionSecret, "0123456789abcdef0123456789abcdef")
	assert.Equal(t, cfg.Global.Defaults.WebhookSecret, "global-secret")
	assert.DeepEqual(t, cfg.Global.Defaults.AllowedIPs, []string{"10.0.0.0/8"})
	assert.Equal(t, cfg.Global.Defaults.MessageTTLSeconds, 3600)

	assert.Equal(t, len(cfg.Users), 1)
	assert.Equal(t, cfg.Users[0].Username, "alice")
	assert.Equal(t, cfg.Users[0].Password, "secret123")
	assert.DeepEqual(t, cfg.Users[0].Roles, []string{"admin"})
	assert.DeepEqual(t, cfg.Users[0].Channels, []string{"*"})

	assert.Equal(t, len(cfg.Channels), 1)
	assert.Equal(t, cfg.Channels[0].ID, "proj1")
}

func TestLoadBootstrap_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.json")
	content := `{
		"global": {
			"server": {
				"max_body_size": 200,
				"cors_origin": "https://example.com"
			}
		},
		"users": [
			{"username": "bob", "password": "pass", "roles": ["channel_viewer"], "channels": ["proj1"]}
		],
		"channels": [
			{"id": "proj1"}
		]
	}`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	cfg, err := LoadBootstrap(path)
	assert.NilError(t, err)

	assert.Assert(t, cfg.Global != nil)
	assert.Equal(t, cfg.Global.Server.MaxBodySize, 200)
	assert.Equal(t, cfg.Global.Server.CORSOrigin, "https://example.com")

	assert.Equal(t, len(cfg.Users), 1)
	assert.Equal(t, cfg.Users[0].Username, "bob")

	assert.Equal(t, len(cfg.Channels), 1)
	assert.Equal(t, cfg.Channels[0].ID, "proj1")
}

func TestLoadBootstrap_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	content := `{{{{{invalid yaml`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	_, err = LoadBootstrap(path)
	assert.ErrorContains(t, err, "parse bootstrap")
}

func TestLoadBootstrap_FileNotFound(t *testing.T) {
	_, err := LoadBootstrap("/nonexistent/path/bootstrap.yaml")
	assert.ErrorContains(t, err, "read bootstrap file")
}

func TestApplyBootstrap_GlobalConfig(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Global: &domain.GlobalConfig{
			Server: domain.ServerConfig{
				MaxBodySize:        500,
				CORSOrigin:         "https://app.example.com",
				BehindReverseProxy: true,
			},
		},
	}

	err := rs.ApplyBootstrap(context.Background(), cfg)
	assert.NilError(t, err)

	got, err := rs.GetGlobalConfig(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, got.Server.MaxBodySize, 500)
	assert.Equal(t, got.Server.CORSOrigin, "https://app.example.com")
	assert.Assert(t, got.Server.BehindReverseProxy)
}

func TestApplyBootstrap_Users_PasswordHashed(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Users: []BootstrapUser{
			{
				Username: "alice",
				Password: "my-secret-password",
				Roles:    []string{"admin"},
				Channels: []string{"*"},
			},
		},
	}

	err := rs.ApplyBootstrap(context.Background(), cfg)
	assert.NilError(t, err)

	user, err := rs.GetUserByUsername(context.Background(), "alice")
	assert.NilError(t, err)
	assert.Equal(t, user.Username, "alice")
	assert.Assert(t, user.PasswordHash != "")
	assert.Assert(t, user.PasswordHash != "my-secret-password")

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("my-secret-password"))
	assert.NilError(t, err)

	assert.DeepEqual(t, user.Roles, []string{"admin"})
	assert.DeepEqual(t, user.Channels, []string{"*"})
}

func TestApplyBootstrap_Projects(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Channels: []BootstrapChannel{
			{ID: "proj-a"},
			{ID: "proj-b"},
		},
	}

	err := rs.ApplyBootstrap(context.Background(), cfg)
	assert.NilError(t, err)

	channels, err := rs.ListChannels(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(channels), 2)

	gotA, err := rs.GetChannel(context.Background(), "proj-a")
	assert.NilError(t, err)
	assert.Equal(t, gotA.ID, "proj-a")

	gotB, err := rs.GetChannel(context.Background(), "proj-b")
	assert.NilError(t, err)
	assert.Equal(t, gotB.ID, "proj-b")
}

func TestApplyBootstrap_DoubleApplication(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Channels: []BootstrapChannel{
			{ID: "proj1"},
		},
	}

	err := rs.ApplyBootstrap(context.Background(), cfg)
	assert.NilError(t, err)

	err = rs.ApplyBootstrap(context.Background(), &BootstrapConfig{})
	assert.ErrorContains(t, err, "FSM already has data")
}

func TestApplyBootstrap_TokenChannel(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Channels: []BootstrapChannel{
			{
				ID:         "token-channel",
				AccessMode: "token",
				AccessTokens: []BootstrapAccessToken{
					{ID: "tok-1", Name: "produce", Token: "raw-secret", Scope: "produce"},
				},
			},
		},
	}

	err := rs.ApplyBootstrap(context.Background(), cfg)
	assert.NilError(t, err)

	ch, err := rs.GetChannel(context.Background(), "token-channel")
	assert.NilError(t, err)
	assert.Equal(t, ch.AccessMode, "token")
	assert.Equal(t, len(ch.AccessTokens), 1)
	assert.Equal(t, ch.AccessTokens[0].ID, "tok-1")
	assert.Equal(t, ch.AccessTokens[0].Scope, "produce")
	// Plaintext is hashed, never persisted in the clear.
	assert.Equal(t, ch.AccessTokens[0].TokenHash, hashBootstrapToken("raw-secret"))
	assert.Assert(t, ch.AccessTokens[0].TokenHash != "raw-secret")
}

func TestLoadBootstrap_TokenChannelYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.yaml")
	content := `channels:
  - id: proj1
    access_mode: token
    access_tokens:
      - id: tok-1
        name: produce
        token: raw-secret
        scope: produce
`
	err := os.WriteFile(path, []byte(content), 0644)
	assert.NilError(t, err)

	cfg, err := LoadBootstrap(path)
	assert.NilError(t, err)
	assert.Equal(t, len(cfg.Channels), 1)
	assert.Equal(t, cfg.Channels[0].AccessMode, "token")
	assert.Equal(t, len(cfg.Channels[0].AccessTokens), 1)
	assert.Equal(t, cfg.Channels[0].AccessTokens[0].Token, "raw-secret")
	assert.Equal(t, cfg.Channels[0].AccessTokens[0].Scope, "produce")
}

func TestApplyBootstrap_TokenScopeDefaultsToBoth(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg := &BootstrapConfig{
		Channels: []BootstrapChannel{
			{
				ID:         "scopeless-channel",
				AccessMode: "token",
				AccessTokens: []BootstrapAccessToken{
					// No scope: must behave like service.CreateAccessToken,
					// which defaults any unknown scope to "both" — otherwise
					// ValidateChannelToken silently rejects the token.
					{ID: "tok-1", Name: "no-scope", Token: "raw-secret"},
				},
			},
		},
	}

	err := rs.ApplyBootstrap(context.Background(), cfg)
	assert.NilError(t, err)

	ch, err := rs.GetChannel(context.Background(), "scopeless-channel")
	assert.NilError(t, err)
	assert.Equal(t, len(ch.AccessTokens), 1)
	assert.Equal(t, ch.AccessTokens[0].Scope, "both")
}
