# Plan: Dagger pipeline — build, push to GHCR, validate on ephemeral Kubernetes

## 1. Overview & goals

Add a single, self-contained **Dagger module** (Go SDK, module path
`dagger/gohookbridge`, located at `dagger/gohookbridge/`) that:

1. Builds the project Docker image (`Dockerfile`) tagged with the **current
   project version** — resolved by the LLM/agent caller and passed in as a
   required argument (the module's source `Directory` has no `.git`, so version
   resolution cannot happen inside the module).
2. Pushes that image to **GHCR** (`ghcr.io/webcenter-fr/gohookbridge`).
3. Provisions an ephemeral single-node **k3s** cluster *inside Dagger* (a Dagger
   service + an in-pipeline `registry:2` service), deploys the freshly built
   image with the **existing** `helm/gohookbridge` chart, runs a smoke/health +
   webhook-relay validation, and returns a human-reviewable report string (the
   LLM redirects it to `tmp/dagger-validation-report.md`).

The build and push are performed by the published Dagger module
**`github.com/disaster37/dagger-library-go/image@2.0.19`** (module path
`dagger/image`, Go package `main`). Because that module is not importable from a
plain Go program, our pipeline is itself a Dagger module that declares the
dependency and orchestrates its `WithBuildArg → Lint → Build → Push` functions.

The pipeline is invoked by the LLM/agent via
`dagger call -m dagger/gohookbridge ci ...` — no Makefile targets and no CI
workflow: Dagger replaces the Makefile, and the LLM is the caller.

### Scope

In scope: the `dagger/gohookbridge` module (module code + committed generated
code under `dagger/gohookbridge/internal/`), the `Dockerfile` version-injection
arg, module-local unit tests, the integration test (env-guarded), and
documentation. No Makefile targets, no CI workflow, no changes to `web/` or
`helm/` (see §11).

Out of scope (explicit):

- Pointing the pipeline at a **pre-existing** cluster (e.g. the "home" k3s
  cluster in `AGENTS.local.md`). The pipeline always provisions a throwaway
  k3s cluster. Reusing an external kubeconfig is a possible future extension.
- HA (3-replica) validation. The ephemeral cluster runs `replicas: 1`,
  `raft.tls.enabled: false` — a smoke test, not an HA certification. The
  existing `tests/integration` suite already covers multi-node Raft.
- Any change to `web/` frontend code (see §8).
- OCI Helm chart publishing (already handled by `releaser.yaml`).
- Multi-architecture image builds (Dagger builds native `linux/amd64`, matching
  CI runners). Cross-arch publishing stays with goreleaser/buildx.

## 2. Affected files

### NEW

