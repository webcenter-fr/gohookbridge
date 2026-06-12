# Fix Raft Migration Code Review Issues

## Context
The Raft+BoltDB migration introduced 13 issues covering RBAC token mismatch, security leaks, missing auth gating, and performance problems. This plan addresses all findings plus documentation and security disclosure updates required by the architectural change.

---

## Fixes

### 1. CRITICAL: Fix context key type mismatch (acl.go / auth.go)
**Problem:** `GetUsernameFromContext` in `store/acl.go:15` reads `ctx.Value("username")` (raw `string`), but `RequireAuth` in `auth.go:27,134` stores with `contextKeyUsername` which is type `contextKey` (not `string`). Go context values match on both type AND value — different types never match, so ALL permission checks fail.

**Fix:**
- Export `const UsernameContextKey = "username"` from `store/acl.go` (plain string, no typed wrapper).
- Update `GetUsernameFromContext` to use `UsernameContextKey`.
- Update `auth.go` to import and use `store.UsernameContextKey` instead of its private `contextKeyUsername`.
- Remove private `contextKey` type from `store/acl.go` (keep typed context key only for `contextKeyProjectID`, or convert it too).

### 2. CRITICAL: Strip session secret from global config API response (api.go)
**Problem:** `getGlobalConfig` at `store/api.go:153` serializes the full `GlobalConfig` including `Server.SessionSecret`. Any user with `PermGlobalRead` can read the HMAC cookie signing key.

**Fix:**
- Define a `GlobalConfigResponse` struct in `types.go` that omits `SessionSecret`.
- In `getGlobalConfig`, copy to response struct, setting `SessionSecret` to `"<redacted>"`.
- In `updateGlobalConfig`, accept the full config but never overwrite an existing secret with empty string.

### 3. CRITICAL: Replace hardcoded insecure session secret (server.go)
**Problem:** `server.go:681` falls back to `"insecure-default-secret-change-me"` when no secret is configured and auth is enabled.

**Fix:**
- When auth enabled and no session secret configured: generate a 32-byte crypto/rand secret, print it to stderr, store in Raft via `SetSessionSecret`.
- If node is not the leader, fatal error telling operator to set secret via bootstrap or on the leader node.

### 4. CRITICAL: Gate /admin page and /api routes behind setup mode when no users (server.go)
**Problem:** `server.go:717` — `/admin` page renders without auth when no users exist. `server.go:764` — API routes mounted with zero auth middleware when no users.

**Fix:**
- `/admin` handler: always require a valid session. If `authConfig == nil` (no users, no OIDC), redirect to `/login` or return 404 — never render the admin page unprotected.
- API routes: when `authConfig == nil`, wire `setupModeMiddleware` around the API router.
- Fix `setupModeMiddleware` (`api.go:387`): when `setupEnd.IsZero()`, set it to `now + 5 min` and persist to Raft. When past expiry, return 401 for all requests. Only call `next.ServeHTTP` when in the setup window.

### 5. WARNING: Fix replay token bypass when session secret is empty (server.go)
**Problem:** `handleReplayPost` at `server.go:363` sets `replayToken := rs.SessionSecret()`. When no session secret, `replayToken == ""`, so the `else if` at line 388 never fires.

**Fix:**
- Remove the `replayToken`/session secret gate entirely.
- When `token == ""` (no Bearer header), always resolve the project's `ReplayToken` via `rs.ResolveProjectConfig`. If a token is configured on the project or globally, return 401. Only allow if no replay token is configured anywhere.

### 6. WARNING: Warn/fatal on deprecated CLI flags and env vars (flags.go, auth_config.go)
**Problem:** Old flags (`--webhook-signature`, `--replay-token`, `--allowed-ips`, `--trust-proxy`, `--footer`, `--encrypted-channels-file`, `--cors-origin`, `--max-body-size`, `--auth-config-file`, `--auth-session-secret`) removed silently.

