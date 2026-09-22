//go:build integration

// Package integration_test contains the black-box end-to-end test: it starts
// the real server binary wiring (internal/server) on free ports, posts a
// webhook, and consumes it over SSE using only exported APIs.
package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
	"github.com/webcenter-fr/gohookbridge/internal/app"
	"github.com/webcenter-fr/gohookbridge/internal/server"
	"gotest.tools/v3/assert"
)

// newServerContext builds a cli.Context carrying the server flags and their
// defaults, overridden by the given args.
func newServerContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()
	cliApp := cli.NewApp()
	cliApp.Flags = app.ServerFlags
	set := flag.NewFlagSet("integration", flag.ContinueOnError)
	for _, f := range cliApp.Flags {
		assert.NilError(t, f.Apply(set))
	}
	assert.NilError(t, set.Parse(args))
	return cli.NewContext(cliApp, set, nil)
}

// freeTCPAddr returns a currently-free loopback host:port.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	addr := l.Addr().String()
	assert.NilError(t, l.Close())
	return addr
}

func freePort(t *testing.T) int {
	t.Helper()
	addr := freeTCPAddr(t)
	_, portStr, err := net.SplitHostPort(addr)
	assert.NilError(t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	assert.NilError(t, err)
	return port
}

func TestServerEndToEndWebhookRelay(t *testing.T) {
	fixturesDir, err := filepath.Abs("../fixtures")
	assert.NilError(t, err)

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
		"--bootstrap-config-file", filepath.Join(fixturesDir, "bootstrap.yaml"),
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

	// Wait for the server to become ready.
	readyURL := baseURL + "/health"
	client := &http.Client{}
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(runCtx, http.MethodGet, readyURL, nil)
		assert.NilError(t, err)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			<-runDone
			t.Fatal("server did not become ready in time")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Read the fixture payload and post it as a webhook.
	payload, err := os.ReadFile(filepath.Join(fixturesDir, "webhook-payload.json"))
	assert.NilError(t, err)
	payload = bytes.TrimSpace(payload)

	postReq, err := http.NewRequestWithContext(runCtx, http.MethodPost, baseURL+"/itest-channel", bytes.NewReader(payload))
	assert.NilError(t, err)
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := client.Do(postReq)
	assert.NilError(t, err)
	body, err := io.ReadAll(postResp.Body)
	assert.NilError(t, err)
	postResp.Body.Close()
	assert.Equal(t, postResp.StatusCode, http.StatusAccepted, "webhook POST failed: %s", body)

	// Consume the event over SSE and assert the body is relayed.
	eventsURL := baseURL + "/events/itest-channel"
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer streamCancel()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, eventsURL, nil)
	assert.NilError(t, err)

	streamResp, err := client.Do(req)
	assert.NilError(t, err)
	defer streamResp.Body.Close()
	assert.Equal(t, streamResp.StatusCode, http.StatusOK)

	relayedBody, ok := readRelayedBody(t, streamResp.Body)
	assert.Assert(t, ok, "no webhook event relayed over SSE")
	assert.Equal(t, string(relayedBody), string(payload))

	// Shut the server down and make sure Run returns cleanly.
	cancel()
	select {
	case err := <-runDone:
		assert.NilError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

// readRelayedBody scans SSE data lines until it finds a webhook event, then
// returns the base64-decoded bodyB payload.
func readRelayedBody(t *testing.T, r io.Reader) ([]byte, bool) {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == `{"message":"connected"}` || data == `{"message":"ready"}` {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		bodyB, ok := event["bodyB"].(string)
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(bodyB)
		if err != nil {
			continue
		}
		return decoded, true
	}
	return nil, false
}
