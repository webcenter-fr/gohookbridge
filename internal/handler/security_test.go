package handler

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// F23: the API responses carry browser hardening headers and /api/* is no-store.
func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("api no-store", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/me", nil))
		assert.Equal(t, w.Header().Get("X-Frame-Options"), "DENY")
		assert.Equal(t, w.Header().Get("X-Content-Type-Options"), "nosniff")
		assert.Equal(t, w.Header().Get("Cache-Control"), "no-store")
	})

	t.Run("hsts only on TLS", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil))
		assert.Equal(t, w.Header().Get("Strict-Transport-Security"), "")

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
		req.TLS = &tls.ConnectionState{}
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Assert(t, w.Header().Get("Strict-Transport-Security") != "")
	})
}

// F23: cross-origin mutations are rejected; same-origin and headerless
// (non-browser) requests pass.
func TestMutationOriginValidation(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()
	cfg, err := svc.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	cfg.Server.CORSOrigin = "https://app.example.com"
	assert.NilError(t, svc.UpdateGlobalConfig(ctx, cfg))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := MutationOriginValidation(svc)(next)

	cases := []struct {
		name   string
		method string
		origin string
		want   int
	}{
		{"same origin allowed", http.MethodPost, "https://app.example.com", http.StatusOK},
		{"foreign origin rejected", http.MethodPost, "https://evil.example.com", http.StatusForbidden},
		{"no origin allowed", http.MethodPost, "", http.StatusOK},
		{"safe method ignored", http.MethodGet, "https://evil.example.com", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), tc.method, "/api/channels/", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, w.Code, tc.want)
		})
	}
}
