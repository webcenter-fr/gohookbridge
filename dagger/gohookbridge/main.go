// Package main implements the gohookbridge Dagger module: it builds the
// project image with the caller-resolved version, optionally pushes it to
// GHCR, and optionally validates it on an ephemeral k3s cluster deployed
// with the repository's helm/gohookbridge chart.
//
// Dependency-binding accessor spelling, verified with `go doc
// dagger/gohookbridge/internal/dagger` after `dagger install
// github.com/disaster37/dagger-library-go/image@2.0.19` and `dagger develop`
// (dagger CLI v0.21.8): the dependency module's types (Image, ImageBuild) and
// functions are generated into THIS module's internal/dagger package
// (internal/dagger/image.gen.go next to internal/dagger/dagger.gen.go); they
// are not imported from "dagger/image/internal/dagger". The dependency's
// module-root constructor New(nil, nil) is reached as dag.Image() (Query.Image,
// optional arguments passed through dagger.ImageOpts), and credentials are
// wrapped with dag.SetSecret(name, plaintext) *dagger.Secret (Query.SetSecret).
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dagger/gohookbridge/internal/dagger"
	"dagger/gohookbridge/internal/pipeline"
)

const (
	// defaultRegistry / defaultRepository assemble to the GHCR target
	// "ghcr.io/webcenter-fr/gohookbridge" (Push joins registryUrl + "/" +
	// repositoryName + ":" + version).
	defaultRegistry   = "ghcr.io"
	defaultRepository = "webcenter-fr/gohookbridge"
	// defaultChannelID is the smoke-test webhook channel.
	defaultChannelID = "dagger-smoke"
	// defaultTimeout bounds the whole pipeline.
	defaultTimeout = "15m"
	// k3sImage is a pinned k3s version matching the repo's documented cluster
	// (AGENTS.local.md: k3s v1.33.6).
	k3sImage = "rancher/k3s:v1.33.6-k3s1"
	// helmImage / kubectlImage / curlImage are the in-pipeline tool images.
	// kubectlImage lives in the bitnamilegacy org: upstream docker.io/bitnami
	// images were archived and tag 1.33 no longer resolves there (same image,
	// legacy org).
	helmImage    = "alpine/helm:3.17.2"
	kubectlImage = "bitnamilegacy/kubectl:1.33"
	curlImage    = "alpine:3.21"
)

// Gohookbridge is the Dagger module's main object; its method Ci is the
// module's single entrypoint (the Dagger Go SDK exposes module functions as
// methods, not package-level functions).
type Gohookbridge struct{}

