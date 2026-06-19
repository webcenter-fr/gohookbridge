# Plan: Split into Multiple Binary Targets

## Goal

Create 3 build targets from the same repository and Go module, producing smaller focused binaries:

| Binary | Roles | Estimated size | Heavy deps |
|---|---|---|---|
| `gohookbridge` | server + client + proxy + replay + keygen | ~26MB (unchanged) | all |
| `gohookbridge-client` | client + replay | ~3-4MB | sse, mapstructure, backoff, go-github |
| `gohookbridge-proxy` | proxy + produce + keygen | ~2-3MB | only stdlib + urfave/cli |

## Current Problem

All code lives in a single `gohookbridge/` Go package. Any binary that imports it compiles **all** files, including `server.go` which pulls in `hashicorp/raft`, `bbolt`, `nats-server`, `go-chi`, and the embedded Vue SPA (~20MB+ of dead weight for client/proxy binaries).

## Solution: Restructure into Sub-packages

Move role-specific code into sub-packages so each `cmd/` binary only imports what it needs.

### New Directory Structure

```
cmd/
  gohookbridge/              # Full binary (all roles)
    main.go
  gohookbridge-client/       # Client-only binary
    main.go
  gohookbridge-proxy/        # Proxy/produce/keygen-only binary
    main.go

gohookbridge/                # Shared package (LIGHTWEIGHT - no heavy deps)
  app.go                     # MakeApp(), getLogger(), getNewHookURL(), completion, keygen
  app_test.go
  crypto.go                  # NaCl box encryption (shared)
  crypto_test.go
  encryption.go              # AES encryption (shared)
  encryption_test.go
  flags.go                   # All CLI flags (shared, just data)

gohookbridge/server/         # Server sub-package (heavy deps)
  command.go                 # Command() *cli.Command
  server.go                  # HTTP server, handlers, EventBroker
  server_test.go
  auth.go                    # Session + RBAC
  auth_test.go
  auth_oidc.go               # OIDC
  auth_config.go             # Auth config migration stub
  protected_channels.go      # Protected channels migration stub
  protected_channels_test.go
  migrate.go                 # Config migration
  templates/
    favicon.svg

gohookbridge/client/         # Client sub-package
  command.go                 # Command() *cli.Command, ReplayCommand() *cli.Command
  client.go                  # SSE client, goSmee, replayDataOpts, serveHealthEndpoint
  client_test.go
  replay.go                  # GitHub replay
  replay_test.go
  hook_list.go               # Hook/delivery listing
  hook_list_test.go
  interface.go               # GHOp interface
  templates/
    version
    replay_script.tmpl.bash
    replay_script.tmpl.httpie.bash

gohookbridge/proxy/          # Proxy sub-package (lightweight)
  command.go                 # Command() *cli.Command, ProduceCommand() *cli.Command
  proxy.go                   # HTTP encrypting proxy
  produce.go                 # CLI producer
  produce_test.go

gohookbridge/web/            # Web sub-package (embeds SPA)
  handler.go                 # SPAHandler() http.Handler
  static/                    # Vue.js build output (gitignored, embedded at build time)

gohookbridge/store/          # UNCHANGED
gohookbridge/nats/           # UNCHANGED
gohookbridge/store/storetest/ # UNCHANGED
```

### Dependency Graph (no circular imports)

```
cmd/gohookbridge        → gohookbridge + server + client + proxy + web
cmd/gohookbridge-client → gohookbridge + client
cmd/gohookbridge-proxy  → gohookbridge + proxy

server  → gohookbridge (crypto) + store + nats + web
client  → gohookbridge (crypto, encryption)
proxy   → gohookbridge (crypto)
web     → (stdlib only)
```

## Implementation Steps

### Phase 1: Create sub-package directories and move files

1. Create directories:
   - `cmd/gohookbridge/`
   - `cmd/gohookbridge-client/`
   - `cmd/gohookbridge-proxy/`
   - `gohookbridge/server/`
   - `gohookbridge/client/`
   - `gohookbridge/proxy/`
   - `gohookbridge/web/` (already exists as dir with `static/`)

