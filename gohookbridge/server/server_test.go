package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/nats"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/store/storetest"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/web"
	"gotest.tools/v3/assert"
)

func TestEventBroker(t *testing.T) {
	t.Run("Subscribe and Publish", func(t *testing.T) {
		eb := NewEventBroker()

		channel := "test-channel"
		subscriber := eb.Subscribe(channel)

		assert.Equal(t, subscriber.Channel, channel)
		assert.Assert(t, subscriber.Events != nil, "Events channel should not be nil")
		assert.Equal(t, len(eb.subscribers[channel]), 1)

		testData := []byte(`{"test":"data"}`)
		eb.Publish(channel, testData)

		receivedData := <-subscriber.Events
		assert.DeepEqual(t, receivedData, testData)

		eb.Unsubscribe(channel, subscriber)

		_, ok := eb.subscribers[channel]
		assert.Assert(t, !ok, "channel state should be removed when the last subscriber unsubscribes")

		_, isOpen := <-subscriber.Events
		assert.Assert(t, !isOpen, "Channel should be closed after unsubscribing")
	})

	t.Run("Multiple Subscribers", func(t *testing.T) {
		eb := NewEventBroker()
		channel := "test-channel"

		sub1 := eb.Subscribe(channel)
		sub2 := eb.Subscribe(channel)

		assert.Equal(t, len(eb.subscribers[channel]), 2)

		testData := []byte(`{"test":"data"}`)
		eb.Publish(channel, testData)

		assert.DeepEqual(t, <-sub1.Events, testData)
		assert.DeepEqual(t, <-sub2.Events, testData)

		eb.Unsubscribe(channel, sub1)

		assert.Equal(t, len(eb.subscribers[channel]), 1)

		testData2 := []byte(`{"test":"data2"}`)
		eb.Publish(channel, testData2)

		assert.DeepEqual(t, <-sub2.Events, testData2)
	})

	t.Run("Multiple Subscriptions Same Channel", func(t *testing.T) {
		eb := NewEventBroker()
		channel := "multi-channel"

		sub1 := eb.Subscribe(channel)
		sub2 := eb.Subscribe(channel)

		assert.Equal(t, len(eb.subscribers[channel]), 2)

		testData := []byte(`{"test":"data"}`)
		eb.Publish(channel, testData)

		assert.DeepEqual(t, <-sub1.Events, testData)
		assert.DeepEqual(t, <-sub2.Events, testData)

		eb.Unsubscribe(channel, sub1)
		eb.Unsubscribe(channel, sub2)

		_, ok := eb.subscribers[channel]
		assert.Assert(t, !ok)
	})
}

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
	rs := storetest.NewRaftStore(t)

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

		handler := handleWebhookPost(broker, rs)
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

		handler := handleWebhookPost(broker, rs)
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

		handler := handleWebhookPost(broker, rs)
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

		handler := handleWebhookPost(broker, rs)
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
	})

	t.Run("Signature Validation", func(t *testing.T) {
		payload := []byte(`{"event":"test"}`)

		assert.NilError(t, rs.CreateChannel(&store.Channel{
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

		handler := handleWebhookPost(broker, rs)
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

		handler = handleWebhookPost(broker, rs)
		handler(w, req)

		resp = w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	})
}

func TestHandleEventsGet(t *testing.T) {
	broker := newNatsBroker(t, 4243)
	rs := storetest.NewRaftStore(t)

	sessionSecret = deriveSessionSecret("test-secret")

	err := rs.CreateUser(&store.User{
		ID:       "user-1",
		Username: "alice",
		Roles:    []string{"channel_viewer"},
		Channels: []string{"e2e-channel"},
	})
	assert.NilError(t, err)

	err = rs.CreateChannel(&store.Channel{
		ID:                "e2e-channel",
		EncryptionMode:    "e2e",
		EncryptionPubKeys: []string{"dummy-key"},
		AccessMode:        "public",
	})
	assert.NilError(t, err)

	t.Run("SessionAuth_Allowed", func(t *testing.T) {
		token, err := encodeSession(&sessionToken{
			Username:  "alice",
			Method:    "internal",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, sessionSecret)
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			channelAccessMiddleware(rs, "consume")(handleEventsGet(broker, rs)).ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"message":"connected"}`)
		}))
		cancel()
		<-done
	})

	t.Run("SessionAuth_Forbidden", func(t *testing.T) {
		token, err := encodeSession(&sessionToken{
			Username:  "alice",
			Method:    "internal",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, sessionSecret)
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/unknown-channel", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "unknown-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		channelAccessMiddleware(rs, "consume")(handleEventsGet(broker, rs)).ServeHTTP(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusForbidden)
	})

	t.Run("TokenAuth_Allowed", func(t *testing.T) {
		assert.NilError(t, rs.UpdateChannel(&store.Channel{ID: "e2e-channel", AccessMode: "token"}))
		tokenRaw, _, err := rs.CreateAccessToken("e2e-channel", "consume-token", "consume")
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel?token="+tokenRaw, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		channelAccessMiddleware(rs, "consume")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
	})

	t.Run("TokenAuth_Unauthorized", func(t *testing.T) {
		assert.NilError(t, rs.UpdateChannel(&store.Channel{ID: "e2e-channel", AccessMode: "token"}))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		channelAccessMiddleware(rs, "consume")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
	})

	t.Run("E2EChannel_RelayEncrypted", func(t *testing.T) {
		assert.NilError(t, rs.UpdateChannel(&store.Channel{ID: "e2e-channel", AccessMode: "public"}))
		err := broker.Publish("e2e-channel", []byte(`{"encrypted":true,"ciphertext":"dGVzdA=="}`))
		assert.NilError(t, err)
		time.Sleep(100 * time.Millisecond)

		token, err := encodeSession(&sessionToken{
			Username:  "alice",
			Method:    "internal",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, sessionSecret)
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			channelAccessMiddleware(rs, "consume")(handleEventsGet(broker, rs)).ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"encrypted":true,"ciphertext":"dGVzdA=="}`)
		}), "E2E channel should relay encrypted data without 404")

		cancel()
		<-done
	})

	t.Run("Allows Plaintext Subscriber On Unprotected Channel", func(t *testing.T) {
		err := broker.Publish("plain-channel", []byte(`{"plain":true}`))
		assert.NilError(t, err)
		time.Sleep(100 * time.Millisecond)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/plain-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "plain-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handleEventsGet(broker, rs).ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"plain":true}`)
		}))

		body := response.Body.String()
		assert.Assert(t, strings.Contains(body, `{"message":"connected"}`))
		assert.Assert(t, strings.Contains(body, `{"message":"ready"}`))
		assert.Assert(t, strings.Contains(body, `{"plain":true}`))
		assert.Assert(t, !strings.Contains(body, `"ciphertext"`))

		cancel()
		<-done
	})
}

