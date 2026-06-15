# Fix Login 404 — `/api/auth/login` returns 404

## Root Causes

### 1. Double `/api` prefix in store routes (ALL API calls broken)
`store/api.go:22` wraps all routes in `r.Route("/api", func(r chi.Router) { ... })`, but `server.go:753` already mounts `apiRouter` at `/api` via chi's `Mount`. This creates `/api/api/projects`, `/api/api/global`, etc. — none of the frontend's `/api/*` calls match.

### 2. Routing conflict: `/api` Mount shadows explicit auth routes
`mainRouter` has explicit routes at `/api/auth/methods`, `/api/auth/login`, `/api/auth/logout` (lines 738-740), but `mainRouter.Mount("/api", apiRouter)` at line 753 may intercept those requests first. In chi, `Mount` adds a `/api/*` catch-all that can shadow more specific explicit routes registered on the same router.

### 3. Inconsistent base URL in client
`web/src/api/client.ts:92` — the `login()` method uses a hardcoded absolute path `fetch('/api/auth/login', ...)` instead of using `this.base` like `getAuthMethods()` (`this.request('/auth/methods')`). While not the cause of 404, it's a consistency bug.

The `getAuthMethods()` call (`GET /api/auth/methods`) also 404s. LoginView falls back to showing the username/password form (line 69-72), but the actual `login()` call also 404s.

---

## Files to Change

### A. `gohookbridge/store/api.go` — Remove double `/api` prefix

**Line 22**: Remove `r.Route("/api", func(r chi.Router) {`
**Line 60**: Remove corresponding `})`
**All inner routes**: Un-indent by one level (they no longer live inside the `/api` group)

After fix, routes inside apiRouter become:
- `/projects`, `/global`, `/users`, `/rbac`, `/me`

Since apiRouter is mounted at `/api` via `mainRouter.Mount("/api", apiRouter)`, the final URLs become `/api/projects`, `/api/global`, etc.

Also need to handle the public auth routes — see section C.

### B. `gohookbridge/server.go` — Fix routing for public auth endpoints

**Lines 737-740**: Remove the three explicit auth routes from `mainRouter`:
```go
mainRouter.Get("/api/auth/methods", apiAuthMethodsHandler(rs))
mainRouter.Post("/api/auth/login", apiLoginHandler(rs))
mainRouter.Post("/api/auth/logout", apiLogoutHandler())
```

**Replace with**: Register them on a new `publicApiRouter` mounted at `/api/auth`, placed BEFORE the `/api` mount so chi prefers the more specific mount:

```go
publicApiRouter := chi.NewRouter()
publicApiRouter.Get("/methods", apiAuthMethodsHandler(rs))
publicApiRouter.Post("/login", apiLoginHandler(rs))
publicApiRouter.Post("/logout", apiLogoutHandler(rs))
mainRouter.Mount("/api/auth", publicApiRouter)
```

This ensures the public auth routes don't go through `RequireAuthDynamic` middleware.

### C. `web/src/api/client.ts` — Fix inconsistent URL pattern

**Line 91-102**: Change `login()` method to use `this.request()` with the base prefix like all other methods, OR change `fetch('/api/auth/login', ...)` to use the same pattern. Since `login` needs special error handling and credentials, keep the explicit `fetch` but change the path to be consistent.

Actually, the simplest fix here is to just change `/api/auth/login` to use `this.base`:
```typescript
const res = await fetch(`${this.base}/auth/login`, {
```
Same applies to `logout()` at line 105: change `'/logout'` to handle consistently.

---

## Implementation Status

### ✅ COMPLETED: Step 1 - Removed double `/api` prefix in `store/api.go`

Routes now defined as `/projects`, `/global`, `/users`, `/rbac`, `/me` inside `apiRouter`. Mounted at `/api` gives correct final paths.

### ✅ COMPLETED: Step 2 - Moved auth endpoints to `publicApiRouter` in `server.go`

Created `publicApiRouter` mounted at `/api/auth` before the main `/api` mount. Routes:
- GET `/api/auth/methods` → auth methods
- POST `/api/auth/login` → login handler
- POST `/api/auth/logout` → logout handler

### ✅ COMPLETED: Step 3 - Fixed login/logout URL patterns in `client.ts`

Changed:
- `login()`: `/api/auth/login` → `${this.base}/auth/login`
- `logout()`: `/logout` → `${this.base}/auth/logout`

---

## Implementation Order

1. **✅ `gohookbridge/store/api.go`** — Remove double `/api` prefix (core fix) - DONE
2. **✅ `gohookbridge/server.go`** — Move auth endpoints to publicApiRouter - DONE
3. **✅ `web/src/api/client.ts`** — Fix login/logout URL patterns - DONE

## Verification

To verify the fix works:
1. Start the server: `./bin/gohookbridge --dev-admin --dev-admin-password secret123`
2. Open browser to `http://localhost:3333`
3. Attempt login with admin/password
4. Expected: POST `/api/auth/login` returns 200 (not 404)
5. Expected: GET `/api/auth/methods` returns JSON with auth methods
6. All other API endpoints (`/api/projects`, `/api/me`, etc.) should work

Go build verified: ✅ PASS
TypeScript check: Pre-existing errors unrelated to these changes