2. Move server files to `gohookbridge/server/`:
   - `server.go` → `gohookbridge/server/server.go`
   - `server_test.go` → `gohookbridge/server/server_test.go`
   - `auth.go` → `gohookbridge/server/auth.go`
   - `auth_test.go` → `gohookbridge/server/auth_test.go`
   - `auth_oidc.go` → `gohookbridge/server/auth_oidc.go`
   - `auth_config.go` → `gohookbridge/server/auth_config.go`
   - `protected_channels.go` → `gohookbridge/server/protected_channels.go`
   - `protected_channels_test.go` → `gohookbridge/server/protected_channels_test.go`
   - `migrate.go` → `gohookbridge/server/migrate.go`
   - Copy `gohookbridge/templates/favicon.svg` → `gohookbridge/server/templates/favicon.svg`

3. Move client files to `gohookbridge/client/`:
   - `client.go` → `gohookbridge/client/client.go`
   - `client_test.go` → `gohookbridge/client/client_test.go`
   - `replay.go` → `gohookbridge/client/replay.go`
   - `replay_test.go` → `gohookbridge/client/replay_test.go`
   - `hook_list.go` → `gohookbridge/client/hook_list.go`
   - `hook_list_test.go` → `gohookbridge/client/hook_list_test.go`
   - `interface.go` → `gohookbridge/client/interface.go`
   - Copy template files:
     - `gohookbridge/templates/version` → `gohookbridge/client/templates/version`
     - `gohookbridge/templates/replay_script.tmpl.bash` → `gohookbridge/client/templates/replay_script.tmpl.bash`
     - `gohookbridge/templates/replay_script.tmpl.httpie.bash` → `gohookbridge/client/templates/replay_script.tmpl.httpie.bash`

4. Move proxy files to `gohookbridge/proxy/`:
   - `proxy.go` → `gohookbridge/proxy/proxy.go`
   - `produce.go` → `gohookbridge/proxy/produce.go`
   - `produce_test.go` → `gohookbridge/proxy/produce_test.go`

5. Move web handler:
   - Create `gohookbridge/web/handler.go` with `package web` and `SPAHandler()` function
   - The `//go:embed` path changes from `web/static/*` to `static/*`
   - Remove old `gohookbridge/web.go`

6. Keep in root `gohookbridge/` package:
   - `app.go` (refactored to export `MakeApp()`)
   - `app_test.go`
   - `crypto.go` + `crypto_test.go`
   - `encryption.go` + `encryption_test.go`
   - `flags.go`
   - Shell completion templates stay in `gohookbridge/templates/`

### Phase 2: Update package declarations and imports

For each moved file:

1. **`gohookbridge/server/*.go`**: Change `package gohookbridge` → `package server`
   - Add import: `gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"`
   - Prefix all crypto calls: `IsEncrypted` → `gohookbridge.IsEncrypted`, `Encrypt` → `gohookbridge.Encrypt`, `AESEncrypt` → `gohookbridge.AESEncrypt`, `ParsePublicKey` → `gohookbridge.ParsePublicKey`, `GenerateKeyPair` → `gohookbridge.GenerateKeyPair`, `EncodePublicKey` → `gohookbridge.EncodePublicKey`
   - Import `gohookbridge/web` for `web.SPAHandler()`
   - Update `//go:embed templates/favicon.svg` path (stays the same relative to new location)
   - `store` and `nats` imports stay the same (already sub-packages)

2. **`gohookbridge/client/*.go`**: Change `package gohookbridge` → `package client`
   - Add import: `gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"`
   - Prefix crypto calls: `LoadKeyPair` → `gohookbridge.LoadKeyPair`, `EncodePublicKey` → `gohookbridge.EncodePublicKey`, `IsEncrypted` → `gohookbridge.IsEncrypted`, `Decrypt` → `gohookbridge.Decrypt`, `IsAESEncrypted` → `gohookbridge.IsAESEncrypted`, `AESDecrypt` → `gohookbridge.AESDecrypt`
   - Update `//go:embed` paths: `templates/version` stays same relative, `templates/replay_script.tmpl.bash` stays same relative
   - Export types/functions needed by `command.go`: `goSmee` → `GoSmee`, `replayDataOpts` → `ReplayDataOpts`, `clientSetup` → `ClientSetup` (or keep unexported and use Command() pattern)
   - Export `serveHealthEndpoint` → `ServeHealthEndpoint` (or keep internal to command.go)

3. **`gohookbridge/proxy/*.go`**: Change `package gohookbridge` → `package proxy`
   - Add import: `gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"`
   - Prefix crypto calls: `ParsePublicKey` → `gohookbridge.ParsePublicKey`, `LoadKeyPair` → `gohookbridge.LoadKeyPair`, `Encrypt` → `gohookbridge.Encrypt`