func TestHandleEventsGetCORSOrigin(t *testing.T) {
	testCases := []struct {
		name           string
		corsOrigin     string
		expectedHeader string
		expectPresent  bool
	}{
		{
			name:           "Wildcard Origin",
			corsOrigin:     "*",
			expectedHeader: "*",
			expectPresent:  true,
		},
		{
			name:           "Specific Origin",
			corsOrigin:     "https://example.com",
			expectedHeader: "https://example.com",
			expectPresent:  true,
		},
		{
			name:          "Empty Origin Omits Header",
			corsOrigin:    "",
			expectPresent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			broker := newNatsBroker(t, 4244)
			rs := storetest.NewRaftStore(t)

			assert.NilError(t, rs.UpdateGlobalConfig(&store.GlobalConfig{
				Server: store.ServerConfig{
					MaxBodySize: 26214400,
					CORSOrigin:  tc.corsOrigin,
				},
				Defaults: store.DefaultChannelConfig{},
			}))

			router := chi.NewRouter()
			router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", handleEventsGet(broker, rs))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/plainchannel1", nil)
			reqCtx, cancel := context.WithCancel(req.Context())
			req = req.WithContext(reqCtx)
			defer cancel()

			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				router.ServeHTTP(response, req)
				close(done)
			}()

			assert.Assert(t, eventually(t, func() bool {
				return strings.Contains(response.Body.String(), `{"message":"connected"}`)
			}))

			headerValue := response.Header().Get("Access-Control-Allow-Origin")
			if tc.expectPresent {
				assert.Equal(t, headerValue, tc.expectedHeader)
			} else {
				assert.Equal(t, headerValue, "")
			}

			cancel()
			<-done
		})
	}
}

