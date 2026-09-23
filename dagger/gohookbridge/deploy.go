package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/gohookbridge/internal/dagger"
	"dagger/gohookbridge/internal/pipeline"
)

const (
	// helmRelease / deployNamespace anchor the smoke deployment (the chart's
	// fullnameOverride equals the release name, so the server Service is
	// "<release>-server").
	helmRelease     = "gohookbridge"
	deployNamespace = "gohookbridge"
	// serverServiceName is the chart-rendered Service for the server pod.
	serverServiceName = helmRelease + "-server"
	// pfPort is the in-container port the kubectl port-forward service listens
	// on; the smoke script reaches it at http://pf:3333.
	pfPort = 3333
)

// writeHelmValues renders the values override file for the smoke deployment:
// replicas 1, raft TLS off, image -> registry:5000/gohookbridge:<version>, and
// a minimal bootstrap admin user. Returns a *dagger.File to mount into helm.
func writeHelmValues(_ context.Context, version string, channelID string) (*dagger.File, error) {
	rendered, err := pipeline.RenderHelmValues(localImageRef, version, channelID)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithNewFile("values.yaml", rendered).File("values.yaml"), nil
}

// deployHelm installs the repo's helm/gohookbridge chart (taken from the
// caller-provided source directory; module functions have no host access) into
// the k3s cluster (namespace "gohookbridge") with the values file,
// --wait --timeout 180s.
func deployHelm(ctx context.Context, source *dagger.Directory, kubeconfig *dagger.File, values *dagger.File) error {
	k3s, _, err := startK3s(ctx)
	if err != nil {
		return err
	}
	chart := source.Directory("helm/gohookbridge")
	exec := dag.Container().From(helmImage).
		WithServiceBinding("k3s", k3s).
		WithFile("/work/kubeconfig.yaml", kubeconfig).
		WithFile("/work/values.yaml", values).
		WithDirectory("/work/chart", chart).
		WithEnvVariable("KUBECONFIG", "/work/kubeconfig.yaml").
		WithExec([]string{
			"helm", "upgrade", "--install", helmRelease, "/work/chart",
			"--namespace", deployNamespace, "--create-namespace",
			"--values", "/work/values.yaml",
			"--wait", "--timeout", "180s",
		}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})

	code, err := exec.ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("run helm install: %w", err)
	}
	if code != 0 {
		stdout, stdoutErr := exec.Stdout(ctx)
		stderr, stderrErr := exec.Stderr(ctx)
		return fmt.Errorf("helm install %q failed (exit %d): %s%s | stderr: %s%s",
			helmRelease, code,
			strings.TrimSpace(stdout), errSuffix(stdoutErr),
			strings.TrimSpace(stderr), errSuffix(stderrErr))
	}
	return nil
}