4. **`gohookbridge/web/handler.go`**: `package web`
   - `//go:embed static/*` (relative to new location)
   - Export `SPAHandler()` (already exported)

### Phase 3: Create Command() functions

Each sub-package exports a `command.go` file that creates CLI commands:

**`gohookbridge/server/command.go`**:
```go
package server

func Command() *cli.Command {
    return &cli.Command{
        Name:  "server",
        Usage: "Make gohookbridge a relay server from your external webhook",
        Action: func(c *cli.Context) error {
            if !isatty.IsTerminal(os.Stdout.Fd()) {
                ansi.DisableColors(true)
            }
            return serve(c)
        },
        Flags: gohookbridge.ServerFlags,
        Subcommands: []*cli.Command{
            {
                Name:   "migrate-config",
                Usage:  "Migrate deprecated environment variables to a bootstrap.yaml config",
                Action: func(_ *cli.Context) error { return migrateConfig(nil) },
            },
        },
    }
}
```

**`gohookbridge/client/command.go`**:
```go
package client

func Command() *cli.Command {
    // The client command action (currently inline in app.go lines 148-233)
    // moves here. It creates goSmee struct and calls clientSetup().
    return &cli.Command{
        Name:      "client",
        Usage:     "Make a client from the relay server to your local service",
        UsageText: "gohookbridge client [command options] SMEE_URL LOCAL_SERVICE_URL",
        Action:    clientAction,
        Flags:     append(gohookbridge.CommonFlags, gohookbridge.ClientFlags...),
    }
}

func ReplayCommand() *cli.Command {
    return &cli.Command{
        Name:   "replay",
        Usage:  "Replay payloads from GitHub",
        Action: func(c *cli.Context) error { return replay(c) },
        Flags:  append(gohookbridge.CommonFlags, gohookbridge.ReplayFlags...),
    }
}
```

**`gohookbridge/proxy/command.go`**:
```go
package proxy

func Command() *cli.Command {
    return &cli.Command{
        Name:      "proxy",
        Usage:     "Start an HTTP server that encrypts incoming webhooks and forwards them to a gohookbridge channel",
        UsageText: "gohookbridge proxy --pubkey <key> --listen :9090 --target <server-url>/<channel>",
        Action:    func(c *cli.Context) error { return startProxy(c) },
        Flags:     append(gohookbridge.CommonFlags, gohookbridge.ProxyFlags...),
    }
}

func ProduceCommand() *cli.Command {
    return &cli.Command{
        Name:      "produce",
        Usage:     "Encrypt and send a webhook payload to a gohookbridge channel",
        UsageText: "gohookbridge produce --pubkey <key> <server-url>/<channel> [payload-file]",
        Action:    func(c *cli.Context) error { return produce(c) },
        Flags:     append(gohookbridge.CommonFlags, gohookbridge.ProduceFlags...),
    }
}
```

### Phase 4: Refactor `gohookbridge/app.go`

Transform `app.go` to export `MakeApp()` that accepts commands as variadic args:

```go
package gohookbridge

func MakeApp(commands ...*cli.Command) *cli.App {
    app := &cli.App{
        Name:                   "gohookbridge",
        Usage:                  "...",
        EnableBashCompletion:   true,
        Version:                strings.TrimSpace(string(Version)),
        Flags:                  CommonFlags,
        Commands:               commands,
    }
    return app
}
```

Also export:
- `KeygenCommand() *cli.Command` (uses shared crypto.go)
- `CompletionCommands() []*cli.Command` (zsh, bash, fish)
- Flags as exported vars: `CommonFlags`, `ServerFlags`, `ClientFlags`, `ReplayFlags`, `ProduceFlags`, `ProxyFlags`, `KeygenFlags`
- `Version` variable (extract from client.go, move embed to app.go or a new version.go)

Remove from `app.go`:
- All inline command actions (moved to sub-package command.go files)
- `makeapp()` function (replaced by `MakeApp()`)
- `Run()` function (moved to cmd/ binaries)

### Phase 5: Create `cmd/` entry points

