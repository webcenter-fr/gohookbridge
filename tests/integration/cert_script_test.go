//go:build integration

package integration_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// F14: scripts/generate-raft-certs.sh emits per-ordinal leaves whose SANs
// cover the pod FQDN and the headless service FQDN.
func TestGenerateRaftCertsSANs(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is not installed")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	script := filepath.Join(repoRoot, "scripts", "generate-raft-certs.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("script not found: %v", err)
	}

	out := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, script,
		"--out", out,
		"--name", "gohookbridge",
		"--replicas", "2",
		"--headless", "gohookbridge-server-headless",
		"--namespace", "gohookbridge",
		"--cluster-domain", "cluster.local",
		"--validity-days", "30",
	)
	assert.NilError(t, cmd.Run(), "generate-raft-certs.sh failed")

	for ordinal := 0; ordinal < 2; ordinal++ {
		certPath := filepath.Join(out, string(rune('0'+ordinal)), "tls.crt")
		data, err := os.ReadFile(certPath)
		assert.NilError(t, err)
		block, _ := pem.Decode(data)
		assert.Assert(t, block != nil, "no PEM block in %s", certPath)
		cert, err := x509.ParseCertificate(block.Bytes)
		assert.NilError(t, err)

		wantPod := "gohookbridge-server-" + string(rune('0'+ordinal)) + ".gohookbridge-server-headless.gohookbridge.svc.cluster.local"
		wantHeadless := "gohookbridge-server-headless.gohookbridge.svc.cluster.local"
		assert.Assert(t, containsString(cert.DNSNames, wantPod), "leaf %d missing pod SAN %q (has %v)", ordinal, wantPod, cert.DNSNames)
		assert.Assert(t, containsString(cert.DNSNames, wantHeadless), "leaf %d missing headless SAN", ordinal)
	}

	// The node-set directory feeds the shared Secret.
	for _, name := range []string{"ca.crt", "0.crt", "0.key", "1.crt", "1.key"} {
		_, err := os.Stat(filepath.Join(out, "nodes", name))
		assert.NilError(t, err, "nodes/%s missing", name)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
