package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"github.com/webcenter-fr/gohookbridge/pkg/nats"
	"gotest.tools/v3/assert"
)

// setupGatingFixture provisions a service with an admin, a channel viewer and
// a test channel.
func setupGatingFixture(t *testing.T) *service.Service {
	t.Helper()
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "test-channel"}))
	assert.NilError(t, svc.CreateUser(ctx, &domain.User{
		ID:       "admin",
		Username: "admin",
		Roles:    []string{"admin"},
		Channels: []string{"*"},
	}))
	assert.NilError(t, svc.CreateUser(ctx, &domain.User{
		ID:       "viewer",
		Username: "viewer",
		Roles:    []string{"channel_viewer"},
		Channels: []string{"test-channel"},
	}))
	return svc
}

func usernameMiddleware(username string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if username != "" {
				//nolint:revive,staticcheck // context keys are package-level string constants by design
				ctx := context.WithValue(r.Context(), UsernameContextKey, username)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// newGatedRouter wires the operational endpoints exactly like
// internal/server/server.go: ChannelContext + RequirePermission(PermChannelWrite)
// for /send and replay, RequirePermission(PermAll) for generate-encryption-key.
func newGatedRouter(svc *service.Service, broker *nats.Broker, username string) chi.Router {
	r := chi.NewRouter()
	r.Use(usernameMiddleware(username))
	r.Group(func(g chi.Router) {
		g.Use(ChannelContext) // first Use = outermost
		g.Use(RequirePermission(svc, domain.PermChannelWrite))
		g.Post("/send/{channel:"+ChannelIDPattern+"}", HandleTestPayloadSend(broker, svc))
		g.Post("/channels/{channel:"+ChannelIDPattern+"}/events/{eventId:"+EventIDPattern+"}/replay", HandleEventReplay(broker, svc))
	})
	r.Group(func(g chi.Router) {
		g.Use(RequirePermission(svc, domain.PermAll))
		g.Post("/channels/{channel:"+ChannelIDPattern+"}/generate-encryption-key", HandleGenerateEncryptionKey(svc))
	})
	return r
}

func TestOperationalEndpointsRequireChannelWrite(t *testing.T) {
	svc := setupGatingFixture(t)
	broker := newNatsBroker(t, 4250)

	paths := []string{
		"/send/test-channel",
		"/channels/test-channel/events/00112233/replay",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Run("admin allowed", func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", contentType)
				newGatedRouter(svc, broker, "admin").ServeHTTP(w, req)
				assert.Equal(t, w.Code, http.StatusAccepted, "body: %s", w.Body.String())
			})

			t.Run("channel viewer forbidden", func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", contentType)
				newGatedRouter(svc, broker, "viewer").ServeHTTP(w, req)
				assert.Equal(t, w.Code, http.StatusForbidden)
			})

			t.Run("no username in context returns 401", func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", contentType)
				newGatedRouter(svc, broker, "").ServeHTTP(w, req)
				assert.Equal(t, w.Code, http.StatusUnauthorized, "setup mode must not bypass the permission check")
			})
		})
	}
}

func TestGenerateEncryptionKeyRequiresAdmin(t *testing.T) {
	svc := setupGatingFixture(t)
	broker := newNatsBroker(t, 4251)

	t.Run("admin allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/channels/test-channel/generate-encryption-key", strings.NewReader(`{"mode":"server_side"}`))
		newGatedRouter(svc, broker, "admin").ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)

		ch, err := svc.GetChannel(context.Background(), "test-channel")
		assert.NilError(t, err)
		assert.Assert(t, ch.EncryptionKey != "")
	})

	t.Run("non-admin forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/channels/test-channel/generate-encryption-key", strings.NewReader(`{}`))
		newGatedRouter(svc, broker, "viewer").ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusForbidden)
	})

	t.Run("no username in context returns 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/channels/test-channel/generate-encryption-key", strings.NewReader(`{}`))
		newGatedRouter(svc, broker, "").ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusUnauthorized)
	})
}

func TestChannelContextSetsChannelID(t *testing.T) {
	svc := setupGatingFixture(t)

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		channelID, _ := r.Context().Value(contextKeyChannelID).(string)
		assert.Equal(t, channelID, "test-channel")
		assert.Assert(t, svc.UserHasPermission(r.Context(), "admin", domain.PermChannelWrite, channelID))
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/send/test-channel", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channel", "test-channel")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	//nolint:revive,staticcheck // context keys are package-level string constants by design
	req = req.WithContext(context.WithValue(req.Context(), UsernameContextKey, "admin"))

	w := httptest.NewRecorder()
	ChannelContext(next).ServeHTTP(w, req)
	assert.Assert(t, called)
}