**Fix:**
- In `serve()`, check old env vars (`GOSMEE_WEBHOOK_SIGNATURE`, `GOSMEE_REPLAY_TOKEN`, `GOSMEE_ALLOWED_IPS`, `GOSMEE_TRUST_PROXY`, `GOSMEE_FOOTER`, `GOSMEE_ENCRYPTED_CHANNELS_FILE`, `GOSMEE_CORS_ORIGIN`, `GOSMEE_MAX_BODY_SIZE`, `GOSMEE_AUTH_CONFIG_FILE`, `GOSMEE_AUTH_SESSION_SECRET`). If any are set, `fmt.Fprintf(os.Stderr, ...)` + `os.Exit(1)` with migration instructions pointing to `bootstrap.yaml` and Admin UI.
- `LoadAuthConfig` in `auth_config.go`: if path is non-empty, fatal with migration message.
- `LoadProtectedChannels` in `protected_channels.go`: same.

### 7. WARNING: Optimize GetUserByUsername with username index (raft.go)
**Problem:** `GetUserByUsername` at `raft.go:367` calls `listFSMKeys` to get ALL `/users/` keys, then opens BoltDB `View` per key. O(n) scan on every login.

**Fix:**
- Add secondary index: `/users/by-username/{username}` → value containing `{"user_id":"..."}` in BoltDB, written on create/update, cleaned on delete.
- `GetUserByUsername` reads the index first (O(1) BoltDB read), then delegates to `GetUser`.
- All index writes go through Raft `applyCommand`.

### 8. WARNING: Fix CreateProject TOCTOU race (raft.go)
**Problem:** `CreateProject` at `raft.go:260-265` reads local BoltDB (not through Raft) for duplicate check. Two concurrent creates on different nodes both pass.

**Fix:**
- Move duplicate check into `FSM.Apply` as a new `"create-project"` command type. If project exists, return error via apply response.
- In `CreateProject`, send the command and check the apply response for errors.

### 9. WARNING: Batch ApplyBootstrap into single Raft commit (bootstrap.go)
**Problem:** `ApplyBootstrap` calls `CreateUser`/`CreateProject` individually — each a full Raft consensus round.

**Fix:**
- Define a `"bootstrap"` FSM command with full `BootstrapConfig` as payload.
- In `FSM.Apply`, process all entities in a single BoltDB transaction.
- This also fixes the duplicate-check TOCTOU for bootstrap entities.

### 10. SUGGESTION: Deduplicate encodePublicKey / EncodePublicKey (bridge.go)
**Problem:** `bridge.go:7` defines `encodePublicKey` duplicating `crypto.go:75` `EncodePublicKey` (both use `base64.RawURLEncoding`).

**Fix:**
- Remove `encodePublicKey` from `bridge.go`. Import and use `EncodePublicKey` from the `gosmee` package.
- Since `bridge.go` is in `store` package and cannot import `gosmee` (circular), move `EncodePublicKey` and `ParsePublicKey` into a new shared `encoding` utility (e.g., keep in `crypto.go` but make `store/bridge.go` call it via a re-export in a shared location, or extract to a `/gosmee/encoding/key.go` package).
- Simplest: add an exported `EncodePublicKey` in `gosmee/crypto.go` and call it from `store/bridge.go` through a thin wrapper or change `ProtectedChannels.IsAllowed` to accept a raw key byte slice and compare directly, avoiding encoding entirely.

### 11. SUGGESTION: Deduplicate test helpers (server_test.go)
**Problem:** `server_test.go:217` duplicates `store` package test helper `newTestRaftStore`.

**Fix:**
- Export `NewTestRaftStore` from `store/raft_test.go` (make it a public test helper via `store/storetest` or `export_test.go`), then call from `server_test.go`.

---

## Documentation Updates

### 12. DOCS: Update SECURITY.md for Raft + RBAC/ACL architecture

**What changed:** All security configuration moved from CLI flags/env vars to Raft-stored config (managed via `bootstrap.yaml` or Admin UI API). New RBAC/ACL auth model added with users, roles, permissions, and OIDC support.

**Changes to SECURITY.md:**

