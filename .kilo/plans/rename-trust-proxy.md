# Rename "Trust Proxy" to "Behind Reverse Proxy"

## Summary

Rename the `trust_proxy` / `TrustProxy` field to `behind_reverse_proxy` /
`BehindReverseProxy` across all layers (Go backend, Vue frontend, docs). The
label in the admin UI changes from "Trust Proxy" to "Behind Reverse Proxy".

## Naming convention

| Context | Old name | New name |
|---|---|---|
| Go struct field | `TrustProxy` | `BehindReverseProxy` |
| JSON key (API + Raft) | `trust_proxy` | `behind_reverse_proxy` |
| YAML key (bootstrap) | `trustproxy` (auto-lowercased from `TrustProxy`) | `behindreverseproxy` (auto-lowercased from `BehindReverseProxy`) |
| Vue UI label | `Trust Proxy` | `Behind Reverse Proxy` |
| CLI flag (deprecated, kept for migration) | `--trust-proxy` | **unchanged** (deprecated flag, keep old name) |
| Env var (deprecated, kept for migration) | `GOSMEE_TRUST_PROXY` | **unchanged** (deprecated env, keep old name) |

## Files to modify (in order)

### 1. `gohookbridge/store/types.go`

Rename the Go struct fields and JSON tags.

**Line 41-44** — `ServerConfig`:
```go
// OLD
TrustProxy    bool   `json:"trust_proxy"`

// NEW
BehindReverseProxy    bool   `json:"behind_reverse_proxy"`
```

**Line 54-56** — `ServerConfigResponse`:
```go
// OLD
TrustProxy    bool   `json:"trust_proxy"`

// NEW
BehindReverseProxy    bool   `json:"behind_reverse_proxy"`
```

**Line 128** — `defaultGlobalConfig()`:
```go
// OLD
TrustProxy:  false,

// NEW
BehindReverseProxy:  false,
```

### 2. `gohookbridge/store/api.go`

**Line 182** — `GetGlobalConfig()` response builder:
```go
// OLD
TrustProxy:    cfg.Server.TrustProxy,

// NEW
BehindReverseProxy:    cfg.Server.BehindReverseProxy,
```

### 3. `gohookbridge/store/raft.go`

**Lines 733-738** — method name and field access:
```go
// OLD
func (rs *RaftStore) ResolveTrustProxy() bool {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return false
	}
	return global.Server.TrustProxy
}

// NEW
func (rs *RaftStore) ResolveBehindReverseProxy() bool {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return false
	}
	return global.Server.BehindReverseProxy
}
```

### 4. `gohookbridge/server.go`

**Line 408-409** — `getRealIP()` function signature and parameter:
```go
// OLD
func getRealIP(r *http.Request, trustProxy bool) (net.IP, error) {
	if trustProxy {

// NEW
func getRealIP(r *http.Request, behindReverseProxy bool) (net.IP, error) {
	if behindReverseProxy {
```

**Lines 462-463** — caller in `ipRestrictMiddleware`:
```go
// OLD
trustProxy := rs.ResolveTrustProxy()
clientIP, err := getRealIP(r, trustProxy)

// NEW
behindReverseProxy := rs.ResolveBehindReverseProxy()
clientIP, err := getRealIP(r, behindReverseProxy)
```

**Line 736** — deprecated env var map (KEEP old env var name, update only the flag reference comment):
```go
// OLD
"GOSMEE_TRUST_PROXY":             "--trust-proxy",

// NEW — keep env var name, update the right-hand comment to reflect it was the old flag
"GOSMEE_TRUST_PROXY":             "--trust-proxy (deprecated, use global config server.behind_reverse_proxy)",
```

### 5. `gohookbridge/migrate.go`

**Line 33** — field access when reading deprecated env var:
```go
// OLD
global.Server.TrustProxy = v == "true" || v == "1"

// NEW
global.Server.BehindReverseProxy = v == "true" || v == "1"
```

### 6. `gohookbridge/store/raft_test.go`

**Line 105** — default config assertion:
```go
// OLD
assert.Assert(t, !cfg.Server.TrustProxy)

// NEW
assert.Assert(t, !cfg.Server.BehindReverseProxy)
```

**Line 118** — struct literal in `TestRaftStore_UpdateGlobalConfig`:
```go
// OLD
TrustProxy:  true,

// NEW
BehindReverseProxy:  true,
```

**Line 133** — assertion after update:
```go
// OLD
assert.Assert(t, cfg.Server.TrustProxy)

// NEW
assert.Assert(t, cfg.Server.BehindReverseProxy)
```

### 7. `gohookbridge/store/bootstrap_test.go`

**Line 18** — YAML test data (auto-lowercased from Go field name):
```go
// OLD
    trustproxy: true

// NEW
    behindreverseproxy: true
```

