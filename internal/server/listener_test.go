package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/handler"
	"gotest.tools/v3/assert"
)

func TestResolveListenerConfig(t *testing.T) {
	tests := []struct {
		name          string
		address       string
		port          int
		publicAddress string
		publicPort    int
		wantInternal  string
		wantPublic    string
		wantErr       string
	}{
		{
			name:          "public disabled",
			address:       "localhost",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    0,
			wantInternal:  "localhost:8081",
			wantPublic:    "",
		},
		{
			name:          "public enabled",
			address:       "127.0.0.1",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    8082,
			wantInternal:  "127.0.0.1:8081",
			wantPublic:    "0.0.0.0:8082",
		},
		{
			name:          "same port rejected",
			address:       "localhost",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    8081,
			wantErr:       "must differ from port",
		},
		{
			name:          "negative public port rejected",
			address:       "localhost",
			port:          8081,
			publicAddress: "0.0.0.0",
			publicPort:    -1,
			wantErr:       "public-port must be >= 0",
		},
		{
			name:          "non-positive port rejected",
			address:       "localhost",
			port:          0,
			publicAddress: "0.0.0.0",
			publicPort:    0,
			wantErr:       "port must be greater than 0",
		},
		{
			name:          "ipv6 addresses bracketed by JoinHostPort",
			address:       "::1",
			port:          8081,
			publicAddress: "::",
			publicPort:    8082,
			wantInternal:  "[::1]:8081",
			wantPublic:    "[::]:8082",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := resolveListenerConfig(tt.address, tt.port, tt.publicAddress, tt.publicPort)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, cfg.internalAddr, tt.wantInternal)
			assert.Equal(t, cfg.publicAddr, tt.wantPublic)
		})
	}
}

func TestEffectivePublicAddr(t *testing.T) {
	assert.Equal(t, effectivePublicAddr("localhost", 8081), "localhost:8081")
	assert.Equal(t, effectivePublicAddr("127.0.0.1", 8081), "127.0.0.1:8081")
	assert.Equal(t, effectivePublicAddr("::1", 8081), "[::1]:8081")
	assert.Equal(t, effectivePublicAddr("", 8081), ":8081")
}

// SEC-004: both listeners bound reads and keep-alive but never write timeouts
// (the SSE /events stream must stay open indefinitely).
func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:8081", http.NotFoundHandler())
	assert.Equal(t, srv.ReadHeaderTimeout, 10*time.Second)
	assert.Equal(t, srv.ReadTimeout, 60*time.Second)
	assert.Equal(t, srv.IdleTimeout, 120*time.Second)
	assert.Equal(t, srv.WriteTimeout, time.Duration(0))
}

// INFO: autocert needs a hostname (SNI), never a full URL.
func TestAutoCertHostname(t *testing.T) {
	tests := []struct {
		publicURL string
		want      string
	}{
		{"https://example.com", "example.com"},
		{"https://example.com:8443/path", "example.com"},
		{"http://localhost:3333", "localhost"},
		{"https://[::1]:8443", "::1"},
		{"://bad", ""},
	}
	for _, tt := range tests {
		t.Run(tt.publicURL, func(t *testing.T) {
			assert.Equal(t, autoCertHostname(tt.publicURL), tt.want)
		})
	}
}

// SEC-017: every response from the internal router carries the hardening
// headers (SPA fallback included).
func TestRouterSecurityHeaders(t *testing.T) {
	r := chi.NewRouter()
	r.Use(handler.SecurityHeaders(true))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/does-not-exist", nil))

	assert.Equal(t, w.Code, http.StatusNotFound)
	assert.Equal(t, w.Header().Get("X-Content-Type-Options"), "nosniff")
	assert.Equal(t, w.Header().Get("X-Frame-Options"), "DENY")
	assert.Equal(t, w.Header().Get("Referrer-Policy"), "no-referrer")
	assert.Assert(t, w.Header().Get("Content-Security-Policy") != "")
	assert.Equal(t, w.Header().Get("Strict-Transport-Security"), "max-age=63072000; includeSubDomains")
}