- **Security Model section:** Replace `internet → [gosmee server]` diagram with new trust boundaries: add `/login`, `/admin` page, Raft cluster inter-node communication, and API surface.
- **Threat Model table:** Add rows for:
  - Unauthorized admin/API access → RBAC ACL + session authentication
  - Session cookie hijacking → Secure/HttpOnly cookie + HMAC-signed tokens
  - Raft log tampering → Raft consensus + BoltDB integrity
  - Unauthorized config mutations → RBAC permission checks (`global:write`, `users:write`, etc.)
- **Recommended Baseline:** Replace all `--flag` references with bootstrap.yaml + Admin UI instructions. The checklist should read:
  - Run behind TLS (nginx, Caddy)
  - Configure projects via Admin UI or `bootstrap.yaml` with webhook signatures and allowed IPs
  - Set `MaxBodySize` globally or per-project
  - Create an admin user in `bootstrap.yaml` on first boot
  - Set `session_secret` in `bootstrap.yaml` global config
  - Run as non-root user
  - Enable encrypted channels per-project
  - Set replay tokens per-project
  - Configure CORS origin globally
- **Protecting the Webhook Intake:** Replace all `--webhook-signature`, `--allowed-ips`, `GOSMEE_WEBHOOK_SIGNATURE`, `GOSMEE_ALLOWED_IPS` flag/env var references with: configure via `bootstrap.yaml` per-project (or globally as defaults), or use Admin UI.
- **Protecting the Replay Endpoint:** Replace `--replay-token` / `GOSMEE_REPLAY_TOKEN` with: set `replay_token` per-project or globally via `bootstrap.yaml` or Admin UI.
- **Trusting Proxy Headers Safely:** Replace `--trust-proxy` / `GOSMEE_TRUST_PROXY` with: set `trust_proxy: true` in global config `server:` block via `bootstrap.yaml` or Admin UI.
- **Payload Size Limits:** Replace `--max-body-size` with `max_body_size` per-project or global default.
- **Restricting SSE Cross-Origin:** Replace `--cors-origin` / `GOSMEE_CORS_ORIGIN` with `cors_origin` in global config.
- **Rotating Webhook Secrets:** Update to reflect that secrets are now in Raft store, rotated via Admin UI or API — no restart needed.
- **NEW section: Authentication and RBAC (ACL):** Document:
  - Internal user auth: username/password with bcrypt hashing, stored in Raft
  - OIDC auth: per-provider configuration via Admin API
  - Session management: HMAC-SHA256 signed cookies, 24h expiry, HttpOnly/Secure/SameSite
  - RBAC model: 3 default roles (`admin`, `project_admin`, `project_viewer`), custom roles via API
  - Permission model: global read/write, users read/write, rbac read/write, project read/write/view, wildcard `*`
  - Project scoping: users limited to specific project IDs or wildcard `*`
- **NEW section: Bootstrap Configuration Security:** Document `bootstrap.yaml` format, emphasize:
  - Bootstrap file is read once on first boot, never again
  - File should be `0600` when it contains passwords/secrets
  - Session secret and OIDC client secrets should be pre-configured in bootstrap
  - Delete or archive the bootstrap file after initial cluster setup
- **NEW section: Raft Cluster Security:** Document:
  - Raft inter-node communication uses TCP (not TLS in current implementation)
  - Recommend running Raft transport on private network interfaces only
  - Recommend firewall rules restricting Raft port to cluster nodes only
  - The Raft data directory (`raft-dir`) contains all config including secrets — protect with filesystem permissions
- **Setup Mode:** Document the 5-minute unauthenticated API window on fresh install, warning operators to create the first user promptly.
- **Reporting Vulnerabilities:** Add note that RBAC permission bypass bugs (context key type mismatches specifically) are critical — list the contact method.

### 13. DOCS: Update README.md for Raft + RBAC/ACL architecture

**Changes to README.md:**

- **Server section:** Replace CLI flag documentation with new configuration model:
  - Document `bootstrap.yaml` format with full example
  - Document Admin UI at `/admin` with screenshot or description of tabs
  - Document new `--raft-*` flags (`--raft-dir`, `--raft-node-id`, `--raft-bind-addr`, `--raft-peers`, `--bootstrap-config-file`)
  - Document remaining server flags (`--public-url`, `--port`, `--address`, `--tls-cert`, `--tls-key`, `--auto-cert`)
