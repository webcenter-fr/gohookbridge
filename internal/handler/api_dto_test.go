package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

// F08: omitted fields are preserved (pointer semantics), not zeroed.
func TestChannelUpdatePreservesOmittedFields(t *testing.T) {
	svc, router := setupAPI(t)
	ctx := context.Background()

	ch := &domain.Channel{
		ID:                "preserve",
		Description:       "original",
		WebhookSecret:     "webhook-secret-value",
		AllowedIPs:        []string{"10.0.0.0/8"},
		MaxBodySize:       4242,
		MessageTTLSeconds: 111,
	}
	assert.NilError(t, svc.CreateChannel(ctx, ch))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/channels/preserve", map[string]any{"description": "updated"}))
	assert.Equal(t, w.Code, http.StatusOK, "body: %s", w.Body.String())

	got, err := svc.GetChannel(ctx, "preserve")
	assert.NilError(t, err)
	assert.Equal(t, got.Description, "updated")
	assert.Equal(t, got.WebhookSecret, "webhook-secret-value")
	assert.Equal(t, got.MaxBodySize, 4242)
	assert.Equal(t, got.MessageTTLSeconds, 111)
	assert.Equal(t, len(got.AllowedIPs), 1)
}

func TestChannelUpdateRejectsInvalidMode(t *testing.T) {
	_, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/channels/whatever", map[string]any{"encryption_mode": "bogus"}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

// F08: a global update with only one server field must not erase defaults.
func TestGlobalUpdatePreservesDefaults(t *testing.T) {
	svc, router := setupAPI(t)
	ctx := context.Background()

	cfg, err := svc.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	cfg.Defaults.WebhookSecret = "default-secret"
	cfg.Defaults.AllowedIPs = []string{"192.168.0.0/16"}
	cfg.Defaults.MessageTTLSeconds = 999
	cfg.Server.Footer = "keep-me"
	assert.NilError(t, svc.UpdateGlobalConfig(ctx, cfg))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/global", map[string]any{
		"server": map[string]any{"max_body_size": 1000},
	}))
	assert.Equal(t, w.Code, http.StatusOK, "body: %s", w.Body.String())

	got, err := svc.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	assert.Equal(t, got.Server.MaxBodySize, 1000)
	assert.Equal(t, got.Server.Footer, "keep-me")
	assert.Equal(t, got.Defaults.WebhookSecret, "default-secret")
	assert.Equal(t, got.Defaults.MessageTTLSeconds, 999)
	assert.Equal(t, len(got.Defaults.AllowedIPs), 1)
}

// F09: read APIs never disclose secret material.
func TestChannelResponsesRedactSecrets(t *testing.T) {
	svc, router := setupAPI(t)
	ctx := context.Background()

	ch := &domain.Channel{
		ID:            "redact",
		WebhookSecret: "super-secret-webhook",
		EncryptionKey: "super-secret-aes-key",
		AccessTokens: []domain.ChannelAccessToken{
			{ID: "t1", Name: "cli", TokenHash: "hashed-token-value", Scope: "both"},
		},
	}
	assert.NilError(t, svc.CreateChannel(ctx, ch))

	for _, path := range []string{"/channels/redact", "/channels"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("GET", path, nil))
		assert.Equal(t, w.Code, http.StatusOK, "body: %s", w.Body.String())
		body := w.Body.String()
		assert.Assert(t, !strings.Contains(body, "super-secret-webhook"), "%s leaked webhook secret", path)
		assert.Assert(t, !strings.Contains(body, "super-secret-aes-key"), "%s leaked encryption key", path)
		assert.Assert(t, !strings.Contains(body, "hashed-token-value"), "%s leaked token hash", path)
		assert.Assert(t, strings.Contains(body, `"webhook_secret_configured":true`), "%s missing configured flag", path)
		assert.Assert(t, strings.Contains(body, `"encryption_key_configured":true`), "%s missing configured flag", path)
	}
}

func TestGlobalResponseRedactsDefaultSecret(t *testing.T) {
	svc, router := setupAPI(t)
	ctx := context.Background()

	cfg, err := svc.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	cfg.Defaults.WebhookSecret = "default-global-secret"
	assert.NilError(t, svc.UpdateGlobalConfig(ctx, cfg))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("GET", "/global", nil))
	assert.Equal(t, w.Code, http.StatusOK)

	body := w.Body.String()
	assert.Assert(t, !strings.Contains(body, "default-global-secret"), "global response leaked default secret")
	assert.Assert(t, strings.Contains(body, `"webhook_secret_configured":true`))
}
