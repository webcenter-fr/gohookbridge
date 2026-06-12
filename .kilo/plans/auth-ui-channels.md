# Plan: UI & Channel Authentication

## Objective
Add internal (login+password) and OIDC authentication to protect the web UI and channel creation endpoints, preventing unauthorized access while keeping webhook ingestion and SSE streams open.

## Scope
- Add a login page (`/login`)
- Protect `GET /`, `GET /{channel}` (UI), `GET /new` (channel creation)
- Keep webhook POST `/`{channel}` and SSE `/events/{channel}` open
- Replay endpoint already has `--replay-token` protection; keep it as-is

---

## Files to create

### 1. `gosmee/auth.go` — Session & auth core
**Session management:**
- `sessionSecret [32]byte` — HMAC key for signing session tokens (derived from flag/env)
- `sessionCookieName = "gosmee_session"`
- `sessionToken` struct: `{Username, Method ("internal"|"oidc"), Provider, ExpiresAt int64}`
- `encodeSession(sessionToken) string` — JSON + Base64URL + HMAC-SHA256 signature appended
- `decodeSession(token string) (*sessionToken, error)` — verify signature, parse, check expiry
- `setSessionCookie(w, token)` — sets `HttpOnly; Secure; SameSite=Lax; Path=/; MaxAge=86400` cookie
- `clearSessionCookie(w)` — clears the cookie

**Middleware:**
- `RequireAuth(config *AuthConfig) func(http.Handler) http.Handler` — chi middleware that:
  1. Reads session cookie
  2. If valid: injects username into `context` via `r.WithContext(context.WithValue(...))` and calls next
  3. If invalid: redirects to `/login?redirect=<original_url>` for GET, returns 401 for non-GET

**Handlers:**
- `LoginHandler(config *AuthConfig) http.HandlerFunc` — serves `GET /login` (HTML login page) and handles `POST /login` (username+password validation against bcrypt hashes)
- `LogoutHandler() http.HandlerFunc` — clears session cookie, redirects to `/login`

**Password validation:**
- `ValidatePassword(hash string, password string) bool` — uses `golang.org/x/crypto/bcrypt` to compare

### 2. `gosmee/auth_config.go` — Config loading
Auth config file format (JSON, path from `--auth-config-file` flag):

```json
{
  "internal": {
    "enabled": true,
    "users": [
      {
        "username": "admin",
        "password_hash": "$2a$10$..."
      }
    ]
  },
  "oidc": {
    "enabled": true,
    "providers": [
      {
        "id": "google",
        "name": "Google",
        "client_id": "...",
        "client_secret": "...",
        "issuer_url": "https://accounts.google.com",
        "scopes": ["openid", "profile", "email"]
      }
    ]
  }
}
```

**Types:**
```go
type AuthConfig struct {
    Internal InternalConfig
    OIDC     OIDCConfig
}
type InternalConfig struct {
    Enabled bool
    Users   []InternalUser
}
type InternalUser struct {
    Username     string
    PasswordHash string
}
type OIDCConfig struct {
    Enabled   bool
    Providers []OIDCProvider
}
type OIDCProvider struct {
    ID           string
    Name         string
    ClientID     string
    ClientSecret string
    IssuerURL    string
    Scopes       []string
}
```

`LoadAuthConfig(path string) (*AuthConfig, error)` — reads & unmarshals JSON.

If `path == ""`, return `nil` (auth disabled entirely, backward compatible).

### 3. `gosmee/auth_oidc.go` — OIDC handlers
- `NewOIDCHandler(provider OIDCProvider, sessionSecret []byte, publicURL string) *OIDCHandler`
  - On init: fetch `{issuerURL}/.well-known/openid-configuration` to get `authorization_endpoint`, `token_endpoint`, `userinfo_endpoint` (do this once at startup)
- `OIDCHandler.LoginHandler() http.HandlerFunc` — `GET /auth/oidc/{provider_id}/login`
  - Generates `state` param (random hex), stores in a short-lived signed cookie (`oidc_state`)
  - Generates `nonce` param, stores in cookie
  - Redirects to provider's authorization endpoint with `response_type=code`, `scope`, `state`, `nonce`, `redirect_uri`
- `OIDCHandler.CallbackHandler() http.HandlerFunc` — `GET /auth/oidc/{provider_id}/callback`
  - Validates `state` against cookie
  - Exchanges `code` for token at token endpoint
  - Calls `userinfo_endpoint` with access token to get user identity (email/sub)
  - Creates session token with `Method: "oidc"`, `Provider: providerID`
  - Sets session cookie, redirects to original URL (from `redirect` cookie or `/`)

### 4. `gosmee/templates/login.tmpl` — Login page
`//go:embed` into auth.go.

Simple HTML page with:
- Username + password form (POSTs to `/login`)
- If OIDC providers are configured, shows "Login with {Name}" buttons linking to `/auth/oidc/{id}/login`
- Displays error message from query param `?error=`
- `redirect` query param preserved as hidden field

