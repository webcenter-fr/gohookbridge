package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

func TestChannelAccessMiddleware(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	// Create a channel with an access token
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "token-chan"}))

	produceRaw, _, err := svc.CreateAccessToken(ctx, "token-chan", "produce-token", "produce")
	assert.NilError(t, err)
	consumeRaw, _, err := svc.CreateAccessToken(ctx, "token-chan", "consume-token", "consume")
	assert.NilError(t, err)
	bothRaw, _, err := svc.CreateAccessToken(ctx, "token-chan", "both-token", "both")
	assert.NilError(t, err)

	// Create a public channel (no tokens)
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "public-chan"}))

	t.Run("POST returns 401 when access_mode=token and no token provided", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/token-chan", strings.NewReader(`{"test":true}`))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "token-chan")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		middleware := ChannelAccessMiddleware(svc, "produce", service.NewBanTracker())
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "produce", service.NewBanTracker())
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "produce", service.NewBanTracker())
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "produce", service.NewBanTracker())
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())
		nextCalled := false
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		middleware := ChannelAccessMiddleware(svc, "produce", service.NewBanTracker())
		middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
	})
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

		ip, err := service.GetRealIP(req, false)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "192.168.0.1")

		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", "10.0.0.1")

		ip, err = service.GetRealIP(req, true)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "10.0.0.1")

		ip, err = service.GetRealIP(req, false)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "127.0.0.1")

		req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "invalid"

		_, err = service.GetRealIP(req, false)
		assert.Assert(t, err != nil)
	})

	t.Run("IP Restrict Middleware", func(t *testing.T) {
		svc := service.NewService(storetest.NewRaftStore(t), nil)
		assert.NilError(t, svc.CreateChannel(context.Background(), &domain.Channel{
			ID:         "test",
			AllowedIPs: []string{"127.0.0.1"},
		}))
		middleware := IPRestrictMiddleware(svc)

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
