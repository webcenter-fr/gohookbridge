//go:build integration

// Package integration_test contains the black-box end-to-end tests. This file
// exercises the gohookbridge Dagger module (build, ephemeral registry, k3s,
// Helm, smoke validation) through the `dagger call` CLI; it is env-guarded
// because it needs the dagger binary, a Docker daemon, and DAGGER_E2E=1.
package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// TestDaggerPipelineEndToEnd runs `dagger call -m dagger/gohookbridge ci`
// with --skip-push (no GHCR credentials needed): build the image, push it to
// the in-pipeline registry, deploy the Helm chart on an ephemeral k3s
// cluster, and assert the report contains "validation passed".
func TestDaggerPipelineEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Dagger pipeline end-to-end test in -short mode")
	}
	if os.Getenv("DAGGER_E2E") != "1" {
		t.Skip("set DAGGER_E2E=1 to run the Dagger pipeline end-to-end test")
	}
	if _, err := exec.LookPath("dagger"); err != nil {
		t.Skip("the dagger binary is not installed")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	assert.NilError(t, err, "resolve repository root: %s", err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dagger", "call",
		"-m", "dagger/gohookbridge", "ci",
		"--source", ".",
		"--version", resolvePipelineVersion(ctx),
		"--skip-push",
	)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	assert.NilError(t, err, "dagger call failed:\n%s", output)
	assert.Assert(t, strings.Contains(string(output), "validation passed"),
		"report must contain the success marker:\n%s", output)
}

// resolvePipelineVersion mirrors the documented version-resolution one-liner:
// the latest git tag with the leading "v" stripped, falling back to "dev"
// when the repository has no tags (or git fails).
func resolvePipelineVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "dev"
	}
	tag := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	if tag == "" {
		return "dev"
	}
	return tag
}
