# Vue 3 SPA + Auth-Protected UI Plan

## Goal

Replace Go `html/template`-based UI with a Vue 3 + Vite SPA embedded in the Go binary. All UI routes require authentication. OIDC is primary auth when configured; local auth is the fallback.

## Architecture

```
Browser (SPA) ─── /api/* (JSON)  ─── Go chi router ─── RaftStore
              ─── /events/* (SSE) ─── Go SSE handler
              ─── /auth/oidc/*     ─── OIDC handlers
              ─── /*               ─── Go serves embedded SPA index.html
```

Session cookies (same-origin, no CORS for auth) — the existing cookie-based auth mechanism stays exactly as-is. The SPA reads auth state from `/api/me`.

---

## Phase 1: Vue 3 + Vite Project Scaffold

### 1.1 Create `web/` directory with Vite + Vue 3

```
web/
├── index.html
├── package.json
├── vite.config.ts
├── tsconfig.json
└── src/
    ├── main.ts
    ├── App.vue
    ├── router/
    │   └── index.ts
    ├── stores/
    │   ├── auth.ts            # Pinia: session state, login/logout
    │   ├── channels.ts        # Pinia: channel CRUD
    │   └── events.ts          # Pinia: SSE event stream per channel
    ├── api/
    │   └── client.ts          # Typed fetch wrapper for /api/* endpoints
    ├── views/
    │   ├── LoginView.vue
    │   ├── DashboardView.vue       # Channel list + create
    │   ├── ChannelView.vue         # Single channel: settings + live SSE events
    │   ├── AdminGlobalView.vue     # Global config
    │   ├── AdminUsersView.vue      # User management
    │   ├── AdminRBACView.vue       # Roles & role bindings
    │   └── AdminOIDCView.vue       # OIDC provider management
    └── components/
        ├── AppLayout.vue           # Nav bar, sidebar, content slot
        ├── EventFeed.vue           # SSE real-time event display
        ├── JsonViewer.vue          # JSON tree view for payloads
        └── ProtectedRoute.vue      # Wrapper: redirect to /login if unauthenticated
```

### 1.2 Dependencies

| Package | Purpose |
|---|---|
| `vue@3` | Framework |
| `vue-router@4` | Client-side routing |
| `pinia` | State management |
| `naive-ui` | Component library (tables, modals, forms — dark theme built-in) |
| `vite` | Build tool |
| `@vitejs/plugin-vue` | Vue SFC compiler |
| `typescript` | Type checking |

### 1.3 Vite Config

```ts
// vite.config.ts
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../gohookbridge/web/static',
    emptyOutDir: true,
  },
})
```

Output lands at `gohookbridge/web/static/` — single `index.html` + hashed JS/CSS assets in `assets/`. Go embeds the entire directory.

---

## Phase 2: Go Backend Changes

### 2.1 Embed the SPA

```go
// gohookbridge/web.go (new file)
package gohookbridge

import "embed"

//go:embed web/static/*
var spaAssets embed.FS
```

### 2.2 SPA File Server + Fallback Handler

```go
func spaHandler() http.Handler {
    sub, _ := fs.Sub(spaAssets, "web/static")
    fileServer := http.FileServer(http.FS(sub))

    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Try to serve the exact file
        path := strings.TrimPrefix(r.URL.Path, "/")
        f, err := sub.Open(path)
        if err == nil {
            f.Close()
            fileServer.ServeHTTP(w, r)
            return
        }
        // SPA fallback: serve index.html for all unmatched routes
        r.URL.Path = "/"
        fileServer.ServeHTTP(w, r)
    })
}
```

### 2.3 Route Restructuring in `serve()` (server.go)

**Keep as-is (unchanged):**
- `GET /favicon.ico`
- `GET /version`, `/health`, `/livez`
- `GET /events/{channel}` — SSE (no auth required, channel validation still applies)
- `POST /{channel}` — webhook ingestion (unchanged)
- `POST /replay/{channel}` — replay (unchanged)
- `GET /login`, `POST /login`, `POST /logout` — auth endpoints
- `GET /auth/oidc/{id}/login`, `GET /auth/oidc/{id}/callback` — OIDC
- `Mount /api` — all REST API handlers (already auth-protected)

