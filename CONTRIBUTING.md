# Contributing to gohookbridge

## Project structure

```
.
├── cmd/                         # Binary entry points
│   ├── gohookbridge/            # Full binary (server + client + proxy + keygen)
│   ├── gohookbridge-client/     # Client-only binary (client + replay)
│   └── gohookbridge-proxy/      # Proxy-only binary (proxy + produce + keygen)
├── gohookbridge/               # Shared package (LIGHTWEIGHT - no heavy deps)
│   ├── app.go                  # MakeApp(), GetLogger(), KeygenCommand(), CompletionCommands()
│   ├── crypto.go               # NaCl box encryption helpers
│   ├── encryption.go           # AES-256-GCM encryption helpers
│   ├── flags.go                # Shared CLI flag definitions (exported)
│   ├── uuid.go                 # UUID generation (shared)
│   ├── server/                 # Server sub-package (heavy deps: Raft, NATS, chi)
│   │   ├── command.go          # Server CLI command definition
│   │   ├── server.go           # HTTP server, SSE, webhook handlers
│   │   ├── auth.go             # Session + RBAC authentication
│   │   ├── auth_oidc.go        # OIDC authentication
│   │   ├── ratelimit.go        # IP rate limiter + ban tracker + middlewares
│   │   ├── migrate.go          # Config migration helper
│   │   └── templates/          # Server-specific embedded templates
│   ├── client/                 # Client sub-package
│   │   ├── command.go          # Client + replay CLI command definitions
│   │   ├── client.go           # SSE client that forwards to local service
│   │   ├── replay.go           # GitHub API webhook replay
│   │   ├── hook_list.go        # Hook/delivery listing
│   │   ├── interface.go        # GitHub API abstraction (GHOp interface)
│   │   └── templates/          # Client-specific embedded templates
│   ├── proxy/                  # Proxy sub-package (lightweight)
│   │   ├── command.go          # Proxy + produce CLI command definitions
│   │   ├── proxy.go            # HTTP encrypting proxy
│   │   └── produce.go          # CLI encrypted webhook producer
│   ├── web/                    # Web sub-package (embeds SPA)
│   │   ├── handler.go          # SPAHandler() http.Handler
│   │   └── static/             # Vite build output (gitignored, embedded at build time)
│   ├── nats/                   # Embedded NATS broker + ring buffer
│   ├── store/                  # Raft + BoltDB persistence layer
│   │   ├── raft.go             # Raft cluster setup
│   │   ├── bolt.go             # BoltDB log/stable store
│   │   ├── fsm.go              # Raft FSM applying config commands
│   │   ├── api.go              # Public API: projects, users, RBAC, global config
│   │   ├── acl.go              # Access control checks
│   │   ├── bridge.go           # Protected channels abstraction
│   │   ├── bootstrap.go        # First-boot config bootstrapping
│   │   ├── types.go            # Shared data types
│   │   └── storetest/          # Test helpers for store setup
│   └── templates/              # Shell completions, version
├── web/                        # Vue.js 3 frontend (SPA admin UI)
│   ├── src/
│   │   ├── api/                # HTTP client wrappers
│   │   ├── components/         # Reusable Vue components
│   │   ├── router/             # Vue Router config
│   │   ├── stores/             # Pinia state stores
│   │   └── views/              # Route-level page components
│   ├── vite.config.ts          # Vite build + dev proxy config
│   └── tsconfig.json
├── main.go                     # Program entrypoint (full binary)
├── Makefile                    # Build, test, lint targets (3 binary targets)
├── Dockerfile                  # Multi-stage container build
├── misc/                       # Deployment manifests, systemd units
└── hack/                       # Release helper scripts
```

## Local development

### Prerequisites

