# Plan: Rename Project from `gosmee` to `gohookbridge`

## Summary

- **New name**: `gohookbridge`
- **Binary**: `gohookbridge`
- **Go module**: `github.com/webcenter-fr/gohookbridge`
- **Package**: `gohookbridge/` (replaces `gosmee/`)
- **License**: Apache 2.0 → MIT, with a `NOTICE` file preserving original attribution
- **GitHub owner**: `webcenter-fr` (replaces `chmouel`)

---

## Step 1: Rename the Core Go Package Directory & Files

### 1a. Rename directory + update package declarations

```bash
git mv gosmee/ gohookbridge/
```

Then in **all** `gohookbridge/*.go` files, replace:
- `package gosmee` → `package gohookbridge`

Files affected (23 files, all under `gohookbridge/`; `nats/` and `store/` sub-packages stay unchanged):
```
gohookbridge/version_test.go
gohookbridge/client_test.go
gohookbridge/auth_oidc.go
gohookbridge/random.go
gohookbridge/crypto.go
gohookbridge/replay.go
gohookbridge/protected_channels.go
gohookbridge/app.go
gohookbridge/interface.go
gohookbridge/crypto_test.go
gohookbridge/auth_test.go
gohookbridge/migrate.go
gohookbridge/app_test.go
gohookbridge/client.go
gohookbridge/replay_test.go
gohookbridge/hook_list_test.go
gohookbridge/flags.go
gohookbridge/auth.go
gohookbridge/protected_channels_test.go
gohookbridge/hook_list.go
gohookbridge/server.go
gohookbridge/server_test.go
gohookbridge/auth_config.go
```

### 1b. Update all Go import paths

In all `*.go` files, replace:
- `"github.com/chmouel/gosmee/gosmee/` → `"github.com/webcenter-fr/gohookbridge/gohookbridge/`
- `github.com/chmouel/gosmee` → `github.com/webcenter-fr/gohookbridge`

Files with imports to update:
- `main.go`: `gosmee "github.com/chmouel/gosmee/gosmee"` → `gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"`
- `gohookbridge/server.go` (line 23-24): import paths
- `gohookbridge/server_test.go` (line 23-25): import paths
- `gohookbridge/auth.go` (line 17): import path
- `gohookbridge/migrate.go` (line 9): import path
- `gohookbridge/protected_channels.go` (line 7): import path
- `gohookbridge/auth_config.go` (line 7): import path
- `gohookbridge/store/storetest/helper.go` (line 7): import path

---

## Step 2: Update `go.mod`

```diff
- module github.com/chmouel/gosmee
+ module github.com/webcenter-fr/gohookbridge
```

Then run:
```bash
go mod tidy
```

This regenerates `go.sum` automatically.

---

## Step 3: Rename Misc/Deployment Files

### 3a. Rename files

```bash
git mv misc/com.chmouel.gosmee.plist misc/com.webcenter-fr.gohookbridge.plist
git mv misc/gosmee-server-deployment.yaml misc/gohookbridge-server-deployment.yaml
git mv misc/gosmee-client-deployment.yaml misc/gohookbridge-client-deployment.yaml
git mv misc/gosmee-server.service misc/gohookbridge-server.service
git mv misc/gosmee.service misc/gohookbridge.service
git mv Formula/gosmee.rb Formula/gohookbridge.rb
```

### 3b. Update content in each file

**`misc/com.webcenter-fr.gohookbridge.plist`**:
Replace all `chmouel`, `gosmee` with `webcenter-fr`, `gohookbridge`.

**`misc/gohookbridge-server-deployment.yaml`**:
- Image: `ghcr.io/webcenter-fr/gohookbridge:main`
- All comment references: `gosmee` → `gohookbridge`
- All label/name references: `gosmee` → `gohookbridge`

**`misc/gohookbridge-client-deployment.yaml`**:
- Image: `ghcr.io/webcenter-fr/gohookbridge:main`
- All comment references: `gosmee` → `gohookbridge`
- All label/name references: `gosmee` → `gohookbridge`

**`misc/gohookbridge-server.service`**:
- `Description=Gohookbridge Server`
- `ExecStart=gohookbridge server`

**`misc/gohookbridge.service`**:
- `Description=Gohookbridge Forward`
- `ExecStart=gohookbridge client ...`

**`misc/README.md`**:
- All URLs: `chmouel/gosmee` → `webcenter-fr/gohookbridge`
- File names: `com.chmouel.gosmee.plist` → `com.webcenter-fr.gohookbridge.plist`, `gosmee.service` → `gohookbridge.service`
- Commands: `gosmee` → `gohookbridge`

**`Formula/gohookbridge.rb`**:
- Class name: `Gosmee` → `Gohookbridge`
- All `chmouel/gosmee` → `webcenter-fr/gohookbridge`
- All `$GOSMEE_` → `$GOHOOKBRIDGE_`
- Binary name: `gosmee` → `gohookbridge`