**Line 35** — assertion:
```go
// OLD
assert.Assert(t, cfg.Global.Server.TrustProxy)

// NEW
assert.Assert(t, cfg.Global.Server.BehindReverseProxy)
```

**Line 105** — struct literal in `TestApplyBootstrap_GlobalConfig`:
```go
// OLD
TrustProxy:  true,

// NEW
BehindReverseProxy:  true,
```

**Line 117** — assertion:
```go
// OLD
assert.Assert(t, got.Server.TrustProxy)

// NEW
assert.Assert(t, got.Server.BehindReverseProxy)
```

### 8. `gohookbridge/server_test.go`

No struct field changes — only comments referencing "trust proxy" in English prose:

**Line 608** — comment:
```go
// OLD
// Test X-Forwarded-For with trust proxy

// NEW
// Test X-Forwarded-For with behind reverse proxy
```

**Line 617** — comment:
```go
// OLD
// Test X-Forwarded-For without trust proxy

// NEW
// Test X-Forwarded-For without behind reverse proxy
```

### 9. `web/src/api/client.ts`

**Line 28** — TypeScript interface:
```typescript
// OLD
trust_proxy: boolean

// NEW
behind_reverse_proxy: boolean
```

### 10. `web/src/views/AdminGlobalView.vue`

**Line 15** — form item label:
```html
<!-- OLD -->
<n-form-item label="Trust Proxy">

<!-- NEW -->
<n-form-item label="Behind Reverse Proxy">
```

**Line 16** — v-model binding:
```html
<!-- OLD -->
<n-switch v-model:value="form.server.trust_proxy" />

<!-- NEW -->
<n-switch v-model:value="form.server.behind_reverse_proxy" />
```

**Line 48** — reactive initial value:
```typescript
// OLD
trust_proxy: false,

// NEW
behind_reverse_proxy: false,
```

**Line 63** — assignment from loaded config:
```typescript
// OLD
form.server.trust_proxy = config.value.server.trust_proxy

// NEW
form.server.behind_reverse_proxy = config.value.server.behind_reverse_proxy
```

### 11. `README.md`

Three locations, update `trust_proxy` to `behind_reverse_proxy`:

**Line 161** (prose):
```
// OLD
- Configure `trust_proxy` in global config when your Ingress...

// NEW
- Configure `behind_reverse_proxy` in global config when your Ingress...
```

**Line 360** (YAML example):
```yaml
# OLD
    trust_proxy: true

# NEW
    behind_reverse_proxy: true
```

**Line 483** (prose):
```
// OLD
> If you run gohookbridge with `trust_proxy: true` in global config...

// NEW
> If you run gohookbridge with `behind_reverse_proxy: true` in global config...
```

### 12. `SECURITY.md`

Multiple references. For each occurrence:

- **Line 74**: Prose mentioning `--trust-proxy` → update to mention `behind_reverse_proxy` in Raft config. The `--trust-proxy` flag examples are historical migration context — update the prose but keep old flag names in code examples that are clearly marked as legacy.
- **Lines 78, 84, 90**: CLI examples with `--trust-proxy` — add a note that these are legacy CLI examples and should now use `behind_reverse_proxy: true` in global config.
- **Line 97**: Prose with `GOSMEE_TRUST_PROXY` — add deprecation note.
- **Line 103**: `trust_proxy: true` → `behind_reverse_proxy: true`
- **Lines 105, 107, 109, 112**: Prose references to `--trust-proxy` — update to reflect new naming.
- **Line 340**: YAML example `trust_proxy: true` → `behind_reverse_proxy: true`

Specific edit operations:

**Line 74**:
```
// OLD
The examples below use `--trust-proxy` because they assume gohookbridge sits behind a reverse proxy. **Only enable `--trust-proxy` when...

// REPLACE ENTIRE BLOCK — see detailed replacement below
```

**Line 78, 84, 90** — replace the `--trust-proxy \` lines with a comment `# Note: --trust-proxy was a legacy flag. Use behind_reverse_proxy: true in global config instead.`

Actually, let me think about this more carefully. These are SECURITY.md examples for the "Restricting Webhook Sources by IP" section showing how to do IP allowlisting. Since `--trust-proxy` was a legacy CLI flag and the examples use it heavily, we need to decide how to handle this section.

The examples at lines 76-95 show `gohookbridge server --trust-proxy --allowed-ips ...` — these are CLI calls using the old flag. The whole section needs to be updated to reflect that configuration is now done via bootstrap.yaml/Admin UI.

**Plan for SECURITY.md updates:**

A. **Lines 74-75** (intro paragraph): Replace references to `--trust-proxy` flag with guidance to set `behind_reverse_proxy: true` in global config.

