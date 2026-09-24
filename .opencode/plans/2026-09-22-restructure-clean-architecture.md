# Restructure gohookbridge to 2026 Clean Architecture

Issue #15 — "feat: restructure the project"
Blog reference: https://reintech.io/blog/go-project-structure-2026-clean-architecture-best-practices

> Work happens on a new branch `feat/restructure-clean-architecture` from the default branch
> (`main`). Do NOT open a PR. This document is a plan only; the coder executes it verbatim.

---

## 0. Decisions already made (do not re-litigate)

| Decision | Choice |
|---|---|
| Refactor depth | **Full clean architecture (deep)** — strict dependency inversion. Handlers never import the repository package. Domain defines repository interfaces; repository implements them. |
| New top-level dirs | **Minimal + document** — `scripts/` (rename `hack/`), `tests/` (integration + fixtures skeleton), `api/` (placeholder `openapi.yaml`). `config/` and `migrations/` are **documented-only** (no new Go config package, no SQL migration framework). |
| `context.Context` | **Add `context.Context`** as the first parameter to every repository-interface and service method. |
| CLI plumbing home | `internal/app` (package `app`) — MakeApp, flags, completions, keygen, logger. (Avoids a package literally named `cli`, which would collide with `github.com/urfave/cli/v2`.) |
| Composition root home | `internal/server` (package `server`) — the ONLY package that imports `repository` + `service` + `handler` + `nats`. `cmd/` becomes thin wrappers. |
| Naming clash avoidance | `pkg/urlutil` (not `url`), `pkg/uuid` (not `github.com/google/uuid` usage), `pkg/crypto` (stdlib `crypto` root is never imported unaliased in this codebase). |

---

## 1. Goal & scope

### Goal
Restructure the Go backend from the current `gohookbridge/…` flat/shared package layout to the
2026 clean-architecture layout (blog structure), with strict dependency direction
`handler → service → domain`, `repository → domain`, and the domain package defining the
repository interfaces. Update `CONTRIBUTING.md` to document and enforce the new structure.
Keep the Nuxt frontend working unchanged in behavior. All tests and CI green.

### In scope
1. New directory skeleton: `internal/{domain,service,repository,handler,server,app,client,proxy,web}`, `pkg/{crypto,encryption,uuid,urlutil,nats}`, `api/`, `scripts/`, `tests/{integration,fixtures}`.
2. Extract domain entities + sentinel errors + repository interfaces into `internal/domain`.
3. Move Raft/BoltDB/FSM persistence into `internal/repository` (implements `domain.Repository`).
4. Extract business logic into `internal/service` (single application `Service` type depending only on `domain`).
5. Move HTTP handlers/middleware into `internal/handler` (depends on `service` + `domain`, never `repository`).
6. Move composition root `serve()` into `internal/server` (wires everything; only importer of all layers).
7. Move reusable code to `pkg/`; CLI framework to `internal/app`; client/proxy to `internal/client`/`internal/proxy`; SPA embed to `internal/web`.
8. Add `context.Context` to all repository/service method signatures.
9. Introduce domain sentinel errors (`ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidArgument`) and switch handlers from string-matching to `errors.Is`.
10. Rename `hack/` → `scripts/`; add `api/openapi.yaml` placeholder; add `tests/integration/` + `tests/fixtures/` with one black-box integration test + fixtures.
11. Rewrite `CONTRIBUTING.md` project-structure + package-organization sections; update `Makefile`, `Dockerfile`, `Dockerfile.goreleaser` (no change needed — verified), `.goreleaser.yml`, `.github/workflows/*`, `web/scripts/copy-to-static.mjs`, `.gitignore`, `.dockerignore`, and docs (`README.md`, `quickstart.md`, `design.md`) that reference old paths.
12. Delete the redundant root `main.go` (duplicate of `cmd/gohookbridge/main.go`).

### Explicitly out of scope
- Generating a complete OpenAPI spec (only a placeholder `api/openapi.yaml`).
- A new `config/` Go package (config model stays CLI flags + Raft-stored `bootstrap.yaml`).
- A `migrations/` framework or SQL migrations (in-code migrations remain).
- Behavioral changes other than error mapping via sentinel errors (no new features, no logic rewrites beyond what the split requires).
- Frontend feature changes (only the `copy-to-static.mjs` output path + any doc references change).

---

## 2. Target directory tree (backend)

Module path: `github.com/webcenter-fr/gohookbridge`