- Go 1.25+
- Node.js 20+ and npm 9+
- `make` (GNU Make)
- `golangci-lint` (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`)
- `markdownlint` (optional, for markdown linting: `npm install -g markdownlint-cli`)

### First time setup

```shell
git clone https://github.com/webcenter-fr/gohookbridge
cd gohookbridge
go mod tidy
```

### Running the backend (hot reload)

The Makefile includes a `dev-server` target that uses `reflex` to rebuild and restart the Go server when files change:

```shell
# Install reflex once
go install github.com/cespare/reflex@latest

# Start the backend with live reload
make dev-server
```

This starts the server on `http://localhost:3333` with the admin UI disabled (no embedded assets during dev). Start the UI dev server separately as described below.

To run without hot reload for one-off debugging:

go run ./cmd/gohookbridge server --address 0.0.0.0 --port 8081
```

To auto-create an admin user on first boot (no bootstrap config needed), add the `--dev-admin` flag:

go run ./cmd/gohookbridge server --dev-admin
```

On first start with no users, this creates an `admin` user and writes the password to `raft-data/admin-password.txt`. Use `--dev-admin-password` to set a specific password instead of a random one.

### Running the UI (hot reload)

The Vue.js SPA runs with Vite's dev server, which proxies API calls to the Go backend:

```shell
cd web
npm install
npm run dev          # starts at http://localhost:5173
```

The Vite dev proxy (configured in `web/vite.config.ts`) forwards these paths to `http://localhost:3333`:

| Frontend path | Proxied to backend |
|---|---|
| `/api/*` | `http://localhost:3333` |
| `/events/*` | `http://localhost:3333` |
| `/auth/*` | `http://localhost:3333` |
| `/login` | `http://localhost:3333` |
| `/logout` | `http://localhost:3333` |

**Recommended workflow:** open two terminals — one for `make dev-server` (backend), one for `cd web && npm run dev` (UI). Work against `http://localhost:5173` in the browser; the SPA is served from Vite and API calls are proxied to the Go backend.

### Building everything

```shell
make build
# Output: bin/gohookbridge
```

This runs in order:
1. `cd web && npm ci && npm run build` — produces `gohookbridge/web/static/`
2. `go build -o bin/gohookbridge ./cmd/gohookbridge` — embeds the UI and links the binary

The repository also produces two smaller, focused binaries:

```shell
make build-client   # Client-only: client + replay (~12MB stripped)
make build-proxy    # Proxy-only: proxy + produce + keygen (~9MB stripped)
make build-all      # All three binaries
```

### Testing the client

To test webhook forwarding end-to-end:

```shell
# Terminal 1: start a local echo server
python3 -m http.server 8080

./bin/gohookbridge server --address 0.0.0.0 --port 8081

./bin/gohookbridge client "$CHANNEL" http://localhost:8081

# Terminal 4: send a test webhook
curl -X POST "$CHANNEL" \
  -H "Content-Type: application/json" \
  -d '{"hello":"world"}'
```

## Backend conventions

### Code style

- Follow standard Go idioms as enforced by `golangci-lint` with the project's `.golangci.yml` configuration.
- Run `make fmt` (go fmt) or `make fumpt` (gofumpt with extra rules) before committing.
- Run `make lint-go` before opening a PR — the CI runs the same command.
- Keep functions short and focused. Extract helpers when a function exceeds ~50 lines.
- Prefer explicit error handling over panics. Never ignore returned errors without a comment explaining why.
- Use `gotest.tools/v3/assert` for test assertions (already used across the codebase).
- Name test helpers with the `t.Helper()` call at the top.
- Avoid package-level mutable state and `init()` functions. Configuration should be passed explicitly.
- **JSON API responses must return empty slices (`[]`) instead of `null`.** When a function returns a slice that will be JSON-encoded, initialize it with `make([]T, 0)` or `[]T{}` rather than `var s []T` (which produces `null` when nil). Go's `encoding/json` encodes nil slices as `null`, causing frontend `TypeError: Cannot read properties of null` errors when templates iterate over the response. Apply the same nil-to-empty conversion at the call site before `writeJSON`/`json.Encode` when consuming a slice from external code.

### Package organization

- **`gohookbridge/` root** — CLI setup, HTTP handlers, webhook logic. Kept flat intentionally; extract sub-packages only when the group of types has a clear standalone responsibility.
- **`gohookbridge/store/`** — Persistence and Raft consensus. The `store.API` interface is the public contract; implementations live in separate files (`bolt.go`, `raft.go`, `fsm.go`).
- **`gohookbridge/server/`** — Server logic: HTTP server, SSE, webhook handlers, auth. Depends on `store`, `nats`, and `web` sub-packages.
- **`gohookbridge/client/`** — Client logic: SSE client, GitHub replay, hook listing. Depends on shared `gohookbridge/` for crypto.
- **`gohookbridge/proxy/`** — Proxy logic: encrypting HTTP proxy and CLI producer. Depends on shared `gohookbridge/` for crypto.
- **`gohookbridge/web/`** — SPA static asset embedding. No Go code other than the `embed` directive and file server.
- **`cmd/`** — Binary entry points. Each binary only imports the sub-packages it needs.

### Adding a new CLI flag

Flags are defined in `gohookbridge/flags.go` and shared between the `server` and `client` subcommands. Add new flags there; avoid duplicating flag definitions across commands.

### Adding a new HTTP route

The server router is assembled in `gohookbridge/server/server.go`. Add new routes there and implement handlers in the same package or extract them into focused files (e.g., `gohookbridge/server/auth_oidc.go` for OIDC-related handlers).

### Rate limiting and ban patterns

`gohookbridge/server/ratelimit.go` contains three components:

- **`rateLimiter`** — Sliding window counter per IP (`map[string][]time.Time`). Exposes `allow(ip, maxRequests, windowSeconds) bool`. Used by `rateLimitMiddleware`.
- **`banTracker`** — Credential failure counter with deduplication per IP. Tracks unique credential fingerprints (SHA-256 hashes) to distinguish misconfiguration from attacks. Exposes `recordFailure(ip, fingerprint)`, `banIfSuspicious(ip, maxUnique, banDuration)`, `isBanned(ip)`, `listBans()`, and `unban(ip)`.
- **Middlewares** — `banMiddleware` (403 if banned) and `rateLimitMiddleware` (429 if over limit). Both use `getRealIP()` for proxy-aware IP resolution.

The middlewares are wired into both `mainRouter` and `restrictedRouter` via chi's `Use()` method in `serve()`.

### Channel access token authentication

Channel access tokens are passed as a URL query parameter (`?token=xxx`) rather than an HTTP header. This is a deliberate design choice forced by GitHub webhooks: GitHub only allows configuring a webhook URL, with no ability to set custom headers. The same `--token` query-param approach is used consistently across the client, proxy, and produce CLI tools for uniformity.

For SSE consumers, the token is also accepted via the `Authorization: Bearer` header as an alternative, but the query parameter remains the primary mechanism.

### Encryption vs Access Control

Gohookbridge separates **access control** from **encryption**:

- **Access control** determines who can connect to a channel:
  - Browser UI: Session-based auth with RBAC permission checks (`channel:read`)
  - CLI clients: Channel access tokens with scope validation (`consume`/`produce`)

- **Encryption** determines the data format:
  - **No encryption**: Plaintext data
  - **Server-side encryption** (`encryption_mode: "server_side"`): Server decrypts before sending
  - **E2E encryption** (`encryption_mode: "e2e"`): Server relays encrypted data as-is; client decrypts

The SSE endpoint (`GET /events/{channel}`) checks access control but does not enforce encryption rules. E2E channels can be accessed by any authorized user; the encrypted data is relayed as-is. Client-side decryption is optional and performed in the browser using the private key.

## UI conventions

### Technology stack

- **Framework:** Vue 3 (Composition API, `<script setup lang="ts">`)
- **State management:** Pinia
- **Routing:** Vue Router 4
- **Component library:** Naive UI
- **Language:** TypeScript (strict mode)

### Component patterns

- Prefer `<script setup lang="ts">` for all `.vue` files.
- Keep components small and single-responsibility. Extract reusable pieces into `web/src/components/`.
- Use Pinia stores for shared state. Each store in `web/src/stores/` covers one domain (auth, channels, events).
- API calls go through `web/src/api/client.ts`. Wrap all fetch calls there rather than calling `fetch` directly from components.
- Views are route-level page components in `web/src/views/`.
- Use Naive UI components for forms, buttons, tables, and layout. Do not mix in other UI libraries.

### TypeScript

- `vue-tsc --noEmit` runs as part of `npm run build` and in CI. Ensure your code passes type-checking: `cd web && npx vue-tsc --noEmit`.
- Define interfaces for API response types rather than using `any`.

### Styling

- Rely on Naive UI's built-in theme and components for consistency.
- If custom styles are needed, use `<style scoped>` in `.vue` files.

## Testing

### Running tests

```shell
make test
# equivalent to: go test -v ./...
```

### Running coverage

```shell
make html-coverage
# generates tmp/c.out and opens an HTML coverage report in the browser
```

### Test structure

Tests live alongside the code they test, following Go convention:

```
gohookbridge/
├── server.go → server_test.go
├── crypto.go → crypto_test.go
├── store/
│   ├── bolt.go   → bolt_test.go
│   ├── raft.go   → raft_test.go
│   └── fsm.go    → fsm_test.go
```

### Test patterns

The project uses `testing` stdlib with `gotest.tools/v3/assert` for assertions. Follow these patterns:

**Table-driven tests** (for exhaustive edge-case coverage):

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "hello", "HELLO", false},
        {"empty input", "", "", false},
        {"special chars", "a+b", "A+B", false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Feature(tt.input)
            if tt.wantErr {
                assert.ErrorContains(t, err, "expected substring")
                return
            }
            assert.NilError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

**HTTP handler tests** (use `httptest` and chi route context):

```go
func TestHandler(t *testing.T) {
    t.Run("success", func(t *testing.T) {
        req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/path", bodyReader)
        req.Header.Set("Content-Type", "application/json")

        // Inject chi URL params
        rctx := chi.NewRouteContext()
        rctx.URLParams.Add("channel", "test-channel")
        req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

        w := httptest.NewRecorder()
        handler(w, req)

        assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)
    })
}
```

**Sub-tests with `t.Run`** (group related scenarios):

```go
func TestEventBroker(t *testing.T) {
    t.Run("Subscribe and Publish", func(t *testing.T) { ... })
    t.Run("Multiple Subscribers", func(t *testing.T) { ... })
    t.Run("Encrypted Subscribers Receive Ciphertext", func(t *testing.T) { ... })
}
```

**Helper functions** (mark with `t.Helper()`):

```go
func newNatsBroker(t *testing.T, port int) *nats.Broker {
    t.Helper()
    b, err := nats.New(nats.Config{...})
    assert.NilError(t, err)
    t.Cleanup(func() { b.Shutdown() })
    return b
}
```

### Store tests

Store tests use `storetest.NewRaftStore(t)` to create an in-memory Raft-backed store. This helper automatically bootstraps a single-node Raft cluster with a temporary BoltDB — no external dependencies needed.

```go
rs := storetest.NewRaftStore(t)
assert.NilError(t, rs.CreateProject(&store.Project{ID: "test"}))
```

### NATS broker tests

NATS integration tests start an embedded NATS server with unique ports per test to avoid conflicts:

```go
broker := newNatsBroker(t, 4242)  // unique port per test
historical, live := broker.Subscribe("channel", 10)
err := broker.Publish("channel", []byte(`{"key":"value"}`))
```

### Guidelines for good coverage

- **Unit tests:** every new exported function should have at least one test. Test both the happy path and the most common error paths.
- **Edge cases:** test empty inputs, invalid inputs, boundary values, and concurrency where applicable.
- **HTTP handlers:** test all status codes the handler can return (2xx, 4xx, 5xx). Test with and without required headers.
- **Store operations:** test create, read, update, delete, and the "not found" case.
- **Do not test** Go standard library behavior, third-party library internals, or trivial getters/setters.

### Pre-commit

The project uses `pre-commit` hooks configured in `.pre-commit-config.yaml`. Install them with:

```shell
pre-commit install --hook-type pre-push
```

On each push, the hooks run linting and tests automatically. You can also run them manually:

```shell
pre-commit run --all-files
```

## Code quality

| Tool | Command | What it checks |
|---|---|---|
| `golangci-lint` | `make lint-go` | 50+ linters (config in `.golangci.yml`) |
| `go fmt` | `make fmt` | Standard Go formatting |
| `gofumpt` | `make fumpt` | Stricter Go formatting |
| `markdownlint` | `make lint-md` | Markdown style (part of `make lint`) |
| `vue-tsc` | `cd web && npx vue-tsc --noEmit` | TypeScript type checking |
| `pre-commit` | `pre-commit run` | Combined hooks on push |

Run `make lint` before opening a PR — it runs both Go and Markdown linting.

## Pull request process

1. Fork the repository and create a feature branch.
2. Make your changes following the conventions above.
3. Write or update tests to cover your changes.
4. Run `make lint` and `make test` locally.
5. If you changed the UI, run `cd web && npx vue-tsc --noEmit`.
6. Ensure `make build` succeeds (this validates the full Go + UI build pipeline).
7. Open a PR against the `main` branch with a clear description of the change.

The CI workflow (`.github/workflows/go.yml`) runs lint, test, and build on every push and PR. All three must pass before merging.
