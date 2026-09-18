# Remove orphan files (gosmee / Vue / pre-Helm leftovers)

## 1. Objective & scope

**What "orphan" means here:** a file that is no longer used by the build, CI,
release, or documentation because it belongs to a previous identity/framework/
structure of this repository, OR a file whose reference has rotted so it can
never be consumed. This repo is a fork/rename of `gosmee` (smee.io tooling), and
its frontend moved from Vue 3 + Vite + Naive UI to Nuxt 4 + Nuxt UI, and its
K8s deployment moved from raw manifests to a Helm chart.

**In scope (delete):**
- Raw K8s `Deployment` manifests superseded by `helm/gohookbridge/`.
- `default.nix` (unreferenced Nix definition).
- Dead duplicate replay templates in `gohookbridge/templates/`.
- Old Vite build output `web/dist/` and old agent tooling `.kilo/`.

**In scope (fix, not delete):**
- `misc/replayview` — keep, but fix stale "gosmee" wording.

**In scope (config/doc edits):** `.gitignore`, `.dockerignore`, `Makefile`,
`README.md`, `quickstart.md`, `CONTRIBUTING.md`.

**Out of scope (see §7):** `gosmee_session` cookie rename, `GOSMEE_DEBUG_SERVICE`
env var rename, `README.md` Nix/nixpkgs section, full doc refresh, and any
runtime-behavior change.

**No runtime behavior changes.** All Go code changes are limited to deleting
templates that are not referenced by any `//go:embed` directive.

## 2. Method & evidence

**Tracked-status caveat:** this environment has no shell access, so tracked
status was inferred from `.gitignore` (a file not listed there is treated as
tracked/committed; a file listed there is untracked/gitignored). The coder MUST
confirm with `git ls-files` before running `rm -rf` (see §5 guard).

Evidence gathered by reading/grepping the repo:

- **`misc/replayview`** — tracked (not in `.gitignore`). Referenced in
  `README.md:704` and `:716`. Client still saves replay scripts
  (`--saveDir`, see `quickstart.md:86` and `gohookbridge/client/replay.go`).
  Decision: **KEEP + FIX** wording (`gosmee`→`gohookbridge`, 3 comment strings).
- **`misc/gohookbridge-server-deployment.yaml`** — tracked. Referenced in
  `README.md:171,179` and `quickstart.md:205,209`. Content is `kind: Deployment`,
  `replicas: 1`, `emptyDir` for Raft data — cannot express the current
  StatefulSet/HA/mTLS architecture. `AGENTS.local.md:33` mandates Helm.
  Decision: **DELETE** + rewrite docs to Helm.
- **`misc/gohookbridge-client-deployment.yaml`** — tracked. Referenced in
  `README.md:172,196` and `quickstart.md:262,268`. Same stale pattern.
  Decision: **DELETE** + rewrite docs to Helm.
- **`default.nix`** — tracked. `grep default.nix|nixpkgs|buildGoModule` finds
  only `README.md:126,130`, which reference the *external* `nixpkgs` package
  (`nix run nixpkgs#gohookbridge`), not this file. No CI/Makefile/goreleaser
  reference (`.goreleaser.yml` has `brews`/`nfpms`/`aurs`/`dockers`, no `nix`).
  Decision: **DELETE**.
- **`gohookbridge/templates/replay_script.tmpl.bash`** and
  **`gohookbridge/templates/replay_script.tmpl.httpie.bash`** — tracked. NOT
  embedded: `gohookbridge/client/client.go:36,39` embeds
  `templates/replay_script.tmpl.*`, which resolves to
  `gohookbridge/client/templates/` (go:embed is package-relative). The root
  copies are byte-identical to `gohookbridge/client/templates/*` (verified by
  reading both files). `app.go` embeds only `version`, `zsh_completion.zsh`,
  `bash_completion.bash`. Decision: **DELETE** (dead duplicates).
