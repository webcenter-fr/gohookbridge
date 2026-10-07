package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"dagger/gohookbridge/internal/dagger"
	"dagger/gohookbridge/internal/pipeline"
)

// Release-side constants. golangImage is the Go toolchain base for Goreleaser
// (matches go.mod's 1.25 line); nodeVersion/goreleaserVersion/craneVersion are
// pinned release artifacts downloaded inside pipeline containers.
const (
	golangImage        = "golang:1.25"
	nodeVersion        = "v22.14.0"
	goreleaserVersion  = "v2.18.2"
	craneVersion       = "v0.20.3"
	goreleaserDistPath = "/src/dist"
	// defaultChartsRepository is the GHCR repository the helm chart is pushed
	// to: ghcr.io/webcenter-fr/charts/gohookbridge (keeps the chart package
	// separate from the container image package ghcr.io/webcenter-fr/gohookbridge).
	defaultChartsRepository = "webcenter-fr/charts"
	// chartName anchors the chart metadata edits and the push reference.
	chartName = "gohookbridge"
)

// imagePlatforms are the platforms the server image is built and published
// for (multi-arch manifest). amd64 is first: it is the variant Ci reuses for
// the ephemeral k3s validation.
var imagePlatforms = []dagger.Platform{
	"linux/amd64",
	"linux/arm64",
	"linux/s390x",
	"linux/ppc64le",
}

// component describes one distributable binary + its container image.
type component struct {
	name       string            // "server" | "client" | "proxy"
	suffix     string            // repository suffix: "" | "-client" | "-proxy"
	dockerfile string            // build-context dockerfile, relative to the source root
	platforms  []dagger.Platform // image platforms to build/publish (amd64 first)
}

// clientProxyPlatforms are the image platforms for the client/proxy images. The
// client/proxy binaries are only distributed for amd64/arm64 (goreleaser builds
// those two archs for them), so the images mirror that set. amd64 is first: it is
// the variant reused for the in-pipeline registry publish and boot checks.
var clientProxyPlatforms = []dagger.Platform{
	"linux/amd64",
	"linux/arm64",
}

// allComponents lists the three components in a stable order (server first so the
// server amd64 variant stays the smoke target).
var allComponents = []component{
	{name: "server", suffix: "", dockerfile: "Dockerfile", platforms: imagePlatforms},
	{name: "client", suffix: "-client", dockerfile: "Dockerfile.client", platforms: clientProxyPlatforms},
	{name: "proxy", suffix: "-proxy", dockerfile: "Dockerfile.proxy", platforms: clientProxyPlatforms},
}

// componentRepository returns the full GHCR repository path for a component given
// the base repository (default "webcenter-fr/gohookbridge"): "" -> base,
// "-client" -> base+"-client", "-proxy" -> base+"-proxy".
func componentRepository(base, suffix string) string { return base + suffix }

// localRepositoryName returns the in-pipeline registry repository name for a
// component suffix: "" -> "gohookbridge", "-client" -> "gohookbridge-client", ...
func localRepositoryName(suffix string) string { return "gohookbridge" + suffix }

// imagePushResult carries the outcome of buildAndPushImage back to the tasks
// that report it (Ci and PublishImage). built is the amd64 variant of the
// built image (nil when the build failed), reused by Ci for the in-pipeline
// registry publish and the binary boot checks.
type imagePushResult struct {
	component    string // "server" | "client" | "proxy"
	suffix       string // "" | "-client" | "-proxy"
	versionRef   string
	latestRef    string
	digest       string
	latestDigest string
	lintAdvisory string
	skipped      bool
	built        *dagger.Container
}