---

## Step 4: Update Build & CI Configuration

### 4a. `Makefile`

```diff
- NAME  := gosmee
+ NAME  := gohookbridge
```
Also fix hardcoded `gosmee` references at lines 36, 46 to use `$(NAME)`:
- Line 36: `@rm -rf $(OUTPUT_DIR)/gosmee` → `@rm -rf $(OUTPUT_DIR)/$(NAME)`
- Line 41: `-o $(OUTPUT_DIR)/gosmee main.go` → `-o $(OUTPUT_DIR)/$(NAME) main.go`

### 4b. `Dockerfile`

```diff
- COPY . /go/src/github.com/chmouel/gosmee
+ COPY . /go/src/github.com/webcenter-fr/gohookbridge
- WORKDIR /go/src/github.com/chmouel/gosmee
+ WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
- RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gosmee .
+ RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gohookbridge .
```
Replace:
- `useradd gosmee` → `useradd gohookbridge`
- `WORKDIR /home/gosmee` → `WORKDIR /home/gohookbridge`
- `ENTRYPOINT ["/usr/local/bin/gosmee"]` → `ENTRYPOINT ["/usr/local/bin/gohookbridge"]`

### 4c. `.goreleaser.yml`

```diff
- - /bin/sh -c "printf {{.Version }} > gosmee/templates/version"
+ - /bin/sh -c "printf {{.Version }} > gohookbridge/templates/version"
```

Replace ALL occurrences of:
- `binary: gosmee` → `binary: gohookbridge`
- `owner: chmouel` → `owner: webcenter-fr`
- `name: gosmee` → `name: gohookbridge` (brew repository name)
- `https://github.com/chmouel/gosmee` → `https://github.com/webcenter-fr/gohookbridge`
- `"gosmee - A webhook` → `"gohookbridge - A webhook`
- `install "gosmee" => "gosmee"` → `install "gohookbridge" => "gohookbridge"`
- `(bash_completion/"gosmee").write` → `(bash_completion/"gohookbridge").write`
- `(zsh_completion/"_gosmee").write` → `(zsh_completion/"_gohookbridge").write`
- `(fish_completion/"gosmee.fish").write` → `(fish_completion/"gohookbridge.fish").write`
- All binary references in `nfpms` and `aurs` sections: `gosmee` → `gohookbridge`
- `maintainer: Chmouel Boudjnah <chmouel@chmouel.com>` → keep or update as appropriate

### 4d. `default.nix`

```diff
- name = "gosmee-${version}";
+ name = "gohookbridge-${version}";
- printf ${version} > $sourceRoot/gosmee/templates/version
+ printf ${version} > $sourceRoot/gohookbridge/templates/version
- homepage = "https://github.com/chmouel/gosmee";
+ homepage = "https://github.com/webcenter-fr/gohookbridge";
- $out/bin/gosmee completion bash > $out/share/bash-completion/completions/gosmee
+ $out/bin/gohookbridge completion bash > $out/share/bash-completion/completions/gohookbridge
- $out/bin/gosmee completion zsh > $out/share/zsh/site-functions/_gosmee
+ $out/bin/gohookbridge completion zsh > $out/share/zsh/site-functions/_gohookbridge
```

---

## Step 5: Update Documentation

### 5a. `README.md`

Replace ALL occurrences of:
- `github.com/chmouel/gosmee` → `github.com/webcenter-fr/gohookbridge`
- `chmouel/gosmee` → `webcenter-fr/gohookbridge`
- `ghcr.io/chmouel/gosmee` → `ghcr.io/webcenter-fr/gohookbridge`
- `gosmee client` → `gohookbridge client`
- `gosmee server` → `gohookbridge server`
- `gosmee keygen` → `gohookbridge keygen`
- `gosmee replay` → `gohookbridge replay`
- `gosmee completion` → `gohookbridge completion`
- `brew tap chmouel/gosmee` → `brew tap webcenter-fr/gohookbridge`
- `brew install gosmee` → `brew install gohookbridge`
- `https://blog.chmouel.com/posts/gosmee-...` → update or remove
- Title `# gosmee` → `# gohookbridge`
- All standalone `Gosmee` → `Gohookbridge` (case-sensitive)
- `bin/gosmee` → `bin/gohookbridge`
- Blog post URLs: keep or update as appropriate
- Author info (lines 651-655): update or remove as appropriate

### 5b. `design.md`

- Title: `# Gosmee HA Webhook Relay` → `# Gohookbridge HA Webhook Relay`
- All shell command examples: `gosmee server` → `gohookbridge server`
- All `gosmee` in diagrams/text → `gohookbridge`

### 5c. `quickstart.md`