### 5. `gosmee/auth_test.go` — Tests
- `TestSessionTokenRoundTrip` — encode then decode a valid token; verify invalid/expired/tampered tokens fail
- `TestValidatePassword` — bcrypt hash verification
- `TestLoadAuthConfig` — valid config, empty path returns nil, missing file error
- `TestAuthConfigNoFile` — nil config means auth disabled
- `TestRequireAuthMiddleware` — valid cookie passes, missing cookie redirects to login, expired returns redirect
- `TestLoginHandlerGET` — returns login page HTML
- `TestLoginHandlerPOST_Internal` — valid credentials set session cookie; invalid credentials return login page with error
- `TestLoginHandlerPOST_Disabled` — returns 404 when internal auth is not enabled
- `TestLogoutHandler` — clears cookie, redirects to /login
- `TestOIDCLoginHandler` — redirects to provider authorization endpoint with correct params
- `TestOIDCCallbackHandler` — mock OIDC provider token/userinfo endpoints (using httptest.Server); verify a valid code exchange sets session cookie
- `TestOIDCCallback_InvalidState` — rejects callback with mismatched state
- `TestFullProtectedFlow` — integration test: start gosmee server with auth config, try accessing UI without auth (gets redirect), login (gets session), access UI (success), logout (gets redirect), access UI again (redirect)

Test helpers:
- `newTestAuthConfig()` — creates a minimal in-memory AuthConfig with a bcrypt-hashed test user
- `newTestSessionToken(username, method)` — creates a valid session token

---

## Files to modify

### 6. `gosmee/flags.go` — Add server flags
Add to `serverFlags`:
```go
&cli.StringFlag{
    Name:   "auth-config-file",
    Usage:  "Path to JSON authentication configuration file",
    EnvVars: []string{"GOSMEE_AUTH_CONFIG_FILE"},
},
&cli.StringFlag{
    Name:   "auth-session-secret",
    Usage:  "Secret key for signing session cookies (32+ chars)",
    EnvVars: []string{"GOSMEE_AUTH_SESSION_SECRET"},
},
```

### 7. `gosmee/server.go` — Apply auth middleware
In `serve()`:
1. Call `LoadAuthConfig(authConfigFile)` after existing config loading (after line ~703)
2. If authConfig is not nil, derive `sessionSecret` from `--auth-session-secret` (SHA-256 hash it to 32 bytes)
3. Register login/logout/OIDC routes on `mainRouter` (before auth middleware):
   ```go
   mainRouter.Get("/login", auth.LoginHandler(authConfig))
   mainRouter.Post("/login", auth.LoginHandler(authConfig))
   mainRouter.Post("/logout", auth.LogoutHandler())
   mainRouter.Get("/auth/oidc/{provider_id}/login", oidcLoginHandler)
   mainRouter.Get("/auth/oidc/{provider_id}/callback", oidcCallbackHandler)
   ```
4. Create a protected sub-router for auth-required GET routes:
   ```go
   protectedRouter := chi.NewRouter()
   protectedRouter.Use(auth.RequireAuth(authConfig))
   protectedRouter.Get("/", serveIndex(publicURL, footer, protectedChannels))
   protectedRouter.Get(channelPath, serveIndex(publicURL, footer, protectedChannels))
   protectedRouter.Get("/new", showNewURL(publicURL, protectedChannels))
   ```
5. Remove the original `mainRouter.Get` registrations for `/`, `channelPath`, `/new`
6. Mount `protectedRouter` to the final router for GET requests only (when auth is enabled)
7. Keep health/version/favicon on `mainRouter` (unprotected)
8. Conditional: if authConfig is nil, keep existing behavior (no auth)

---

## Route matrix (final state)

| Route | Method | Auth required? | Notes |
|-------|--------|---------------|-------|
| `/health`, `/livez`, `/version` | GET | No | Health checks |
| `/favicon.ico` | GET | No | Favicon |
| `/events/{channel}` | GET | No | SSE stream for clients |
| `/{channel}` | POST | No | Webhook ingestion (protected by signature+IP) |
| `/replay/{channel}` | POST | No (own token) | Protected by `--replay-token` |
| `/login` | GET/POST | No | Auth pages |
| `/logout` | POST | No | Logout |
| `/auth/oidc/{id}/login` | GET | No | OIDC redirect start |
| `/auth/oidc/{id}/callback` | GET | No | OIDC callback |
| `/` | GET | **Yes** | Redirects to new channel |
| `/{channel}` | GET | **Yes** | Web UI |
| `/new` | GET | **Yes** | Channel creation |

---

## Implementation order

1. Create `gosmee/auth_config.go` — config types + loader
2. Create `gosmee/auth.go` — session management + internal login + middleware
3. Create `gosmee/templates/login.tmpl` — login page
4. Create `gosmee/auth_oidc.go` — OIDC handlers
5. Modify `gosmee/flags.go` — add server flags
6. Modify `gosmee/server.go` — wire auth into router
7. Create `gosmee/auth_test.go` — comprehensive tests

---

## Dependencies
- `golang.org/x/crypto/bcrypt` — already in `go.mod`
- No new external dependencies needed — OIDC uses `net/http` directly against provider endpoints, manual OIDC discovery

## Backward compatibility
- If `--auth-config-file` is not set, **all behavior is unchanged** — no auth middleware is applied
- Existing flags and routes are preserved when auth is disabled