func TestChannelAccessMiddleware(t *testing.T) {
	rs := storetest.NewRaftStore(t)

	// Create a channel with an access token
	assert.NilError(t, rs.CreateChannel(&store.Channel{ID: "token-chan"}))

	produceRaw, _, err := rs.CreateAccessToken("token-chan", "produce-token", "produce")
	assert.NilError(t, err)
	consumeRaw, _, err := rs.CreateAccessToken("token-chan", "consume-token", "consume")
	assert.NilError(t, err)
	bothRaw, _, err := rs.CreateAccessToken("token-chan", "both-token", "both")
	assert.NilError(t, err)

	// Create a public channel (no tokens)
	assert.NilError(t, rs.CreateChannel(&store.Channel{ID: "public-chan"}))

	t.Run("POST returns 401 when access_mode=token and no token provided", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/token-chan", strings.NewReader(`{"test":true}`))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "produce")
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
	})

	t.Run("POST returns 202 when access_mode=token and valid produce token in query", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/token-chan?token="+produceRaw, strings.NewReader(`{"test":true}`))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "produce")
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusAccepted)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)
		assert.Assert(t, nextCalled)
	})

	t.Run("POST returns 401 when token has consume-only scope", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/token-chan?token="+consumeRaw, strings.NewReader(`{"test":true}`))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "produce")
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
		assert.Assert(t, !nextCalled)
	})

	t.Run("POST works with public channel (backward compat)", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/public-chan", strings.NewReader(`{"test":true}`))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "public-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "produce")
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
		assert.Assert(t, nextCalled)
	})

	t.Run("SSE returns 401 when access_mode=token and no token", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/token-chan", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "consume")
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
	})

	t.Run("SSE returns 200 with valid consume token via query param", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/token-chan?token="+consumeRaw, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "consume")
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
		assert.Assert(t, nextCalled)
	})

	t.Run("SSE returns 200 with valid both-scope token via Bearer header", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/token-chan", nil)
		req.Header.Set("Authorization", "Bearer "+bothRaw)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "consume")
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
		assert.Assert(t, nextCalled)
	})

	t.Run("SSE returns 401 when token has produce-only scope", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/token-chan?token="+produceRaw, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "consume")
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
		assert.Assert(t, !nextCalled)
	})

	t.Run("POST returns 401 with invalid token", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/token-chan?token=invalidtoken123", strings.NewReader(`{"test":true}`))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := channelAccessMiddleware(rs, "produce")
		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
	})
}

func TestSPAHandler(t *testing.T) {
	t.Run("serves index.html for root path", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		web.SPAHandler().ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})

	t.Run("serves index.html for unknown paths (SPA fallback)", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/some/unknown/path", nil)
		w := httptest.NewRecorder()
		web.SPAHandler().ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})
}

func TestEffectivePublicURL(t *testing.T) {
	t.Run("returns explicit public URL unchanged", func(t *testing.T) {
		assert.Equal(t, effectivePublicURL("https://hooks.example.com", "localhost:3333", false), "https://hooks.example.com")
	})

	t.Run("defaults to http address when tls is disabled", func(t *testing.T) {
		assert.Equal(t, effectivePublicURL("", "localhost:3333", false), "http://localhost:3333")
	})

	t.Run("defaults to https address when tls is enabled", func(t *testing.T) {
		assert.Equal(t, effectivePublicURL("", "localhost:3333", true), "https://localhost:3333")
	})
}