```text
.
├── cmd/                              # thin entrypoints — DI wiring only
│   ├── gohookbridge/
│   │   └── main.go                   # app.MakeApp(server.Command(), client.Command(), …)
│   ├── gohookbridge-client/
│   │   └── main.go
│   └── gohookbridge-proxy/
│       └── main.go
├── internal/
│   ├── domain/                       # entities + value types + sentinel errors + repository interfaces (NO deps on other internal pkgs)
│   │   ├── types.go                  # Channel, User, Role, Permission, OIDCProvider, RoleMapping, ChannelRoleMapping, ClientCursor, UserBinding, MigrateChannel
│   │   ├── config.go                 # GlobalConfig, ServerConfig, DefaultChannelConfig, DefaultGlobalConfig(), ResolveChannelConfig()
│   │   ├── auth.go                   # AuthConfig, InternalConfig, InternalUser, OIDCConfig
│   │   ├── errors.go                 # ErrNotFound, ErrAlreadyExists, ErrInvalidArgument
│   │   └── repository.go             # ChannelRepository, UserRepository, RBACRepository, ConfigRepository, Repository, ChannelChangeNotifier
│   ├── repository/                   # persistence: Raft + BoltDB + FSM — implements domain.Repository
│   │   ├── raft.go                   # RaftStore, RaftConfig, lifecycle + CRUD (ctx + domain errors)
│   │   ├── bolt.go                   # boltLogStore, boltStableStore, Snapshot, FSM data helpers
│   │   ├── fsm.go                    # FSM, fsmCommand, bootstrap payloads
│   │   ├── raft_discovery.go         # RaftPeer, PeerResolver, resolvers, PodSANs
│   │   ├── raft_streamlayer.go       # hostAddr, plain/tls/retrying stream layers
│   │   ├── raft_tls.go               # RaftTLSConfig, LoadOrBuildRaftTLS, MintingCA
│   │   ├── bootstrap.go              # BootstrapConfig, LoadBootstrap, ApplyBootstrap, IsIPAllowed
│   │   ├── migrate.go                # MigrateRBAC (in-code data migration)
│   │   └── storetest/                # test helper (NewRaftStore, SetupProtectedChannels, …)
│   │       └── helper.go
│   ├── service/                      # business logic — depends ONLY on domain + pkg/*
│   │   ├── service.go                # Service struct, NewService(repo domain.Repository, notifier domain.ChannelChangeNotifier)
│   │   ├── repository.go             # CRUD passthrough (delegates to domain.Repository)
│   │   ├── channel.go                # ResolveChannel*, ProtectedChannels
│   │   ├── auth.go                   # BuildAuthConfig, ValidatePassword, CreateDevAdmin, IsSetupMode
│   │   ├── authorization.go          # UserHasPermission, UserChannels, IsAdmin, GetUserPermissions, HasChannelRole
│   │   ├── token.go                  # GenerateAccessToken, HashToken, CreateAccessToken, DeleteAccessToken, ValidateChannelToken
│   │   ├── session.go                # SessionToken, deriveSessionSecret, encodeSession, decodeSession, generateRandomHex
│   │   ├── ratelimit.go              # RateLimiter, BanTracker, BanInfo, fingerprints, recordCredentialFailure
│   │   └── events.go                 # EventBroker, Subscriber (legacy in-memory broker; kept for its tests)
│   ├── handler/                      # HTTP/gRPC/MQ adapters — depends on service + domain + pkg/*
│   │   ├── api.go                    # RegisterAPIHandlers, admin CRUD handlers, DTOs, writeJSON/writeError/sanitize/writeCSV/validateStruct
│   │   ├── webhook.go                # handleWebhookPost, handleTestPayloadSend, handleEventReplay, handleGenerateEncryptionKey, signature validators
│   │   ├── events.go                 # handleEventsGet, sseLoop, retVersion, retReadyz, retStartup
│   │   ├── auth.go                   # cookies, RequireAuth, RequireAuthDynamic, LogoutHandler, apiLogin/Logout/AuthMethods
│   │   ├── oidc.go                   # OIDCHandler + handlers
│   │   ├── middleware.go             # RequirePermission, RequireChannelACLPermission, SetupMode/AdaptiveAuth, ipRestrict, channelAccess, context keys
│   │   ├── ratelimit.go              # banMiddleware, rateLimitMiddleware, apiBansHandler, apiUnbanHandler
│   │   ├── leader_forward.go         # leaderForwardMiddleware, leaderInfo interface
│   │   └── logging.go                # safeLogger, safeLogFormatter
│   ├── server/                       # composition root — imports repository + service + handler + nats + app
│   │   ├── server.go                 # Server struct, NewServer, Run (old serve()), applyBootstrapOnce, watchSingleNodeRecovery, brokerTTLNotifier, initDevAdmin
│   │   ├── command.go                # server.Command()
│   │   ├── migrate.go                # migrateConfig (migrate-config subcommand)
│   │   ├── k8s.go                    # newK8sClientset, statefulSetReplicaReader
│   │   ├── raft_generation.go        # raftGenerationStore
│   │   └── raft_tls.go               # buildRaftTLSConfig, effectiveNamespace
│   ├── app/                          # CLI framework (flags, MakeApp, logger, completions, keygen)
│   │   ├── app.go
│   │   ├── flags.go
│   │   └── templates/                # version, zsh_completion.zsh, bash_completion.bash
│   ├── client/                       # client + replay (unchanged logic, moved)
│   │   ├── client.go, command.go, interface.go, replay.go, hook_list.go
│   │   └── templates/                # replay_script.tmpl.{bash,httpie.bash}
│   ├── proxy/                        # proxy + produce (moved)
│   │   ├── proxy.go, command.go, produce.go
│   └── web/                          # SPA go:embed shim
│       ├── handler.go
│       └── static/                   # gitignored, regenerated by make build
├── pkg/                              # public reusable code only
│   ├── crypto/                       # crypto.go — NaCl box (GenerateKeyPair, Encrypt, Decrypt, …)
│   ├── encryption/                   # encryption.go — AES-256-GCM
│   ├── uuid/                         # uuid.go — GenerateUUID
│   ├── urlutil/                      # url.go — URLWithQueryParam
│   └── nats/                         # broker.go, buffer.go — embedded NATS + ring buffer
├── api/
│   └── openapi.yaml                  # placeholder (full spec out of scope)
├── config/                           # NOT created — documented-only in CONTRIBUTING.md
├── migrations/                       # NOT created — documented-only (in-code migrations)
├── scripts/
│   └── generate-release-notes.sh     # moved from hack/
├── tests/
│   ├── integration/
│   │   └── server_integration_test.go  # //go:build integration — black-box end-to-end
│   └── fixtures/
│       ├── bootstrap.yaml             # sample bootstrap fixture
│       └── webhook-payload.json       # sample webhook body
├── web/                              # Nuxt 4 frontend (unchanged; copy-to-static.mjs dst updated)
├── Makefile, Dockerfile, Dockerfile.goreleaser, .goreleaser.yml, .golangci.yml, .github/, .gitignore, .dockerignore
```

---

## 3. Exhaustive relocation table

Legend: `→` = move (rename); `SPLIT →` = contents distributed across multiple new files.
Test files move alongside the package that owns the symbols they test. When a test file covers
symbols that now live in multiple packages, it is split accordingly.

### 3.1 Root

| Old path | Action | New path |
|---|---|---|
| `main.go` | DELETE | — (duplicate of `cmd/gohookbridge/main.go`) |

### 3.2 `gohookbridge/` (root package `gohookbridge`)

| Old | New |
|---|---|
| `app.go` | `internal/app/app.go` (package `app`; `Version`, `GetLogger`, `GetNewHookURL`, `KeygenCommand`, `CompletionCommands`, `MakeApp`, `Run`) |
| `flags.go` | `internal/app/flags.go` (all `*Flags` vars + consts) |
| `crypto.go` | `pkg/crypto/crypto.go` (package `crypto`) |
| `encryption.go` | `pkg/encryption/encryption.go` (package `encryption`) |
| `uuid.go` | `pkg/uuid/uuid.go` (package `uuid`) |
| `url.go` | `pkg/urlutil/url.go` (package `urlutil`) |
| `templates/version` | `internal/app/templates/version` |
| `templates/zsh_completion.zsh` | `internal/app/templates/zsh_completion.zsh` |
| `templates/bash_completion.bash` | `internal/app/templates/bash_completion.bash` |
| `app_test.go` | `internal/app/app_test.go` |
| `flags_test.go` | `internal/app/flags_test.go` |
| `crypto_test.go` | `pkg/crypto/crypto_test.go` |
| `encryption_test.go` | `pkg/encryption/encryption_test.go` |

### 3.3 `gohookbridge/store/` (package `store`) — the god-object, split by layer

