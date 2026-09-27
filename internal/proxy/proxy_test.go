package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcenter-fr/gohookbridge/pkg/crypto"
	"gotest.tools/v3/assert"
)

// SEC-012: forwarded requests must not carry client-supplied identity or
// connection-state headers, and X-Forwarded-For must be the real client.
func TestSanitizeForwardedHeaders(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		wantXFF    string
	}{
		{"host:port remote addr becomes XFF", "203.0.113.7:51234", "203.0.113.7"},
		{"remote addr without port leaves XFF unset", "203.0.113.7", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://target/", nil)
			assert.NilError(t, err)
			req.Header = http.Header{
				"Cookie":              {"session=attacker"},
				"Authorization":       {"Bearer attacker-token"},
				"X-Forwarded-For":     {"1.2.3.4"},
				"X-Forwarded-Host":    {"evil.example.com"},
				"X-Forwarded-Proto":   {"https"},
				"X-Real-IP":           {"1.2.3.4"},
				"Forwarded":           {"for=1.2.3.4"},
				"Connection":          {"keep-alive"},
				"Keep-Alive":          {"timeout=5"},
				"Proxy-Authorization": {"Basic attacker"},
				"TE":                  {"trailers"},
				"Trailer":             {"X-Whatever"},
				"Upgrade":             {"websocket"},
				"Content-Length":      {"123"},
				"Transfer-Encoding":   {"chunked"},
				"X-Hub-Signature-256": {"sha256=deadbeef"},
				"X-Gitlab-Token":      {"shared-secret"},
				"X-Gitea-Signature":   {"abcdef"},
				"X-Hub-Signature":     {"sha1=deadbeef"},
			}

			sanitizeForwardedHeaders(req, tt.remoteAddr)

			stripped := []string{
				"Cookie", "Authorization", "X-Forwarded-Host", "X-Forwarded-Proto",
				"X-Real-IP", "Forwarded", "Connection", "Keep-Alive",
				"Proxy-Authorization", "TE", "Trailer", "Upgrade",
				"Content-Length", "Transfer-Encoding",
			}
			for _, h := range stripped {
				assert.Equal(t, req.Header.Get(h), "", "%s must be stripped", h)
			}
			assert.Equal(t, req.Header.Get("X-Forwarded-For"), tt.wantXFF)

			// Downstream signature validation must keep working.
			assert.Equal(t, req.Header.Get("X-Hub-Signature-256"), "sha256=deadbeef")
			assert.Equal(t, req.Header.Get("X-Gitlab-Token"), "shared-secret")
			assert.Equal(t, req.Header.Get("X-Gitea-Signature"), "abcdef")
			assert.Equal(t, req.Header.Get("X-Hub-Signature"), "sha1=deadbeef")
		})
	}
}

// SEC-012 end-to-end: a POST through the proxy handler reaches the target with
// sanitized headers, the real client XFF, and an encrypted body.
func TestProxyHandlerSanitizesForwardedHeaders(t *testing.T) {
	pub, _, err := crypto.GenerateKeyPair()
	assert.NilError(t, err)

	var got *http.Request
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	handler := newProxyHandler(pub, target.URL, &http.Client{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/http/channel", strings.NewReader(`{"hello":"world"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "session=attacker")
	req.Header.Set("Authorization", "Bearer attacker-token")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, w.Code, http.StatusAccepted, "body: %s", w.Body.String())
	assert.Assert(t, got != nil)
	assert.Equal(t, got.Header.Get("X-Forwarded-For"), "192.0.2.1", "must be the real client, not the client-supplied value")
	assert.Equal(t, got.Header.Get("Cookie"), "")
	assert.Equal(t, got.Header.Get("Authorization"), "")
	assert.Equal(t, got.Header.Get("X-Real-IP"), "")
	assert.Equal(t, got.Header.Get("Connection"), "")
	assert.Equal(t, got.Header.Get("X-Hub-Signature-256"), "sha256=deadbeef")
	assert.Equal(t, got.Header.Get("Content-Type"), "application/json")
	assert.Assert(t, got.ContentLength > 0)
	assert.Equal(t, w.Body.String(), `{"ok":true}`)
}
