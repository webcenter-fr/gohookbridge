//go:build integration

// Package integration_test contains the black-box end-to-end tests. This file
// renders the Helm chart with `helm template` to prove the "at least one auth
// provider" invariant is enforced at render time. It is env-guarded because it
// needs the helm binary on PATH.
package integration_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestHelmRenderAuthInvariant(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err, "resolve repository root: %s", err)

	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	t.Run("defaults render", func(t *testing.T) {
		stderr, err := runHelmTemplate(t, chartPath)
		assert.NilError(t, err, "helm template failed:\n%s", stderr)
	})

	t.Run("internal disabled without providers fails", func(t *testing.T) {
		stderr, err := runHelmTemplate(t, chartPath, "--set", "server.auth.internal.enabled=false")
		assert.Assert(t, err != nil, "helm template should have failed")
		assert.Assert(t, strings.Contains(stderr, "at least one auth provider"),
			"stderr must mention the invariant:\n%s", stderr)
	})

	t.Run("internal disabled with OIDC provider renders", func(t *testing.T) {
		stderr, err := runHelmTemplate(t, chartPath,
			"--set", "server.auth.internal.enabled=false",
			"--set", "server.auth.oidc.providers[0].id=google",
			"--set", "server.auth.oidc.providers[0].issuer_url=https://accounts.google.com",
			"--set", "server.auth.oidc.providers[0].client_id=x",
			"--set", "server.auth.oidc.providers[0].client_secret_ref.name=s",
			"--set", "server.auth.oidc.providers[0].client_secret_ref.key=k",
		)
		assert.NilError(t, err, "helm template failed:\n%s", stderr)
	})
}

// runHelmTemplate runs `helm template gohookbridge <chartPath> [extraArgs...]`
// and returns stderr and the command error.
func runHelmTemplate(t *testing.T, chartPath string, extraArgs ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	args := append([]string{"template", "gohookbridge", chartPath}, extraArgs...)
	cmd := exec.CommandContext(ctx, "helm", args...)
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

// runHelmTemplateOut is like runHelmTemplate but also captures stdout.
func runHelmTemplateOut(t *testing.T, chartPath string, extraArgs ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := append([]string{"template", "gohookbridge", chartPath}, extraArgs...)
	cmd := exec.CommandContext(ctx, "helm", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// F05: enabling NetworkPolicy renders default-deny plus the allow rules.
func TestNetworkPolicyRendered(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	out, stderr, err := runHelmTemplateOut(t, chartPath, "--set", "networkPolicy.enabled=true")
	assert.NilError(t, err, "helm template failed:\n%s", stderr)
	assert.Assert(t, strings.Contains(out, "kind: NetworkPolicy"), "expected NetworkPolicy resources")
	assert.Assert(t, strings.Contains(out, "-default-deny"), "expected a default-deny policy")
	assert.Assert(t, strings.Contains(out, "-allow-peer-server"), "expected a peer allow policy")
}

// F06: private ingress without an internal LB or allowlist fails the render.
func TestInternalIngressFailsClosedOnMissingPrivateExposure(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	_, stderr, err := runHelmTemplateOut(t, chartPath,
		"--set", "server.ingress.enabled=true",
		"--set", "server.ingress.private=true",
	)
	assert.Assert(t, err != nil, "private ingress with no exposure must fail")
	assert.Assert(t, strings.Contains(stderr, "allowlistSourceRange"), "stderr must explain the fail-closed rule:\n%s", stderr)

	_, stderr, err = runHelmTemplateOut(t, chartPath,
		"--set", "server.ingress.enabled=true",
		"--set", "server.ingress.private=true",
		"--set", "server.ingress.internalLoadBalancer=true",
	)
	assert.NilError(t, err, "internal LB should satisfy the rule:\n%s", stderr)

	_, stderr, err = runHelmTemplateOut(t, chartPath,
		"--set", "server.ingress.enabled=true",
		"--set", "server.ingress.allowlistSourceRange[0]=10.0.0.0/8",
	)
	assert.NilError(t, err, "allowlist should satisfy the rule:\n%s", stderr)
}

// F14: the server ServiceAccount must not create or update Secrets.
func TestServerSALacksSecretWrite(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	out, stderr, err := runHelmTemplateOut(t, chartPath)
	assert.NilError(t, err, "helm template failed:\n%s", stderr)
	assert.Assert(t, !strings.Contains(out, `verbs: ["create"]`), "server Role must not create secrets")
	assert.Assert(t, !strings.Contains(out, `verbs: ["get", "update"]`), "server Role must not update secrets")
}

// F14: per-pod certificate material is projected from the node-set Secret.
func TestManualCertMounts(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	out, stderr, err := runHelmTemplateOut(t, chartPath)
	assert.NilError(t, err, "helm template failed:\n%s", stderr)
	assert.Assert(t, strings.Contains(out, "subPathExpr: $(POD_NAME).crt"), "expected per-pod cert projection")
	assert.Assert(t, strings.Contains(out, "/etc/gohookbridge/tls/tls.key"), "expected TLS key mount path")
	assert.Assert(t, strings.Contains(out, "--raft-tls-ca-cert"), "expected manual raft CA flag")
	// Multi-node listener serves HTTPS so leader forwarding can use mTLS (F13).
	assert.Assert(t, strings.Contains(out, "--tls-cert"), "expected HTTPS internal listener flag")
	assert.Assert(t, strings.Contains(out, "scheme: HTTPS"), "expected HTTPS probes")
}

// F28: workloads run locked down.
func TestPodSecurityContextRendered(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	out, stderr, err := runHelmTemplateOut(t, chartPath,
		"--set", "client.enabled=true",
		"--set", "proxy.enabled=true",
	)
	assert.NilError(t, err, "helm template failed:\n%s", stderr)
	assert.Assert(t, strings.Contains(out, "runAsNonRoot: true"))
	assert.Assert(t, strings.Contains(out, "allowPrivilegeEscalation: false"))
	assert.Assert(t, strings.Contains(out, "readOnlyRootFilesystem: true"))
	assert.Assert(t, strings.Contains(out, `drop: ["ALL"]`))
	assert.Assert(t, strings.Contains(out, "automountServiceAccountToken: false"))
}

// F04: helm package must exclude forbidden files (secrets, local values, docs).
func TestHelmPackageExcludesForbiddenFiles(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("the helm binary is not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err)
	chartPath := filepath.Join(repoRoot, "helm", "gohookbridge")

	dest := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "helm", "package", chartPath, "--destination", dest)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	assert.NilError(t, cmd.Run(), "helm package failed:\n%s", stderr.String())

	entries, err := os.ReadDir(dest)
	assert.NilError(t, err)
	assert.Assert(t, len(entries) == 1, "expected exactly one package, got %d", len(entries))

	f, err := os.Open(filepath.Join(dest, entries[0].Name()))
	assert.NilError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	assert.NilError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)

	forbidden := []string{"values-home.yaml", "SECURITY_AUDIT_REPORT.md", "AGENTS.local.md", ".git/"}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		assert.NilError(t, err)
		for _, bad := range forbidden {
			assert.Assert(t, !strings.Contains(hdr.Name, bad), "packaged forbidden entry %q", hdr.Name)
		}
		assert.Assert(t, !strings.HasSuffix(hdr.Name, ".md"), "packaged markdown file %q", hdr.Name)
	}
}