| Old | New | Notes |
|---|---|---|
| `types.go` | SPLIT → `internal/domain/types.go` + `internal/domain/config.go` | entities→types.go; `GlobalConfig`/`ServerConfig`/`DefaultChannelConfig`/`defaultGlobalConfig`→`DefaultGlobalConfig()`/`resolveChannelConfig`→`ResolveChannelConfig`→config.go; `migrateChannel`→`MigrateChannel`; delete `MigrateChannelForTest`; `GlobalConfigResponse`/`ServerConfigResponse` move to `internal/handler/api.go` |
| `api.go` | → `internal/handler/api.go` | `apiHandler` now holds `*service.Service`; `RegisterAPIHandlers(r chi.Router, svc *service.Service)`; `GlobalConfigResponse`/`ServerConfigResponse` land here; `ChannelChangeNotifier`→`internal/domain/repository.go` |
| `acl.go` | SPLIT → `internal/service/authorization.go` + `internal/handler/middleware.go` | business (`UserHasPermission`, `UserChannels`, `IsAdmin`, `GetUserPermissions`, `HasChannelRole` + helpers)→service; middleware (`RequirePermission`, `RequireChannelACLPermission`, `SetupModeMiddleware`, `AdaptiveAuthMiddleware`) + context keys (`UsernameContextKey`, `GroupsContextKey`, `contextKeyChannelID`, `GetUsernameFromContext`, `GetGroupsFromContext`)→handler |
| `bridge.go` | SPLIT → `internal/service/channel.go` + `internal/service/auth.go` | `ProtectedChannels`+`NewProtectedChannels*`→channel.go; `BuildAuthConfig`→auth.go; `AuthConfig`/`InternalConfig`/`InternalUser`/`OIDCConfig`→`internal/domain/auth.go` |
| `tokens.go` | → `internal/service/token.go` | `GenerateAccessToken`/`HashToken` pure funcs; `CreateAccessToken`/`DeleteAccessToken`/`ValidateChannelToken` become `Service` methods |
| `migrate.go` | → `internal/repository/migrate.go` | `MigrateRBAC(ctx) error` |
| `bootstrap.go` | → `internal/repository/bootstrap.go` | `LoadBootstrap`, `ApplyBootstrap(ctx,…)`, `IsIPAllowed`, `GetDefaultRoles` |
| `raft.go` | → `internal/repository/raft.go` | `RaftStore` (CRUD + lifecycle); CRUD methods gain `ctx`; `ResolveChannel*`/`SessionSecret`/`SetSessionSecret`/`ResolveCORS*`/`ResolveBehind*`/`ResolveFooter`/`IsSetupMode`/`CreateDevAdmin`/`CreateAccessToken`/`DeleteAccessToken`/`ValidateChannelToken`/`BuildAuthConfig` REMOVED (→ service); CRUD now returns `domain.*` + `domain.Err*` |
| `bolt.go` | → `internal/repository/bolt.go` | unchanged |
| `fsm.go` | → `internal/repository/fsm.go` | `fsmBootstrapPayload` references `domain.GlobalConfig`/`domain.Channel`/`domain.User` |
| `raft_discovery.go` | → `internal/repository/raft_discovery.go` | unchanged |
| `raft_streamlayer.go` | → `internal/repository/raft_streamlayer.go` | unchanged |
| `raft_tls.go` | → `internal/repository/raft_tls.go` | unchanged |
| `storetest/helper.go` | → `internal/repository/storetest/helper.go` (package `storetest`) | `store.Channel`→`domain.Channel`, `store.GlobalConfig`→`domain.GlobalConfig`, `store.RaftConfig`→`repository.RaftConfig`, `store.NewRaftStore`→`repository.NewRaftStore` |
| `api_test.go` | → `internal/handler/api_test.go` | rewrite to construct `service.NewService(storetest.NewRaftStore(t), nil)` |
| `acl_test.go` | SPLIT → `internal/service/authorization_test.go` + `internal/handler/middleware_test.go` | |
| `bolt_test.go` | → `internal/repository/bolt_test.go` | |
| `raft_test.go` | → `internal/repository/raft_test.go` | |
| `fsm_test.go` | → `internal/repository/fsm_test.go` | |
| `raft_discovery_test.go` | → `internal/repository/raft_discovery_test.go` | |
| `raft_streamlayer_test.go` | → `internal/repository/raft_streamlayer_test.go` | |
| `raft_tls_test.go` | → `internal/repository/raft_tls_test.go` | |
| `raft_multinode_test.go` | → `internal/repository/raft_multinode_test.go` | stays white-box (unexported helpers) |
| `raft_single_recovery_test.go` | → `internal/repository/raft_single_recovery_test.go` | |
| `bootstrap_test.go` | → `internal/repository/bootstrap_test.go` | |
| `tokens_test.go` | → `internal/service/token_test.go` | |

### 3.4 `gohookbridge/server/` (package `server`)

| Old | New | Notes |
|---|---|---|
| `server.go` | SPLIT → `internal/handler/webhook.go` + `internal/handler/events.go` + `internal/handler/logging.go` + `internal/service/events.go` + `internal/server/server.go` | webhook handlers (`handleWebhookPost`,`handleTestPayloadSend`,`handleEventReplay`,`handleGenerateEncryptionKey`,`validateWebhookSignature`+per-provider,`publishEvent`,`writeJSONResponse`,`errorIt`,`effectivePublicURL`)→handler/webhook.go; SSE/health (`handleEventsGet`,`sseLoop`,`retVersion`,`retReadyz`,`retStartup`)→handler/events.go; `safeLogger`/`safeLogFormatter`→handler/logging.go; `EventBroker`/`Subscriber`→service/events.go; `serve()`→`internal/server/server.go` as `Server.Run`; `applyBootstrapOnce`,`toRaftPeers`,`watchSingleNodeRecovery`,`brokerTTLNotifier`,`initDevAdmin`→server.go |
| `command.go` | → `internal/server/command.go` | `server.Command()` |
| `auth.go` | SPLIT → `internal/service/session.go` + `internal/service/auth.go` + `internal/handler/auth.go` | `sessionToken`/`deriveSessionSecret`/`encodeSession`/`decodeSession`/`generateRandomHex`→service/session.go; `ValidatePassword`→service/auth.go; `setSessionCookie`/`clearSessionCookie`/`RequireAuth`/`RequireAuthDynamic`/`LogoutHandler`/`apiAuthMethodsHandler`/`apiLoginHandler`/`apiLogoutHandler`→handler/auth.go. **Remove package-level `sessionSecret` var** → pass `[32]byte` via handler constructor/fields. |
| `auth_oidc.go` | → `internal/handler/oidc.go` | `OIDCDiscovery`,`OIDCHandler`,`NewOIDCHandler`, handlers |
| `auth_config.go` | DELETE | dead shim (`LoadAuthConfig` unused; type aliases replaced by direct `domain.AuthConfig` usage) |
| `ratelimit.go` | SPLIT → `internal/service/ratelimit.go` + `internal/handler/ratelimit.go` | `rateLimiter`→`RateLimiter`, `banTracker`→`BanTracker`, `BanInfo`, fingerprints, `recordCredentialFailure`→service; `banMiddleware`/`rateLimitMiddleware`/`apiBansHandler`/`apiUnbanHandler`→handler |
| `migrate.go` | → `internal/server/migrate.go` | `migrateConfig` (migrate-config subcommand) |
| `k8s.go` | → `internal/server/k8s.go` | `newK8sClientset`,`statefulSetReplicaReader` |
| `raft_generation.go` | → `internal/server/raft_generation.go` | `raftGenerationStore` |
| `leader_forward.go` | → `internal/handler/leader_forward.go` | `leaderForwardMiddleware` + `leaderInfo` interface + `isReadOnlyMethod` |
| `raft_tls.go` | → `internal/server/raft_tls.go` | `buildRaftTLSConfig`,`effectiveNamespace` |
| `server_test.go` | SPLIT → `internal/handler/webhook_test.go` + `internal/handler/events_test.go` + `internal/service/events_test.go` + `internal/server/server_test.go` | map each `t.Run` subtree to its new package |
| `auth_test.go` | SPLIT → `internal/handler/auth_test.go` + `internal/service/session_test.go` | |
| `ratelimit_test.go` | SPLIT → `internal/handler/ratelimit_test.go` + `internal/service/ratelimit_test.go` | |
| `leader_forward_test.go` | → `internal/handler/leader_forward_test.go` | |
| `raft_generation_test.go` | → `internal/server/raft_generation_test.go` | |
| `k8s_test.go` | → `internal/server/k8s_test.go` | |
| `raft_tls_test.go` | → `internal/server/raft_tls_test.go` | |

### 3.5 `gohookbridge/client/`, `gohookbridge/proxy/`, `gohookbridge/nats/`, `gohookbridge/web/`

| Old | New |
|---|---|
| `client/client.go` → | `internal/client/client.go` |
| `client/command.go` → | `internal/client/command.go` |
| `client/interface.go` → | `internal/client/interface.go` |
| `client/replay.go` → | `internal/client/replay.go` |
| `client/hook_list.go` → | `internal/client/hook_list.go` |
| `client/templates/*` → | `internal/client/templates/*` |
| `client/client_test.go`,`hook_list_test.go`,`replay_test.go` → | `internal/client/*_test.go` |
| `proxy/proxy.go`,`command.go`,`produce.go` → | `internal/proxy/*.go` |
| `nats/broker.go`,`buffer.go` → | `pkg/nats/broker.go`,`buffer.go` (package `nats`) |
| `nats/broker_test.go` → | `pkg/nats/broker_test.go` |
| `web/handler.go` → | `internal/web/handler.go` (package `web`) |
| `web/static/*` → | `internal/web/static/*` (gitignored, regenerated) |
| `web/handler_test.go` → | `internal/web/handler_test.go` |

