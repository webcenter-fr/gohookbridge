package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcenter-fr/gohookbridge/pkg/crypto"

	"gotest.tools/v3/assert"
)

// TestProxyHeaderAllowlist proves F10 on the encrypting proxy: only
// allowlisted provider metadata and ingestion/signature headers are forwarded
// to the server. Credentials, arbitrary custom headers and inbound
// proxy/identity headers are dropped, and X-Forwarded-For is replaced with the
// immediate client IP so a public producer cannot forge the identity the
// server's trusted-proxy handling (F18) sees.
func TestProxyHeaderAllowlist(t *testing.T) {
	pub, _, err := crypto.GenerateKeyPair()
	assert.NilError(t, err)

	var gotAuth, gotCookie, gotXFF, gotRealIP, gotCustom, gotEvent, gotToken, gotUA string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotRealIP = r.Header.Get("X-Real-Ip")
		gotCustom = r.Header.Get("X-Foo")
		gotEvent = r.Header.Get("X-Github-Event")
		gotToken = r.Header.Get("X-Gitlab-Token")
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	handler := proxyHandler(pub, upstream.URL, upstream.Client(), 1<<20)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hello":"world"}`))
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("X-Gitlab-Token", "gitlab-secret")
	req.Header.Set("User-Agent", "producer/1.0")
	// Must be dropped at the trust boundary (F10).
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("X-Api-Key", "api-key-secret")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-Ip", "1.2.3.4")
	req.Header.Set("X-Foo", "bar")

	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)

	// Allowlisted provider metadata and the ingestion token are forwarded.
	assert.Equal(t, gotEvent, "push")
	assert.Equal(t, gotToken, "gitlab-secret")
	assert.Equal(t, gotUA, "producer/1.0")

	// Credentials and custom headers never cross the boundary.
	assert.Equal(t, gotAuth, "")
	assert.Equal(t, gotCookie, "")
	assert.Equal(t, gotCustom, "")

	// The client IP replaces any inbound forwarding header (F10/F18).
	assert.Equal(t, gotXFF, "203.0.113.7")
	assert.Equal(t, gotRealIP, "")
}

// TestProxyMaxBodySize proves F11: the proxy bounds the inbound body before
// reading it, so an oversized producer is rejected early instead of exhausting
// memory.
func TestProxyMaxBodySize(t *testing.T) {
	pub, _, err := crypto.GenerateKeyPair()
	assert.NilError(t, err)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	handler := proxyHandler(pub, upstream.URL, upstream.Client(), 1024)

	big := strings.Repeat("a", 2048)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, w.Result().StatusCode, http.StatusRequestEntityTooLarge)

	// A body within the budget is accepted.
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)
}

// TestProxyBodyIsEncrypted proves the proxy still encrypts the body for the
// channel before forwarding.
func TestProxyBodyIsEncrypted(t *testing.T) {
	pub, priv, err := crypto.GenerateKeyPair()
	assert.NilError(t, err)

	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	handler := proxyHandler(pub, upstream.URL, upstream.Client(), 1<<20)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hello":"world"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)

	assert.Assert(t, crypto.IsEncrypted(gotBody), "upstream body must be an encrypted envelope")
	decrypted, err := crypto.Decrypt(gotBody, priv)
	assert.NilError(t, err)
	assert.Equal(t, string(decrypted), `{"hello":"world"}`)
}

// TestRedactedTargetURL proves F33: the startup log never prints the channel
// access token embedded in the target URL.
func TestRedactedTargetURL(t *testing.T) {
	assert.Equal(t,
		redactedTargetURL("https://relay.example.com:8081/mychannel?token=secret-token"),
		"https://relay.example.com:8081/mychannel?token=%3Credacted%3E")
	assert.Equal(t,
		redactedTargetURL("https://relay.example.com:8081/mychannel"),
		"https://relay.example.com:8081/mychannel")
	assert.Equal(t, redactedTargetURL("://bad-url"), "<invalid-url>")
}
