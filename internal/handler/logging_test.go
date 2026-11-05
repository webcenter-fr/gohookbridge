package handler

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestSafeLoggerRedactsLogWithoutMutatingRequest pins both halves of the log
// redaction contract: the access log must never contain the raw channel token,
// and the redaction must not leak into the live request (downstream middleware
// such as ChannelAccessMiddleware reads r.URL.Query().Get("token") to
// authenticate webhook POSTs).
func TestSafeLoggerRedactsLogWithoutMutatingRequest(t *testing.T) {
	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	var seenToken, seenRequestURI string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.URL.Query().Get("token")
		seenRequestURI = r.RequestURI
		w.WriteHeader(http.StatusAccepted)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/itest-channel?token=super-secret", nil)
	w := httptest.NewRecorder()
	SafeLogger(inner).ServeHTTP(w, req)

	assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)
	// Downstream auth still sees the real token and request URI.
	assert.Equal(t, seenToken, "super-secret")
	assert.Equal(t, seenRequestURI, "/itest-channel?token=super-secret")

	// The access log carries the redacted token only.
	logs := logBuf.String()
	assert.Assert(t, strings.Contains(logs, "redacted"), "log must redact the token: %s", logs)
	assert.Assert(t, !strings.Contains(logs, "super-secret"), "log leaked the raw token: %s", logs)
}

// F33: OIDC callback query credentials (code/state/client_secret) are redacted
// from logs without mutating the live request.
func TestOIDCCallbackRedaction(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/auth/oidc/test/callback?code=SECRETCODE9&state=SECRETSTATE9&client_secret=SECRETCS9&token=SECRETTOK9", nil)

	clean := sanitizeRequestForLog(req)
	for _, secret := range []string{"SECRETCODE9", "SECRETSTATE9", "SECRETCS9", "SECRETTOK9"} {
		assert.Assert(t, !strings.Contains(clean.RequestURI, secret), "log URI leaked %q", secret)
	}
	assert.Assert(t, strings.Contains(clean.RequestURI, "redacted"))

	// The live request must be untouched.
	assert.Assert(t, strings.Contains(req.URL.RawQuery, "SECRETCODE9"), "live request was mutated")
	assert.Assert(t, strings.Contains(req.URL.RawQuery, "SECRETSTATE9"), "live request was mutated")
}