// buildAndPushImage lints (hadolint, failure threshold error) and builds the
// component's Dockerfile with the resolved VERSION build-arg for every platform
// in comp.platforms, then optionally publishes the multi-arch image to the
// registry under <version> and <latest>. Credentials (plain username + Secret
// password) are only required when a push is requested; they are wired into
// the engine through Container.WithRegistryAuth, mirroring the
// dagger-library-go image module's Push. It is the shared core of the Ci and
// PublishImage tasks.
func buildAndPushImage(ctx context.Context, source *dagger.Directory, comp component, resolved, registry, repositoryName, registryUsername string, passSecret *dagger.Secret, pushLatest, skipPush bool) (imagePushResult, error) {
	imageRef := registry + "/" + componentRepository(repositoryName, comp.suffix) + ":" + resolved
	result := imagePushResult{component: comp.name, suffix: comp.suffix, versionRef: imageRef}

	img := dag.Image().WithBuildArg("VERSION", dagger.ImageWithBuildArgOpts{Value: resolved})
	lintOutput, lintErr := img.Lint(ctx, source, dagger.ImageLintOpts{
		Dockerfile: comp.dockerfile,
		Threshold:  "error",
	})
	if lintErr != nil {
		// Hadolint exits non-zero only at the failure threshold, so this is a
		// fatal Dockerfile error (below-threshold findings stay advisory).
		return result, fmt.Errorf("lint %s (hadolint, failure threshold error): %w", comp.dockerfile, lintErr)
	}
	result.lintAdvisory = lintOutput

	variants := make([]*dagger.Container, 0, len(comp.platforms))
	for _, platform := range comp.platforms {
		variant := source.DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: comp.dockerfile,
			Platform:   platform,
			BuildArgs:  []dagger.BuildArg{{Name: "VERSION", Value: resolved}},
		})
		if _, err := variant.Sync(ctx); err != nil {
			return result, fmt.Errorf("build image %s for %s: %w", imageRef, platform, err)
		}
		if !skipPush {
			variant = variant.WithRegistryAuth(registry, registryUsername, passSecret)
		}
		variants = append(variants, variant)
	}
	result.built = variants[0]

	if skipPush {
		result.skipped = true
		return result, nil
	}

	ref, err := variants[0].Publish(ctx, imageRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants[1:],
	})
	if err != nil {
		return result, fmt.Errorf("push image %s: %w", imageRef, err)
	}
	result.digest = ref

	if pushLatest {
		latestRef := registry + "/" + componentRepository(repositoryName, comp.suffix) + ":latest"
		latestDigest, err := variants[0].Publish(ctx, latestRef, dagger.ContainerPublishOpts{
			PlatformVariants: variants[1:],
		})
		if err != nil {
			return result, fmt.Errorf("push image %s: %w", latestRef, err)
		}
		result.latestRef = latestRef
		result.latestDigest = latestDigest
	}
	return result, nil
}

// buildAndPushAll builds and (optionally) pushes all three components in order,
// returning one result per component. It aborts on the first component failure;
// components built before the failure may already be pushed (re-running is idempotent).
func buildAndPushAll(ctx context.Context, source *dagger.Directory, resolved, registry, repositoryName, registryUsername string, passSecret *dagger.Secret, pushLatest, skipPush bool) ([]imagePushResult, error) {
	results := make([]imagePushResult, 0, len(allComponents))
	for _, comp := range allComponents {
		res, err := buildAndPushImage(ctx, source, comp, resolved, registry, repositoryName, registryUsername, passSecret, pushLatest, skipPush)
		if err != nil {
			return results, fmt.Errorf("build/push component %q: %w", comp.name, err)
		}
		results = append(results, res)
	}
	return results, nil
}

// PublishImage builds the three component Dockerfiles (server, client, proxy)
// with the given version and pushes each image to the container registry (GHCR
// by default) under <version> and, optionally, :latest. It is the
// release-pipeline counterpart of Ci without the ephemeral Kubernetes
// validation. Returns a Markdown summary of the pushed references.
func (m *Gohookbridge) PublishImage(
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
	registryPassword *dagger.Secret,
	// +optional
	// +default=false
	pushLatest bool,
	// +optional
	// +default=false
	skipPush bool,
) (string, error) {
	if registry == "" {
		registry = defaultRegistry
	}
	if repositoryName == "" {
		repositoryName = defaultRepository
	}
	resolved := pipeline.ResolveVersion(version)
	if err := pipeline.ValidateVersion(resolved); err != nil {
		return "", err
	}
	if err := pipeline.ValidateRegistryPath(registry); err != nil {
		return "", err
	}
	if err := pipeline.ValidateRegistryPath(repositoryName); err != nil {
		return "", err
	}

	var passSecret *dagger.Secret
	if !skipPush {
		if registryUsername == "" || registryPassword == nil {
			return "", errors.New("push requested but registry credentials are missing: pass --registry-username <name> and --registry-password env:VAR (or file:path); credential values are never logged")
		}
		passSecret = registryPassword
	}

	results, err := buildAndPushAll(ctx, source, resolved, registry, repositoryName, registryUsername, passSecret, pushLatest, skipPush)
	if err != nil {
		return "", err
	}

	var summary strings.Builder
	for _, res := range results {
		lintSection := "- hadolint (failure threshold `error`): passed"
		if findings := strings.TrimSpace(res.lintAdvisory); findings != "" {
			lintSection += " with advisory findings:\n\n```text\n" + findings + "\n```"
		}
		summary.WriteString(fmt.Sprintf("### %s\n- Built image: `%s`\n%s\n", res.component, res.versionRef, lintSection))
		if res.skipped {
			summary.WriteString("- Push skipped (--skip-push)\n")
		} else {
			summary.WriteString(fmt.Sprintf("- Pushed `%s`: digest %s\n", res.versionRef, res.digest))
			if res.latestDigest != "" {
				summary.WriteString(fmt.Sprintf("- Pushed `%s`: digest %s\n", res.latestRef, res.latestDigest))
			}
		}
		summary.WriteString("\n")
	}
	return summary.String(), nil
}

