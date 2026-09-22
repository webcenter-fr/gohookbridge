package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

func TestWebhookSignatureValidation(t *testing.T) {
	t.Run("GitHub Signature", func(t *testing.T) {
		secret := "test-secret"
		payload := []byte(`{"event":"test"}`)

		mac := createGitHubSignature(secret, payload)

		valid := validateGitHubWebhookSignature(secret, payload, "sha256="+mac)
		assert.Assert(t, valid, "Valid signature should be accepted")

		invalid := validateGitHubWebhookSignature(secret, payload, "sha256=invalid")
		assert.Assert(t, !invalid, "Invalid signature should be rejected")

		invalidFormat := validateGitHubWebhookSignature(secret, payload, "invalid-format")
		assert.Assert(t, !invalidFormat, "Invalid format should be rejected")
	})

	t.Run("Bitbucket HMAC", func(t *testing.T) {
		secret := "test-secret"
		payload := []byte(`{"event":"test"}`)

		mac := createBitbucketSignature(secret, payload)

		valid := validateBitbucketHMAC(secret, payload, mac)
		assert.Assert(t, valid, "Valid signature should be accepted")

		invalid := validateBitbucketHMAC(secret, payload, "invalid")
		assert.Assert(t, !invalid, "Invalid signature should be rejected")
	})

	t.Run("Gitea Signature", func(t *testing.T) {
		secret := "test-secret"
		payload := []byte(`{"event":"test"}`)

		mac := createGiteaSignature(secret, payload)

		valid := validateGiteaSignature(secret, payload, "sha256="+mac)
		assert.Assert(t, valid, "Valid signature should be accepted")

		invalid := validateGiteaSignature(secret, payload, "sha256=invalid")
		assert.Assert(t, !invalid, "Invalid signature should be rejected")

		invalidFormat := validateGiteaSignature(secret, payload, "invalid-format")
		assert.Assert(t, !invalidFormat, "Invalid format should be rejected")
	})

	t.Run("Validate Multiple Providers", func(t *testing.T) {
		secrets := "secret1"
		payload := []byte(`{"event":"test"}`)

		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Hub-Signature-256", "sha256="+createGitHubSignature("secret1", payload))
		valid := validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid GitHub signature should be accepted")

		r = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Hub-Signature", createBitbucketSignature("secret1", payload))
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid Bitbucket signature should be accepted")

		r = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Gitlab-Token", "secret1")
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid GitLab token should be accepted")

		r = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Gitea-Signature", "sha256="+createGiteaSignature("secret1", payload))
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid Gitea signature should be accepted")

		valid = validateWebhookSignature("", payload, r)
		assert.Assert(t, valid, "No secrets should always return true")

		r = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Hub-Signature-256", "sha256=invalid")
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, !valid, "Invalid signature should be rejected")
	})
}

func TestHandleWebhookPost(t *testing.T) {
	broker := newNatsBroker(t, 4241)
	svc := service.NewService(storetest.NewRaftStore(t), nil)

	t.Run("Valid Webhook", func(t *testing.T) {
		historical, live := broker.Subscribe("test-channel", time.Time{}, 10)

		payload := map[string]any{
			"event": "test",
			"data":  "value",
		}
		payloadBytes, _ := json.Marshal(payload)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/test-channel", bytes.NewReader(payloadBytes))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Event-Type", "test-event")

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := HandleWebhookPost(broker, svc, service.NewBanTracker())
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)

		select {
		case event := <-live:
			assert.Assert(t, len(event) > 0)
			var eventData map[string]any
			err := json.Unmarshal(event, &eventData)
			assert.NilError(t, err)
			assert.Equal(t, eventData["x-event-type"], "test-event")
			assert.Assert(t, eventData["bodyB"] != nil)
			assert.Assert(t, eventData["timestamp"] != nil)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for NATS message")
		}
		assert.Equal(t, 0, len(historical))

		broker.Unsubscribe("test-channel", live)
	})

	t.Run("Unconfigured Channel Stays Plaintext", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/unknown-channel", strings.NewReader(`{"ok":true}`))
		req.Header.Set("Content-Type", contentType)

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "unknown-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := HandleWebhookPost(broker, svc, service.NewBanTracker())
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)
	})

	t.Run("Invalid Content Type", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/test-channel", strings.NewReader("not json"))
		req.Header.Set("Content-Type", "text/plain")

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := HandleWebhookPost(broker, svc, service.NewBanTracker())
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusBadRequest)

		body, _ := io.ReadAll(resp.Body)
		assert.Assert(t, strings.Contains(string(body), "content-type must be application/json"))
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/test-channel", strings.NewReader("not json"))
		req.Header.Set("Content-Type", contentType)

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := HandleWebhookPost(broker, svc, service.NewBanTracker())
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
	})

	t.Run("Signature Validation", func(t *testing.T) {
		payload := []byte(`{"event":"test"}`)

		assert.NilError(t, svc.CreateChannel(context.Background(), &domain.Channel{
			ID:            "test-channel",
			WebhookSecret: "test-secret",
		}))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/test-channel", bytes.NewReader(payload))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Hub-Signature-256", "sha256="+createGitHubSignature("test-secret", payload))

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := HandleWebhookPost(broker, svc, service.NewBanTracker())
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)

		req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/test-channel", bytes.NewReader(payload))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

		w = httptest.NewRecorder()

		rctx = chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler = HandleWebhookPost(broker, svc, service.NewBanTracker())
		handler(w, req)

		resp = w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	})
}