---

## 4. Package & data-structure changes

### 4.1 `internal/domain` (package `domain`)

**Imports**: stdlib + `github.com/go-playground/validator/v10`. No other internal package.

**types.go** (unchanged field-for-field, moved verbatim):
`Channel`, `ChannelAccessToken`, `User`, `Role`, `Permission` (string type + `PermAll`…`PermChannelView` consts),
`DefaultRoles`, `OIDCProvider`, `RoleMapping`, `ChannelRoleMapping`, `ClientCursor`, `UserBinding`,
the `validate` package-level var + `channelid` validator registration (`//nolint:gochecknoinits`),
`MigrateChannel(p *Channel)` (renamed from `migrateChannel`).

**config.go**:
```go
type GlobalConfig struct{ Server ServerConfig `json:"server" yaml:"server"`; Defaults DefaultChannelConfig `json:"defaults" yaml:"defaults"` }
type ServerConfig struct{ ... }          // unchanged
type DefaultChannelConfig struct{ ... }  // unchanged
func DefaultGlobalConfig() *GlobalConfig // renamed from defaultGlobalConfig (now exported)
func ResolveChannelConfig(p *Channel, global *GlobalConfig) *Channel // renamed from resolveChannelConfig (exported)
```
`GlobalConfigResponse` / `ServerConfigResponse` are **moved to `internal/handler`** (API DTOs).

**auth.go**:
```go
type AuthConfig struct{ Internal InternalConfig; OIDC OIDCConfig }
type InternalConfig struct{ Enabled bool; Users []InternalUser }
type InternalUser struct{ Username, PasswordHash string }
type OIDCConfig struct{ Enabled bool; Providers []OIDCProvider }
```

**errors.go**:
```go
package domain
import "errors"
var (
    ErrNotFound       = errors.New("not found")
    ErrAlreadyExists  = errors.New("already exists")
    ErrInvalidArgument = errors.New("invalid argument")
)
```

**repository.go** (THE core of "domain defines repository interfaces"):
```go
package domain
import "context"

type ChannelChangeNotifier interface {
    OnChannelChanged(channelID string, ttlSeconds int)
}

type ChannelRepository interface {
    GetChannel(ctx context.Context, id string) (*Channel, error)
    ListChannels(ctx context.Context) ([]*Channel, error)
    CreateChannel(ctx context.Context, p *Channel) error
    UpdateChannel(ctx context.Context, p *Channel) error
    DeleteChannel(ctx context.Context, id string) error
    CreateChannelRoleMapping(ctx context.Context, m *ChannelRoleMapping) error
    ListChannelRoleMappings(ctx context.Context, channelID string) ([]ChannelRoleMapping, error)
    DeleteChannelRoleMapping(ctx context.Context, channelID, entryID string) error
}

type UserRepository interface {
    GetUser(ctx context.Context, id string) (*User, error)
    GetUserByUsername(ctx context.Context, username string) (*User, error)
    ListUsers(ctx context.Context) ([]*User, error)
    CreateUser(ctx context.Context, u *User) error
    UpdateUser(ctx context.Context, u *User) error
    DeleteUser(ctx context.Context, id string) error
}

type RBACRepository interface {
    GetRole(ctx context.Context, name string) (*Role, error)
    ListRoles(ctx context.Context) ([]Role, error)
    CreateRole(ctx context.Context, r Role) error
    CreateRoleMapping(ctx context.Context, m *RoleMapping) error
    ListRoleMappings(ctx context.Context) ([]RoleMapping, error)
    DeleteRoleMapping(ctx context.Context, id string) error
    GetUserRoleMappings(ctx context.Context, userID string) ([]RoleMapping, error)
    GetGroupRoleMappings(ctx context.Context, groupName string) ([]RoleMapping, error)
    GetUserChannelRoleMappings(ctx context.Context, userID string) ([]ChannelRoleMapping, error)
    GetGroupChannelRoleMappings(ctx context.Context, groupName string) ([]ChannelRoleMapping, error)
}

type ConfigRepository interface {
    GetGlobalConfig(ctx context.Context) (*GlobalConfig, error)
    UpdateGlobalConfig(ctx context.Context, cfg *GlobalConfig) error
    OIDCProviders(ctx context.Context) ([]OIDCProvider, error)
    SetOIDCProviders(ctx context.Context, providers []OIDCProvider) error
    GetClientCursor(ctx context.Context, channel, clientID string) (*ClientCursor, error)
    SetClientCursor(ctx context.Context, cursor *ClientCursor) error
    GetSetupModeEndTime(ctx context.Context) time.Time
    SetSetupModeEndTime(ctx context.Context, t time.Time) error
}

type Repository interface {
    ChannelRepository
    UserRepository
    RBACRepository
    ConfigRepository
}
```
(`GetSetupModeEndTime` keeps its original single-return `time.Time`; everything else adds `ctx`.)

### 4.2 `internal/repository` (package `repository`)