- **`web/dist/`** — untracked/gitignored (`.gitignore:12`, `.dockerignore:9`).
  Contains old Vite output (`index.html`, `200.html`, `404.html`, `admin/`,
  `channels/`, `login/`, `assets/`, `favicon.svg`, `logo.svg`). Nuxt outputs to
  `web/.output/` (`web/package.json` `build` = `nuxt generate && node
  scripts/copy-to-static.mjs`). Decision: **DELETE**.
- **`.kilo/`** — untracked/gitignored (`.gitignore:7`). Old Kilo Code agent
  tooling (`plans/` 30 files, `worktrees/alike-wolf/`). Superseded by
  `.opencode/`. Also referenced by `Makefile:5` exclusion (dead).
  Decision: **DELETE**.
- **`web/.nuxtrc`** — untracked/gitignored (`.gitignore:11`). Content
  `setups.@nuxt/test-utils="4.3.2"` matches current devDependency
  (`web/package.json` `@nuxt/test-utils ^4.3.2`). Auto-regenerated. NOT an
  orphan — **KEEP** (no action; keep the `.gitignore` entry).
- **Regenerable artifacts** — `bin/`, `raft-data/node1.db`, `web/.nuxt/`,
  `web/.output/`, `web/node_modules/`, `gohookbridge/web/static/` are current
  (not old-framework). Decision: **LEAVE in place** (they regenerate on
  `make web-build`/`make build`; `raft-data` is local dev state). Classified
  OPTIONAL-CLEAN, no action taken.

**Confirmed KEEP (verified, do not delete):**
- `Formula/gohookbridge.rb` — `.goreleaser.yml:72-88` (`brews`, `directory: Formula`).
- `Dockerfile.goreleaser` — `.goreleaser.yml:141,150,159,168` (4 docker builds).
- `hack/generate-release-notes.sh` — `.github/workflows/releaser.yaml:57`.
- `misc/gohookbridge-server.service`, `misc/gohookbridge.service` —
  `.goreleaser.yml:118,121` (nfpms). `misc/com.webcenter-fr.gohookbridge.plist`
  + `misc/README.md` — `misc/README.md` references the plist/service files.
- `gohookbridge/templates/favicon.svg` — `server/server.go:47`; `version`,
  `zsh_completion.zsh`, `bash_completion.bash` — `app.go:19,22,25`;
  `gohookbridge/client/templates/replay_script.tmpl.{bash,httpie.bash}` —
  `client/client.go:36,39`.
- `web/public/favicon.svg`, `web/public/logo.svg`, `web/vitest.config.ts`,
  `web/tsconfig.json`, `web/scripts/copy-to-static.mjs`, `web/package-lock.json`,
  `web/package.json`, `web/nuxt.config.ts`.
- `helm/gohookbridge/values-home.yaml` (gitignored, `AGENTS.local.md:44`),
  `AGENTS.local.md` (gitignored local guide).
- `design.md`, `quickstart.md`, `README.md`, `SECURITY.md`, `LICENSE`, `NOTICE`,
  `go.mod`, `go.sum`, `main.go`, `cmd/`, `gohookbridge/`, `web/app/`,
  `web/tests/`, `.github/`, `.vale/`, `.vale.ini`, `.markdownlint.json`,
  `.yamllint`, `.pre-commit-config.yaml`, `.golangci.yml`, `.goreleaser.yml`,
  `Dockerfile`, `Makefile`.
- `.opencode/plans/nuxt-migration.md`, `.opencode/plans/raft-ha-hardening.md`,
  `.opencode/package.json`, `.opencode/package-lock.json`,
  `.opencode/node_modules/` (gitignored by `.opencode/.gitignore`).
- `smee.io` references in `gohookbridge/flags.go:122`, `gohookbridge/app.go:126`
  (intentional input source) and `README.md:52` (background blog post).

## 3. Classification table