- **NEW section: Authentication:** Document:
  - `/login` page for internal auth
  - OIDC provider configuration via API
  - First-boot setup: create admin user in `bootstrap.yaml`, then manage via Admin UI
  - Role-based access: admin, project_admin, project_viewer
- **Protected client channels:** Replace `--encrypted-channels-file` with per-project `encryption_enabled` + `encryption_pub_keys` in `bootstrap.yaml` or Admin UI.
- **Caddy/Nginx sections:** Update examples to remove deprecated flags; keep TLS/proxy instructions but remove `--trust-proxy` from CLI args (it's now in Raft config).
- **Security section in README:** Replace `--replay-token`/`--cors-origin` references with `bootstrap.yaml` equivalents, link to updated SECURITY.md.
- **Kubernetes deployment:** Update `misc/gosmee-server-deployment.yaml` to use `--raft-*` flags, document bootstrap ConfigMap approach.
- **Caveats:** Update to reflect that auth and RBAC are now production-grade features (not "intended for development only").

### 14. DOCS: Update Kubernetes deployment manifests (misc/)

**Changes to `misc/gosmee-server-deployment.yaml`:**
- Add `--raft-dir` volume mount (persistent volume for Raft data)
- Add `--bootstrap-config-file` pointing to a ConfigMap or Secret
- Remove all deprecated env vars (`GOSMEE_WEBHOOK_SIGNATURE`, `GOSMEE_REPLAY_TOKEN`, `GOSMEE_ALLOWED_IPS`, `GOSMEE_TRUST_PROXY`, `GOSMEE_FOOTER`, `GOSMEE_ENCRYPTED_CHANNELS_FILE`, `GOSMEE_CORS_ORIGIN`, `GOSMEE_MAX_BODY_SIZE`, `GOSMEE_AUTH_CONFIG_FILE`, `GOSMEE_AUTH_SESSION_SECRET`)
- Add health check for `/health` endpoint

**Changes to `misc/gosmee-client-deployment.yaml`:**
- No major changes needed (client still uses URL-based config), but update comments.

---

## Implementation Order
1. Fix #1 (context key) — blocks all RBAC testing
2. Fix #2 (session secret leak) — security
3. Fix #3 (hardcoded secret) — security
4. Fix #4 (admin/API auth gating) — security
5. Fix #5 (replay token bypass) — security
6. Fix #7 (username index) — performance
7. Fix #8 (TOCTOU race) — correctness
8. Fix #9 (batch bootstrap) — performance
9. Fix #6 (deprecated flags/envar warnings) — migration UX
10. Fix #10 (encodePublicKey dedup) — maintainability
11. Fix #11 (test helper dedup) — maintainability
12. Update #12 (SECURITY.md) — documentation
13. Update #13 (README.md) — documentation
14. Update #14 (Kubernetes manifests) — documentation

---

## Files Affected
| File | Issues |
|------|--------|
| `gosmee/store/acl.go` | #1 |
| `gosmee/auth.go` | #1 |
| `gosmee/store/api.go` | #2, #4 (setupModeMiddleware) |
| `gosmee/store/types.go` | #2 |
| `gosmee/server.go` | #3, #4, #5, #6 |
| `gosmee/auth_config.go` | #6 |
| `gosmee/protected_channels.go` | #6 |
| `gosmee/store/raft.go` | #7, #8 |
| `gosmee/store/fsm.go` | #8, #9 |
| `gosmee/store/bootstrap.go` | #9 |
| `gosmee/store/bridge.go` | #10 |
| `gosmee/crypto.go` | #10 |
| `gosmee/server_test.go` | #11 |
| `SECURITY.md` | #12 |
| `README.md` | #13 |
| `misc/gosmee-server-deployment.yaml` | #14 |
| `misc/gosmee-client-deployment.yaml` | #14 |