**Route all other GET requests to the SPA:**

```go
// SPA handler for all non-API, non-SSE, non-webhook GET requests
mainRouter.NotFound(spaHandler())
```

**NOTE:** Since the old template routes (serveIndex, showNewURL, admin page) are now handled by the SPA, remove their chi route registrations. The SPA does client-side routing for `/`, `/{channel}`, `/admin/*`, etc.

### 2.4 Remove Old Template Code

Delete from `server.go`:
- `serveIndex()` function
- `showNewURL()` function
- Admin template handler (the inline func serving adminTmpl)
- `//go:embed templates/index.tmpl`, `admin.tmpl` directives
- `indexTmpl`, `adminTmpl` variables

Delete files:
- `gohookbridge/templates/index.tmpl`
- `gohookbridge/templates/admin.tmpl`

Keep `login.tmpl` for now (auth endpoints still server-rendered; Phase 4 migrates login to SPA).

### 2.5 `/api/me` Enhancement

The existing `/api/me` endpoint already returns user info. Ensure it returns OIDC provider list so the SPA login page can show OIDC buttons:

```json
{
  "username": "...",
  "roles": [...],
  "projects": [...],
  "permissions": [...],
  "auth_methods": {
    "oidc_providers": [{"id": "google", "name": "Google"}],
    "local_enabled": true
  }
}
```

Add a new unauthenticated endpoint for the login page:

```go
// GET /api/auth/methods — public, returns available auth methods
r.Get("/api/auth/methods", func(w http.ResponseWriter, r *http.Request) {
    cfg := store.BuildAuthConfig(rs)
    // returns { oidc_providers: [...], local_enabled: bool }
})
```

---

## Phase 3: Auth Implementation in SPA

### 3.1 Auth Store (`stores/auth.ts`)

```ts
// On app load:
// 1. Call GET /api/me
// 2. If 200: user is authenticated, store user info
// 3. If 401: user is unauthenticated

// On login (local):
// 1. POST /login with FormData { username, password, redirect }
// 2. On 302 redirect, check /api/me again

// On login (OIDC):
// 1. Redirect browser to /auth/oidc/{id}/login?redirect={current}
// 2. OIDC callback sets cookie, redirects back to SPA
// 3. SPA loads /api/me on mount
```

### 3.2 Router Guards

```ts
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.checkSession()  // calls /api/me

  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }

  if (to.meta.requiresAdmin && !auth.isAdmin) {
    return { path: '/dashboard' }
  }
})
```

All routes except `/login` require auth.

### 3.3 Login Page (`LoginView.vue`)

Layout:
```
┌─────────────────────────────┐
│        Gosmee Logo          │
│     Sign in to continue     │
│                             │
│  ┌───────────────────────┐  │
│  │  🔐 Login with Google │  │  ← OIDC buttons (primary, top)
│  └───────────────────────┘  │
│  ┌───────────────────────┐  │
│  │  🔐 Login with GitHub │  │
│  └───────────────────────┘  │
│                             │
│  ──────── or ────────       │  ← divider (only if both enabled)
│                             │
│  Username: [___________]    │  ← local auth form (secondary)
│  Password: [___________]    │
│  [Sign In]                  │
└─────────────────────────────┘
```

Logic:
- On mount, call `/api/auth/methods` to get available providers
- OIDC buttons render at top (primary position)
- Local form below (if users exist)
- If only local: only show the form
- If only OIDC: only show provider buttons, no divider

---

## Phase 4: Vue Views Implementation

### 4.1 Dashboard (`DashboardView.vue`)
- List all channels (projects) the user has access to
- "New Channel" button → modal with channel ID input
- Each channel row: name, URL, signature count, actions (view, settings, delete)
- Uses Naive UI `n-data-table`, `n-modal`, `n-button`