| Path | Tracked? | Category | Evidence | Action |
|---|---|---|---|---|
| `misc/replayview` | yes | KEEP + FIX | README:704,716; client `--saveDir` still current | Edit 3 `gosmee`→`gohookbridge` strings |
| `misc/gohookbridge-server-deployment.yaml` | yes | DELETE | README:171,179; quickstart:205,209; `kind: Deployment` + `emptyDir`, pre-Helm | `git rm` |
| `misc/gohookbridge-client-deployment.yaml` | yes | DELETE | README:172,196; quickstart:262,268; pre-Helm | `git rm` |
| `default.nix` | yes | DELETE | no references (README nixpkgs is external) | `git rm` |
| `gohookbridge/templates/replay_script.tmpl.bash` | yes | DELETE | not embedded (client embeds `client/templates/` copy) | `git rm` |
| `gohookbridge/templates/replay_script.tmpl.httpie.bash` | yes | DELETE | not embedded (dead duplicate) | `git rm` |
| `web/dist/` | no (gitignored) | DELETE | old Vite output; Nuxt uses `.output/` | `rm -rf` |
| `.kilo/` | no (gitignored) | DELETE | old Kilo agent tooling; superseded by `.opencode/` | `rm -rf` |
| `web/.nuxtrc` | no (gitignored) | KEEP | current `@nuxt/test-utils` 4.3.2 auto-gen | none |
| `bin/`, `raft-data/`, `web/.nuxt/`, `web/.output/`, `web/node_modules/`, `gohookbridge/web/static/` | no (gitignored) | OPTIONAL-CLEAN (leave) | current regenerable artifacts | none |
| `Formula/gohookbridge.rb`, `Dockerfile.goreleaser`, `hack/generate-release-notes.sh`, `misc/*.service`, `misc/*.plist`, `misc/README.md`, embedded templates (non-dup), `web/public/*`, `web/scripts/*`, `helm/*`, `.github/*`, `Dockerfile`, `Makefile`, all `*.md` (except edited sections) | yes | KEEP | referenced by goreleaser/workflows/embeds (see §2) | none |

## 4. Exact deletion list — tracked files

Run each `git rm` from the repo root. One command per file.

```shell
# Raw K8s Deployment manifest — superseded by helm/gohookbridge (AGENTS.local.md mandates Helm).
git rm misc/gohookbridge-server-deployment.yaml

# Raw K8s Deployment manifest — superseded by helm/gohookbridge.
git rm misc/gohookbridge-client-deployment.yaml

# Unreferenced Nix package definition (goreleaser handles brews/nfpms/aurs/dockers).
git rm default.nix

# Dead duplicate: client/client.go embeds gohookbridge/client/templates/replay_script.tmpl.bash instead.
git rm gohookbridge/templates/replay_script.tmpl.bash

# Dead duplicate: client/client.go embeds gohookbridge/client/templates/replay_script.tmpl.httpie.bash instead.
git rm gohookbridge/templates/replay_script.tmpl.httpie.bash
```

## 5. Exact deletion list — untracked/gitignored files

**Guard first:** confirm these are untracked (expected output is empty):

```shell
git ls-files web/dist .kilo
```

If the command prints anything, those paths ARE tracked — do not use `rm -rf`;
use `git rm -r <path>` instead and skip the `rm -rf` below.

```shell
# Old Vue/Vite SPA build output (Nuxt now outputs to web/.output/).
rm -rf web/dist

# Old Kilo Code agent tooling (superseded by .opencode/).
rm -rf .kilo
```

Optional hygiene (harmless; only relevant if `.kilo/worktrees/alike-wolf` was a
registered git worktree):

```shell
git worktree prune
```

## 6. Exact config/doc edits

### 6.1 `.gitignore`

Remove two lines (`.kilo/` and `web/dist`). Resulting file:

```text
bin/*
.envrc
result
tmp
raft-data/
node_modules/
AGENTS.local.md
web/.nuxt/
web/.output/
web/.nuxtrc
gohookbridge/web/static/
helm/gohookbridge/values-home.yaml
```

(Keep `web/.nuxtrc` — current test-utils artifact.)

### 6.2 `.dockerignore`

Remove the `web/dist` line. Resulting file:

```text
.git
.gitignore
bin
tmp
raft-data
web/node_modules
web/.nuxt
web/.output
gohookbridge/web/static
```

### 6.3 `Makefile`

Old (lines 1–5):

