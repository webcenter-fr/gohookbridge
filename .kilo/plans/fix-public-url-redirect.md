# Fix: Public URL redirection to 0.0.0.0 instead of current host

## Problem

When server is started with `server --address 0.0.0.0 --port 3333` (no `--public-url`), `effectivePublicURL()` falls back to constructing `http://0.0.0.0:3333` from the bind address. This causes:

- Redirect to `/` visits redirects to `http://0.0.0.0:3333/<channel>` (line 170, `server.go`)
- Template renders URLs as `http://0.0.0.0:3333/<channel>` (line 178, `server.go`)
- `/new` endpoint returns `http://0.0.0.0:3333/<channel>` (line 159, `server.go`)
- Admin page passes `PublicURL` as `http://0.0.0.0:3333` to template (line 807)

`0.0.0.0` is not a routable address — the browser resolves connections via `localhost` or the machine's real hostname/IP.

`--public-url` **is** the right flag for users behind a proxy with a known domain (and OIDC callbacks need it), but users on `localhost` just want the URL to match what's in their browser address bar.

## Root cause

`publicURL` is computed once at startup as a global string, using `--address` (a bind address) as fallback. Request handlers use this global string without considering the `Host` header from the actual request.

## Places where `publicURL` is used

| Location | File:line | Purpose |
|---|---|---|
| `serveIndex` redirect | `server.go:170` | Redirect `/` to `/<channel>` |
| `serveIndex` template var | `server.go:178` | Show webhook URL in index template |
| `showNewURL` | `server.go:159` | Return new channel URL |
| Admin handler | `server.go:807` | Pass `PublicURL` to admin template |
| OIDC LoginHandler | `auth_oidc.go:78` | OIDC redirect URI |
| OIDC exchangeCode | `auth_oidc.go:155` | OIDC redirect URI (token exchange) |
| Console log | `server.go:844` | Startup log message |

## Plan

### 1. Add `requestBaseURL` helper

Add a function that resolves the proper base URL for a given request:

```go
func requestBaseURL(r *http.Request, explicitPublicURL, portAddr string, sslEnabled bool) string {
    if explicitPublicURL != "" {
        return explicitPublicURL
    }
    scheme := "http://"
    if sslEnabled {
        scheme = "https://"
    }
    return scheme + r.Host
}
```

### 2. Keep `effectivePublicURL` for startup log and OIDC

`effectivePublicURL` stays as-is — it provides a sensible fallback for the startup log message. For OIDC, `--public-url` **must** be set explicitly to a real, externally resolvable URL (existing behavior, no change needed).

### 3. Modify `serveIndex` to resolve URL from request

Change signature to accept `explicitPublicURL string` instead of `publicURL string`. Inside:
- Line 170: Use `requestBaseURL(r, explicitPublicURL, ...)` for redirect
- Line 178: Use `requestBaseURL(r, explicitPublicURL, ...)` for template URL

### 4. Modify `showNewURL` to resolve URL from request

Same pattern — use `requestBaseURL` to build the `/new` response.

### 5. Modify admin handler to resolve URL from request

In the inline admin handler at `server.go:796-814`, use `requestBaseURL` to determine `PublicURL` for the template.

### 6. Update `serve()` to pass relevant params

Pass the original `--public-url` flag value (empty string if not set) alongside `portAddr` and `sslEnabled` to the handlers that need it. The `serve()` function already has all these values in scope.

### Files changed

- `gohookbridge/server.go` — all changes in one file

### Behavior after fix

| Scenario | Before | After |
|---|---|---|
| `--address 0.0.0.0 --port 3333` (no `--public-url`) | Redirects to `http://0.0.0.0:3333/...` | Redirects to `http://<r.Host>/...` (e.g. `http://localhost:3333/...`) |
| `--address 0.0.0.0 --port 3333 --public-url https://hooks.example.com` | Uses `https://hooks.example.com` | Uses `https://hooks.example.com` (unchanged) |
| `--address localhost --port 3333` | Uses `http://localhost:3333` | Uses `http://<r.Host>` (same in practice) |