| Path | Purpose |
|---|---|
| `dagger/gohookbridge/dagger.json` | Module manifest; declares `github.com/disaster37/dagger-library-go/image@2.0.19` (created by `dagger init` + `dagger install`). |
| `dagger/gohookbridge/go.mod` / `go.sum` | The module's own Go module (module path `dagger/gohookbridge`); depends on `dagger.io/dagger` + the `image` dependency. |
| `dagger/gohookbridge/internal/**` | Generated module bindings — **committed** (like the dependency's own repo). |
| `dagger/gohookbridge/main.go` | Module declaration + the `Ci` orchestration function (the single LLM entrypoint). |
| `dagger/gohookbridge/k3s.go` | `startRegistry`, `startK3s` (k3s service + kubeconfig + registries mirror). |
| `dagger/gohookbridge/deploy.go` | `deployHelm`, `writeHelmValues`. |
| `dagger/gohookbridge/validate.go` | `validateDeployment` + embedded `smokeScript` + report assembly. |
| `dagger/gohookbridge/helpers.go` | Pure logic: `trimVersionTag`, `resolveVersion`, `renderHelmValues`, `buildReport`. |
| `dagger/gohookbridge/helpers_test.go` | Unit tests (pure logic, no engine needed). |
| `tests/integration/dagger_pipeline_test.go` | Env-guarded integration test (runs `dagger call` via `os/exec`). |

### MODIFIED

| Path | Change |
|---|---|
| `Dockerfile` | Add `ARG BUILDPLATFORM`, default `TARGETARCH`, `ARG VERSION` + version-file injection (see §2a). |
| `CONTRIBUTING.md` | Add "Dagger pipeline" section. |
| `README.md` | Add "Dagger pipeline (CI/CD)" section. |
| `quickstart.md` | Add "Validate with Dagger" subsection. |
| `AGENTS.local.md` | Add "Build and push the image" step to §3 Deployment (see §9). |

### REMOVED

| Path | Change |
|---|---|
| `cmd/gohookbridge-dagger/` | Deleted — replaced by the Dagger module. |
| `internal/daggerpipeline/` | Deleted — replaced by the Dagger module. |
| `go.mod` / `go.sum` (root) | Reverted — no `dagger.io/dagger` dependency in the root module (module isolation, §11). |
| `.github/workflows/` | None added (unchanged: no new CI workflow). |

No change to the Helm chart (all needed knobs already exist as values:
`server.image.repository/tag`, `server.replicas`, `server.raft.tls.enabled`,
`server.bootstrap.config`).

### 2a. Exact `Dockerfile` change

The `Dockerfile` carries the version-injection args; the module feeds `VERSION`
through the dependency's `WithBuildArg(ctx, "VERSION", version, nil)` (see §4).

```dockerfile
# Defaults so the Dockerfile also builds under plain BuildKit/Dagger without
# buildx's automatic platform args (buildx overrides these anyway).
ARG BUILDPLATFORM=linux/amd64

FROM --platform=$BUILDPLATFORM node:22-alpine AS webbuild
# ... unchanged webbuild stage (npm ci + nuxt generate + static/index.html guard) ...

FROM --platform=$BUILDPLATFORM golang:latest AS builder
COPY . /go/src/github.com/webcenter-fr/gohookbridge
COPY --from=webbuild /src/gohookbridge/web/static /go/src/github.com/webcenter-fr/gohookbridge/gohookbridge/web/static
WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
ARG TARGETARCH=amd64
ARG VERSION=dev
# Inject the resolved version into the embedded version file so the binary's
# /version endpoint reports the exact image tag (mirrors the goreleaser hook).
RUN printf '%s' "$VERSION" > /go/src/github.com/webcenter-fr/gohookbridge/gohookbridge/templates/version
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gohookbridge ./cmd/gohookbridge
```

The `gohookbridge/templates/version` file (currently `dev`, embedded via
`//go:embed templates/version` in `gohookbridge/app.go`) is how the app reports
its version (`app.Version`), so injecting `VERSION` here is the correct,
goreleaser-consistent mechanism. The module passes `VERSION=<resolved version>`
as a build arg via the dependency's `WithBuildArg`.

## 3. Data structures

### 3a. The dependency module (verified surface — use EXACTLY)

Module `github.com/disaster37/dagger-library-go/image@2.0.19` (repo branch
`v2`, module path `dagger/image`, package `main`):

- `type Image struct { ... }` — private fields: `BaseHadolintContainer`,
  `BuildContainer`, `BuildArgs []dagger.BuildArg`.
- `New(baseHadolintContainer *dagger.Container, buildContainer *dagger.Container) *Image`
  — both optional/nil-able. nil hadolint → defaults to
  `ghcr.io/hadolint/hadolint:2.12.0`; non-nil `buildContainer` bypasses
  Dockerbuild entirely.
- `(*Image).WithBuildArg(ctx, name, value string, secretValue *dagger.Secret) (*Image, error)`
  — accumulates `dagger.BuildArg`; `secretValue` resolved via `.Plaintext`.
- `(*Image).Build(source *dagger.Directory, dockerfile string, withDirectories []*dagger.Directory) *ImageBuild`
  — runs `source.DockerBuild(DirectoryDockerBuildOpts{Dockerfile, BuildArgs: m.BuildArgs})`;
  `dockerfile` defaults to `"Dockerfile"`; `withDirectories` optional.
- `(*ImageBuild).GetContainer() *dagger.Container`.
- `(*ImageBuild).Push(ctx, repositoryName, version string, withRegistryUsername, withRegistryPassword *dagger.Secret, registryUrl string) (string, error)`
  — semver-normalizes `version`, calls `WithRegistryAuth(registryUrl, username,
  passwordSecret)` ONLY when BOTH secrets non-nil, then
  `Container.Publish(registryUrl + "/" + repositoryName + ":" + version)`;
  returns the digest.
- `(*Image).Lint(ctx, source *dagger.Directory, dockerfile, severity string) (string, error)`
  — hadolint.
- `(*Image).Ci(...)` — one-shot lint+build+push. **NOT used**: it does not
  support `:latest`, and its push path cannot be skipped per-tag.

**Accessor spelling:** the exact import/accessor for these types inside our
module (either the module's `internal/dagger` package or the dependency's
`dagger/image/internal/dagger` import — whichever `dagger develop` produced)
MUST be verified with `go doc` during implementation and recorded in a doc
comment. See §10 step 1 and §12.

### 3b. Module function signature

```go
// Package main implements the gohookbridge Dagger module.
package main

// Ci builds, optionally pushes to GHCR, and optionally validates on an
// ephemeral k3s cluster. It is the single LLM entrypoint.
func Ci(
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
) (string, error)
```

The `+required` / `+optional` / `+default` comments are Dagger's Go argument
annotations; `dagger develop` turns them into the `dagger call` flag surface.

### 3c. Constants

```go
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
    helmImage    = "alpine/helm:3.17.2"
    kubectlImage = "bitnami/kubectl:1.33"
    curlImage    = "alpine:3.21"
)
```

## 4. Function signatures & behavioral contracts

Files (package `main`): `main.go` = module + `Ci` orchestration; `k3s.go` =
registry + k3s services; `deploy.go` = helm; `validate.go` = smoke + report;
`helpers.go` = pure logic.

### `main.go` — `Ci` orchestration

`Ci` is the single LLM entrypoint. Behavior:

1. **(a) Version normalization** — defensively trim a leading `"v"` (and
   surrounding whitespace) from `version`; if the result is empty, fall back to
   `"dev"` (`helpers.trimVersionTag` + `resolveVersion`, pure, unit-tested).
2. **(b) Build + push** — if `!skipPush` and `!dryRun`:
   - Instantiate the dependency: `img := image.New(nil, nil)`.
   - `img.WithBuildArg(ctx, "VERSION", version, nil)`.
   - `Lint(ctx, source, "Dockerfile", "error")` — fail (return error) if the
     output is non-empty.
   - `built := img.Build(source, "Dockerfile", nil)` (returns `*ImageBuild`).
   - Force evaluation: `built.GetContainer().Sync(ctx)`.
   - Wrap registry credentials: when both `registryUsername` and
     `registryPassword` are non-empty, `dag.SetSecret("registry-username", ...)`
     / `dag.SetSecret("registry-password", ...)` → `*dagger.Secret`; otherwise
     pass `nil` for both.
   - `Push(ctx, repositoryName, version, userSecret, passSecret, registry)` —
     returns the digest.
   - If `pushLatest`: a second `Push(ctx, repositoryName, "latest", userSecret,
     passSecret, registry)`.
3. **(c) Ephemeral cluster validation** — if `!skipK8s` and `!dryRun`: run the
   existing ephemeral `registry:2` + k3s + helm + smoke validation (§4
   `k3s.go`/`deploy.go`/`validate.go`, same behavior as the previous
   in-pipeline flow): start an in-pipeline `registry:2`, start single-node k3s
   with a registries mirror for `registry:5000`, `helm install` the existing
   `helm/gohookbridge` chart, smoke-check `/version` + `/health` + `/` +
   webhook→SSE, and assemble the report.
4. **(d) Report** — return the human-reviewable Markdown report string. The LLM
   redirects it to `tmp/dagger-validation-report.md`. When `dryRun=true`,
   return a dry-run report of the resolved inputs (version, repository, flags)
   without building, pushing, or provisioning the cluster.

Secrets: `registryUsername`/`registryPassword` are plain-string function args,
wrapped via `dag.SetSecret` into `*dagger.Secret` for the dependency's `Push`;
never logged.

### `helpers.go` — pure logic (unit-tested)

```go
// trimVersionTag strips one leading "v" and surrounding whitespace from a
// version string (e.g. "v1.2.3" -> "1.2.3", "  dev  " -> "dev").
func trimVersionTag(tag string) string

// resolveVersion returns trimVersionTag(version), or "dev" when the result is
// empty. This is the module-side defensive guard; the caller (LLM) resolves
// the real version and passes it in.
func resolveVersion(version string) string

// renderHelmValues renders the helm values-override map to YAML for the smoke
// deployment (see deploy.go:writeHelmValues).
func renderHelmValues(repository string, version string, channelID string) (string, error)

// buildReport assembles the final Markdown report from the build/push/k8s
// results. Pure: takes strings/structured results, returns Markdown.
func buildReport(sections ...reportSection) string
```

### `k3s.go` — registry + k3s services

```go
// startRegistry starts an in-pipeline registry:2 service (hostname "registry",
// port 5000) that the k3s cluster pulls the freshly built image from. Avoids
// depending on private-by-default GHCR packages inside the ephemeral cluster.
func startRegistry(ctx context.Context) (*dagger.Service, error)

// startK3s starts a single-node k3s service (hostname "k3s") with a registries
// mirror for "registry:5000", and returns (service, kubeconfig) where the
// kubeconfig has server rewritten to "https://k3s:6443" and
// insecure-skip-tls-verify:true (ephemeral cluster only).
func startK3s(ctx context.Context) (*dagger.Service, *dagger.File, error)
```

### `deploy.go` — helm

```go
// writeHelmValues renders the values override file for the smoke deployment:
// replicas 1, raft TLS off, image -> registry:5000/gohookbridge:<version>, and
// a minimal bootstrap admin user. Returns a *dagger.File to mount into helm.
func writeHelmValues(ctx context.Context, version string, channelID string) (*dagger.File, error)

// deployHelm installs the repo's helm/gohookbridge chart into the k3s cluster
// (namespace "gohookbridge") with the values file, --wait --timeout 180s.
func deployHelm(ctx context.Context, kubeconfig *dagger.File, values *dagger.File) error
```

### `validate.go` — smoke + report

```go
// validateDeployment waits for the server pod to be Ready, port-forwards the
// <release>-server Service, runs the smoke script, and returns a Markdown
// report string. On failure it captures kubectl describe/logs into the report.
func validateDeployment(ctx context.Context, kubeconfig *dagger.File, channelID string, version string) (string, error)
```

Embedded asset:

```go
// smokeScript is a POSIX sh script run inside an alpine container bound to the
// port-forward service. It checks /version, /health, / (UI), webhook POST (202)
// and SSE relay (connected/ready + bodyB round-trip). See §7 for content.
const smokeScript = `...`
```

## 5. Error handling strategy

- **Wrapping:** every boundary wraps with `fmt.Errorf("context: %w", err)`.
- **Fatal (return error → non-zero exit):** Dagger engine unavailable (`dagger
  call` fails to connect); missing registry credentials when a push is
  requested (both `registryUsername` and `registryPassword` must be non-empty);
  dependency `WithBuildArg` error; `Lint` non-empty output (hadolint failures);
  `Build` failure; `Sync` failure; `Push` failure; k3s/registry start failure;
  helm install failure; validation failure.
- **Warnings (logged, continue):** version was `"v"`-prefixed (trimmed); empty
  version → `dev` fallback; `:latest` skipped (when `pushLatest=false`).
- **Secret redaction:** `registryUsername`/`registryPassword` are never
  `fmt`-printed or included in error text. They are wrapped via
  `dag.SetSecret(...)` and passed to the dependency's `Push` as `*dagger.Secret`
  so they never materialize in command args/stdout. Errors naming a missing
  credential name only the *argument name* (`--registry-username` /
  `--registry-password`), never a value.
- **Cleanup:** Dagger tears down all services (k3s, registry, port-forward) when
  the session ends, so no explicit `helm uninstall` is required for the
  ephemeral cluster. A best-effort `kubectl delete namespace` is NOT run (the
  cluster is destroyed wholesale).

## 6. Edge cases

| Case | Behavior |
|---|---|
| Empty / `"v"`-prefixed version | `resolveVersion` strips the `v` and falls back to `dev`. Logged as a warning when trimmed or when `dev` is used. |
| Version resolution (git) | Performed by the LLM *outside* the module (the module's source `Directory` has no `.git`). Documented in §9. |
| Missing registry credentials | Fatal only when a push is requested; `--skip-push` still allows build + k8s validation. |
| Dagger engine unavailable | `dagger call` fails to connect → fatal with remediation hint (install Docker / set `_EXPERIMENTAL_DAGGER_RUNNER_HOST`). |
| k3s/registry image pull failure | `startK3s`/`startRegistry` error → fatal, wrapped. |
| Helm install failure | `deployHelm` returns wrapped error; `validateDeployment` would not run; report includes `kubectl get events`. |
| Image push failure | `Push` returns wrapped error (auth/network/registry) → fatal. |
| Port conflicts | No host ports are used. Intra-Dagger ports are dynamic (`WithExposedPort` + `AsService`); port-forward uses `0.0.0.0:3333` inside a dedicated service. |
| Pod never Ready | `kubectl wait --for=condition=Ready --timeout=180s` fails → validation captures `kubectl describe pod` + `kubectl logs` into the report, then returns error. |
| Validation assertion failure | `validateDeployment` returns error with the failing check + pod logs. |
| `dryRun=true` | No build/push/cluster side effects; returns a dry-run report of the resolved inputs. |

## 7. Validation (exact commands & expected outcomes)

Root module (unchanged; `dagger/` is a separate Go module):

```shell
go build ./...
golangci-lint run ./...
go test ./...
```

Dagger module (separate Go module):

```shell
cd dagger/gohookbridge
go build ./...
go vet ./...
golangci-lint run ./...
go test ./...
```

Engine-dependent (requires a Docker daemon for the Dagger engine):

```shell
# Build + ephemeral k3s validation, no GHCR push (no creds needed):
dagger call -m dagger/gohookbridge ci --source . --version <v> --skip-push

# Full: build + GHCR push + ephemeral k3s validation
# (requires --registry-username / --registry-password):
dagger call -m dagger/gohookbridge ci --source . --version <v>

# Build + push only (no k8s):
dagger call -m dagger/gohookbridge ci --source . --version <v> --skip-k8s

# Regenerating the module must produce no diff (generated code is committed):
dagger develop -m dagger/gohookbridge
```

Existing Makefile targets may be **run** (`make build`, `make test`, `make
lint`), never edited.

The smoke script (embedded in `validate.go`) must verify, against
`http://pf:3333` (port-forwarded `<release>-server`):

1. `GET /version` → 200, JSON `version == <resolved version>` and header
   `X-Gohookbridge-Version == <resolved version>`.
2. `GET /health` → 200.
3. `GET /` → 200 (SPA `index.html` present → UI available).
4. `POST /<channel>` → 202.
5. SSE `GET /events/<channel>` emits `{"message":"connected"}` then, after a
   second POST, an event whose base64 `bodyB` decodes to the posted payload.

## 8. Test strategy

### Unit tests (inside the module, `package main`, `gotest.tools/v3/assert`)

- `dagger/gohookbridge/helpers_test.go` — pure logic only, run with plain
  `go test` in the module dir (no engine needed):
  - `TestTrimVersionTag`: `"v1.2.3"→"1.2.3"`, `"1.2.3"→"1.2.3"`,
    `"  dev "→"dev"`, `""→""`, `"v"→""`.
  - `TestResolveVersion`: `"v1.2.3"→"1.2.3"`, `""→"dev"`, `"v"→"dev"`,
    `"dev"→"dev"`.
  - `TestRenderHelmValues`: renders the expected YAML keys (image repo/tag,
    replicas 1, raft tls off, bootstrap admin).
  - `TestBuildReport`: report assembly order/content; secrets never present.
  - `TestSmokeScriptConstant`: the embedded script contains the five checks of §7.

Engine-touching functions (`startRegistry`, `startK3s`, `deployHelm`,
`validateDeployment`, and the `Ci` orchestration of `WithBuildArg`/`Lint`/
`Build`/`Sync`/`Push`) are kept thin and are **not** unit-tested — they are
exercised by the integration test below.

### Integration test

- `tests/integration/dagger_pipeline_test.go`, build tag `integration`:
  - Skips with `t.Skip` when `testing.Short()` OR `os.Getenv("DAGGER_E2E") != "1"`.
  - Skips when the `dagger` binary is absent (`exec.LookPath("dagger")` fails).
  - Runs `dagger call -m dagger/gohookbridge ci --source . --version <v>
    --skip-push` via `os/exec`, and asserts the report contains
    `validation passed` (mirrors `server_integration_test.go`'s webhook→SSE
    assertion but against the in-cluster deployment).
  - This is the "integration test for backend (publisher/consumer path)" that
    AGENTS.md requires.

### Frontend (`web/`)

**No changes.** The image build compiles the Nuxt assets inside the Dockerfile
webbuild stage; no Vue/TS code, store, page, or test changes. The smoke test's
`GET /` check validates the embedded UI is served. State this explicitly in the
PR description.

## 9. Documentation updates

All invocation examples become `dagger call -m dagger/gohookbridge ci ...`,
with the version-resolution one-liner and the `> tmp/dagger-validation-report.md`
redirect:

```bash
VERSION=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//'); \
[ -z "$VERSION" ] && VERSION=dev
dagger call -m dagger/gohookbridge ci --source . --version "$VERSION" \
  > tmp/dagger-validation-report.md
```

### `CONTRIBUTING.md` — new section `### Dagger pipeline (build, push, validate)`

Add under `Local development` (after "Testing the client"):

- Prerequisites: Docker daemon (Dagger auto-starts its engine via Docker) or
  `_EXPERIMENTAL_DAGGER_RUNNER_HOST`; optional `GHCR_USERNAME`/`GHCR_TOKEN` for
  pushing (passed as `--registry-username` / `--registry-password`).
- `dagger call -m dagger/gohookbridge ci --source . --version <v>` — full build
  + GHCR push + ephemeral k3s validation.
- Variants: `--skip-push --skip-k8s` (build only), `--skip-k8s` (build + push),
  `--skip-push` (build + k8s, no GHCR), `--push-latest` (also publish `:latest`).
- Where results land: the report string, redirected to
  `tmp/dagger-validation-report.md`.

### `README.md` — new section `## Dagger pipeline (CI/CD)`

Under `Installation` (after `### Docker`):

- Bullet: "Build, publish to GHCR, and validate on an ephemeral k3s cluster with
  `dagger call -m dagger/gohookbridge ci --source . --version <v>` (a Dagger
  module consuming `disaster37/dagger-library-go/image`; image tagged with the
  caller-resolved version)."
- Bullet: "Requires a Docker daemon; the GHCR push reads
  `--registry-username`/`--registry-password`."
- Bullet: "Validation report redirected to `tmp/dagger-validation-report.md`."

### `quickstart.md` — new subsection `### Validate with Dagger`

Under `Kubernetes Quick Start`:

- Code block: the version-resolution one-liner + `dagger call -m
  dagger/gohookbridge ci --source . --version "$VERSION"` (and note the
  ephemeral cluster is torn down automatically; `--skip-push` skips the GHCR
  push).

### `AGENTS.local.md` — new step in `## 3. Deployment`

Insert the following at the **top** of `AGENTS.local.md` §3 "Deployment", before
the existing "Use helm and maintain the values.yaml here." bullet:

```bash
1. Build and push the image:
   VERSION=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//'); \
   [ -z "$VERSION" ] && VERSION=dev
   dagger call -m dagger/gohookbridge ci --source . --version "$VERSION" \
     > tmp/dagger-validation-report.md      # build + GHCR push + ephemeral k3s validation, or
   dagger call -m dagger/gohookbridge ci --source . --version "$VERSION" \
     --skip-k8s                             # build + push only (no k8s)
   # Tags the image with the resolved version ("v" stripped) and pushes it to
   # ghcr.io/webcenter-fr/gohookbridge (pass --registry-username /
   # --registry-password for GHCR auth).

2. Redeploy the "home" cluster with the freshly pushed version:
   # a. Set server.image.tag in helm/gohookbridge/values-home.yaml to the
   #    resolved version.
   export KUBECONFIG=/home/user/.kube/home
   helm upgrade --install gohookbridge ./helm/gohookbridge \
     --namespace gohookbridge --create-namespace \
     --values helm/gohookbridge/values-home.yaml
   # Keep HA (3 replicas) as documented in §2.

3. Validate per §4 (webhook -> SSE in both encryption modes, UI available)
   against https://gohookbridge-test.home.webcenter.fr, and confirm
   `curl -s https://gohookbridge-test.home.webcenter.fr/version` reports the
   new version.
```

Note: the Dagger pipeline itself still provisions its **own** ephemeral k3s
cluster (per §1 out-of-scope). The "home"-cluster redeploy above is a separate,
manual/agent step that consumes the freshly pushed GHCR image.

## 10. Step-by-step implementation plan

Branch: `feat/dagger-ghcr-k8s` created from `main`.

1. **Bootstrap the Dagger module** — `dagger init --sdk=go dagger/gohookbridge`
   (or equivalent), `dagger install github.com/disaster37/dagger-library-go/image@2.0.19`,
   then `dagger develop`. `dagger.json` gains the dependency; the module's own
   `go.mod` (module path `dagger/gohookbridge`) gains `dagger.io/dagger` + the
   dependency; generated code under `dagger/gohookbridge/internal/dagger/` is
   **committed**. Verify the dependency-binding accessor/constructor spelling
   via `go doc` and record it in a doc comment. If the dagger CLI/engine is
   unavailable, hand-mirror the generated structure (the dependency's repo is
   the reference) and record that `dagger develop` must be re-run — never
   silently skip. Root `go.mod`/`go.sum`: **untouched**.
2. **Dockerfile** — confirm the exact change in §2a is present (global `ARG
   BUILDPLATFORM`, `ARG TARGETARCH=amd64`, `ARG VERSION=dev`, `RUN printf`
   version injection). `WithBuildArg(ctx, "VERSION", ...)` feeds it.
3. **Module code** — `main.go` (`Ci` orchestration per §4), `k3s.go`
   (`startRegistry`, `startK3s`), `deploy.go` (`writeHelmValues`,
   `deployHelm`), `validate.go` (`validateDeployment` + `smokeScript`),
   `helpers.go` (`trimVersionTag`, `resolveVersion`, `renderHelmValues`,
   `buildReport`).
4. **Unit tests** — `dagger/gohookbridge/helpers_test.go` (§8, pure logic).
5. **Integration test** — `tests/integration/dagger_pipeline_test.go` (§8,
   `os/exec` of `dagger call`).
6. **Docs** — `CONTRIBUTING.md`, `README.md`, `quickstart.md`, `AGENTS.local.md`
   (§9).
7. **Verify** — run §7 commands locally (root module, module dir, engine-driven
   `dagger call`, and `dagger develop` no-diff).

### Git / PR strategy

- Conventional commits: `feat(dagger):`, `build(docker):`, `ci(dagger):`,
  `test(dagger):`, `docs(dagger):`.
- PR ready when, locally: `go build ./...`, `golangci-lint run ./...`,
  `go test ./...` (root); `cd dagger/gohookbridge && go build ./... && go vet
  ./... && golangci-lint run ./... && go test ./...`; `dagger call -m
  dagger/gohookbridge ci --source . --version <v> --skip-push`; and `dagger
  develop` produces no diff. Mark PR as non-breaking, frontend-untouched.

## 11. Decisions & Alternatives

1. **Pipeline is a Dagger module at `dagger/gohookbridge/` (module name
   "gohookbridge", Go SDK) — not a plain Go program.** Chosen: REQUIRED to
   consume the `disaster37/dagger-library-go/image` module — its Go code is
   package `main` (module path `dagger/image`), not importable from a plain
   program, and Dagger exposes no dynamic-invocation API for typed consumption.
   The module's generated code under `dagger/gohookbridge/internal/` is
   committed (like the dependency's repo). Rejected: the plain
   `cmd/gohookbridge-dagger` + `internal/daggerpipeline` program.
2. **Build + push via `github.com/disaster37/dagger-library-go/image@2.0.19`.**
   Chosen: a published, pinned module avoids hand-rolling build + publish;
   its verified surface is recorded in §3a. Rejected: hand-rolling
   `DockerBuild`/`Publish` with the raw SDK.
3. **Orchestrate `WithBuildArg → Lint → Build → Sync → Push` manually, NOT the
   dependency's `Ci`.** Chosen: `Ci` cannot publish `:latest` and its push
   cannot be skipped per-tag; manual orchestration gives us `:latest` and the
   skip flags. Rejected: the dependency's one-shot `Ci`.
4. **Version = required `--version` function arg resolved by the LLM** (`git
   describe --tags --abbrev=0`, `v` stripped, fallback `dev`). Chosen: the
   module's source `Directory` has no `.git`, so resolution happens in the
   caller. The defensive `v`-trim + `dev` fallback remains as a unit-tested
   pure helper.
5. **Registry credentials = plain-string function args wrapped via
   `dag.SetSecret`.** Chosen: no dependency on dagger-CLI secret-arg syntax;
   secrets are passed as `*dagger.Secret` to the dependency's `Push` only when
   both are non-empty, and never logged. Rejected: CLI secret-arg flags.
6. **Root `go.mod` untouched.** Chosen: module isolation — `dagger/gohookbridge`
   is its own Go module; the root module never gains `dagger.io/dagger`.
   Rejected: adding `dagger.io/dagger` to the root module.
7. **LLM-invoked module, no Makefile, no CI workflow.** The user explicitly
   decided Dagger replaces the Makefile and the LLM is the caller; the pipeline
   is invoked via `dagger call -m dagger/gohookbridge ci ...`. Rejected:
   Makefile targets (user decision) and a GH Actions wrapper (LLM-only, user
   decision).
8. **Ephemeral k3s as a Dagger service + in-pipeline `registry:2` (mirror
   `registry:5000`).** Chosen: fully self-contained, runs identically locally
   and in CI, no host `kind`/`k3s` install, no chart change. The ephemeral
   cluster pulls the freshly built image from the in-pipeline registry (same
   bytes as the GHCR push) because GHCR packages are private by default.
   Rejected: (a) `kind` on the host — needs Docker-in-Docker; (b) in-cluster
   `imagePullSecrets` against GHCR — requires a chart change; (c) the "home"
   cluster — machine-specific, gitignored, not CI-safe.
9. **Single-replica, no-TLS smoke deployment.** Chosen: fastest deterministic
   validation of the feature (the pipeline itself). HA/mTLS is already covered
   by existing multi-node Raft tests. Rejected: 3-replica mTLS smoke (slower,
   flakier, not needed to prove the pipeline works).
10. **FUTURE (out of scope):** the same library ships `k3s/` and `helm/`
    modules that could later replace our hand-rolled cluster plumbing. Not
    adopted now — the current in-pipeline registry+k3s+helm flow is already
    validated.

## 12. Risks

- **`dagger develop` requires the dagger CLI + engine (Docker)** in the
  implementation environment. If unavailable, the coder hand-mirrors the
  generated structure and records that `dagger develop` must be re-run (never
  silently skipped).
- **Dependency-binding accessor spelling** must be verified via `go doc` (the
  module's `internal/dagger` package vs. the dependency's
  `dagger/image/internal/dagger` import — whichever `dagger develop` produced)
  and recorded in a doc comment.
- **Generated code under `dagger/gohookbridge/internal/` must be committed**;
  an uncommitted `internal/` breaks `dagger call` for other machines/CI.
- **Dagger engine bootstrap on the runner:** `dagger call` auto-downloads the
  CLI and starts the engine via Docker; if a runner lacks Docker, the call
  fails at connect (mitigated by the remediation hint +
  `_EXPERIMENTAL_DAGGER_RUNNER_HOST`).
- **Dockerfile `--platform=$BUILDPLATFORM` under Dagger:** mitigated by the
  global `ARG BUILDPLATFORM=linux/amd64` default (§2a); smoke-test on first run.
- **k3s registry mirror plumbing** (`registries.yaml` for `registry:5000`) is
  the most fragile piece; if containerd still refuses plain-HTTP, fall back to
  `--tls-san registry` + a TLS registry — recorded as the known mitigation.
- **Large dependency tree:** the module's `go.mod` grows with `dagger.io/dagger`
  + the `image` dependency; acceptable and expected, and isolated to the module
  (not the root `go.mod`).