B. **Lines 76-95** (shell examples): These examples use deprecated `--trust-proxy` and `--allowed-ips` CLI flags. Add a header note saying these are legacy examples, and that configuration is now done via bootstrap.yaml or Admin UI. Keep the IP ranges as useful reference.

C. **Line 97**: Update prose — `GOSMEE_TRUST_PROXY` is deprecated, `GOSMEE_ALLOWED_IPS` is deprecated. Add note about migration.

D. **Line 101-112** ("Trusting Proxy Headers Safely" section): The section title should be updated. References to `--trust-proxy` → `behind_reverse_proxy` in Raft config. References to `trust_proxy: true` in YAML → `behind_reverse_proxy: true`.

E. **Line 340** (YAML example): `trust_proxy: true` → `behind_reverse_proxy: true`

Let me detail the exact replacements:

**Section A — Lines 74-75, replace the paragraph:**
```
// OLD
The examples below use `--trust-proxy` because they assume gohookbridge sits behind a reverse proxy. **Only enable `--trust-proxy` when gohookbridge is reachable exclusively through a trusted proxy that overwrites the forwarded headers** (see the warning under [Trusting Proxy Headers Safely](#trusting-proxy-headers-safely) below). If gohookbridge is directly reachable, drop `--trust-proxy` from these commands so the allowlist is enforced against the real connection address.

// NEW
Set `behind_reverse_proxy: true` in global config (via `bootstrap.yaml` or Admin UI) when gohookbridge sits behind a reverse proxy. **Only enable `behind_reverse_proxy` when gohookbridge is reachable exclusively through a trusted proxy that overwrites the forwarded headers** (see the warning under [Behind a Reverse Proxy Safely](#behind-a-reverse-proxy-safely) below). If gohookbridge is directly reachable, leave `behind_reverse_proxy` off so the allowlist is enforced against the real connection address.
```

**Section B — Lines 76-95, replace CLI examples with note + YAML example:**
Replace the three shell code blocks with a single note and configuration example.

**Section C — Line 97:**
```
// OLD
Use `--trust-proxy` when gohookbridge sits behind a reverse proxy so that `X-Forwarded-For` / `X-Real-IP` headers are used for the client IP. Both IPv4 and IPv6 addresses and CIDR ranges are supported. You can also set allowed IPs via the `GOSMEE_ALLOWED_IPS` environment variable (comma-separated) and enable proxy trust via `GOSMEE_TRUST_PROXY`.

// NEW
Set `behind_reverse_proxy: true` in global config when gohookbridge sits behind a reverse proxy so that `X-Forwarded-For` / `X-Real-IP` headers are used for the client IP. Both IPv4 and IPv6 addresses and CIDR ranges are supported. Allowed IPs can be set via `bootstrap.yaml` or Admin UI in the `allowed_ips` field per-channel or in global defaults.
```

**Section D — Lines 101-112 ("Trusting Proxy Headers Safely" section):**
- Anchor: `#trusting-proxy-headers-safely` → `#behind-a-reverse-proxy-safely`
- Title: `### Trusting Proxy Headers Safely` → `### Behind a Reverse Proxy Safely`
- Line 103: `trust_proxy: true` → `behind_reverse_proxy: true`
- Line 105: `--trust-proxy` → `behind_reverse_proxy`
- Line 107: `--trust-proxy` → `behind_reverse_proxy`  
- Line 109: `--trust-proxy` → `behind_reverse_proxy` (in "while `--trust-proxy` is on" → "while `behind_reverse_proxy` is on")
- Line 112: `--trust-proxy` → `behind_reverse_proxy` (in "leave `--trust-proxy` off" → "leave `behind_reverse_proxy` off")

**Section E — Line 340:**
```yaml
# OLD
    trust_proxy: true

# NEW
    behind_reverse_proxy: true
```

### 13. `design.md`

**Line 120** — table cell:
```
// OLD
| `/global/server/` | `ServerConfig` JSON | MaxBodySize, TrustProxy, CORS, etc. |

// NEW
| `/global/server/` | `ServerConfig` JSON | MaxBodySize, BehindReverseProxy, CORS, etc. |
```

### 14. Frontend build artifact — NO MANUAL CHANGE

`gohookbridge/web/static/assets/AdminGlobalView-V4-zFwti.js` is a build artifact
generated by Vite. It will be regenerated with the new field names when `make
build` is run. Do not edit this file manually.

### 15. Historical plan files — DO NOT EDIT

Files under `.kilo/plans/` are historical planning documents. Do not rename
references to `trust_proxy` in these files.

## Verification steps

After all changes:

1. `make lint-go` — Go linter passes
2. `make test` — all Go tests pass
3. `cd web && npx vue-tsc --noEmit` — TypeScript type checking passes
4. `make build` — full build succeeds with new field names in the embedded SPA
5. Manual smoke test: start server, open Admin UI, verify "Behind Reverse Proxy" label appears under Global Configuration