```makefile
NAME  := gohookbridge
TARGET_URL ?= http://localhost:8080
SMEE_URL ?= https://smee.io/new
IMAGE_VERSION ?= latest
MD_FILES := $(shell git ls-files '*.md' ':(exclude).vale/*' ':(exclude).kilo/*')
```

New (lines 1–2):

```makefile
NAME  := gohookbridge
MD_FILES := $(shell git ls-files '*.md' ':(exclude).vale/*')
```

`TARGET_URL`/`SMEE_URL`/`IMAGE_VERSION` are dead (no `$(...)` reference anywhere
in the Makefile or repo; the `SMEE_URL`/`TARGET_URL` hits in
`gohookbridge/client/*` are unrelated template/env strings).

### 6.4 `README.md` — replace the "Raw YAML manifests" section

Old (the whole block from the `#### Raw YAML manifests` heading through the line
`For detailed configuration options, please refer to the documentation comments in each deployment file.`):

```markdown
#### Raw YAML manifests

Two deployment configurations are available:

- [gohookbridge-server-deployment.yaml](./misc/gohookbridge-server-deployment.yaml) - For deploying the public-facing server component
- [gohookbridge-client-deployment.yaml](./misc/gohookbridge-client-deployment.yaml) - For deploying the client component that forwards to internal services

#### Server Deployment

The server deployment exposes a public webhook endpoint to receive incoming webhook events:

```shell
kubectl apply -f misc/gohookbridge-server-deployment.yaml
```

Key configuration:

- Set `--public-url` to your actual domain where the service will be exposed
- Configure an Ingress with TLS or use a service mesh for production use
- Set `--raft-dir` to a persistent volume for Raft data durability
- Add `--bootstrap-config-file` pointing to a ConfigMap or Secret with your bootstrap configuration
- For security, configure webhook signatures, allowed IPs, and auth via `bootstrap.yaml` or Admin UI
- Configure `behind_reverse_proxy` in global config when your Ingress is the sole path to gohookbridge and overwrites `X-Forwarded-For` / `X-Real-IP`; otherwise the allowlist can be bypassed by spoofed headers (see [SECURITY.md](./SECURITY.md#behind-a-reverse-proxy-safely))

#### Client Deployment

The client deployment connects to a gohookbridge server (either your own or smee.io) and forwards webhook events to internal services:

```shell
kubectl apply -f misc/gohookbridge-client-deployment.yaml
```

Key configuration:

- Adjust the first argument to your gohookbridge server URL or smee.io channel
- Change the second argument to your internal service URL (e.g., `http://service.namespace:8080`)
- The `--saveDir` flag enables saving webhook payloads to `/tmp/save` for later inspection

For detailed configuration options, please refer to the documentation comments in each deployment file.
```

New:

```markdown
The Helm chart is the only supported Kubernetes deployment path; raw manifests
were removed. It renders the server as a StatefulSet with Raft HA, mTLS,
per-pod PVCs, and a headless Service. Key values:

- `server.publicURL` — the public webhook endpoint
- `server.ingress` — Ingress with TLS (`hosts` / `tls`)
- `server.bootstrap.config` — admin user + session secret (bootstrap.yaml content)
- `server.storage.size` — per-pod Raft data PVC size
- `client.channelURL` / `client.targetURL` — client forwarding source/target

