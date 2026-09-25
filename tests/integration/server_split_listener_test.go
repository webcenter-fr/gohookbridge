//go:build integration

// Black-box tests for the split listener: a public listener serving only
// webhook ingestion + health, alongside the unchanged internal listener
// (UI, API, SSE, internal publish). Both share the same NATS broker,
// Service, RateLimiter, and BanTracker.
package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/server"
	"gotest.tools/v3/assert"
)

// runningServer is a server started for the split-listener tests. Cleanup
// cancels Run and asserts it returns cleanly.
type runningServer struct {
	runCtx       context.Context
	internalBase string // base URL of the internal listener
	publicBase   string // base URL of the public listener ("" when disabled)
	payload      []byte // fixture webhook payload
}

// startServer boots the real server wiring on free ports, waits for the
// internal listener to answer /health, and registers cleanup. When
// publicPort is 0 the public listener is not enabled at all.
func startServer(t *testing.T, publicPort int) *runningServer {
	t.Helper()
	return startServerWithBootstrap(t, publicPort, "bootstrap.yaml")
}

// startServerWithBootstrap is startServer with a custom bootstrap fixture.
func startServerWithBootstrap(t *testing.T, publicPort int, bootstrap string) *runningServer {
	t.Helper()
	fixturesDir, err := filepath.Abs("../fixtures")
	assert.NilError(t, err)

	internalPort := freePort(t)
	args := []string{
		"--address", "127.0.0.1",
		"--port", fmt.Sprintf("%d", internalPort),
		"--raft-dir", t.TempDir(),
		"--raft-bind-addr", freeTCPAddr(t),
		"--nats-port", fmt.Sprintf("%d", freePort(t)),
		"--nats-cluster-port", fmt.Sprintf("%d", freePort(t)),
		"--bootstrap-config-file", filepath.Join(fixturesDir, bootstrap),
	}
	if publicPort > 0 {
		args = append(args, "--public-port", fmt.Sprintf("%d", publicPort), "--public-address", "127.0.0.1")
	}

	srv, err := server.NewServer(newServerContext(t, args...))
	assert.NilError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- srv.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runDone:
			assert.NilError(t, err)
		case <-time.After(15 * time.Second):
			t.Error("server did not shut down in time")
		}
	})

	payload, err := os.ReadFile(filepath.Join(fixturesDir, "webhook-payload.json"))
	assert.NilError(t, err)

	h := &runningServer{
		runCtx:       runCtx,
		internalBase: fmt.Sprintf("http://127.0.0.1:%d", internalPort),
		payload:      bytes.TrimSpace(payload),
	}
	if publicPort > 0 {
		h.publicBase = fmt.Sprintf("http://127.0.0.1:%d", publicPort)
	}
	waitHealthy(runCtx, t, h.internalBase+"/health")
	return h
}