**`cmd/gohookbridge/main.go`** (full binary):
```go
package main

import (
    "log"
    "os"

    gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
    "github.com/webcenter-fr/gohookbridge/gohookbridge/client"
    "github.com/webcenter-fr/gohookbridge/gohookbridge/proxy"
    "github.com/webcenter-fr/gohookbridge/gohookbridge/server"
)

func main() {
    app := gohookbridge.MakeApp(
        server.Command(),
        client.Command(),
        client.ReplayCommand(),
        proxy.Command(),
        proxy.ProduceCommand(),
        gohookbridge.KeygenCommand(),
    )
    app.Commands = append(app.Commands, gohookbridge.CompletionCommands()...)
    if err := app.Run(os.Args); err != nil {
        log.Fatal(err)
    }
}
```

**`cmd/gohookbridge-client/main.go`** (client-only):
```go
package main

import (
    "log"
    "os"

    gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
    "github.com/webcenter-fr/gohookbridge/gohookbridge/client"
)

func main() {
    app := gohookbridge.MakeApp(
        client.Command(),
        client.ReplayCommand(),
        gohookbridge.KeygenCommand(),
    )
    app.Commands = append(app.Commands, gohookbridge.CompletionCommands()...)
    if err := app.Run(os.Args); err != nil {
        log.Fatal(err)
    }
}
```

**`cmd/gohookbridge-proxy/main.go`** (proxy-only):
```go
package main

import (
    "log"
    "os"

    gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
    "github.com/webcenter-fr/gohookbridge/gohookbridge/proxy"
)

func main() {
    app := gohookbridge.MakeApp(
        proxy.Command(),
        proxy.ProduceCommand(),
        gohookbridge.KeygenCommand(),
    )
    app.Commands = append(app.Commands, gohookbridge.CompletionCommands()...)
    if err := app.Run(os.Args); err != nil {
        log.Fatal(err)
    }
}
```

### Phase 6: Update build infrastructure

**`main.go`** (root): Delete. Entry points are now in `cmd/`.

**`Makefile`** changes:
```makefile
# Update existing build target
$(OUTPUT_DIR)/$(NAME): FORCE
    go build $(FLAGS) -o $@ ./cmd/gohookbridge

# Add new targets
build-client:
    go build $(FLAGS) -o $(OUTPUT_DIR)/gohookbridge-client ./cmd/gohookbridge-client

build-proxy:
    go build $(FLAGS) -o $(OUTPUT_DIR)/gohookbridge-proxy ./cmd/gohookbridge-proxy

build-all: build build-client build-proxy

# Update dev-server
dev-server:
    reflex -r '.*\.(tmpl|go)' -s go run ./cmd/gohookbridge -- server --footer "..."
```

**`.goreleaser.yml`** changes:
```yaml
builds:
  - id: gohookbridge
    main: ./cmd/gohookbridge
    binary: gohookbridge
    # ... existing config
  - id: gohookbridge-client
    main: ./cmd/gohookbridge-client
    binary: gohookbridge-client
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags: [-w, -s]
  - id: gohookbridge-proxy
    main: ./cmd/gohookbridge-proxy
    binary: gohookbridge-proxy
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags: [-w, -s]
```

**`Dockerfile`** changes:
```dockerfile
# Update build command
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gohookbridge ./cmd/gohookbridge
```

### Phase 7: Fix tests

1. Update test package declarations in moved test files
2. Update test imports to use the new package paths
3. `storetest/` package is unchanged (already a sub-package)
4. Run `go test ./...` to verify all tests pass

### Phase 8: Update documentation

1. **`CONTRIBUTING.md`**: Update project structure diagram, build commands, dev workflow
2. **`AGENTS.md`**: No changes needed (already references sub-packages)
3. **`README.md`**: Add section about the 3 binary targets and when to use each

### Phase 9: Verify

1. `make build` → produces `bin/gohookbridge` (~26MB)
2. `make build-client` → produces `bin/gohookbridge-client` (~3-4MB)
3. `make build-proxy` → produces `bin/gohookbridge-proxy` (~2-3MB)
4. `make test` → all tests pass
5. `make lint` → no lint errors
6. `cd web && npx vue-tsc --noEmit` → TypeScript passes
7. Verify each binary works:
   - `bin/gohookbridge server --dev-admin`
   - `bin/gohookbridge-client --help` (should NOT show server commands)
   - `bin/gohookbridge-proxy --help` (should NOT show server/client commands)

## Risk Assessment

- **High risk**: This is a large refactoring touching every file. Must be done carefully with compilation checks after each phase.
- **Mitigation**: Run `go build ./...` after each phase to catch issues early.
- **Backward compatibility**: The full `gohookbridge` binary behaves identically. Only the build infrastructure changes.
- **No breaking changes**: CLI flags, commands, and behavior remain the same.