func TestRetVersion(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()

	retVersion(w, req)

	resp := w.Result()
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, resp.Header.Get("Content-Type"), contentType)
	assert.Equal(t, resp.Header.Get(versionHeaderName), strings.TrimSpace(string(gohookbridge.Version)))

	body, _ := io.ReadAll(resp.Body)
	var response map[string]string
	err := json.Unmarshal(body, &response)
	assert.NilError(t, err)
	assert.Equal(t, response["version"], strings.TrimSpace(string(gohookbridge.Version)))
}

func TestIPRestrictions(t *testing.T) {
	t.Run("Parse IP Ranges", func(t *testing.T) {
		ranges := []string{
			"192.168.0.0/24",
			"10.0.0.1",
			"2001:db8::/32",
		}

		ipRanges, err := parseIPRanges(ranges)
		assert.NilError(t, err)
		assert.Equal(t, len(ipRanges.networks), 2)
		assert.Equal(t, len(ipRanges.ips), 1)

		assert.Assert(t, ipRanges.contains(net.ParseIP("192.168.0.100")))
		assert.Assert(t, ipRanges.contains(net.ParseIP("10.0.0.1")))
		assert.Assert(t, ipRanges.contains(net.ParseIP("2001:db8::1")))

		assert.Assert(t, !ipRanges.contains(net.ParseIP("192.168.1.1")))
		assert.Assert(t, !ipRanges.contains(net.ParseIP("10.0.0.2")))
		assert.Assert(t, !ipRanges.contains(net.ParseIP("2001:db9::1")))

		_, err = parseIPRanges([]string{"invalid"})
		assert.Assert(t, err != nil)
	})

	t.Run("Get Real IP", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.0.1:12345"

		ip, err := getRealIP(req, false)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "192.168.0.1")

		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", "10.0.0.1")

		ip, err = getRealIP(req, true)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "10.0.0.1")

		ip, err = getRealIP(req, false)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "127.0.0.1")

		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "invalid"

		_, err = getRealIP(req, false)
		assert.Assert(t, err != nil)
	})

	t.Run("IP Restrict Middleware", func(t *testing.T) {
		rs := storetest.NewRaftStore(t)
		assert.NilError(t, rs.CreateChannel(&store.Channel{
			ID:         "test",
			AllowedIPs: []string{"127.0.0.1"},
		}))
		middleware := ipRestrictMiddleware(rs)

		nextCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})

		addChannelCtx := func(req *http.Request) *http.Request {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("channel", "test")
			return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		}

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		req = addChannelCtx(req)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()

		middleware(next).ServeHTTP(w, req)
		assert.Assert(t, nextCalled, "Next handler should be called for allowed IP")
		assert.Equal(t, w.Result().StatusCode, http.StatusOK)

		nextCalled = false
		req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		req = addChannelCtx(req)
		req.RemoteAddr = "192.168.0.1:12345"
		w = httptest.NewRecorder()

		middleware(next).ServeHTTP(w, req)
		assert.Assert(t, !nextCalled, "Next handler should not be called for disallowed IP")
		assert.Equal(t, w.Result().StatusCode, http.StatusForbidden)

		nextCalled = false
		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req = addChannelCtx(req)
		req.RemoteAddr = "192.168.0.1:12345"
		w = httptest.NewRecorder()

		middleware(next).ServeHTTP(w, req)
		assert.Assert(t, nextCalled, "Next handler should be called for GET request regardless of IP")
		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
	})
}