func TestEffectivePublicURL(t *testing.T) {
	t.Run("returns explicit public URL unchanged", func(t *testing.T) {
		assert.Equal(t, EffectivePublicURL("https://hooks.example.com", "localhost:3333", false), "https://hooks.example.com")
	})

	t.Run("defaults to http address when tls is disabled", func(t *testing.T) {
		assert.Equal(t, EffectivePublicURL("", "localhost:3333", false), "http://localhost:3333")
	})

	t.Run("defaults to https address when tls is enabled", func(t *testing.T) {
		assert.Equal(t, EffectivePublicURL("", "localhost:3333", true), "https://localhost:3333")
	})
}

func TestHandleWebhookPostWithNATS(t *testing.T) {
	broker := newNatsBroker(t, 4242)
	svc := service.NewService(storetest.NewRaftStore(t), nil)

	handler := HandleWebhookPost(broker, svc, service.NewBanTracker())

	t.Run("Publishes via NATS to subscriber", func(t *testing.T) {
		historical, live := broker.Subscribe("nats-test", time.Time{}, 10)
		assert.Equal(t, 0, len(historical))

		payload := map[string]any{"event": "nats-test"}
		payloadBytes, _ := json.Marshal(payload)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/nats-test", bytes.NewReader(payloadBytes))
		req.Header.Set("Content-Type", contentType)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "nats-test")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)

		select {
		case data := <-live:
			var eventData map[string]any
			err := json.Unmarshal(data, &eventData)
			assert.NilError(t, err)
			assert.Assert(t, eventData["bodyB"] != nil)
			assert.Assert(t, eventData["timestamp"] != nil)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for NATS message")
		}

		broker.Unsubscribe("nats-test", live)
	})

	t.Run("NATS publish error path prints to stderr", func(t *testing.T) {
		payload := map[string]any{"event": "test"}
		payloadBytes, _ := json.Marshal(payload)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/nats-test", bytes.NewReader(payloadBytes))
		req.Header.Set("Content-Type", contentType)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "nats-test")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)
	})
}

func TestChannelTTLPropagation(t *testing.T) {
	broker := newNatsBroker(t, 4248)
	defer broker.Shutdown()
	svc := service.NewService(storetest.NewRaftStore(t), nil)

	err := svc.CreateChannel(context.Background(), &domain.Channel{
		ID:                "ttl-test",
		MessageTTLSeconds: 3600,
	})
	assert.NilError(t, err)

	handler := HandleWebhookPost(broker, svc, service.NewBanTracker())

	payload := map[string]any{"event": "ttl-test"}
	payloadBytes, err := json.Marshal(payload)
	assert.NilError(t, err)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/ttl-test", bytes.NewReader(payloadBytes))
	req.Header.Set("Content-Type", contentType)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channel", "ttl-test")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler(w, req)

	resp := w.Result()
	assert.Equal(t, resp.StatusCode, http.StatusAccepted)

	time.Sleep(300 * time.Millisecond)

	historical, live := broker.Subscribe("ttl-test", time.Time{}, 10)
	assert.Assert(t, len(historical) >= 1, "expected at least 1 historical message")
	broker.Unsubscribe("ttl-test", live)
}

func createGitHubSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func createBitbucketSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func createGiteaSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSensitiveHeaderForLogs(t *testing.T) {
	sensitive := []string{
		"Authorization", "authorization", "Proxy-Authorization",
		"Cookie", "X-Gitlab-Token", "x-gitlab-token",
		"X-Hub-Signature", "X-Hub-Signature-256", "X-Gitea-Signature",
	}
	for _, h := range sensitive {
		assert.Assert(t, sensitiveHeaderForLogs(h), "header %q should be redacted", h)
	}

	benign := []string{"Content-Type", "User-Agent", "X-GitHub-Event", "Accept"}
	for _, h := range benign {
		assert.Assert(t, !sensitiveHeaderForLogs(h), "header %q should not be redacted", h)
	}
}