Replace ALL occurrences:
- `github.com/chmouel/gosmee` → `github.com/webcenter-fr/gohookbridge`
- `ghcr.io/chmouel/gosmee` → `ghcr.io/webcenter-fr/gohookbridge`
- `gosmee` as command → `gohookbridge`
- `gosmee` as image/service name → `gohookbridge`
- `bin/gosmee` → `bin/gohookbridge`

### 5d. `SECURITY.md`

```diff
- Please report security issues by opening a [GitHub issue](https://github.com/chmouel/gosmee/issues).
+ Please report security issues by opening a [GitHub issue](https://github.com/webcenter-fr/gohookbridge/issues).
```

---

## Step 6: Update Templates

### 6a. `gohookbridge/templates/index.tmpl`

- Line 7: `<title>Gosmee - Webhook Forwarder</title>` → `<title>Gohookbridge - Webhook Forwarder</title>`
- Line 639: `brew tap chmouel/gosmee https://github.com/chmouel/gosmee` → `brew tap webcenter-fr/gohookbridge https://github.com/webcenter-fr/gohookbridge`
- Line 640: `brew install gosmee` → `brew install gohookbridge`
- Line 642: `copyText('brew tap chmouel/gosmee ... && brew install gosmee')` → update
- Line 646: `href="https://github.com/chmouel/gosmee/...install` → `href="https://github.com/webcenter-fr/gohookbridge/...install`
- Line 668: `gosmee client {{ .URL }} http://localhost:8080` → `gohookbridge client {{ .URL }} http://localhost:8080`
- Line 669: `copyText('gosmee client ...` → `copyText('gohookbridge client ...`
- Line 691: `href="https://github.com/chmouel/gosmee/releases/v{{ .Version }}"` → `href="https://github.com/webcenter-fr/gohookbridge/releases/v{{ .Version }}"`
- Line 1080, 1084, 1161: `gosmee-replay-token` → `gohookbridge-replay-token`

### 6b. `gohookbridge/templates/admin.tmpl`

- Line 6: `<title>gosmee Admin</title>` → `<title>gohookbridge Admin</title>`
- Line 49: `<h1>gosmee Admin</h1>` → `<h1>gohookbridge Admin</h1>`

---

## Step 7: Update License

### 7a. Replace LICENSE

Replace the Apache 2.0 license with the MIT license.

### 7b. Create NOTICE file

Create a `NOTICE` file at the repository root with the original attribution:

```
gohookbridge
Copyright 2026 Webcenter

This product includes software derived from gosmee
(https://github.com/chmouel/gosmee), which was:

  Copyright 2023 Chmouel Boudjnah
  Licensed under the Apache License, Version 2.0

The original Apache 2.0 license terms can be found at:
http://www.apache.org/licenses/LICENSE-2.0
```

---

## Step 8: Update Remaining Files

### 8a. `.github/workflows/` (all yml files)

Search and replace `chmouel/gosmee` → `webcenter-fr/gohookbridge` and `gosmee` → `gohookbridge` in workflow files.

### 8b. `hack/generate-release-notes.sh`

Update author line if desired.

### 8c. `gohookbridge/client_test.go` line 161

```diff
- # Copyright 2023 Chmouel Boudjnah <chmouel@chmouel.com>
+ # Copyright 2026 Webcenter
```

### 8d. Check for any remaining hardcoded references

Run:
```bash
rg -i "chmouel/gosmee" --no-ignore
rg '"gosmee"' --no-ignore
```

Fix any remaining references found.

---

## Step 9: Verification

```bash
# 1. Build
make build

# 2. Run tests
go test ./...

# 3. Lint
make lint

# 4. Verify binary runs
./bin/gohookbridge --help

# 5. Check no remaining old references
rg "chmouel/gosmee" --no-ignore  # Should return nothing
```

---

## License Analysis

Apache 2.0 (section 4) permits creating derivative works under a different license. The original is a permissive license. Since you've significantly rewritten the codebase:

- **You CAN switch to MIT** — Apache 2.0 §4 explicitly allows "additional or different license terms" for derivative works.
- **You MUST retain attribution** — Apache 2.0 §4(c) requires retaining "all copyright, patent, trademark, and attribution notices from the Source form of the Work." This is satisfied by the `NOTICE` file created in Step 7b.
- **One caveat**: any original Apache-licensed files that remain mostly unchanged should theoretically keep their original copyright header. Since you say the codebase is fully rewritten, this is less of a concern, but the NOTICE file provides a safe harbor.

---

## Files NOT to Change

- `.gitignore` — no references to rename
- `go.sum` — auto-regenerated by `go mod tidy`
- `.vale/`, `.markdownlint.json`, `.yamllint`, `.pre-commit-config.yaml`, `.golangci.yml` — no references
- `gohookbridge/templates/favicon.svg`, `*.tmpl.bash`, `version` — no references
- `gohookbridge/nats/` — package name stays `nats` (no rename needed)
- `gohookbridge/store/` — package name stays `store` (no rename needed)
- `gohookbridge/store/storetest/` — package name stays `storetest`
