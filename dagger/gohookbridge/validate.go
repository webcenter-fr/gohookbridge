package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/gohookbridge/internal/dagger"
	"dagger/gohookbridge/internal/pipeline"
)

// validateDeployment waits for the server pod to be Ready, port-forwards the
// <release>-server Service, runs the smoke script, and returns a Markdown
// report string. On failure it captures kubectl describe/logs into the report
// and returns an error carrying the failing check (and pod logs). The nonce is
// injected as an env var on every exec (kubectl wait, port-forward, smoke,
// diagnostics) so none of them can replay a previous run's cached result
// against a fresh cluster.
func validateDeployment(ctx context.Context, kubeconfig *dagger.File, channelID string, version string, nonce string) (string, error) {
	k3s, _, err := startK3s(ctx, nonce)
	if err != nil {
		return "collect cluster state: " + err.Error(), err
	}
	kubectlBase := func() *dagger.Container {
		return dag.Container().From(kubectlImage).
			WithFile("/kubeconfig.yaml", kubeconfig).
			WithEnvVariable("KUBECONFIG", "/kubeconfig.yaml").
			WithEnvVariable(runNonceEnv, nonce).
			WithServiceBinding("k3s", k3s)
	}

	// Wait for the server pod to be Ready (belt and braces after helm --wait).
	wait := kubectlBase().WithExec([]string{
		"kubectl", "wait", "--for=condition=Ready", "pods",
		"-l", "app.kubernetes.io/component=server",
		"--namespace", deployNamespace, "--timeout=180s",
	}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	waitCode, err := wait.ExitCode(ctx)
	if err != nil {
		return "kubectl wait failed to run: " + err.Error(),
			fmt.Errorf("wait for server pod: %w", err)
	}
	if waitCode != 0 {
		waitOut := combined(ctx, wait)
		details := "server pod never became Ready\n\n```text\n" + waitOut + "\n```\n\n" + collectDiagnostics(ctx, kubeconfig, nonce)
		return details, fmt.Errorf("server pod not ready within 180s: %s", waitOut)
	}

	// Port-forward the server Service inside a dedicated service.
	pf := kubectlBase().
		WithExposedPort(pfPort, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.NetworkProtocolTcp,
			Description: "gohookbridge server port-forward",
		}).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"kubectl", "port-forward",
				"--address", "0.0.0.0",
				"--namespace", deployNamespace,
				"svc/" + serverServiceName,
				fmt.Sprintf("%d:%d", pfPort, pfPort),
			},
		})
	pfStarted, err := pf.Start(ctx)
	if err != nil {
		details := "port-forward service failed to start: " + err.Error() + "\n\n" + collectDiagnostics(ctx, kubeconfig, nonce)
		return details, fmt.Errorf("start port-forward for svc/%s: %w", serverServiceName, err)
	}

	// Run the smoke checks against http://pf:3333.
	smoke := dag.Container().From(curlImage).
		WithEnvVariable("BASE_URL", fmt.Sprintf("http://pf:%d", pfPort)).
		WithEnvVariable("CHANNEL_ID", channelID).
		WithEnvVariable("EXPECTED_VERSION", version).
		WithEnvVariable(runNonceEnv, nonce).
		WithServiceBinding("pf", pfStarted).
		WithNewFile("/smoke.sh", pipeline.SmokeScript).
		WithExec([]string{"sh", "/smoke.sh"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	code, runErr := smoke.ExitCode(ctx)
	stdout, stdoutErr := smoke.Stdout(ctx)
	stderr, stderrErr := smoke.Stderr(ctx)
	checkOutput := strings.TrimSpace(stdout + stderr + errSuffix(stdoutErr) + errSuffix(stderrErr))
	if runErr != nil {
		details := "smoke script failed to run: " + runErr.Error() + "\n\n" + collectDiagnostics(ctx, kubeconfig, nonce)
		return details, fmt.Errorf("run smoke script: %w", runErr)
	}
	if code != 0 {
		details := "smoke checks failed:\n\n```text\n" + checkOutput + "\n```\n\n" + collectDiagnostics(ctx, kubeconfig, nonce)
		return details, fmt.Errorf("smoke validation failed (exit %d): %s; pod logs:\n%s",
			code, checkOutput, podLogs(ctx, kubeconfig, nonce))
	}
	return "```text\n" + checkOutput + "\n```\n\nvalidation passed", nil
}

// collectDiagnostics runs best-effort kubectl commands (events, pods,
// describe, logs) against the ephemeral cluster and returns their combined
// output for the report. Errors are tolerated: diagnostics are best-effort.
// The nonce keeps the diagnostic execs from replaying a previous run's cached
// output for a fresh cluster.
func collectDiagnostics(ctx context.Context, kubeconfig *dagger.File, nonce string) string {
	k3s, _, err := startK3s(ctx, nonce)
	if err != nil {
		return "collect diagnostics: " + err.Error()
	}
	commands := [][]string{
		{"kubectl", "get", "events", "--namespace", deployNamespace, "--sort-by=.lastTimestamp"},
		{"kubectl", "get", "pods", "-o", "wide", "--namespace", deployNamespace},
		{"kubectl", "describe", "pods", "-l", "app.kubernetes.io/component=server", "--namespace", deployNamespace},
		{"kubectl", "logs", "-l", "app.kubernetes.io/component=server", "--namespace", deployNamespace, "--tail=200", "--prefix=true"},
	}
	base := dag.Container().From(kubectlImage).
		WithFile("/kubeconfig.yaml", kubeconfig).
		WithEnvVariable("KUBECONFIG", "/kubeconfig.yaml").
		WithEnvVariable(runNonceEnv, nonce).
		WithServiceBinding("k3s", k3s)

	var b strings.Builder
	for _, args := range commands {
		exec := base.WithExec(args, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
		b.WriteString("\n### $" + strings.Join(args, " ") + "\n\n```text\n")
		b.WriteString(combined(ctx, exec))
		b.WriteString("\n```\n")
	}
	return b.String()
}

// podLogs returns the server pod logs (best-effort) for validation errors.
func podLogs(ctx context.Context, kubeconfig *dagger.File, nonce string) string {
	k3s, _, err := startK3s(ctx, nonce)
	if err != nil {
		return "collect pod logs: " + err.Error()
	}
	exec := dag.Container().From(kubectlImage).
		WithFile("/kubeconfig.yaml", kubeconfig).
		WithEnvVariable("KUBECONFIG", "/kubeconfig.yaml").
		WithEnvVariable(runNonceEnv, nonce).
		WithServiceBinding("k3s", k3s).
		WithExec([]string{
			"kubectl", "logs", "-l", "app.kubernetes.io/component=server",
			"--namespace", deployNamespace, "--tail=200", "--prefix=true",
		}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	return combined(ctx, exec)
}

// combined returns the stdout+stderr of an executed container (best-effort).
func combined(ctx context.Context, ctr *dagger.Container) string {
	var b strings.Builder
	if stdout, err := ctr.Stdout(ctx); err != nil {
		b.WriteString("stdout unavailable: " + err.Error() + "\n")
	} else {
		b.WriteString(stdout)
	}
	if stderr, err := ctr.Stderr(ctx); err != nil {
		b.WriteString("stderr unavailable: " + err.Error() + "\n")
	} else {
		b.WriteString(stderr)
	}
	return b.String()
}

// errSuffix renders an auxiliary error (stdout/stderr capture) inline.
func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return " (capture failed: " + err.Error() + ")"
}