// Goreleaser runs `goreleaser release --clean` inside a container equipped
// with Go, Node (the goreleaser `before` hooks run `npm ci` / `npm run
// build`), git, and the pinned goreleaser binary. It creates the GitHub
// release for the checked-out tag and uploads the cross-platform binaries,
// checksums, and nfpm/brew packages as release assets. AUR publishing is
// skipped when no AUR private key is provided. Returns the dist/ directory
// (export it with `export --path dist` to attach the artifacts). snapshot
// runs an unversioned local build without any publish/release side effect
// (for pipeline development).
func (m *Gohookbridge) Goreleaser(
	ctx context.Context,
	// +required
	source *dagger.Directory,
	// +required
	version string,
	// +optional
	ghToken *dagger.Secret,
	// +optional
	aurKey *dagger.Secret,
	// +optional
	// +default=false
	snapshot bool,
) (*dagger.Directory, error) {
	resolved := pipeline.ResolveVersion(version)
	if err := pipeline.ValidateVersion(resolved); err != nil {
		return nil, err
	}

	setup := []string{
		"set -euxo pipefail",
		"apt-get update",
		"apt-get install -y --no-install-recommends ca-certificates curl git xz-utils",
		fmt.Sprintf("curl -fsSL https://nodejs.org/dist/%s/node-%s-linux-x64.tar.xz | tar -xJ -C /usr/local --strip-components=1", nodeVersion, nodeVersion),
		fmt.Sprintf("curl -fsSL https://github.com/goreleaser/goreleaser/releases/download/%s/goreleaser_Linux_x86_64.tar.gz | tar -xz -C /usr/local/bin goreleaser", goreleaserVersion),
	}
	base := dag.Container().
		From(golangImage).
		WithExec([]string{"bash", "-c", joinShell(setup)})

	args := []string{"release", "--clean"}
	if snapshot {
		args = append(args, "--snapshot")
	} else {
		if ghToken == nil {
			return nil, errors.New("goreleaser release (non-snapshot) requires --gh-token (a Dagger Secret, e.g. env:GITHUB_TOKEN)")
		}
		if aurKey == nil {
			args = append(args, "--skip=aur,aur-source")
		}
	}

	ctr := base.
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"git", "config", "--global", "user.email", "gohookbridge-ci@users.noreply.github.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "gohookbridge CI"})
	if ghToken != nil {
		ctr = ctr.WithSecretVariable("GITHUB_TOKEN", ghToken)
	}
	if aurKey != nil {
		ctr = ctr.WithSecretVariable("AUR_PRIVATE_KEY", aurKey)
	}
	ctr = ctr.WithExec(append([]string{"goreleaser"}, args...))

	if _, err := ctr.Sync(ctx); err != nil {
		return nil, fmt.Errorf("goreleaser release (version %s): %w", resolved, err)
	}
	return ctr.Directory(goreleaserDistPath), nil
}