**Imports**: `domain`, `pkg/uuid`, hashicorp/raft, bbolt, goca, bcrypt, k8s.io/*, etc. (heavy deps).

`RaftStore` is unchanged structurally but:
- Its CRUD methods (`GetChannel`, `ListChannels`, `CreateChannel`, `UpdateChannel`, `DeleteChannel`,
  `GetGlobalConfig`, `UpdateGlobalConfig`, `GetUser`, `GetUserByUsername`, `ListUsers`, `CreateUser`,
  `UpdateUser`, `DeleteUser`, `GetRole`, `ListRoles`, `CreateRole`, `CreateRoleMapping`,
  `ListRoleMappings`, `DeleteRoleMapping`, `GetUserRoleMappings`, `GetGroupRoleMappings`,
  `CreateChannelRoleMapping`, `ListChannelRoleMappings`, `DeleteChannelRoleMapping`,
  `GetUserChannelRoleMappings`, `GetGroupChannelRoleMappings`, `OIDCProviders`, `SetOIDCProviders`,
  `GetClientCursor`, `SetClientCursor`, `GetSetupModeEndTime`, `SetSetupModeEndTime`) gain
  `ctx context.Context` as first param and return `domain.*` types.
- **Removed** from `RaftStore` (now service): `ResolveChannelConfig`, `ResolveChannelWebhookSecret`,
  `ResolveChannelAllowedIPs`, `ResolveChannelMaxBodySize`, `ResolveChannelEncryption`,
  `ResolveCORSOrigin`, `ResolveBehindReverseProxy`, `ResolveFooter`, `SessionSecret`, `SetSessionSecret`,
  `IsSetupMode`, `CreateDevAdmin`, `CreateAccessToken`, `DeleteAccessToken`, `ValidateChannelToken`,
  `BuildAuthConfig`, `GetUserBinding`, `UpdateUserBinding`, `ListBindings`.
- `GetChannel` returns `fmt.Errorf("%w: channel %q", domain.ErrNotFound, id)`.
- `GetUser`/`GetUserByUsername`/`GetRole`/`GetClientCursor` wrap `domain.ErrNotFound`.
- `CreateChannel`'s `resp != nil` path changes from `fmt.Errorf("%v", resp)` to `return resp.(error)`
  so the FSM's wrapped `domain.ErrAlreadyExists` survives; `fsm.go` `applyCreateChannel` returns
  `fmt.Errorf("%w: channel %q", domain.ErrAlreadyExists, id)`.
- `CreateRole` default-role guard returns `fmt.Errorf("%w: role %q", domain.ErrAlreadyExists, r.Name)`.
- `MigrateRBAC(ctx context.Context) error` (in `migrate.go`).
- `ApplyBootstrap(ctx context.Context, cfg *BootstrapConfig) error` (in `bootstrap.go`).

Lifecycle methods NOT in `domain.Repository` (kept as exported methods on `RaftStore`, used only by
the composition root): `WaitForLeader`, `WaitForSelfLeadership`, `WaitForCleanState`, `IsCleanState`,
`IsStarted`, `IsVoter`, `IsLeader`, `LeaderAddress`, `GetConfiguration`, `AddVoter`, `RemoveServer`,
`ReconcileMembership`, `StartJoinLoop`, `StepDown`, `Apply`, `applyCommand`, `HasData`, `Close`, `Shutdown`.

`var _ domain.Repository = (*RaftStore)(nil)` compile-time assertion added to `raft.go`.

### 4.3 `internal/service` (package `service`)

**Imports**: `domain`, `pkg/uuid`, `golang.org/x/crypto/bcrypt`. NO repository/handler import.

```go
type Service struct {
    repo     domain.Repository
    notifier domain.ChannelChangeNotifier
}
func NewService(repo domain.Repository, notifier domain.ChannelChangeNotifier) *Service
```

Exported methods (grouped by file) — all previously on `RaftStore`/`store`/`server`, now with `ctx`:

- **repository.go** (passthrough so handlers go through the service layer):
  `GetChannel`, `ListChannels`, `CreateChannel`, `UpdateChannel`, `DeleteChannel`,
  `GetUser`, `GetUserByUsername`, `ListUsers`, `CreateUser`, `UpdateUser`, `DeleteUser`,
  `GetRole`, `ListRoles`, `CreateRole`, `CreateRoleMapping`, `ListRoleMappings`, `DeleteRoleMapping`,
  `GetUserRoleMappings`, `GetGroupRoleMappings`, `CreateChannelRoleMapping`, `ListChannelRoleMappings`,
  `DeleteChannelRoleMapping`, `GetGlobalConfig`, `UpdateGlobalConfig`, `OIDCProviders`, `SetOIDCProviders`,
  `GetClientCursor`, `SetClientCursor`, `GetSetupModeEndTime`, `SetSetupModeEndTime`,
  `ListBindings`, `UpdateUserBinding` — all `(ctx context.Context, …)` delegating to `s.repo`.
- **channel.go**: `ResolveChannelConfig(ctx, id) (*domain.Channel, error)`, `ResolveChannelWebhookSecret`,
  `ResolveChannelAllowedIPs`, `ResolveChannelMaxBodySize`, `ResolveChannelEncryption`,
  `ResolveCORSOrigin(ctx) string`, `ResolveBehindReverseProxy(ctx) bool`, `ResolveFooter(ctx) string`,
  `SessionSecret(ctx) string`, `SetSessionSecret(ctx, secret) error`,
  `NewProtectedChannels() *ProtectedChannels`, `NewProtectedChannelsDynamic() *ProtectedChannels`,
  type `ProtectedChannels` with `Has(channel) bool`, `IsAllowed(channel string, pub *[32]byte) bool`.
- **auth.go**: `BuildAuthConfig(ctx) *domain.AuthConfig`, `ValidatePassword(hash, password) bool`,
  `CreateDevAdmin(ctx, password) error`, `IsSetupMode(ctx) bool`.
- **authorization.go**: `UserHasPermission(ctx, username, perm domain.Permission, channelID) bool`,
  `UserChannels(ctx, username) ([]string, error)`, `IsAdmin(ctx, username) bool`,
  `GetUserPermissions(ctx, username) []string`, `HasChannelRole(ctx, username, channelID, role) bool`.
- **token.go**: `GenerateAccessToken() (string, string)`, `HashToken(raw) string`,
  `CreateAccessToken(ctx, channelID, name, scope) (raw string, token domain.ChannelAccessToken, err error)`,
  `DeleteAccessToken(ctx, channelID, tokenID) error`,
  `ValidateChannelToken(ctx, channelID, rawToken, requiredScope) bool`.
- **session.go**: type `SessionToken` (renamed from `sessionToken`), `deriveSessionSecret(secret string) [32]byte`,
  `encodeSession(t *SessionToken, secret [32]byte) (string, error)`, `decodeSession(s string, secret [32]byte) (*SessionToken, error)`,
  `generateRandomHex() string`.
- **ratelimit.go**: `RateLimiter` (renamed from `rateLimiter`) + `NewRateLimiter()`,
  `BanTracker` (renamed from `banTracker`) + `NewBanTracker()`, `BanInfo`, `RecordCredentialFailure`,
  fingerprint helpers, `ExtractSignatureValue`. (Unexported helpers `recordFailure`, `banIfSuspicious`,
  `isBanned`, `listBans`, `unban`, `allow` stay unexported; exported surface needed by handler:
  `BanTracker.ListBans() []BanInfo`, `BanTracker.Unban(ip string)`, `BanTracker.IsBanned(ip) bool`,
  `RateLimiter.Allow(ip string, max int, window int) bool`.)
- **events.go**: `EventBroker` + `Subscriber` + `NewEventBroker()` (legacy in-memory broker, kept for tests).

### 4.4 `internal/handler` (package `handler`)

**Imports**: `service`, `domain`, `pkg/crypto`, `pkg/nats`, `chi`, urfave/cli (only where needed). NEVER `repository`.

Key signatures (changed from `*store.RaftStore`):
```go
func RegisterAPIHandlers(r chi.Router, svc *service.Service)
type apiHandler struct { svc *service.Service }
func handleWebhookPost(broker *nats.Broker, svc *service.Service, banTracker *service.BanTracker) http.HandlerFunc
func handleTestPayloadSend(broker *nats.Broker, svc *service.Service) http.HandlerFunc
func handleEventsGet(broker *nats.Broker, svc *service.Service) http.HandlerFunc
func handleEventReplay(broker *nats.Broker, svc *service.Service) http.HandlerFunc
func handleGenerateEncryptionKey(svc *service.Service) http.HandlerFunc
func ipRestrictMiddleware(svc *service.Service) func(http.Handler) http.Handler
func channelAccessMiddleware(svc *service.Service, requiredScope string, banTracker *service.BanTracker) func(http.Handler) http.Handler
func RequirePermission(svc *service.Service, perm domain.Permission) func(http.Handler) http.Handler
func RequireChannelACLPermission(svc *service.Service) func(http.Handler) http.Handler
func RequireAuthDynamic(svc *service.Service, secret [32]byte) func(http.Handler) http.Handler
func apiLoginHandler(svc *service.Service, secret [32]byte, banTracker *service.BanTracker) http.HandlerFunc
func leaderForwardMiddleware(rs leaderInfo, httpPort int) func(http.Handler) http.Handler // leaderInfo{IsLeader() bool; LeaderAddress() string}
func banMiddleware(tracker *service.BanTracker, svc *service.Service) func(http.Handler) http.Handler
func rateLimitMiddleware(limiter *service.RateLimiter, svc *service.Service) func(http.Handler) http.Handler
```
Context keys `UsernameContextKey`, `GroupsContextKey`, `contextKeyChannelID` and helpers
`GetUsernameFromContext`, `GetGroupsFromContext` live here. Session cookie helpers
(`setSessionCookie`, `clearSessionCookie`) and `sessionCookieName`/`sessionMaxAge` live here.
`apiHandler` call sites replace `h.rs.X(...)` → `h.svc.X(ctx, ...)` and `strings.Contains(err.Error(), "already exists")`
→ `errors.Is(err, domain.ErrAlreadyExists)` (409); `err != nil` on get paths → `errors.Is(err, domain.ErrNotFound)` (404).

### 4.5 `internal/server` (package `server`) — composition root

```go
type Server struct {
    repo   *repository.RaftStore
    svc    *service.Service
    broker *nats.Broker
    rateLimiter *service.RateLimiter
    banTracker  *service.BanTracker
    sessionSecret [32]byte
}
func NewServer(...) (*Server, error)   // performs the full DI wiring formerly in serve()
func (s *Server) Run(ctx context.Context) error  // formerly serve()
func Command() *cli.Command             // formerly server.Command()
func migrateConfig(_ *cli.Context) error
```
`Run` owns: deprecated-env checks, signal.NotifyContext, raft discovery/resolver, TLS build
(`buildRaftTLSConfig`), single-node recovery + generation fencing, `repository.NewRaftStore`,
leader/clean-state waits, `applyBootstrapOnce`, `MigrateRBAC`, `nats.New`, TTL seeding, dev-admin,
`service.NewService(rs, notifier)`, router assembly (uses `handler.RegisterAPIHandlers` etc.), graceful shutdown.

### 4.6 `internal/app` (package `app`)

`MakeApp(commands ...*cli.Command) *cli.App`, `GetLogger`, `GetNewHookURL`, `KeygenCommand`,
`CompletionCommands`, `Run`, `Version` (embeds `templates/version`), and all `*Flags` vars + constants.
`KeygenCommand` imports `pkg/crypto`.

---

## 5. Import rewrite list

Module prefix `github.com/webcenter-fr/gohookbridge`. Systematic mapping:

| Old import | New import(s) — split by symbols used |
|---|---|
| `…/gohookbridge/gohookbridge` (alias `gohookbridge`) | `…/internal/app` (app), `…/pkg/crypto`, `…/pkg/encryption`, `…/pkg/uuid`, `…/pkg/urlutil` |
| `…/gohookbridge/gohookbridge/store` | `…/internal/domain` + `…/internal/repository` + `…/internal/service` + `…/internal/handler` |
| `…/gohookbridge/gohookbridge/store/storetest` | `…/internal/repository/storetest` |
| `…/gohookbridge/gohookbridge/server` | `…/internal/handler` + `…/internal/service` + `…/internal/server` |
| `…/gohookbridge/gohookbridge/client` | `…/internal/client` |
| `…/gohookbridge/gohookbridge/proxy` | `…/internal/proxy` |
| `…/gohookbridge/gohookbridge/nats` | `…/pkg/nats` |
| `…/gohookbridge/gohookbridge/web` | `…/internal/web` |

Symbol-based split rules (apply per file):
- `store.Channel`/`User`/`Role`/`Permission`/`OIDCProvider`/`RoleMapping`/`ChannelRoleMapping`/`ClientCursor`/`UserBinding`/`GlobalConfig`/`ServerConfig`/`DefaultChannelConfig`/`AuthConfig`/`InternalConfig`/`InternalUser`/`OIDCConfig`/`Perm*`/`DefaultRoles`/`Err*` → `domain.*`.
- `store.RaftStore`, `store.RaftConfig`, `store.RaftPeer`, `store.RaftDiscoveryConfig`, `store.NewRaftStore`, `store.NewPeerResolver`, `store.DeriveAdvertiseAddr`, `store.PodSANs`, `store.RaftTLSConfig`, `store.LoadOrBuildRaftTLS`, `store.LoadBootstrap`, `store.BootstrapConfig`, `store.MigrateRBAC` → `repository.*`.
- Business functions `UserHasPermission`, `UserChannels`, `IsAdmin`, `GetUserPermissions`, `HasChannelRole`, `BuildAuthConfig`, `NewProtectedChannels*`, `CreateAccessToken`, `ValidateChannelToken`, `ResolveChannel*`, `SessionSecret`, `SetSessionSecret`, `CreateDevAdmin`, `IsSetupMode` → `service` methods (call as `svc.X(...)`).
- `store.UsernameContextKey`, `store.GroupsContextKey` → `handler.UsernameContextKey`, `handler.GroupsContextKey`.
- `server.Command()` → `server.Command()` (from `internal/server`).
- `gohookbridge.MakeApp`/`GetLogger`/`Version`/`KeygenCommand`/`CompletionCommands`/`DefaultServerPort`/`CommonFlags`/`ServerFlags`/`ClientFlags`/`ReplayFlags`/`ProduceFlags`/`ProxyFlags`/`KeygenFlags` → `app.*`.
- `gohookbridge.GenerateUUID` → `uuid.GenerateUUID`; `gohookbridge.Encrypt/Decrypt/IsEncrypted/ParsePublicKey/EncodePublicKey/LoadKeyPair/SaveKeyPair/GenerateKeyPair` → `crypto.*`; `gohookbridge.AESEncrypt/AESDecrypt/IsAESEncrypted/GenerateAESKey` → `encryption.*`; `gohookbridge.URLWithQueryParam` → `urlutil.URLWithQueryParam`.

Per-file highlights (the coder applies the rules above to every `.go`/`_test.go`; these are the non-obvious ones):
- `cmd/gohookbridge/main.go`, `cmd/gohookbridge-client/main.go`, `cmd/gohookbridge-proxy/main.go`: import `internal/app`, `internal/server`, `internal/client`, `internal/proxy`.
- `internal/server/server.go`: imports `repository`, `service`, `handler`, `domain`, `pkg/nats`, `internal/app` (flags), k8s client-go.
- `internal/handler/*.go`: import `service`, `domain`, `pkg/crypto`, `pkg/nats`; remove all `store` imports.
- `internal/client/*.go`: replace `gohookbridge "…/gohookbridge/gohookbridge"` with `app`, `crypto`, `encryption`, `uuid`, `urlutil` imports (e.g., `client.go` uses `GenerateUUID`, `LoadKeyPair`, `EncodePublicKey`, `IsAESEncrypted`, `AESDecrypt`, `IsEncrypted`, `Decrypt`; `command.go` uses `GetLogger`, `GetNewHookURL`, `DefaultPublicHookURL`, `DefaultLocalDebugURL`, `CommonFlags`, `ClientFlags`; `replay.go` uses `GetLogger`, `CommonFlags`, `ReplayFlags`).
- `internal/proxy/*.go`: replace with `app` (CommonFlags/ProxyFlags/ProduceFlags) + `crypto` (ParsePublicKey/LoadKeyPair/Encrypt) + `urlutil` (URLWithQueryParam).
- `internal/app/*.go`: import `pkg/crypto`; `app.go` no longer needs a `gohookbridge` self-import.
- `internal/repository/storetest/helper.go`: import `domain` + `repository`; `NewRaftStore` returns `*repository.RaftStore`; config/type refs → `domain.*`/`repository.RaftConfig`.

---

## 6. Config changes

- **No new `config/` Go package** (documented in CONTRIBUTING.md). Config model is unchanged:
  CLI flags (`internal/app/flags.go`) + Raft-stored `bootstrap.yaml` (`domain.GlobalConfig` +
  `repository.LoadBootstrap` + `repository.ApplyBootstrap`). All `GOHOOKBRIDGE_*` env vars and defaults preserved verbatim.
- `domain.DefaultGlobalConfig()` carries the same defaults as the old `defaultGlobalConfig()`.
- `domain.ResolveChannelConfig()` carries the same merge logic as the old `resolveChannelConfig()`.

---

## 7. Error handling

- New sentinel errors in `internal/domain/errors.go`: `ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidArgument`.
- Repository wraps them (`%w`) in `GetChannel`, `GetUser`, `GetUserByUsername`, `GetRole`, `GetClientCursor`, `CreateChannel`, `CreateRole`.
- `fsm.applyCreateChannel` returns an error wrapping `domain.ErrAlreadyExists`; `RaftStore.CreateChannel`
  preserves the FSM error (`return resp.(error)`) so `errors.Is` works across the Raft `Apply` boundary.
- Handlers replace `strings.Contains(err.Error(), "already exists")` → `errors.Is(err, domain.ErrAlreadyExists)` (HTTP 409), and bare `err != nil` on get-by-id → `errors.Is(err, domain.ErrNotFound)` (HTTP 404).
- `ErrInvalidArgument` is used for "channel ID required"/"user ID required"/"channel_id required" (HTTP 400). Optional; safe to wrap existing `fmt.Errorf` where the message is an argument error.

---

## 8. Testing plan

### Unit tests (colocated — unchanged per blog: "unit tests next to code")
- All existing `*_test.go` move with their package (see §3). Only import paths + constructor calls change.
- `storetest.NewRaftStore(t)` now returns `*repository.RaftStore`; wrap with `service.NewService(rs, nil)` where handlers are tested.
- New unit tests required:
  - `internal/domain/errors_test.go`: sentinel error identity (trivial).
  - `internal/service/authorization_test.go` / `internal/service/token_test.go` / `internal/service/session_test.go`: cover the moved business logic with a fake `domain.Repository` (in-memory map-based fake) to prove the service no longer depends on `repository`.
  - `internal/handler/api_test.go`: assert `errors.Is` 404/409 mapping via `domain.ErrNotFound`/`domain.ErrAlreadyExists`.

### Integration tests (`tests/integration/`, `tests/fixtures/`)
- `tests/integration/server_integration_test.go` (package `integration_test`, `//go:build integration`):
  black-box test that (a) starts `internal/server` on a free port with a temp raft dir + a
  `tests/fixtures/bootstrap.yaml`-style bootstrap, (b) `POST`s a webhook to `/{channel}` using
  `tests/fixtures/webhook-payload.json`, (c) consumes it via `GET /events/{channel}` (SSE), and
  (d) asserts the body is relayed. Uses only exported APIs (no `repository` internals).
- `tests/fixtures/bootstrap.yaml`: sample `global` + `channels` bootstrap config.
- `tests/fixtures/webhook-payload.json`: a minimal `{"hello":"world"}` JSON body.
- The existing white-box `raft_multinode_test.go` and `raft_single_recovery_test.go` stay in
  `internal/repository` (they test unexported raft helpers); documented in CONTRIBUTING.md.

### Commands
```bash
make web-build                        # regenerates internal/web/static (needed before go test)
go test ./...                         # unit tests (fast, no integration tag)
go test -tags integration ./tests/integration/...   # black-box integration test
cd web && npm test                    # frontend (Vitest)
cd web && npm run typecheck           # frontend typecheck
make lint-go                          # golangci-lint
make build / make build-all           # full build pipeline
```

---

## 9. CI & tooling updates

| File | Change |
|---|---|
| `Makefile` | build guard `test -f gohookbridge/web/static/index.html` → `internal/web/static/index.html`. Add `test-integration: go test -tags integration ./tests/integration/...`. All `./cmd/*` build targets unchanged. |
| `Dockerfile` | `COPY gohookbridge/web/ ./gohookbridge/web/` → `COPY internal/web/ ./internal/web/`; `COPY --from=webbuild /src/gohookbridge/web/static /go/.../gohookbridge/web/static` → `.../internal/web/static`; guard path → `/src/internal/web/static/index.html`. |
| `Dockerfile.goreleaser` | no change (copies prebuilt binary only). |
| `.goreleaser.yml` | `before.hooks[0]` → `printf '{{ .Version }}' > internal/app/templates/version`. `main: ./cmd/*` unchanged. |
| `.golangci.yml` | remove stale `paths` exclusions (`pkg/resolve/resolve.go`, `pkg/provider/gitea/structs`) — optional cleanup; keep `build-tags: [e2e]` and add `integration` if integration tests use that tag. |
| `.github/workflows/go.yml` | no path changes needed (targets `make web-build`, `make lint-go`, `make test`, `make build`, `cd web …`). Optionally add a `test-integration` step. |
| `.github/workflows/releaser.yaml` | `./hack/generate-release-notes.sh` → `./scripts/generate-release-notes.sh`. |
| `web/scripts/copy-to-static.mjs` | `const dst = '../internal/web/static'`. |
| `.gitignore` | `gohookbridge/web/static/` → `internal/web/static/`. |
| `.dockerignore` | `gohookbridge/web/static` → `internal/web/static`. |
| `README.md`, `quickstart.md`, `design.md`, `misc/README.md` | update any `gohookbridge/web/static`, `gohookbridge/store/…`, `gohookbridge/server/…` references to the new paths (see CONTRIBUTING for authoritative tree). |
| `.pre-commit-config.yaml` | no path change (calls `make` targets). |

---

## 10. CONTRIBUTING.md update (explicit issue requirement)

Replace the "Project structure" code block and the "Package organization" subsection with:

1. **Project structure** — the target tree from §2 (cmd/, internal/{domain,service,repository,handler,server,app,client,proxy,web}, pkg/, api/, scripts/, tests/, web/ frontend).
2. **Architecture rules** (new subsection "Clean architecture"):
   - Dependency direction: `handler → service → domain`; `repository → domain`; `pkg` depends on nothing internal; `internal/server` is the composition root and the only package allowed to import `repository` + `service` + `handler`.
   - Domain defines repository interfaces (`domain.Repository` and sub-interfaces); `repository.RaftStore` implements them; repositories return domain entities and wrap domain sentinel errors (`domain.ErrNotFound`, `domain.ErrAlreadyExists`, `domain.ErrInvalidArgument`).
   - Handlers do serialization/validation/error translation (map `errors.Is(domain.ErrNotFound)`→404, `ErrAlreadyExists`→409) and never import `internal/repository`.
   - `context.Context` is the first parameter of every repository/service method.
   - What belongs in `pkg/` (public reusable: crypto, encryption, uuid, urlutil, nats) vs `internal/` (private app code).
3. **Documented-only dirs**: `config/` (config = CLI flags + Raft bootstrap.yaml, not a Go package) and `migrations/` (data migrations are in-code, `internal/repository/migrate.go`); `api/openapi.yaml` is a placeholder (full spec out of scope).
4. **Testing rules**: unit tests colocated; integration tests under `tests/integration/` (build tag `integration`), fixtures under `tests/fixtures/`.
5. Update "Adding a new CLI flag" (flags → `internal/app/flags.go`), "Adding a new HTTP route" (handlers → `internal/handler/`, wiring → `internal/server/server.go`), and the store/server/client/proxy package descriptions.

---

## 11. Edge cases & risks

1. **Import cycles** — biggest risk. `handler → service`, `service → domain`, `repository → domain`, `server → {repository,service,handler}`. Mitigation: `server` (composition root) is separate from `service`; `service` never imports `handler`/`repository`; `app` is a leaf imported by `client`/`proxy`/`server` only. Compile with `go build ./...` after each layer move (§12).
2. **`internal/` visibility** — `internal/*` is importable only from within the module. All consumers are in-module, so this is safe; but any external import of `github.com/webcenter-fr/gohookbridge/gohookbridge/...` breaks (none exist; it's an application, not a library). Document in CONTRIBUTING.
3. **Package-name collisions** — `pkg/crypto` vs stdlib `crypto` (no file imports stdlib `crypto` root); `pkg/uuid` vs `github.com/google/uuid` (only indirect dep); `internal/web` vs top-level `web/` Nuxt dir (distinct dirs, documented); avoided a package named `cli` (urfave/cli) by naming it `internal/app`.
4. **Multiple `x.go` names** — `raft_tls.go` exists in BOTH `server` (→`internal/server/raft_tls.go`) and `store` (→`internal/repository/raft_tls.go`). Keep both names; they live in different packages now, so no collision.
5. **`go:embed` path coupling** — `internal/web/handler.go` embeds `static/*`; `internal/app/app.go` embeds `templates/version`. The static dir and version file must exist at build time. Mitigation: `make web-build` and `.goreleaser.yml` hook write the version file; keep the Makefile build guard.
6. **Session secret global** — the package-level `sessionSecret` var (violates "no package-level mutable state"). Replace with a `[32]byte` field threaded from `Server` → `handler` constructors. Risk of missing a call site; mitigate with a compile pass after moving `auth.go`.
7. **Raft `Apply` error wrapping** — `errors.Is` must survive the `future.Response()` round-trip. Mitigation: `CreateChannel` returns `resp.(error)` (not `fmt.Errorf("%v", resp)`) and `fsm.go` wraps `domain.ErrAlreadyExists`. Add a test for the 409 path.
8. **Test churn** — `server_test.go` (~1000+ lines) and `store` tests reference unexported symbols; split them per §3. Risk of dropped coverage. Mitigation: move tests with their source; run `go test ./...` per step.
9. **Frontend breakage** — only `copy-to-static.mjs` dst path and the embed dir move. The Nuxt `buildAssetsDir`/`/assets/` constraint is unchanged. Verify `make web-build` + `make build`.
10. **CI path breakage** — `build.yml` path filters (`"**.go"`, `"web/**"`) still match; `go.yml` targets are make-based. Only `releaser.yaml` (hack→scripts) and `.goreleaser.yml` (version path) change.
11. **Large diff** — split into the ordered commits in §12; each step compiles and tests green before the next.

---

## 12. Step-by-step implementation order (with verification after each)

Work on branch `feat/restructure-clean-architecture`.

1. **Create skeleton + `pkg/` leaves.** Move `crypto.go`, `encryption.go`, `uuid.go`, `url.go` (+ tests) to `pkg/*`; move `nats/` to `pkg/nats/`. Update their package names and internal references.
   ✔ `go build ./pkg/...` && `go test ./pkg/...`
2. **Create `internal/domain`.** Move entities/config/auth types from `store/types.go` + `bridge.go`; add `errors.go` + `repository.go` interfaces. Delete `MigrateChannelForTest`; export `MigrateChannel`, `DefaultGlobalConfig`, `ResolveChannelConfig`.
   ✔ `go build ./internal/domain/...`
3. **Create `internal/repository`.** Move `store/{raft,bolt,fsm,raft_discovery,raft_streamlayer,raft_tls,bootstrap,migrate}.go` + `storetest/`. Rewire to `domain.*`; add `ctx` to CRUD methods; add sentinel-error wrapping; add `var _ domain.Repository = (*RaftStore)(nil)`; remove service-bound methods (mark with TODO stubs for now or leave broken — do NOT leave broken; move them to service in step 4 first if needed). Recommended: do step 4 (service) immediately after repository compiles.
   ✔ `go build ./internal/repository/...`
4. **Create `internal/service`.** Move business logic (`channel`, `auth`, `authorization`, `token`, `session`, `ratelimit`, `events`) from `store/{acl,bridge,tokens}.go` + `server/{auth,ratelimit}.go` into `Service`. Add `ctx`. Add `NewService`.
   ✔ `go build ./internal/service/...` && `go test ./internal/service/...`
5. **Create `internal/handler`.** Move `store/api.go` + `server/{server,auth,auth_oidc,ratelimit,leader_forward}.go` handler portions. Change `*store.RaftStore` → `*service.Service`; `errors.Is` mapping; add context keys. Split `server_test.go` accordingly.
   ✔ `go build ./internal/handler/...` && `go test ./internal/handler/...`
6. **Create `internal/server`.** Move `serve()` + command + k8s + raft_generation + migrate + server-side raft_tls + wiring helpers. `Run(ctx)`. Remove `sessionSecret` global.
   ✔ `go build ./internal/server/...`
7. **Create `internal/app`, `internal/client`, `internal/proxy`, `internal/web`.** Move root package app/flags/templates → app; client/proxy packages; web embed. Update all imports.
   ✔ `go build ./...` (whole module compiles)
8. **Update `cmd/*/main.go`** and delete root `main.go`.
   ✔ `go build ./cmd/...` && `go run ./cmd/gohookbridge --help`
9. **Update tooling:** Makefile, Dockerfile, `.goreleaser.yml`, `web/scripts/copy-to-static.mjs`, `.gitignore`, `.dockerignore`, `.github/workflows/releaser.yaml`, `.golangci.yml` cleanup.
   ✔ `make web-build` && `make build` && `make build-all`
10. **Add `scripts/`, `api/openapi.yaml`, `tests/integration/` + `tests/fixtures/`.** Rename `hack/`.
    ✔ `go test -tags integration ./tests/integration/...`
11. **Run the full gate:** `make lint-go`, `make test`, `cd web && npm run typecheck`, `cd web && npm test`, `make build-all`.
12. **Update docs:** `CONTRIBUTING.md` (§10), `README.md`, `quickstart.md`, `design.md`.
    ✔ `make lint-md` (markdownlint) — then commit.

---

## 13. Definition of done

- [ ] Branch `feat/restructure-clean-architecture` created; no PR opened.
- [ ] Backend compiles: `go build ./...`.
- [ ] All unit tests pass: `go test ./...`.
- [ ] Integration test passes: `go test -tags integration ./tests/integration/...`.
- [ ] Frontend typecheck + tests pass: `cd web && npm run typecheck && npm test`.
- [ ] Full build succeeds: `make build-all` (validates web embed + 3 binaries).
- [ ] Lint clean: `make lint-go` (and `make lint-md` for docs).
- [ ] No import of `internal/repository` outside `internal/repository`, `internal/server`, and its own tests (grep-verified).
- [ ] `domain.Repository` interface defined and `repository.RaftStore` asserts it via `var _ domain.Repository = (*RaftStore)(nil)`.
- [ ] Sentinels `domain.ErrNotFound`/`domain.ErrAlreadyExists` used with `errors.Is` in handlers (no `strings.Contains(err.Error(), …)` remains).
- [ ] `hack/` removed; `scripts/generate-release-notes.sh` present; `api/openapi.yaml` placeholder present; `tests/integration/` + `tests/fixtures/` populated.
- [ ] `CONTRIBUTING.md` structure + rules rewritten per §10.
- [ ] Root `main.go` deleted; `cmd/*/main.go` are thin wiring-only entrypoints.

---

## Open questions for user

1. **`internal/server` package addition** — the blog lists only `internal/{domain,service,repository,handler}`, but strict layering needs a composition root separate from `service` (to avoid a `handler ↔ service` cycle). I introduced `internal/server`. Confirm this extra package is acceptable (recommended), or fold it into `cmd` (larger, less "thin" entrypoints).
2. **`EventBroker`/`Subscriber` (legacy in-memory broker)** — appears used only by `server_test.go`; `serve()` uses `nats.Broker`. Confirm: keep in `internal/service/events.go` (recommended, preserves tests) or delete it + its tests as dead code.
3. **Dead helpers** (`GetDefaultRoles`, `GetUserBinding`, `IsIPAllowed`, `LoadAuthConfig`, `Run`) — confirm deletion in this PR (recommended) vs. preserve.