func newNatsBroker(t *testing.T, port int) *nats.Broker {
	t.Helper()
	b, err := nats.New(nats.Config{
		NodeID:     t.Name(),
		Port:       port,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	t.Cleanup(func() {
		b.Shutdown()
	})
	return b
}

func TestHandleWebhookPostWithNATS(t *testing.T) {
	broker := newNatsBroker(t, 4241)
	rs := storetest.NewRaftStore(t)

	handler := handleWebhookPost(broker, rs)

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

func TestHandleEventsGetWithNATS(t *testing.T) {
	broker := newNatsBroker(t, 4243)
	rs := storetest.NewRaftStore(t)

	router := chi.NewRouter()
	router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", handleEventsGet(broker, rs))

	t.Run("Delivers historical and live events via NATS", func(t *testing.T) {
		err := broker.Publish("nats-sse-channel", []byte(`{"history":true}`))
		assert.NilError(t, err)
		time.Sleep(200 * time.Millisecond)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/nats-sse-channel", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"history":true}`)
		}))

		body := response.Body.String()
		assert.Assert(t, strings.Contains(body, `{"message":"connected"}`))
		assert.Assert(t, strings.Contains(body, `{"message":"ready"}`))

		broker.Publish("nats-sse-channel", []byte(`{"live":true}`))
		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"live":true}`)
		}))

		cancel()
		<-done
	})

	t.Run("Handles unprotected channel with NATS broker", func(t *testing.T) {
		localRouter := chi.NewRouter()
		rs3 := storetest.NewRaftStore(t)
		localRouter.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", handleEventsGet(broker, rs3))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/unprotected-nats", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			localRouter.ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"message":"connected"}`)
		}))

		cancel()
		<-done
	})
}

func TestNATSFanoutMultipleSubscribers(t *testing.T) {
	broker := newNatsBroker(t, 4244)

	_, live1 := broker.Subscribe("fanout", time.Time{}, 10)
	_, live2 := broker.Subscribe("fanout", time.Time{}, 10)
	defer broker.Unsubscribe("fanout", live1)
	defer broker.Unsubscribe("fanout", live2)

	err := broker.Publish("fanout", []byte("fanout-test"))
	assert.NilError(t, err)

	select {
	case data := <-live1:
		assert.Equal(t, "fanout-test", string(data))
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subscriber 1")
	}

	select {
	case data := <-live2:
		assert.Equal(t, "fanout-test", string(data))
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subscriber 2")
	}
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

func eventually(t *testing.T, predicate func() bool) bool {
	t.Helper()

	for range 50 {
		if predicate() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}

	return false
}

func TestHandleEventsGetWithClientID(t *testing.T) {
	broker := newNatsBroker(t, 4247)
	rs := storetest.NewRaftStore(t)

	err := broker.Publish("cursor-channel", []byte(`{"old":true}`))
	assert.NilError(t, err)
	time.Sleep(100 * time.Millisecond)

	cursorTime := time.Now()
	err = rs.SetClientCursor(&store.ClientCursor{
		Channel:         "cursor-channel",
		ClientID:        "test-client",
		LastTimestampMs: cursorTime.UnixMilli(),
	})
	assert.NilError(t, err)

	time.Sleep(50 * time.Millisecond)
	err = broker.Publish("cursor-channel", []byte(`{"recent":true}`))
	assert.NilError(t, err)
	time.Sleep(200 * time.Millisecond)

	router := chi.NewRouter()
	router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", handleEventsGet(broker, rs))

	t.Run("Delivers only events after cursor", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/cursor-channel?client_id=test-client", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body.String(), `{"recent":true}`)
		}), "recent event should be delivered")

		body := response.Body.String()
		assert.Assert(t, !strings.Contains(body, `{"old":true}`), "old event should NOT be delivered")

		cancel()
		<-done

		cursor, _ := rs.GetClientCursor("cursor-channel", "test-client")
		assert.Assert(t, cursor != nil)
		assert.Assert(t, cursor.LastTimestampMs > cursorTime.UnixMilli())
	})
}

func TestChannelTTLPropagation(t *testing.T) {
	broker := newNatsBroker(t, 4248)
	defer broker.Shutdown()
	rs := storetest.NewRaftStore(t)

	err := rs.CreateChannel(&store.Channel{
		ID:                "ttl-test",
		MessageTTLSeconds: 3600,
	})
	assert.NilError(t, err)

	handler := handleWebhookPost(broker, rs)

	payload := map[string]any{"event": "ttl-test"}
	payloadBytes, _ := json.Marshal(payload)
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