### 4.2 Channel View (`ChannelView.vue`)
- **Settings tab**: channel name, webhook signatures, allowed IPs, max body size, replay token
- **Events tab**: live SSE feed (reuse EventFeed component), replay button
- Uses `n-tabs`, `n-form`, `n-input`, `n-dynamic-input` for signature/IP arrays

### 4.3 Admin Views (behind admin role guard)
- **Global Config**: max body size, trust proxy, CORS origin, footer HTML
- **Users**: CRUD table, create user form with password
- **RBAC**: role list, binding management per user
- **OIDC Providers**: CRUD table (id, name, client_id, client_secret, issuer_url, scopes)

### 4.4 EventFeed Component
- SSE connection to `/events/{channel}`
- Real-time event list with collapse/expand per event
- JSON tree view (use naive-ui or a small JSON viewer)
- Replay button (POST `/replay/{channel}`)
- Status indicator (connected/disconnected/connecting)

### 4.5 AppLayout Component
- Top navbar: logo, channel selector or breadcrumb, user menu (username, logout)
- Sidebar: navigation links (Dashboard, Admin sections if authorized)
- Dark theme matching current color scheme (`--bg: #0F172A`)

---

## Phase 5: Build Integration

### 5.1 Makefile Target

```makefile
.PHONY: web-build
web-build:
	cd web && npm ci && npm run build

.PHONY: build
build: web-build
	go build -o bin/gosmee .
```

### 5.2 Development Workflow

During development, run the Vite dev server (`npm run dev`) with proxy to Go backend:

```ts
// vite.config.ts (dev mode)
export default defineConfig({
  server: {
    proxy: {
      '/api': 'http://localhost:3333',
      '/events': 'http://localhost:3333',
      '/auth': 'http://localhost:3333',
      '/login': 'http://localhost:3333',
      '/logout': 'http://localhost:3333',
    }
  }
})
```

Production: Go serves the pre-built SPA. No dev server needed.

---

## Phase 6: Cleanup

### Files to Remove
- `gohookbridge/templates/index.tmpl`
- `gohookbridge/templates/admin.tmpl`
- `gohookbridge/templates/login.tmpl` (after Phase 3 login SPA migration)

### Code to Remove from `server.go`
- `serveIndex()` function
- `showNewURL()` function
- Admin template rendering block
- `indexTmpl`, `adminTmpl`, `loginTmpl` `//go:embed` directives
- `LoginHandler`/`LoginHandlerDynamic` — replace with API-based login:
  - `POST /api/auth/login` returns JSON `{ ok: true }` + sets cookie (no redirect)
  - `POST /api/auth/logout` clears cookie, returns `{ ok: true }`

### /login Endpoint Migration

Replace the current server-rendered `/login` with an API-driven flow:

```go
// POST /api/auth/login — accepts JSON { username, password }
// Returns 200 + sets session cookie, or 401
r.Post("/api/auth/login", apiLoginHandler(rs))

// GET /api/auth/methods — public, returns available providers
r.Get("/api/auth/methods", apiAuthMethodsHandler(rs))
```

The old `GET /login` and `POST /login` handlers are no longer needed — the SPA handles the login page. The OIDC redirect flow (`/auth/oidc/*`) stays as-is.

### Keep Unchanged
- `POST /logout` — clears session cookie (still needed for OIDC redirect back)
- `GET/POST /auth/oidc/*` — unchanged
- All `/api/*` handlers — unchanged
- `handleEventsGet`, `handleWebhookPost`, `handleReplayPost` — unchanged
- `auth.go` session cookie logic — unchanged

---

## Implementation Order

1. **Scaffold Vue project** — `npm create vue@latest`, install naive-ui, pinia, vue-router
2. **Build auth store + login page** — get auth fully working first
3. **Build AppLayout + router guards** — protected shell
4. **Port Dashboard** — channel list from API
5. **Port Channel view** — settings form + SSE events
6. **Port Admin views** — global, users, RBAC, OIDC
7. **Go backend changes** — embed, SPA handler, cleanup template code
8. **Build integration** — Makefile, dev proxy, test embedded build
9. **Remove old templates** — cleanup
