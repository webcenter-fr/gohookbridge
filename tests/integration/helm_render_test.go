//go:build integration

// Package integration_test contains the black-box end-to-end tests. This file
// renders the Helm chart with `helm template` to prove the "at least one auth
// provider" invariant is enforced at render time. It is env-guarded because it
// needs the helm binary on PATH.
package integration_test

import (
	"context"
	"io"
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
