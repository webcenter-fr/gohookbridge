package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name     string
		secure   bool
		wantHSTS bool
	}{
		{"TLS deployment emits HSTS", true, true},
		{"plain HTTP omits HSTS", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			SecurityHeaders(tt.secure)(inner).ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

			h := w.Header()
			assert.Equal(t, h.Get("X-Content-Type-Options"), "nosniff")
			assert.Equal(t, h.Get("X-Frame-Options"), "DENY")
			assert.Equal(t, h.Get("Referrer-Policy"), "no-referrer")
			csp := h.Get("Content-Security-Policy")
			assert.Assert(t, csp != "")
			assert.Assert(t, strings.Contains(csp, "default-src 'self'"))
			assert.Assert(t, strings.Contains(csp, "frame-ancestors 'none'"))
			assert.Assert(t, strings.Contains(csp, "connect-src 'self'"))
			if tt.wantHSTS {
				assert.Equal(t, h.Get("Strict-Transport-Security"), "max-age=63072000; includeSubDomains")
			} else {
				assert.Equal(t, h.Get("Strict-Transport-Security"), "")
			}
		})
	}
}