// waitHealthy polls baseURL until it answers 200 or the deadline passes.
func waitHealthy(ctx context.Context, t *testing.T, url string) {
	t.Helper()
	client := &http.Client{}
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		assert.NilError(t, err)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not become healthy in time", url)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// getStatus performs a GET and returns the status code.
func getStatus(ctx context.Context, t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	assert.NilError(t, err)
	resp, err := (&http.Client{}).Do(req)
	assert.NilError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// postPayload POSTs payload to url and returns the status code and body.
func postPayload(ctx context.Context, t *testing.T, url string, payload []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	assert.NilError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	assert.NilError(t, err)
	return resp.StatusCode, string(body)
}

// consumeEvent opens the SSE stream for channel on base and returns the
// relayed webhook payload.
func consumeEvent(ctx context.Context, t *testing.T, base, channel string) []byte {
	t.Helper()
	streamCtx, streamCancel := context.WithTimeout(ctx, 15*time.Second)
	defer streamCancel()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, base+"/events/"+channel, nil)
	assert.NilError(t, err)
	resp, err := (&http.Client{}).Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	relayed, ok := readRelayedBody(t, resp.Body)
	assert.Assert(t, ok, "no webhook event relayed over SSE")
	return relayed
}

// TestSplitListenerBackwardCompat covers scenario 1: without --public-port
// exactly one listener runs and serves the full current surface.
func TestSplitListenerBackwardCompat(t *testing.T) {
	unboundPort := freePort(t)
	s := startServer(t, 0)

	status, body := postPayload(s.runCtx, t, s.internalBase+"/itest-channel", s.payload)
	assert.Equal(t, status, http.StatusAccepted, "webhook POST failed: %s", body)
	assert.Equal(t, getStatus(s.runCtx, t, s.internalBase+"/api/auth/methods"), http.StatusOK)
	assert.Equal(t, getStatus(s.runCtx, t, s.internalBase+"/health"), http.StatusOK)

	// The public port was never enabled, so nothing listens on it.
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", unboundPort))
	if err == nil {
		conn.Close()
		t.Fatalf("expected no listener on unused port %d", unboundPort)
	}
}

// TestSplitListenerPublicSurfaceIsNarrow covers scenario 2: the public
// listener serves only webhook ingestion and health.
func TestSplitListenerPublicSurfaceIsNarrow(t *testing.T) {
	s := startServer(t, freePort(t))

	status, body := postPayload(s.runCtx, t, s.publicBase+"/itest-channel", s.payload)
	assert.Equal(t, status, http.StatusAccepted, "webhook POST failed: %s", body)
	assert.Equal(t, getStatus(s.runCtx, t, s.publicBase+"/health"), http.StatusOK)
	waitHealthy(s.runCtx, t, s.publicBase+"/readyz")

	// No API, no SSE, no SPA on the public listener.
	assert.Equal(t, getStatus(s.runCtx, t, s.publicBase+"/api/auth/methods"), http.StatusNotFound)
	assert.Equal(t, getStatus(s.runCtx, t, s.publicBase+"/events/itest-channel"), http.StatusNotFound)
	assert.Equal(t, getStatus(s.runCtx, t, s.publicBase+"/"), http.StatusNotFound)
}

// TestSplitListenerInternalSurfaceIsFull covers scenario 3: the internal
// listener keeps the full route table, including the end-to-end relay.
func TestSplitListenerInternalSurfaceIsFull(t *testing.T) {
	s := startServer(t, freePort(t))

	status, body := postPayload(s.runCtx, t, s.internalBase+"/itest-channel", s.payload)
	assert.Equal(t, status, http.StatusAccepted, "webhook POST failed: %s", body)

	relayed := consumeEvent(s.runCtx, t, s.internalBase, "itest-channel")
	assert.Equal(t, string(relayed), string(s.payload))

	assert.Equal(t, getStatus(s.runCtx, t, s.internalBase+"/api/auth/methods"), http.StatusOK)
	assert.Equal(t, getStatus(s.runCtx, t, s.internalBase+"/"), http.StatusOK)
}

// TestSplitListenerSamePortRejected covers scenario 4: --public-port equal
// to --port fails fast in NewServer.
func TestSplitListenerSamePortRejected(t *testing.T) {
	fixturesDir, err := filepath.Abs("../fixtures")
	assert.NilError(t, err)
	port := freePort(t)

	srv, err := server.NewServer(newServerContext(t,
		"--address", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--public-port", fmt.Sprintf("%d", port),
		"--raft-dir", t.TempDir(),
		"--raft-bind-addr", freeTCPAddr(t),
		"--nats-port", fmt.Sprintf("%d", freePort(t)),
		"--nats-cluster-port", fmt.Sprintf("%d", freePort(t)),
		"--bootstrap-config-file", filepath.Join(fixturesDir, "bootstrap.yaml"),
	))
	assert.Assert(t, err != nil, "expected an error for identical ports, got server %v", srv)
	assert.Assert(t, srv == nil)
}

// TestSplitListenerPublicWebhookConsumedOnInternal covers scenario 5: a
// webhook posted on the public listener fans out over the shared NATS
// broker and is consumed over SSE on the internal listener — the core
// requirement of the split.
func TestSplitListenerPublicWebhookConsumedOnInternal(t *testing.T) {
	s := startServer(t, freePort(t))

	status, body := postPayload(s.runCtx, t, s.publicBase+"/itest-channel", s.payload)
	assert.Equal(t, status, http.StatusAccepted, "webhook POST failed: %s", body)

	relayed := consumeEvent(s.runCtx, t, s.internalBase, "itest-channel")
	assert.Equal(t, string(relayed), string(s.payload))
}

// TestSplitListenerBanSharedAcrossListeners verifies the maintainer
// requirement: banning works on the public listener against the same shared
// BanTracker wiring. A failed webhook signature from the public listener
// bans the client IP; subsequent requests are rejected with 403 on BOTH
// listeners (bans are global per node).
func TestSplitListenerBanSharedAcrossListeners(t *testing.T) {
	s := startServerWithBootstrap(t, freePort(t), "bootstrap-ban.yaml")

	// Failed signature on the public listener records the credential failure
	// and bans 127.0.0.1 (ban_max_unique_failures: 1).
	status, body := postPayload(s.runCtx, t, s.publicBase+"/itest-ban-channel", s.payload)
	assert.Equal(t, status, http.StatusUnauthorized, "expected invalid signature: %s", body)

	// The ban now rejects the same client on the public listener...
	status, body = postPayload(s.runCtx, t, s.publicBase+"/itest-ban-channel", s.payload)
	assert.Equal(t, status, http.StatusForbidden, "public listener should enforce the ban: %s", body)

	// ...and on the internal listener (shared BanTracker: global per node).
	status, body = postPayload(s.runCtx, t, s.internalBase+"/itest-ban-channel", s.payload)
	assert.Equal(t, status, http.StatusForbidden, "internal listener should enforce the ban: %s", body)
}

// TestSplitListenerIPRestrictEnforcedOnBothListeners verifies that the
// channel-scoped middlewares of buildWebhookRouter actually see the matched
// channel: a per-channel allowed_ips restriction must be enforced on BOTH
// listeners. (Regression: chi runs Use-registered middlewares before route
// matching, so chi.URLParam("channel") was empty there and the restriction
// silently resolved against the global defaults — hence the inline r.With
// wiring in buildWebhookRouter.)
func TestSplitListenerIPRestrictEnforcedOnBothListeners(t *testing.T) {
	s := startServerWithBootstrap(t, freePort(t), "bootstrap-allowips.yaml")

	// itest-allow-channel only allows 10.0.0.1; the tests connect from
	// 127.0.0.1, so both listeners must reject the POST with 403.
	for _, base := range []string{s.publicBase, s.internalBase} {
		status, body := postPayload(s.runCtx, t, base+"/itest-allow-channel", s.payload)
		assert.Equal(t, status, http.StatusForbidden, "%s: expected the IP restriction to reject the request: %s", base, body)
		assert.Assert(t, strings.Contains(body, "not allowed"), "%s: expected the IP restriction error, got: %s", base, body)
	}

	// The unrestricted channel still relays on both listeners.
	for _, base := range []string{s.publicBase, s.internalBase} {
		status, body := postPayload(s.runCtx, t, base+"/itest-channel", s.payload)
		assert.Equal(t, status, http.StatusAccepted, "%s: unrestricted channel should relay: %s", base, body)
	}
}

// TestSplitListenerTokenModeEnforcedOnBothListeners proves the produce-token
// path (commit ade970b moved channel-scoped middlewares to inline r.With) on
// BOTH listeners: a token-mode channel accepts a valid produce token (202) and
// rejects missing / invalid / wrong-channel tokens (401).
func TestSplitListenerTokenModeEnforcedOnBothListeners(t *testing.T) {
	s := startServerWithBootstrap(t, freePort(t), "bootstrap-token.yaml")

	for _, base := range []string{s.publicBase, s.internalBase} {
		// No token -> 401.
		status, body := postPayload(s.runCtx, t, base+"/itest-token-channel", s.payload)
		assert.Equal(t, status, http.StatusUnauthorized, "%s: missing token must be rejected: %s", base, body)

		// Invalid token -> 401.
		status, body = postPayload(s.runCtx, t, base+"/itest-token-channel?token=not-the-token", s.payload)
		assert.Equal(t, status, http.StatusUnauthorized, "%s: invalid token must be rejected: %s", base, body)

		// Valid token -> 202.
		status, body = postPayload(s.runCtx, t, base+"/itest-token-channel?token=integration-test-token", s.payload)
		assert.Equal(t, status, http.StatusAccepted, "%s: valid token must be accepted: %s", base, body)

		// Valid token on the WRONG channel -> 401 (tokens are channel-scoped).
		status, body = postPayload(s.runCtx, t, base+"/itest-token-channel-b?token=integration-test-token", s.payload)
		assert.Equal(t, status, http.StatusUnauthorized, "%s: token from another channel must be rejected: %s", base, body)
	}
}
