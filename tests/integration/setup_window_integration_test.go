//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/server"
	"gotest.tools/v3/assert"
)

// waitForHealthy polls the health endpoint until it answers 200 and, on that
// real response, asserts the SEC-017 hardening headers are wired on the
// production router (removing SecurityHeaders from server.go fails here).
func waitForHealthy(ctx context.Context, t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		assert.NilError(t, err)
		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				h := resp.Header
				resp.Body.Close()
				assert.Equal(t, h.Get("X-Content-Type-Options"), "nosniff")
				assert.Assert(t, h.Get("Content-Security-Policy") != "", "CSP header must be set on real responses")
				assert.Equal(t, h.Get("Strict-Transport-Security"), "", "no HSTS on a plain-HTTP listener")
				t.Logf("SEC-017 headers on %s: nosniff=%q csp-set=%t hsts=%q", url, h.Get("X-Content-Type-Options"), h.Get("Content-Security-Policy") != "", h.Get("Strict-Transport-Security"))
				return
			}
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become ready in time: %s", url)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// get performs a GET and returns the status plus body.
func get(ctx context.Context, t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	assert.NilError(t, err)
	resp, err := client.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	assert.NilError(t, err)
	return resp.StatusCode, string(body)
}

// SEC-002: with no users and no OIDC providers the API stays reachable inside
// the persisted 5-minute setup window, reports setup mode, and returns 401
// once the window expires. /api/auth/methods remains reachable throughout.
func TestSetupWindowLifecycle(t *testing.T) {
	httpPort := freePort(t)
	raftAddr := freeTCPAddr(t)
	natsPort := freePort(t)
	natsClusterPort := freePort(t)

	ctx := newServerContext(t,
		"--address", "127.0.0.1",
		"--port", fmt.Sprintf("%d", httpPort),
		"--raft-dir", t.TempDir(),
		"--raft-bind-addr", raftAddr,
		"--nats-port", fmt.Sprintf("%d", natsPort),
		"--nats-cluster-port", fmt.Sprintf("%d", natsClusterPort),
	)

	srv, err := server.NewServer(ctx)
	assert.NilError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		runDone <- srv.Run(runCtx)
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	client := &http.Client{}
	waitForHealthy(runCtx, t, client, baseURL+"/health")

	t.Run("auth methods stay reachable", func(t *testing.T) {
		status, body := get(runCtx, t, client, baseURL+"/api/auth/methods")
		assert.Equal(t, status, http.StatusOK, "body: %s", body)
	})

	t.Run("setup mode reported inside the window", func(t *testing.T) {
		status, body := get(runCtx, t, client, baseURL+"/api/me")
		assert.Equal(t, status, http.StatusOK, "body: %s", body)
		assert.Assert(t, strings.Contains(body, `"setup_mode":true`), "body: %s", body)
		assert.Assert(t, !srv.Service().GetSetupModeEndTime(runCtx).IsZero(), "window must be persisted lazily")
	})

	t.Run("expired window returns 401", func(t *testing.T) {
		assert.NilError(t, srv.Service().SetSetupModeEndTime(runCtx, time.Now().Add(-time.Minute)))
		status, body := get(runCtx, t, client, baseURL+"/api/me")
		assert.Equal(t, status, http.StatusUnauthorized, "body: %s", body)
		assert.Assert(t, strings.Contains(body, "setup window expired"), "body: %s", body)
	})

	t.Run("auth methods still reachable after expiry", func(t *testing.T) {
		status, body := get(runCtx, t, client, baseURL+"/api/auth/methods")
		assert.Equal(t, status, http.StatusOK, "body: %s", body)
	})

	cancel()
	select {
	case err := <-runDone:
		assert.NilError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}