// Ci builds, optionally pushes to GHCR, and optionally validates on an
// ephemeral k3s cluster. It is the single LLM entrypoint; the returned
// Markdown report is redirected by the caller to
// tmp/dagger-validation-report.md.
func (m *Gohookbridge) Ci(
	ctx context.Context,
	// +required
	source *dagger.Directory,
	// +required
	version string,
	// +optional
	// +default="ghcr.io"
	registry string,
	// +optional
	// +default="webcenter-fr/gohookbridge"
	repositoryName string,
	// +optional
	// +default=""
	registryUsername string,
	// +optional
	// +default=""
	registryPassword string,
	// +optional
	// +default=false
	pushLatest bool,
	// +optional
	// +default=false
	skipPush bool,
	// +optional
	// +default=false
	skipK8s bool,
	// +optional
	// +default=false
	dryRun bool,
	// +optional
	// +default="dagger-smoke"
	channelID string,
	// +optional
	// +default="15m"
	timeout string,
) (string, error) {
	// (a) Version normalization: trim a leading "v", fall back to "dev"
	// (pure helpers, unit-tested in internal/pipeline).
	var warnings []string
	trimmedVersion := pipeline.TrimVersionTag(version)
	if trimmedVersion != version {
		warnings = append(warnings, fmt.Sprintf("normalized version %q to %q", version, trimmedVersion))
	}
	resolved := pipeline.ResolveVersion(version)
	if trimmedVersion == "" {
		warnings = append(warnings, `empty version: falling back to "dev"`)
	}

	// Resolve flag defaults (empty values fall back to the documented
	// defaults whether or not the CLI applied an annotation default).
	if registry == "" {
		registry = defaultRegistry
	}
	if repositoryName == "" {
		repositoryName = defaultRepository
	}
	if channelID == "" {
		channelID = defaultChannelID
	}
	if timeout == "" {
		timeout = defaultTimeout
	}
	deadline, err := time.ParseDuration(timeout)
	if err != nil {
		return "", fmt.Errorf("parse timeout %q: %w", timeout, err)
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	if skipPush {
		warnings = append(warnings, "GHCR push skipped (--skip-push)")
	} else if !pushLatest {
		warnings = append(warnings, `":latest" tag skipped (pass --push-latest to publish it)`)
	}
	if skipK8s {
		warnings = append(warnings, "ephemeral Kubernetes validation skipped (--skip-k8s)")
	}

	imageRef := registry + "/" + repositoryName + ":" + resolved
	sections := []pipeline.ReportSection{{
		Title: "Inputs",
		Body: fmt.Sprintf(
			"- Version: %s\n- Image: %s\n- Channel: %s\n- Timeout: %s\n- Push to registry: %t\n- Push :latest: %t\n- Kubernetes validation: %t",
			resolved, imageRef, channelID, timeout, !skipPush, pushLatest, !skipK8s,
		),
	}}
	if len(warnings) > 0 {
		bullets := make([]string, 0, len(warnings))
		for _, warning := range warnings {
			bullets = append(bullets, "- "+warning)
		}
		sections = append(sections, pipeline.ReportSection{
			Title: "Warnings",
			Body:  strings.Join(bullets, "\n"),
		})
	}

	// dryRun: report the resolved inputs without any side effect.
	if dryRun {
		sections = append(sections, pipeline.ReportSection{
			Title: "Dry run",
			Body:  "Dry run: no image was built, pushed, or deployed.",
		})
		return pipeline.BuildReport(sections...), nil
	}

	// (b) Build + push through github.com/disaster37/dagger-library-go/image.
	// Registry credentials are fatal only when a push is requested; --skip-push
	// still allows build + k8s validation. Values are wrapped into Dagger
	// secrets here and never logged or placed in the report.
	var userSecret, passSecret *dagger.Secret
	if !skipPush {
		if registryUsername == "" || registryPassword == "" {
			return "", errors.New("push requested but registry credentials are missing: pass --registry-username and --registry-password (credential values are never logged)")
		}
		userSecret = dag.SetSecret("registry-username", registryUsername)
		passSecret = dag.SetSecret("registry-password", registryPassword)
	}

	img := dag.Image()
	img = img.WithBuildArg("VERSION", dagger.ImageWithBuildArgOpts{Value: resolved})
	lintOutput, lintErr := img.Lint(ctx, source, dagger.ImageLintOpts{
		Dockerfile: "Dockerfile",
		Threshold:  "error",
	})
	if lintErr != nil {
		// Hadolint exits non-zero only at the failure threshold, so this is a
		// fatal Dockerfile error (below-threshold findings stay advisory).
		return "", fmt.Errorf("lint Dockerfile (hadolint, failure threshold error): %w", lintErr)
	}
	lintSection := "- hadolint (failure threshold `error`): passed"
	if findings := strings.TrimSpace(lintOutput); findings != "" {
		lintSection += " with advisory findings:\n\n```text\n" + findings + "\n```"
	}

	built := img.Build(source, dagger.ImageBuildOpts{Dockerfile: "Dockerfile"})
	if _, err := built.GetContainer().Sync(ctx); err != nil {
		return "", fmt.Errorf("build image %s: %w", imageRef, err)
	}
	sections = append(sections, pipeline.ReportSection{
		Title: "Build",
		Body:  lintSection + "\n- Built image: " + imageRef,
	})

	if !skipPush {
		digest, err := built.Push(ctx, repositoryName, resolved, registry, dagger.ImageBuildPushOpts{
			WithRegistryUsername: userSecret,
			WithRegistryPassword: passSecret,
		})
		if err != nil {
			return "", fmt.Errorf("push image %s: %w", imageRef, err)
		}
		pushSection := "- Pushed `" + imageRef + "`: digest " + digest
		if pushLatest {
			latestRef := registry + "/" + repositoryName + ":latest"
			latestDigest, err := built.Push(ctx, repositoryName, "latest", registry, dagger.ImageBuildPushOpts{
				WithRegistryUsername: userSecret,
				WithRegistryPassword: passSecret,
			})
			if err != nil {
				return "", fmt.Errorf("push image %s: %w", latestRef, err)
			}
			pushSection += "\n- Pushed `" + latestRef + "`: digest " + latestDigest
		}
		sections = append(sections, pipeline.ReportSection{Title: "Push", Body: pushSection})
	}

	// (c) Ephemeral cluster validation: in-pipeline registry:2 + k3s + helm
	// install of the existing helm/gohookbridge chart + smoke checks.
	if !skipK8s {
		registrySvc, err := startRegistry(ctx)
		if err != nil {
			return "", err
		}
		localRef := localPushRef + ":" + resolved
		if _, err := built.GetContainer().Publish(ctx, localRef, dagger.ContainerPublishOpts{
			RegistryService: registrySvc,
		}); err != nil {
			return "", fmt.Errorf("publish image %s to the in-pipeline registry: %w", localRef, err)
		}

		_, kubeconfig, err := startK3s(ctx)
		if err != nil {
			return "", err
		}

		values, err := writeHelmValues(ctx, resolved, channelID)
		if err != nil {
			return "", err
		}

		if err := deployHelm(ctx, source, kubeconfig, values); err != nil {
			sections = append(sections, pipeline.ReportSection{
				Title: "Ephemeral Kubernetes validation",
				Body: fmt.Sprintf("helm install failed: %s\n\n%s",
					err, collectDiagnostics(ctx, kubeconfig)),
			})
			return pipeline.BuildReport(sections...), fmt.Errorf("deploy helm chart %q: %w", helmRelease, err)
		}

		k8sReport, err := validateDeployment(ctx, kubeconfig, channelID, resolved)
		sections = append(sections, pipeline.ReportSection{
			Title: "Ephemeral Kubernetes validation",
			Body:  k8sReport,
		})
		if err != nil {
			return pipeline.BuildReport(sections...), err
		}
	}

	// (d) Report.
	sections = append(sections, pipeline.ReportSection{
		Title: "Result",
		Body:  "- Result: **PASSED**",
	})
	return pipeline.BuildReport(sections...), nil
}