When your Ingress is the sole path to gohookbridge and overwrites
`X-Forwarded-For` / `X-Real-IP`, configure `behind_reverse_proxy` in the global
config; otherwise the allowlist can be bypassed by spoofed headers (see
[SECURITY.md](./SECURITY.md#behind-a-reverse-proxy-safely)).
```

### 6.5 `quickstart.md` — replace the raw-manifest "Server Deployment" + "Client Deployment" sections

Old (from the `### Server Deployment` heading through the end of the
`### Client Deployment` code fence — i.e. the block that ends with
`kubectl logs deployment/gohookbridge-client`):

```markdown
### Server Deployment

Deploy the gohookbridge server to receive webhooks from external sources:

```shell
# 1. Edit the public URL in misc/gohookbridge-server-deployment.yaml
#    Replace https://yourserver.example.com with your actual domain

# 2. Apply the deployment
kubectl apply -f misc/gohookbridge-server-deployment.yaml

# 3. Expose the service externally (example with an Ingress)
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gohookbridge-server
spec:
  rules:
  - host: webhook.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: gohookbridge-server
            port:
              number: 80
EOF

# 4. Verify the server is running
kubectl get pods -l app=gohookbridge-server
kubectl logs deployment/gohookbridge-server

# 5. Generate a channel and send a test webhook
CHANNEL=$(curl -s https://webhook.example.com/new)
curl -X POST "https://webhook.example.com/${CHANNEL##*/}" \
  -H "Content-Type: application/json" \
  -d '{"event": "test"}'
```

#### Bootstrap configuration

On first boot, you can initialize the Raft store with an admin user, projects, and global settings:

```shell
# Create a bootstrap ConfigMap
kubectl create configmap gohookbridge-bootstrap --from-file=bootstrap.yaml

# Edit the deployment to add:
#   --bootstrap-config-file /etc/gohookbridge/bootstrap.yaml
# and mount the ConfigMap at /etc/gohookbridge/
```

See the [README](./README.md#bootstrap-configuration) for the `bootstrap.yaml` format.

### Client Deployment

Deploy the gohookbridge client to relay webhooks from a server to an internal service:

```shell
# 1. Edit misc/gohookbridge-client-deployment.yaml
#    Replace the arguments:
#      - "https://yourserver.example.com/your-channel"  → your gohookbridge server or smee.io URL
#      - "http://your-internal-service.namespace:8080"   → your internal service URL

# 2. Apply the deployment
kubectl apply -f misc/gohookbridge-client-deployment.yaml

# 3. Verify
kubectl get pods -l app=gohookbridge-client
kubectl logs deployment/gohookbridge-client
```
```

New:

```markdown
### Server and client (Helm)

The server and client are deployed with the Helm chart (see the "Kubernetes
with Helm" and "High Availability with Helm" sections above). The chart renders
the server as a StatefulSet and exposes all configuration through values:

- `server.publicURL`, `server.ingress` — public endpoint + Ingress/TLS
- `server.bootstrap.config` — admin user, projects, and global settings
  (bootstrap.yaml content, stored in a Secret)
- `client.channelURL` / `client.targetURL` — client forwarding source/target

See [`helm/gohookbridge/values.yaml`](./helm/gohookbridge/values.yaml) for the
full option list, and the [README](./README.md#bootstrap-configuration) for the
`bootstrap.yaml` format.
```

### 6.6 `CONTRIBUTING.md`

Old (line 72):

```text
├── misc/                       # Deployment manifests, systemd units
```

New:

```text
├── misc/                       # System service files (systemd/launchd) + replay helper
```

(No other CONTRIBUTING.md tree change is required by this cleanup; the stale
tree entries listed in the task are out of scope for this PR.)

### 6.7 `misc/replayview` — fix stale identity wording (3 comment/help strings)

Old → New:

1. `# generated by the gosmee. It supports fuzzy finding, previewing event`
   → `# generated by gohookbridge. It supports fuzzy finding, previewing event`
2. `$(basename $(readlink -f $0)) - view gosme replay files`
   → `$(basename $(readlink -f $0)) - view gohookbridge replay files`
3. `produced by gosmee. It uses fzf for fuzzy finding and provides previews of`
   → `produced by gohookbridge. It uses fzf for fuzzy finding and provides previews of`

Leave the `Author: Chmouel Boudjnah <chmouel@chmouel.com> - @chmouel` line
(accurate attribution, not a project-name reference).

## 7. Out-of-scope items

- **`gohookbridge/server/auth.go:20` `sessionCookieName = "gosmee_session"`** —
  renaming changes the session cookie name, invalidating all existing browser
  sessions. Not a file removal. Follow-up (separate PR, requires user approval):
  rename to `gohookbridge_session` and bump/accept a session-logout window.
- **`GOSMEE_DEBUG_SERVICE`** env var in the replay templates
  (`gohookbridge/client/templates/replay_script.tmpl.*`) and
  **`GOSMEE_URL`/`GOSMEE_TARGET_URL`** in `gohookbridge/client/command.go` and
  `replay.go` — renaming changes runtime behavior/env contract. Out of scope.
- **`README.md:124-131` "Nix/NixOS" section** — references the external
  `nixpkgs` package, not the deleted `default.nix`; no dangling reference.
  Full doc refresh (including verifying whether `gohookbridge` is really in
  nixpkgs) is out of scope.
- **Full CONTRIBUTING.md structure-tree refresh** (missing `server/k8s.go`,
  `store/migrate.go`, `store/tokens.go`, etc.) — out of scope; only the `misc/`
  line changes above.
- **`quickstart.md` inline HA StatefulSet manifest (lines ~275-408)** — it's
  inline documentation (not a file) and does not reference any deleted file;
  out of scope.

## 8. Branch & PR steps

Per AGENTS.md, start from the default branch `main`.

```shell
git checkout main
git pull --ff-only
git checkout -b fix/remove-orphan-files

# ... apply the deletions (§4, §5) and edits (§6) ...

git add -A
git commit -m "chore: remove orphan files and stale gosmee/Vite/pre-Helm references"
git push -u origin fix/remove-orphan-files
```

Open the PR:

```shell
gh pr create \
  --base main \
  --head fix/remove-orphan-files \
  --title "chore: remove orphan files and stale gosmee/Vite/pre-Helm references" \
  --body "## Summary
Removes files left over from the gosmee fork/rename, the Vue→Nuxt migration, and the pre-Helm K8s era:
- Delete raw K8s Deployment manifests (\`misc/gohookbridge-*-deployment.yaml\`) superseded by the Helm chart.
- Delete unreferenced \`default.nix\`.
- Delete dead duplicate replay templates in \`gohookbridge/templates/\` (client embeds its own copies).
- Delete old Vite output \`web/dist/\` and old agent tooling \`.kilo/\`.
- Fix stale 'gosmee' wording in \`misc/replayview\`.
- Update \`.gitignore\`, \`.dockerignore\`, \`Makefile\`, \`README.md\`, \`quickstart.md\`, \`CONTRIBUTING.md\` accordingly.

## Verification
- \`make web-build\` then \`make lint\`, \`make test\`, \`make build\`, \`make build-all\` all pass (mirrors CI)."
```

**Fallback if `gh` is unavailable/unauthenticated:** leave the branch pushed and
report the exact title/body above for manual PR creation via the GitHub UI.

## 9. Verification checklist

Run in this exact order (matches CI: `.github/workflows/go.yml` runs
`make lint-go`; `cd web && npm ci && npm run typecheck && npm test`;
`make build`; `make test`). **`make web-build` MUST run first** because
`gohookbridge/web/static/` is gitignored and `go build`/`go test`/`golangci-lint`
fail on `//go:embed static` if it is absent.

```shell
# 0. Baseline: ensure only intended changes exist
git status

# 1. Regenerate the embedded SPA (creates gohookbridge/web/static/)
make web-build

# 2. Lint (Go + Markdown + Nuxt typecheck)
make lint

# 3. Test (rebuilds web, runs frontend tests, then `go test -v ./...`)
make test

# 4. Build the full binary (web-build + go build, includes static guard)
make build

# 5. Build all three binaries
make build-all

# 6. Confirm only intended changes
git status
```

Notes on possibly-missing local tools:
- `golangci-lint` (required by `make lint-go`) — `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.
- `markdownlint` (required by `make lint-md`) — `npm install -g markdownlint-cli`. If unavailable, run `cd web && npm run typecheck` and `golangci-lint run ./...` separately; CI's markdown check is not part of `go.yml` (only `make lint-go` is).
- `vale` — the Makefile only runs vale when `docs/content` exists, which it does not here; skipped.
- Go 1.25+, Node.js 22+, npm 10+.

## 10. Risks & rollback

- **Dangling doc link** — mitigated: every deleted file's references (§6) are
  updated in the same commit. `make lint-md` (markdownlint) and a final `grep`
  catch leftovers. Re-check with:
  `grep -rn "gohookbridge-server-deployment\|gohookbridge-client-deployment\|default.nix\|web/dist\|\.kilo" --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=.opencode`
  (expect only the `.opencode/plans/nuxt-migration.md` historical `templates/server-deployment.yaml` hit, which is a plan archive and intentionally kept).
- **Embed regression** — the deleted `gohookbridge/templates/replay_script.*`
  are not referenced by any `//go:embed`; `go build`/`go test` in §9 prove it.
- **`.kilo` was actually tracked** — the §5 guard (`git ls-files web/dist .kilo`)
  catches this; if it prints paths, use `git rm -r` instead of `rm -rf`.
- **Stale worktree entry** — `git worktree prune` (§5) cleans up if
  `.kilo/worktrees/alike-wolf` was registered.
- **Rollback** — `git revert <commit>` or `git reset --hard origin/main`
  (untracked deletions of `web/dist`/`.kilo` are regenerable and unrecoverable
  from git, but they are gitignored build/agent artifacts and safe to lose).

## 11. Definition of done

- [ ] `misc/gohookbridge-server-deployment.yaml`, `misc/gohookbridge-client-deployment.yaml`, `default.nix`, `gohookbridge/templates/replay_script.tmpl.bash`, `gohookbridge/templates/replay_script.tmpl.httpie.bash` removed from git.
- [ ] `web/dist/` and `.kilo/` removed from disk and from `.gitignore`/`.dockerignore` (and Makefile `.kilo` exclusion).
- [ ] `misc/replayview` still present with `gosmee`/`gosme` wording fixed.
- [ ] `.gitignore`, `.dockerignore`, `Makefile`, `README.md`, `quickstart.md`, `CONTRIBUTING.md` edited exactly as specified.
- [ ] No remaining references: `grep -rn` for the deleted paths returns nothing (except the historical plan archive).
- [ ] `make web-build`, `make lint`, `make test`, `make build`, `make build-all` all pass locally.
- [ ] `git status` shows only the intended changes; no behavior-affecting code changed.
- [ ] Branch `fix/remove-orphan-files` pushed; PR opened against `main` (or PR title/body reported if `gh` unavailable).

## 12. Addendum — corrections applied during review

This plan predates three corrections that were applied in the same commit
(`29e9cab`) or during its review. They are recorded here so the archive matches
the actual change:

- **`gohookbridge/templates/favicon.svg` — DELETED** (the plan's §2/§3 listed it
  as KEEP). It is a byte-identical duplicate of the live, embedded
  `gohookbridge/server/templates/favicon.svg` (`server/server.go:47`). The plan
  mis-attributed the `//go:embed templates/favicon.svg` directive to the root
  copy; `go:embed` is package-relative, so the root copy was dead.
- **`gohookbridge/client/templates/version` — DELETED** (omitted from the plan).
  It is a byte-identical duplicate of the live, embedded
  `gohookbridge/templates/version` (`app.go:19`). The client package does not
  embed a `version` file.

Both deletions are covered by the same verification as the rest of the cleanup
(`make build`/`make test` prove no `//go:embed` target was lost).

- **`web/dist` — KEEP the `.gitignore`/`.dockerignore` entries** (the plan's
  §6.1/§6.2 removed them). `web/dist` is not a dead Vite artifact: Nuxt's
  `symlinkDist()` (`@nuxt/nitro-server`) recreates it as a symlink to
  `.output/public` on every `nuxt generate` when `nitro.options.static` is set
  (which `nuxt.config.ts` does). Removing the ignore entries made `git status`
  dirty after every `make web-build` and let the symlink leak into the Docker
  build context. The entries were restored; the stale symlink itself was still
  deleted (it regenerates).
- **`Makefile` — exclude `.opencode/*` from `MD_FILES`** (the plan removed the
  `.kilo/*` exclusion). The committed plan file
  `.opencode/plans/remove-orphan-files.md` is tracked and failed markdownlint,
  breaking `make lint` and the pre-commit `lint-markdown` hook. The exclusion
  mirrors the removed `.kilo/*` one, since `.opencode/` is agent tooling rather
  than project documentation.