// PublishHelm sets the chart version, appVersion, and the server/client/proxy
// image tags to the release version, lints and packages the chart, then
// pushes it to the OCI registry (GHCR by default) as
// <registry>/<chartsRepository>/gohookbridge with the <version> and :latest
// tags. Returns a Markdown summary of the pushed references.
func (m *Gohookbridge) PublishHelm(
	ctx context.Context,
	// +required
	source *dagger.Directory,
	// +required
	version string,
	// +optional
	// +default="ghcr.io"
	registry string,
	// +optional
	// +default="webcenter-fr/charts"
	chartsRepository string,
	// +optional
	// +default=""
	registryUsername string,
	// +optional
	registryPassword *dagger.Secret,
) (string, error) {
	if registry == "" {
		registry = defaultRegistry
	}
	if chartsRepository == "" {
		chartsRepository = defaultChartsRepository
	}
	resolved := pipeline.ResolveVersion(version)
	if err := pipeline.ValidateVersion(resolved); err != nil {
		return "", err
	}
	if err := pipeline.ValidateRegistryPath(registry); err != nil {
		return "", err
	}
	if err := pipeline.ValidateRegistryPath(chartsRepository); err != nil {
		return "", err
	}
	if registryUsername == "" || registryPassword == nil {
		return "", errors.New("chart push requested but registry credentials are missing: pass --registry-username <name> and --registry-password env:VAR (or file:path); credential values are never logged")
	}

	versionRef := registry + "/" + chartsRepository + "/" + chartName + ":" + resolved
	latestRef := registry + "/" + chartsRepository + "/" + chartName + ":latest"

	script := []string{
		"set -euxo pipefail",
		`sed -i "s/^version:.*/version: ${VERSION}/" helm/gohookbridge/Chart.yaml`,
		`sed -i "s/^appVersion:.*/appVersion: \"${VERSION}\"/" helm/gohookbridge/Chart.yaml`,
		// All three components (server/client/proxy) share the same published
		// image repository; pin their default tags to the released version.
		`sed -i "s/    tag: main/    tag: \"${VERSION}\"/" helm/gohookbridge/values.yaml`,
		"helm lint helm/gohookbridge",
		"helm template gohookbridge helm/gohookbridge >/dev/null",
		"helm package helm/gohookbridge -d /out",
		`printf '%s' "$REGISTRY_PASSWORD" | helm registry login "${REGISTRY}" -u "$REGISTRY_USERNAME" --password-stdin`,
		`printf '%s' "$REGISTRY_PASSWORD" | crane auth login "${REGISTRY}" -u "$REGISTRY_USERNAME" --password-stdin`,
		`helm push "/out/gohookbridge-${VERSION}.tgz" "oci://${REGISTRY}/${CHARTS_REPO}"`,
		`crane copy "${REGISTRY}/${CHARTS_REPO}/${CHART}:${VERSION}" "${REGISTRY}/${CHARTS_REPO}/${CHART}:latest"`,
	}

	ctr := dag.Container().
		From(helmImage).
		WithExec([]string{"apk", "add", "--no-cache", "bash", "curl"}).
		WithExec([]string{"sh", "-c", fmt.Sprintf("curl -fsSL https://github.com/google/go-containerregistry/releases/download/%s/go-containerregistry_Linux_x86_64.tar.gz | tar -xz -C /usr/local/bin crane", craneVersion)}).
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		WithEnvVariable("VERSION", resolved).
		WithEnvVariable("REGISTRY", registry).
		WithEnvVariable("CHARTS_REPO", chartsRepository).
		WithEnvVariable("CHART", chartName).
		WithSecretVariable("REGISTRY_USERNAME", dag.SetSecret("registry-username", registryUsername)).
		WithSecretVariable("REGISTRY_PASSWORD", registryPassword).
		WithExec([]string{"bash", "-c", joinShell(script)})

	if _, err := ctr.Sync(ctx); err != nil {
		return "", fmt.Errorf("publish helm chart (version %s): %w", resolved, err)
	}
	return "- Pushed chart `" + versionRef + "`\n- Pushed chart `" + latestRef + "`", nil
}

// joinShell joins shell command fragments with " && " into one bash -c
// script, aborting the whole script on the first failing fragment (the "&&"
// chain plus the leading "set -euxo pipefail" of the callers).
func joinShell(fragments []string) string {
	joined := ""
	for i, fragment := range fragments {
		if i > 0 {
			joined += " && "
		}
		joined += fragment
	}
	return joined
}
