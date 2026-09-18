# Plan: Migrate admin UI from Vue 3 (Vite) SPA to Nuxt 4 + Helm/K8s first-time deployment

**Feature branch:** `feat/nuxt-migration` (created from `main`; PR opened against `main` at the end).
**Resolves:** GitHub issue #13 ("feat - Use nuxt instead pure vue.js").
**Scope:** rewrite `web/` as a Nuxt 4 SPA (`ssr: false`, static via `nuxt generate`, embedded into the Go binary exactly as today); switch Naive UI → Nuxt UI; update Helm chart for HA (3 replicas) and deploy + validate on the home k3s cluster.

---

## 1. Target architecture

### 1.1 Runtime model (unchanged from today)
- Nuxt 4 app compiled with `ssr: false` + `nuxt generate` → static output (an `index.html` + hashed JS/CSS bundles + `public/` assets).
- Output is copied into `gohookbridge/web/static/` and embedded with `//go:embed` in `gohookbridge/web/handler.go`. **No Node runtime in production.** The single Go binary serves the SPA plus all `/api`, `/events`, `/auth`, `/login`, `/logout`, OIDC routes.

### 1.2 Directory layout under `web/` (Nuxt 4 `srcDir = app/`)
```
web/
├── nuxt.config.ts          # NEW — Nuxt 4 config (see §1.4)
├── package.json            # REWRITTEN — Nuxt deps + scripts
├── tsconfig.json           # REWRITTEN — extends ./.nuxt/tsconfig.json
├── vitest.config.ts        # REWRITTEN — @nuxt/test-utils
├── scripts/
│   └── copy-to-static.mjs  # NEW — copies .output/public → ../gohookbridge/web/static
├── public/
│   ├── logo.svg            # kept (Nuxt serves public/ at root)
│   └── favicon.svg         # kept
├── app/
│   ├── app.vue             # NEW — root component (<UApp>, <NuxtLayout>, <NuxtPage>, <NuxtLoadingIndicator>, <UToast>)
│   ├── app.config.ts       # NEW — Nuxt UI theme (force dark)
│   ├── assets/css/main.css # NEW — @import "tailwindcss"; @import "@nuxt/ui";
│   ├── layouts/default.vue # from AppLayout.vue (sidebar shell)
│   ├── pages/              # from views/
│   │   ├── login.vue
│   │   ├── index.vue
│   │   ├── channels/index.vue
│   │   ├── channels/[id].vue
│   │   ├── admin/global.vue
│   │   ├── admin/users.vue
│   │   ├── admin/rbac.vue
│   │   ├── admin/oidc.vue
│   │   ├── admin/bans.vue
│   │   └── [channel].vue     # NEW — /:channel redirect
│   ├── components/
│   │   ├── EventFeed.vue
│   │   ├── JsonViewer.vue
│   │   └── AppCodeBlock.vue   # NEW — replaces Naive NCode
│   ├── stores/
│   │   ├── auth.ts
│   │   ├── channels.ts
│   │   └── events.ts
│   ├── middleware/
│   │   ├── auth.global.ts     # NEW — replaces router.beforeEach (auth + guest)
│   │   └── admin.ts           # NEW — requiresAdmin
│   └── utils/
│       ├── api.ts             # from api/client.ts
│       ├── crypto.ts
│       └── units.ts
├── tests/                    # NEW — ported + new frontend tests
│   ├── stores/auth.spec.ts
│   ├── stores/channels.spec.ts
│   ├── stores/events.spec.ts
│   ├── components/EventFeed.spec.ts
│   ├── pages/ChannelDetailView.spec.ts
│   └── pages/ChannelsView.spec.ts
```
Deleted (obsolete): `web/src/**`, `web/index.html`, `web/env.d.ts`, `web/vite.config.ts`.

### 1.3 Library choices (verified against Context7)
| Purpose | Package | Notes |
|---|---|---|
| Framework | `nuxt` (Nuxt 4, latest stable) | `srcDir` defaults to `app/` in Nuxt 4 |
| UI | `@nuxt/ui` (v4, latest stable) | requires `tailwindcss`; module `'@nuxt/ui'` |
| State | `pinia` + `@pinia/nuxt` | stores auto-imported from `app/stores/` |
| Crypto | `tweetnacl` + `tweetnacl-util` | unchanged (browser-only) |
| Test | `vitest` + `@nuxt/test-utils` + `@vue/test-utils` | `mountSuspended` |
| Type-check | `vue-tsc` (via `nuxi typecheck`) | `nuxt typecheck` script |

### 1.4 `web/nuxt.config.ts` (required content)
```ts
export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: false },
  ssr: false,                                   // client-only SPA
  modules: ['@nuxt/ui', '@pinia/nuxt'],
  css: ['~/assets/css/main.css'],
  app: {
    baseURL: '/',
    // CRITICAL: Go's go:embed excludes files/dirs starting with '_' or '.'.
    // Nuxt's default buildAssetsDir is '/_nuxt/', which would be silently
    // skipped by go:embed. Rename it to avoid the underscore-prefixed dir.
    // This coupling is also documented in CONTRIBUTING.md and verified by
    // scripts/copy-to-static.mjs (see §6.3).
    buildAssetsDir: '/assets/',
  },
  nitro: {
    // preset: 'static' is optional when ssr:false (nuxt generate infers it),
    // but explicit for clarity. Verified against Nuxt 4 docs.
    preset: 'static',
  },
  colorMode: {
    preference: 'dark',
    fallback: 'dark',
    disableTransition: true,
  },
  runtimeConfig: {
    public: {
      // no env needed today (API is same-origin relative /api)
    },
  },
  typescript: {
    strict: true,
    typeCheck: true,
  },
  // Dev proxy to the Go backend (dev only; Vite's built-in proxy).
  // The Go server default port is 8081 (see §1.5). Nuxt dev server runs on
  // its own port (default 3000) and proxies /api, /events, /auth to 8081.
  // In production, the Go binary serves everything — no proxy needed.
  vite: {
    server: {
      proxy: {
        '/api':    { target: 'http://localhost:8081', changeOrigin: true },
        '/events': { target: 'http://localhost:8081', changeOrigin: true },
        '/auth':   { target: 'http://localhost:8081', changeOrigin: true },
      },
    },
  },
})
```
> Note: `/login` and `/logout` are not real server GET routes (login/logout go through `/api/auth/*`). The legacy Vite proxy listed them, but only `/api`, `/events`, `/auth` are actually exercised by the app. Adding `/login`/`/logout` to the proxy is optional and harmless. (SSE via `/events` must NOT be buffered — Vite's proxy passes streaming through.)

### 1.5 Port conventions (standardized on 8081 for local dev)
- **Go server default port:** `8081` (`gohookbridge/flags.go: DefaultServerPort = 8081`). The `Makefile` `dev-server` target does NOT pass `--port`, so it binds `8081`. This is the canonical local dev port.
- **Nuxt dev server port:** `3000` (Nuxt default). The Nuxt dev server proxies `/api`, `/events`, `/auth` to `http://localhost:8081` via `vite.server.proxy` (see §1.4). Work against `http://localhost:3000` in the browser during development.
- **Helm container port:** `3333` (in-cluster only; the Service and Ingress map this to external traffic). This is a separate concern from local dev — the container port is arbitrary and does not affect local development.
- **CONTRIBUTING.md lines 97 and 121** incorrectly say the dev server starts on `3333` and the Vite proxy targets `3333`. Both will be corrected to `8081` (see §12).

---

## 2. File-by-file migration map

Legend: `→` = destination; bullets = required transformation.

| Source (`web/src/`) | Destination (`web/app/`) | Transformations |
|---|---|---|
| `main.ts` | `app.vue` (+ `@pinia/nuxt`) | Remove `createApp`/`createPinia`/`app.use(naive)`. Pinia auto-registered by `@pinia/nuxt`. Root becomes `<UApp><NuxtLayout><NuxtPage/></NuxtLayout></UApp>` + `<NuxtLoadingIndicator>` + `<UToast/>` |
| `App.vue` | `app.vue` + `app/app.config.ts` | `NConfigProvider :theme=darkTheme` + providers → `<UApp>` + `colorMode: { preference: 'dark' }`. `NMessageProvider`/`NNotificationProvider` → single `<UToast/>`. `NLoadingBarProvider` → `<NuxtLoadingIndicator/>`. Global `body` styles → `app/assets/css/main.css` |
| `router/index.ts` | `app/middleware/auth.global.ts` + `app/middleware/admin.ts` + `pages/` structure | Route table → file-based routes. `beforeEach` → global middleware (see §4). `/:channel` redirect → `pages/[channel].vue` |
| `api/client.ts` | `app/utils/api.ts` | Keep `class ApiClient` + `export const api`. Replace `fetch(...)` with `$fetch(...)` (ofetch) using `{ credentials: 'include', headers: {...} }`; or keep `fetch` (both work same-origin; `$fetch` is idiomatic). **Do NOT use `useFetch`/`useAsyncData`** — stores own state and `ssr:false` makes them unnecessary. All interfaces (UserInfo, Channel, GlobalConfig, User, Role, Binding, OIDCProvider, RoleMapping, ChannelRoleMapping, BanEntry) copied unchanged. Named export `api` for auto-import; interfaces exported explicitly. |
| `stores/auth.ts` | `app/stores/auth.ts` | Unchanged logic; drop explicit `useAuthStore` imports where Nuxt auto-imports (keep explicit imports if simpler). No SSR guards needed (`ssr:false`). |
| `stores/channels.ts` | `app/stores/channels.ts` | Unchanged. |
| `stores/events.ts` | `app/stores/events.ts` | Unchanged (EventSource is client-only; fine under `ssr:false`). |
| `components/AppLayout.vue` | `app/layouts/default.vue` | `<NuxtLayout>` default layout. `NLayout/NLayoutHeader/NLayoutSider` → plain `<div class="flex h-screen">` + `<header>` + `<aside>` with Tailwind. `NMenu` → `UVerticalNavigation` (items from `menuOptions`). `useRouter/useRoute` → Nuxt auto-imported composables. `router-link` → `<NuxtLink to="/">`. `<router-view/>` → `<NuxtPage/>`. |
| `components/EventFeed.vue` | `app/components/EventFeed.vue` | Component mapping per §3. |
| `components/JsonViewer.vue` | `app/components/JsonViewer.vue` | `NCode` → `AppCodeBlock` (new). Keep `decodeBodyB` logic. |
| `utils/crypto.ts` | `app/utils/crypto.ts` | Unchanged (auto-imported named exports: `isE2EEncrypted`, `decryptE2E`, `generateKeyPair`). |
| `utils/units.ts` | `app/utils/units.ts` | Unchanged. |
| `views/LoginView.vue` | `app/pages/login.vue` | `definePageMeta({ layout: false, middleware: 'guest' })`. `useRouter/useRoute` → Nuxt auto-imports. Naive components per §3. |
| `views/DashboardView.vue` | `app/pages/index.vue` | `onMounted` fetch stays; `NModal` "New Channel" form → `UModal` + `UFormField`. |
| `views/ChannelsView.vue` | `app/pages/channels/index.vue` | `NDataTable` selection column → `UTable` `v-model:selection`. Delete modal stays as `UModal`. |
| `views/ChannelDetailView.vue` | `app/pages/channels/[id].vue` | `route.params.id` → `useRoute().params.id`. `NTabs/NTabPane` → `UTabs items`. `NDrawer` → `USlideover side="right"`. `NSelect`/`NInputNumber`/`NDynamicInput` per §3. SSE connect in `onMounted` (unchanged). |
| `views/AdminGlobalView.vue` | `app/pages/admin/global.vue` | `definePageMeta({ middleware: 'admin' })`. |
| `views/AdminUsersView.vue` | `app/pages/admin/users.vue` | `definePageMeta({ middleware: 'admin' })`. `useDialog` delete → `useConfirm` (UModal wrapper) or inline `UModal`. |
| `views/AdminRBACView.vue` | `app/pages/admin/rbac.vue` | `definePageMeta({ middleware: 'admin' })`. |
| `views/AdminOIDCView.vue` | `app/pages/admin/oidc.vue` | `definePageMeta({ middleware: 'admin' })`. |
| `views/AdminBansView.vue` | `app/pages/admin/bans.vue` | `definePageMeta({ middleware: 'admin' })`. `setInterval` + `onUnmounted` unchanged. |
| — (new) | `app/pages/[channel].vue` | Catch-all redirect: `const route = useRoute(); await navigateTo('/channels/' + route.params.channel, { replace: true })` in `<script setup>`. |
| — (new) | `app/components/AppCodeBlock.vue` | Props: `code: string`, `language?: string`. Renders `<pre class="..."><code>{{ code }}</code></pre>` with monospace Tailwind styling (Nuxt UI has no code block). |
| `views/__tests__/*.spec.ts` | `web/tests/**` | See §7.1. |
| `index.html` | deleted | Title/meta via `useHead` in `app.vue` or `app/app.config.ts`. |
| `env.d.ts` | deleted | Nuxt 4 generates types in `.nuxt/`. |
| `vite.config.ts` | deleted | replaced by `nuxt.config.ts`. |
| `public/logo.svg`, `public/favicon.svg` | `web/public/` | unchanged (Nuxt `public/` is at project root, not under `app/`). |

---

## 3. Naive UI → Nuxt UI component mapping table

Full inventory of Naive UI components actually used (grep-verified). Nuxt UI v4 equivalents with **exact prop/slot names** (verified against Context7 Nuxt UI v4 docs). The coder MUST run `npm run typecheck` after porting each page to catch API mismatches before moving to the next page.

| Naive UI | Nuxt UI v4 | Exact API (props, slots, events) |
|---|---|---|
| `NConfigProvider` + `darkTheme` | `colorMode` config + `<UApp>` | `colorMode.preference: 'dark'` in `nuxt.config.ts`. `<UApp>` wraps root in `app.vue`. |
| `NLoadingBarProvider` | `<NuxtLoadingIndicator/>` | Place in `app.vue` above `<UApp>`. No props needed. |
| `NMessageProvider` / `useMessage` | `useToast()` + `<UToast/>` | `const toast = useToast()`; `toast.add({ title: 'msg', color: 'success' \| 'error' \| 'warning' \| 'info' })`. `<UToast/>` in `app.vue`. |
| `NNotificationProvider` | `useToast()` | Merged with messages (single toast system). |
| `NDialogProvider` / `useDialog` | `useOverlay()` + `UModal` | `dialog.warning({ title, content, positiveText, onPositiveClick })` → a small `useConfirm()` composable that calls `useOverlay().create(UModal, { props: { title, description }, on: { confirm: () => resolve(true) } })`. Or inline a `<UModal v-model:open>` with confirm/cancel buttons per page. |
| `NLayout`/`NLayoutHeader`/`NLayoutSider` | plain HTML + Tailwind flex | **No direct equivalent.** Build in `layouts/default.vue`: `<div class="flex h-screen flex-col">` → `<header class="flex items-center justify-between h-14 px-6 border-b">` → `<div class="flex flex-1">` → `<aside class="w-55 border-r p-3">` → `<main class="flex-1 p-6 overflow-y-auto">`. |
| `NMenu` | `UVerticalNavigation` | `:items="[{ label: 'Dashboard', icon: 'i-lucide-layout-dashboard', to: '/' }, ...]"`. `v-model` for active item. Divider: `{ type: 'separator' }`. |
| `NGrid`/`NGi` | Tailwind grid | `class="grid grid-cols-4 gap-3"` on parent; each child is a `<div>`. |
| `NSpace` | Tailwind flex/gap | `class="flex gap-2 items-center"` (horizontal) or `class="flex flex-col gap-2"` (vertical). |
| `NDivider` | `USeparator` | `<USeparator/>` (no props needed). |
| `NForm` | native `<form @submit.prevent>` | Keep manual validation logic. Replace `FormInst.validate()` with a plain async `validate()` function that checks rules and returns `true`/throws. |
| `NFormItem` | `UFormField` | `label="Username"`, `hint="Optional"`, `:error="errorMsg"`. Wrap `<UInput>` inside default slot. |
| `NInput` | `UInput` | `v-model`, `placeholder`, `:disabled`, `type="password"`, `readonly`. Size: `size="sm"`. |
| `NInput` (textarea) | `UTextarea` | `v-model`, `placeholder`, `:rows="3"`, `:maxlength="500"`. |
| `NInputNumber` | `UInput type="number"` | `v-model.number`, `:min="0"`. **No spinner.** |
| `NSelect` (single) | `USelect` | `v-model`, `:items="[{ label: 'X', value: 'x' }]"`, `placeholder`. |
| `NSelect` (multiple) | `USelectMenu` | `v-model` (array), `:items="[...]"`, `multiple`. |
| `NSwitch` | `USwitch` | `v-model` (boolean). |
| `NDynamicInput` | custom `v-for` + `UInput` + `UButton` | **No direct equivalent.** Render `v-for="(ip, i) in ips"` with `<UInput v-model="ips[i]"/>` + `<UButton icon="i-lucide-plus" @click="ips.push('')"/>` + `<UButton icon="i-lucide-trash" @click="ips.splice(i,1)"/>`. |
| `NInputGroup`/`NInputGroupLabel` | `UInputGroup` + `UInput` | `<UInputGroup><UInput .../><UButton .../></UInputGroup>`. |
| `NButton` | `UButton` | `color="primary"` (was `type=primary`), `color="error"` (was `type=error`), `variant="soft"` (was `secondary`), `variant="ghost"` (was `quaternary`), `size="sm"` (was `small`), `size="xs"` (was `tiny`), `:loading`, `block`, `:disabled`. |
| `NDataTable` | `UTable` | `:data`, `:columns="[{ accessorKey: 'id', header: 'ID' }]"`, `:loading`, `v-model:selection` (array of row keys), `#cell-<key>` slot for custom cells. **No `render()` functions** — replace all `h(NTag, ...)` / `h(NButton, ...)` with slot templates. |
| `NCard` | `UCard` | `title="Title"`, no `hoverable` prop (use `class="cursor-pointer hover:shadow-md"`). |
| `NTag` | `UBadge` | `color="success"` (was `type=success`), `color="info"`, `color="warning"`, `color="error"`, `color="neutral"` (was `type=default`), `size="sm"`. |
| `NText` | native `<p>/<span>` + Tailwind | `class="text-sm text-(--ui-text-muted)"` for `depth="3"`. |
| `NH3`/`NH4`/`NH5` | native `<h3>/<h4>/<h5>` | `class="text-lg font-semibold"` etc. |
| `NCode` | **`AppCodeBlock.vue`** (custom) | Nuxt UI has no code block. New component: `<pre class="bg-(--ui-bg-elevated) rounded p-3 overflow-x-auto text-sm font-mono"><code>{{ code }}</code></pre>`. Props: `code: string`, `language?: string`. |
| `NList`/`NListItem` | native `<ul>/<li>` or `UTable` | Use `<ul class="divide-y">` + `<li>` for simple lists; `UTable` for structured data. |
| `NStatistic` | custom `UCard` + large text | **No direct equivalent.** `<UCard><div class="text-3xl font-bold">{{ count }}</div><div class="text-sm text-(--ui-text-muted)">channels available</div></UCard>`. |
| `NIcon` | `UIcon` | `name="i-lucide-layout-dashboard"` (lucide icon names). Replace emoji `h('span', '📋')` with `i-lucide-*` names. |
| `NAlert` | `UAlert` | `title="Title"`, `description="..."`, `color="warning"`. No `:bordered="false"` prop (default is borderless). |
| `NModal` | `UModal` | `v-model:open` (was `v-model:show`), `title="Title"`. Content in default slot. Footer actions in `#footer` slot. |
| `NDrawer`/`NDrawerContent` | `USlideover` | `v-model:open`, `title="Send Payload"`, `side="right"`. Content in default slot. |
| `NSpin` | per-component `:loading` or `<UProgress>` | `UTable :loading`, `UButton :loading`. For wrapping content: `<div :class="{ 'opacity-50 pointer-events-none': loading }">`. |
| `NEmpty` | custom placeholder | **No direct equivalent.** `<div class="flex flex-col items-center gap-2 py-8 text-(--ui-text-muted)"><UIcon name="i-lucide-inbox" class="size-8"/><span>No events yet</span></div>`. |
| `NTabs`/`NTabPane` | `UTabs` | `:items="[{ label: 'Data', slot: 'data' }, { label: 'Settings', slot: 'settings' }]"`. Content per tab via named slots: `<template #data>...</template>`. `v-model` for active tab index. |

### 3.1 Verification loop (mandatory per-page workflow)
After porting each page, the coder MUST:
1. Run `cd web && npm run typecheck` — fix all TypeScript errors before moving on.
2. Visually compare the rendered page against the old Naive UI version (run both dev servers side-by-side: `make dev-server` on 8081, `cd web && npm run dev` on 3000).
3. Confirm all interactive elements work (form submission, modal open/close, table selection, SSE connect/disconnect, toast messages).
4. If a Nuxt UI component API cannot be confirmed against the mapping table above, **fall back to native HTML + Tailwind** (e.g., `<select>` instead of `USelect`, `<table>` instead of `UTable`) and note it as a known gap.

---

## 4. Auth / session handling

### 4.1 Session model (backend unchanged)
- Cookie `gosmee_session` (HttpOnly, Secure, SameSite=Lax, 86400s), set by `POST /api/auth/login`, cleared by `POST /api/auth/logout`.
- `GET /api/me` returns current user (or 401). `RequireAuthDynamic` protects `/api/*`.
- `ssr:false` → all session checks happen in the browser; cookies are sent automatically by `$fetch`/`fetch` with `credentials: 'include'` on same-origin requests. No SSR cookie handling needed.

### 4.2 Route guards → Nuxt middleware
**`app/middleware/auth.global.ts`** (global; replaces `router.beforeEach`):
```ts
export default defineNuxtRouteMiddleware(async (to) => {
  const auth = useAuthStore()
  await auth.checkSession()

  if (to.meta.public !== true && !auth.isAuthenticated) {
    return navigateTo({ path: '/login', query: { redirect: to.fullPath } })
  }
  if (to.meta.guest === true && auth.isAuthenticated) {
    return navigateTo('/')
  }
})
```
**`app/middleware/admin.ts`**:
```ts
export default defineNuxtRouteMiddleware((to) => {
  const auth = useAuthStore()
  if (!auth.isAdmin) return navigateTo('/')
})
```
Page `definePageMeta` declarations:
- `login.vue`: `{ layout: false, public: true, guest: true }`
- all `admin/*.vue`: `{ middleware: 'admin' }` (auth is already enforced globally)
- all other pages under the default layout: no extra meta (auth enforced globally).

### 4.3 `/login`, `/logout`, OIDC
- `/login` is a client page (`pages/login.vue`), NOT a server route. `auth.login()` POSTs `/api/auth/login`, then navigates to `route.query.redirect || '/'`.
- Logout button calls `auth.logout()` (POST `/api/auth/logout`), then `router.push('/login')`.
- OIDC login: `window.location.href = '/auth/oidc/' + providerID + '/login?redirect=...'` — a real server route in `server.go`; dev proxy must forward `/auth` (done).

### 4.4 `/:channel` redirect
`app/pages/[channel].vue` (see §2) reproduces the old `redirect: to => ({ name: 'channel-detail', params: { id } })`.

---

## 5. SSE / EventFeed

Unchanged behavior, moved to Nuxt:
- `stores/events.ts` keeps `EventSource('/events/' + channel)` with `onopen/onmessage/onerror`, `connect/disconnect/clear/setDecryptionKey`.
- E2E decryption stays browser-only via `tweetnacl` (`utils/crypto.ts` `decryptE2E`) on the nested `bodyB` envelope — logic unchanged.
- Lifecycle: `ChannelDetailView` `onMounted` → `eventsStore.connect(channelId)`; `onUnmounted` → `eventsStore.disconnect()`. Manual Connect/Disconnect buttons retained.
- **Reconnection:** the current code does NOT auto-reconnect on `onerror` (it only sets `connected=false`); the user presses "Connect" again. Preserve this exact behavior (no `setTimeout` retry loop) to avoid scope creep; note it as a known limitation.
- `ssr:false` guarantees `EventSource`/`atob`/`navigator` only run client-side; no `.client` guards needed.

---

## 6. Go backend, Makefile, Dockerfile, .gitignore

### 6.1 `gohookbridge/web/handler.go`
Keep `SPAHandler()` signature (`func SPAHandler() http.Handler`) and the `fs.Sub`/`http.FileServer` approach. Changes:

1. **Embed directive stays `//go:embed static/*`** — this works ONLY because `app.buildAssetsDir` is renamed to `/assets/` (no `_` prefix). Document this coupling in a comment. The `scripts/copy-to-static.mjs` script (see §6.3) also verifies no `_`/`.`-prefixed entries exist at the root of `.output/public` before copying.
2. **SPA fallback** — current logic already rewrites unknown paths to `/` and serves `index.html`. Keep it, but harden:
   - Serve the file if it exists (unchanged).
   - For unknown GET paths, serve `index.html` (unchanged) with `Content-Type: text/html; charset=utf-8`.
   - Add cache headers: paths under `/assets/` (hashed, immutable) → `Cache-Control: public, max-age=31536000, immutable`; `index.html` and everything else → `Cache-Control: no-cache`.
3. Concrete pseudocode for the handler:
```go
func SPAHandler() http.Handler {
    sub, _ := fs.Sub(spaAssets, "static")
    fileServer := http.FileServer(http.FS(sub))
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        p := strings.TrimPrefix(r.URL.Path, "/")
        if p != "" {
            if f, err := sub.Open(p); err == nil {
                f.Close()
                if strings.HasPrefix(p, "assets/") {
                    w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
                }
                fileServer.ServeHTTP(w, r)
                return
            }
        }
        w.Header().Set("Cache-Control", "no-cache")
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        r.URL.Path = "/"
        fileServer.ServeHTTP(w, r)
    })
}
```
4. No server route changes required: `mainRouter.NotFound(web.SPAHandler().ServeHTTP)` already serves the SPA for all unmatched GETs, and `/favicon.ico` is served by an existing route.

### 6.1a `gohookbridge/web/handler_test.go` (new — exact test cases)
Extract the fallback logic into a testable helper `spaHandlerFrom(fsys fs.FS) http.Handler` so both `SPAHandler()` and tests can use it. Then write these test cases using `httptest`:

```go
func TestSPAHandler_ServesIndexForDeepLink(t *testing.T) {
    // Create a temp dir with: index.html, assets/app-abc123.js, logo.svg
    // Mount as fs.FS, call spaHandlerFrom(fsys)
    h := spaHandlerFrom(testFS)

    t.Run("root serves index.html", func(t *testing.T) {
        req := httptest.NewRequest("GET", "/", nil)
        w := httptest.NewRecorder()
        h.ServeHTTP(w, req)
        assert.Equal(t, http.StatusOK, w.Code)
        assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
        assert.Assert(t, strings.Contains(w.Body.String(), "<!DOCTYPE html>"))
    })

    t.Run("deep link serves index.html with no-cache", func(t *testing.T) {
        req := httptest.NewRequest("GET", "/channels/my-channel", nil)
        w := httptest.NewRecorder()
        h.ServeHTTP(w, req)
        assert.Equal(t, http.StatusOK, w.Code)
        assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
        assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
    })

    t.Run("hashed asset returns immutable cache header", func(t *testing.T) {
        req := httptest.NewRequest("GET", "/assets/app-abc123.js", nil)
        w := httptest.NewRecorder()
        h.ServeHTTP(w, req)
        assert.Equal(t, http.StatusOK, w.Code)
        assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
    })

    t.Run("missing asset falls back to index.html", func(t *testing.T) {
        req := httptest.NewRequest("GET", "/nonexistent", nil)
        w := httptest.NewRecorder()
        h.ServeHTTP(w, req)
        assert.Equal(t, http.StatusOK, w.Code)
        assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
    })

    t.Run("static asset without assets/ prefix gets no special cache", func(t *testing.T) {
        req := httptest.NewRequest("GET", "/logo.svg", nil)
        w := httptest.NewRecorder()
        h.ServeHTTP(w, req)
        assert.Equal(t, http.StatusOK, w.Code)
        // no Cache-Control header set (or default)
    })
}
```

### 6.2 `Makefile`
- `web-build`: keep `cd web && npm ci && npm run build` (the npm `build` script now runs `nuxt generate` + copy — see §6.3).
- Add a **guard** in the `build` target: after `web-build`, assert that `gohookbridge/web/static/index.html` exists. If missing, fail with a clear error. This prevents a silent `go build` with an empty embed (which would produce a binary with no UI).
  ```makefile
  build: web-build clean
  	@test -f gohookbridge/web/static/index.html || (echo "ERROR: gohookbridge/web/static/index.html missing after web-build. Check nuxt generate output." && exit 1)
  	@echo "building."
  	@mkdir -p $(OUTPUT_DIR)/
  	@go build $(FLAGS) -o $(OUTPUT_DIR)/$(NAME) ./cmd/gohookbridge
  ```
- Add targets and wire them in:
  - `web-typecheck:` `cd web && npm run typecheck` (runs `nuxt typecheck`).
  - `web-test:` `cd web && npm test` (vitest run).
  - Extend `test:` to also run `web-test` (so `make test` covers Go + frontend).
  - Extend `lint:` to also run `web-typecheck` (keeps `make lint` = go + md + ts).
- `dev-server`: unchanged (already proxies-free backend on 8081).

### 6.3 `web/package.json` scripts (final)
```json
"scripts": {
  "dev": "nuxt dev",
  "build": "nuxt generate && node scripts/copy-to-static.mjs",
  "generate": "nuxt generate",
  "typecheck": "nuxt typecheck",
  "test": "vitest run"
}
```
`scripts/copy-to-static.mjs` (Node 20+, uses `fs.cpSync`; includes `_`/`.` guard):
```js
import { cpSync, rmSync, existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const src = '.output/public'
const dst = '../gohookbridge/web/static'

if (!existsSync(src)) {
  console.error('ERROR: .output/public is missing. Did "nuxt generate" run?')
  process.exit(1)
}

// CRITICAL: Go's go:embed silently excludes files/dirs starting with '_' or '.'.
// If Nuxt ever changes its output to include such entries, the embedded binary
// would be silently broken. Fail the build loudly if any such entry exists.
const forbidden = readdirSync(src).filter(e => e.startsWith('_') || e.startsWith('.'))
if (forbidden.length > 0) {
  console.error(`ERROR: .output/public contains entries that go:embed would exclude: ${forbidden.join(', ')}`)
  console.error('These files would be silently missing from the Go binary.')
  console.error('Check nuxt.config.ts app.buildAssetsDir — it must NOT start with _ or .')
  process.exit(1)
}

rmSync(dst, { recursive: true, force: true })
cpSync(src, dst, { recursive: true })
console.log(`Copied ${src} → ${dst}`)
```
(`nuxt generate` outputs `.output/public/index.html` + `assets/` bundles — verified against Nuxt docs for `ssr:false`.)

### 6.4 `Dockerfile`
Current image does NOT build web assets; `go build` with `//go:embed static/*` would fail without `static/`. Add a Node build stage with a guard:
```dockerfile
FROM --platform=$BUILDPLATFORM node:22-alpine AS webbuild
WORKDIR /src
COPY gohookbridge/web/ ./gohookbridge/web/
COPY web/ ./web/
WORKDIR /src/web
RUN npm ci && npm run build          # emits /src/gohookbridge/web/static
# Guard: fail the build if the static output is missing (catches nuxt generate failures)
RUN test -f /src/gohookbridge/web/static/index.html || (echo "ERROR: static/index.html missing after nuxt generate" && exit 1)

FROM --platform=$BUILDPLATFORM golang:latest
COPY . /go/src/github.com/webcenter-fr/gohookbridge
COPY --from=webbuild /src/gohookbridge/web/static /go/src/github.com/webcenter-fr/gohookbridge/gohookbridge/web/static
WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
ARG TARGETARCH
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gohookbridge ./cmd/gohookbridge
# ... unchanged runtime stage (ubi9-minimal, user 1001, ENTRYPOINT) ...
```

### 6.5 `.gitignore`
Add:
```
web/.nuxt/
web/.output/
gohookbridge/web/static/
```
And in the PR, `git rm -r --cached gohookbridge/web/static` to stop tracking the currently-committed Vite build output (CONTRIBUTING.md already documents it as "gitignored build output"; the current files are a leftover). This makes `make build` and the Docker build the source of truth for the embedded assets.

---

## 7. Testing plan

### 7.1 Frontend unit tests (port + new)
`web/vitest.config.ts`:
```ts
import { defineVitestConfig } from '@nuxt/test-utils/config'
export default defineVitestConfig({
  test: { environment: 'nuxt' },
})
```
`package.json` already `"type": "module"`. Ports:
- `tests/pages/ChannelDetailView.spec.ts` and `tests/pages/ChannelsView.spec.ts`: port existing `vi.mock('../../api/client', ...)` mocks to `vi.mock('~/utils/api', ...)`; mount pages with `mountSuspended(Component, { route: '/channels/test-chan' })`. Nuxt UI components resolve via module; stub heavy ones (`UTable`, `USlideover`) where needed for shallow behavior.
- New `tests/stores/auth.spec.ts` (checkSession/login/logout/isAdmin), `tests/stores/channels.spec.ts` (fetch/create/delete), `tests/stores/events.spec.ts` (connect/disconnect/message parsing + E2E decrypt path with mocked `EventSource` and a fixed tweetnacl keypair), `tests/components/EventFeed.spec.ts` (connected/connecting states, encrypted tag), and a `tests/middleware/auth.spec.ts` (auth + admin middleware redirects using `@nuxt/test-utils` router mocks or `navigateTo` spy).

### 7.2 UI + backend integration test (new)
Add a scripted integration test (e.g. `tests/e2e/integration.spec.ts`, run only when `GO_HOOKBRIDGE_E2E=1` to avoid burdening default CI):
1. Build: `make build` (produces `bin/gohookbridge` with embedded Nuxt assets).
2. Start `./bin/gohookbridge server --address 0.0.0.0 --port 8081 --dev-admin` (auto-creates admin).
3. Assert `GET http://localhost:8081/` returns `200` and HTML contains the Nuxt mount (`<div id="__nuxt">`) and `/assets/` script tags.
4. `POST /api/auth/login` with dev-admin credentials (read password from `raft-data/admin-password.txt`), capture session cookie.
5. Create a channel via `POST /api/channels`, then `POST /{channel}` a webhook, open SSE `GET /events/{channel}` (via the session cookie) and assert the `data:` frame arrives.
6. Confirm deep-link fallback: `GET /channels/{id}` returns `200` HTML (SPA fallback).
7. Shut down and clean temp raft dir.

### 7.3 Backend integration tests (must still pass)
Existing `make test` (`go test ./...`) covers api/publisher/consumer/client. Add one new Go test `gohookbridge/web/handler_test.go`:
- `TestSPAHandler_ServesIndexForDeepLink` — embed a fixture `static/` (via `//go:embed` testdata or a temp dir) and assert unknown paths return `index.html`, and `/assets/app.js` returns the hashed file with the immutable cache header.
(Note: the production embed uses `//go:embed static/*`; the test can construct `fs.Sub` on a temp dir with the same logic by extracting the fallback into a helper, e.g. `spaHandlerFrom(fs.FS)` used by both `SPAHandler()` and the test.)

### 7.4 CI updates (`.github/workflows/go.yml`)
- Add `actions/setup-node@v4` (`node-version: 22`) to the **build** job (before `make build`).
- Add a new **web** job that runs **before** the build job (or in parallel; the build job already runs `web-build` independently):
  ```yaml
  web:
    name: Web (Typecheck + Test)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6.0.3
      - uses: actions/setup-node@v4
        with: { node-version: 22 }
      - run: cd web && npm ci
      - run: cd web && npm run typecheck
      - run: cd web && npm test
  ```
- Keep `lint` (go) and `test` (go) jobs; optionally add `make web-typecheck` to lint job for parity.
- `make build` already runs `web-build` (npm ci + nuxt generate + copy), so the build job validates the full Go + Nuxt pipeline. The `web` job provides faster feedback on frontend-only issues.

---

## 8. Helm chart + Kubernetes deployment (HA, 3 replicas)

### 8.1 Raft/NATS HA requirements (from `store/raft.go`, `flags.go`, `design.md`)
- Each pod needs a **stable identity + address**: `--raft-node-id=<pod-name>` and `--raft-bind-addr=0.0.0.0:6001`.
- Peers: `--raft-peers=server-0=server-0.<headless-svc>:6001,server-1=...,server-2=...`.
- NATS mesh: `--nats-routes=nats://server-0.<headless-svc>:6222,...`.
- Each pod needs its **own** raft dir → StatefulSet `volumeClaimTemplates` (current chart uses a single Deployment + one RWO PVC, which is wrong for HA).

### 8.2 `helm/gohookbridge/values.yaml` changes
```yaml
server:
  enabled: true
  replicas: 3                      # HA default
  image: { repository: ghcr.io/webcenter-fr/gohookbridge, tag: <pr-tag>, pullPolicy: IfNotPresent }
  port: 3333
  raftPort: 6001
  natsPort: 4222
  natsClusterPort: 6222
  publicURL: "https://gohookbridge-test.home.webcenter.fr"
  raftDir: /data/raft
  resources:
    requests: { cpu: 100m, memory: 128Mi }
    limits:   { cpu: 500m, memory: 512Mi }
  storage:
    size: 1Gi
    storageClass: ""                # leave "" for default; k3s local-path
  bootstrap:
    enabled: true
    config: {}                      # bootstrap.yaml content (admin user + session secret) OR via ConfigMap
  ingress:
    enabled: true
    className: traefik              # k3s default ingress controller
    hosts: ["gohookbridge-test.home.webcenter.fr"]
    tls:
      - hosts: ["gohookbridge-test.home.webcenter.fr"]
        secretName: gohookbridge-test-tls
  service:
    type: ClusterIP
    port: 3333
  probes:
    liveness:  { httpGet: { path: /livez,  port: http } }
    readiness: { httpGet: { path: /health, port: http } }
```

### 8.3 Template changes

#### 8.3a `_helpers.tpl` — add peer/route computation helpers
Add these helpers to `templates/_helpers.tpl` (before the existing helpers):

```yaml
{{/*
Raft peers string: "server-0=server-0.<headless>.<ns>.svc:6001,server-1=..."
*/}}
{{- define "gohookbridge.raftPeers" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
{{ $name }}-server-{{ $i }}={{ $name }}-server-{{ $i }}.{{ $name }}-server-headless.{{ $ns }}.svc:{{ $.Values.server.raftPort }}
{{- end -}}
{{- end -}}

{{/*
NATS routes string: "nats://server-0.<headless>.<ns>.svc:6222,nats://..."
*/}}
{{- define "gohookbridge.natsRoutes" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
nats://{{ $name }}-server-{{ $i }}.{{ $name }}-server-headless.{{ $ns }}.svc:{{ $.Values.server.natsClusterPort }}
{{- end -}}
{{- end -}}
```

#### 8.3b Replace `templates/server-deployment.yaml` with a StatefulSet
Create `templates/server-statefulset.yaml` (and delete the old `server-deployment.yaml`):

```yaml
{{- if .Values.server.enabled }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{ include "gohookbridge.fullname" . }}-server
  labels:
    {{- include "gohookbridge.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
spec:
  serviceName: {{ include "gohookbridge.fullname" . }}-server-headless
  replicas: {{ .Values.server.replicas }}
  podManagementPolicy: Parallel   # all pods start simultaneously for faster quorum
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ include "gohookbridge.name" . }}
      app.kubernetes.io/component: server
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ include "gohookbridge.name" . }}
        app.kubernetes.io/component: server
    spec:
      {{- if .Values.serviceAccount.create }}
      serviceAccountName: {{ include "gohookbridge.fullname" . }}
      {{- end }}
      containers:
      - name: server
        image: "{{ .Values.server.image.repository }}:{{ .Values.server.image.tag }}"
        imagePullPolicy: {{ .Values.server.image.pullPolicy }}
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        args:
        - server
        - --port
        - "{{ .Values.server.port }}"
        - --public-url
        - {{ .Values.server.publicURL | quote }}
        - --raft-dir
        - {{ .Values.server.raftDir | quote }}
        - --raft-node-id
        - $(POD_NAME)
        - --raft-bind-addr
        - 0.0.0.0:{{ .Values.server.raftPort }}
        - --raft-peers
        - {{ include "gohookbridge.raftPeers" . | quote }}
        - --nats-port
        - "{{ .Values.server.natsPort }}"
        - --nats-cluster-port
        - "{{ .Values.server.natsClusterPort }}"
        - --nats-routes
        - {{ include "gohookbridge.natsRoutes" . | quote }}
        {{- if .Values.server.bootstrap.enabled }}
        - --bootstrap-config-file
        - /etc/gohookbridge/bootstrap.yaml
        {{- end }}
        ports:
        - name: http
          containerPort: {{ .Values.server.port }}
          protocol: TCP
        - name: raft
          containerPort: {{ .Values.server.raftPort }}
          protocol: TCP
        - name: nats
          containerPort: {{ .Values.server.natsPort }}
          protocol: TCP
        - name: nats-cluster
          containerPort: {{ .Values.server.natsClusterPort }}
          protocol: TCP
        {{- with .Values.server.probes }}
        livenessProbe:
          httpGet:
            path: {{ .liveness.httpGet.path }}
            port: {{ .liveness.httpGet.port }}
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: {{ .readiness.httpGet.path }}
            port: {{ .readiness.httpGet.port }}
          initialDelaySeconds: 5
          periodSeconds: 5
        {{- end }}
        volumeMounts:
        - name: raft-data
          mountPath: {{ .Values.server.raftDir }}
        {{- if .Values.server.bootstrap.enabled }}
        - name: bootstrap
          mountPath: /etc/gohookbridge
          readOnly: true
        {{- end }}
        {{- with .Values.server.resources }}
        resources:
          {{- toYaml . | nindent 10 }}
        {{- end }}
      volumes:
      {{- if .Values.server.bootstrap.enabled }}
      - name: bootstrap
        configMap:
          name: {{ include "gohookbridge.fullname" . }}-bootstrap
      {{- end }}
  volumeClaimTemplates:
  - metadata:
      name: raft-data
    spec:
      accessModes: ["ReadWriteOnce"]
      {{- if .Values.server.storage.storageClass }}
      storageClassName: {{ .Values.server.storage.storageClass }}
      {{- end }}
      resources:
        requests:
          storage: {{ .Values.server.storage.size }}
{{- end }}
```

#### 8.3c Add `templates/server-headless-service.yaml`
```yaml
{{- if .Values.server.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "gohookbridge.fullname" . }}-server-headless
  labels:
    {{- include "gohookbridge.labels" . | nindent 4 }}
    app.kubernetes.io/component: server
spec:
  clusterIP: None
  ports:
  - name: raft
    port: {{ .Values.server.raftPort }}
    targetPort: {{ .Values.server.raftPort }}
    protocol: TCP
  - name: nats-cluster
    port: {{ .Values.server.natsClusterPort }}
    targetPort: {{ .Values.server.natsClusterPort }}
    protocol: TCP
  selector:
    app.kubernetes.io/name: {{ include "gohookbridge.name" . }}
    app.kubernetes.io/component: server
{{- end }}
```

#### 8.3d Keep `templates/server-service.yaml` (ClusterIP http/3333) as the ingress backend — unchanged.

#### 8.3e Add `templates/server-bootstrap-configmap.yaml` (when `server.bootstrap.enabled`)
```yaml
{{- if and .Values.server.enabled .Values.server.bootstrap.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "gohookbridge.fullname" . }}-bootstrap
  labels:
    {{- include "gohookbridge.labels" . | nindent 4 }}
data:
  bootstrap.yaml: |
    {{- .Values.server.bootstrap.config | toYaml | nindent 4 }}
{{- end }}
```

#### 8.3f `templates/server-ingress.yaml` — unchanged logic; it already renders `hosts` and `tls` from values.

#### 8.3g Pre-deploy validation (mandatory before `helm upgrade --install`)
```bash
# 1. Lint the chart
helm lint ./helm/gohookbridge

# 2. Render the templates and inspect the generated args
helm template gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge \
  --values helm/gohookbridge/values-home.yaml \
  | grep -A1 'raft-peers\|nats-routes\|raft-node-id'

# 3. Verify the rendered raft-peers string matches:
#    "gohookbridge-server-0=gohookbridge-server-0.gohookbridge-server-headless.gohookbridge.svc:6001,..."
#    (3 entries for replicas=3, comma-separated, no trailing comma)
# 4. Verify the rendered nats-routes string matches:
#    "nats://gohookbridge-server-0.gohookbridge-server-headless.gohookbridge.svc:6222,..."
```

#### 8.3h Rollback / first-boot recovery procedure
If the 3-node Raft cluster never forms quorum (pods crash-loop or stay in `CrashLoopBackOff`):
1. Check logs: `kubectl logs gohookbridge-server-0 -n gohookbridge | grep -i 'raft\|leader\|quorum'`
2. If "no leader" or "timeout waiting for leader": delete all PVCs to reset Raft state:
   ```bash
   kubectl delete pvc -n gohookbridge -l app.kubernetes.io/component=server
   ```
3. Scale to 1 to bootstrap a single-node cluster first:
   ```bash
   kubectl scale statefulset gohookbridge-server -n gohookbridge --replicas=1
   ```
4. Wait for pod-0 to become Ready and confirm leader election in logs.
5. Scale back to 3:
   ```bash
   kubectl scale statefulset gohookbridge-server -n gohookbridge --replicas=3
   ```
6. Verify all 3 pods join the cluster: `kubectl logs gohookbridge-server-1 -n gohookbridge | grep 'raft'` should show follower status.

### 8.4 Deploy command (home cluster)
Create `helm/gohookbridge/values-home.yaml` (gitignored, like `AGENTS.local.md`) with the above home-specific values (replicas 3, publicURL, ingress host/TLS). Then:
```bash
export KUBECONFIG=/home/user/.kube/home
helm upgrade --install gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge --create-namespace \
  --kubeconfig /home/user/.kube/home \
  --values helm/gohookbridge/values-home.yaml
```

### 8.5 Validation on cluster
1. `kubectl --kubeconfig /home/user/.kube/home get pods -n gohookbridge` → 3/3 Running (StatefulSet `gohookbridge-server-0/1/2`).
2. `kubectl logs gohookbridge-server-0 -n gohookbridge` → Raft leader elected, NATS routes established across 3 nodes.
3. **UI availability:** `curl -I https://gohookbridge-test.home.webcenter.fr` → 200; load login page in browser; log in with bootstrap admin; open Dashboard, create a channel, open Channel Detail and confirm SSE connects (live "Connected" status).
4. **Full encrypted (E2E) webhook:**
   - In the channel Settings tab, set Encryption Mode = `e2e`, generate keypair (downloads `gohookbridge-key-<id>.json`), copy public key.
   - Produce: `gohookbridge produce --pubkey <pubkey> https://gohookbridge-test.home.webcenter.fr/<channel> payload.json`
   - Consume: `gohookbridge client --encryption-key-file ./gohookbridge-key-<id>.json https://gohookbridge-test.home.webcenter.fr/<channel> http://localhost:8080` → assert decrypted payload matches.
5. **Partial encrypted (server-side AES) webhook:**
   - Set Encryption Mode = `server_side`, generate key, copy AES key.
   - Post plaintext: `curl -X POST -H 'Content-Type: application/json' -d '{"k":"v"}' https://gohookbridge-test.home.webcenter.fr/<channel>`
   - Consume: `gohookbridge client --encryption-key <aeskey> https://gohookbridge-test.home.webcenter.fr/<channel> http://localhost:8080` → assert decrypted `{"k":"v"}`.
6. Confirm `kubectl get statefulset,pvc -n gohookbridge` shows 3 per-pod PVCs.

---

## 9. Edge cases & error handling
- **Deep links / SPA fallback:** `SPAHandler` serves `index.html` for unknown GETs (already wired via `NotFound`). Add the cache-header rules from §6.1.
- **Base path:** `app.baseURL: '/'` (no subpath); keep `buildAssetsDir` absolute `/assets/` so `index.html` references resolve at root.
- **Asset caching:** hashed `/assets/*` immutable; `index.html` `no-cache` (see §6.1).
- **Session expiry:** `checkSession()` already swallows 401 (`user=null`) → global middleware redirects to `/login?redirect=`. Preserve.
- **SSE reconnect:** preserves current no-auto-reconnect behavior (manual Connect); `onerror` sets `connected=false`.
- **Empty API responses:** backend must keep returning `[]` not `null` (CONTRIBUTING.md rule). The store-side `(await api.x()) || []` guards remain; verify `listChannels`/`listUsers`/`listRoleMappings`/`listChannelRoleMappings` already return `[]` (`store/api.go` already uses `make([]T, 0)` / `filtered`).
- **CORS:** browser app is same-origin → no CORS needed; SSE sets `Access-Control-Allow-Origin` only if `cors_origin` configured (unchanged).
- **CSP:** no CSP headers added (none today); note as out of scope.
- **Rate limiting/ban:** applied server-side to `/api` and webhook POST; UI gets 429/403 which the `api` client surfaces as error toasts. `getRealIP` with `behind_reverse_proxy` must be **true** behind the ingress (set in Global Config) so bans/rate-limit see the real client IP, not the pod IP.
- **OIDC redirect URIs:** OIDC `publicURL`-derived callback is `https://gohookbridge-test.home.webcenter.fr/auth/oidc/<id>/callback`; ensure any configured IdP allows this redirect URI.
- **Large payloads:** `max_body_size` enforced server-side (`MaxBytesReader`); UI already converts bytes↔units via `utils/units.ts`.
- **Browser back/forward:** Nuxt client router (createWebHistory equivalent) handles history; `ssr:false` + static host serves `index.html` on any path (fallback covers refresh on `/channels/x`).

---

## 10. Validation / verification checklist (local + CI parity)

Local (before PR):
```
cd web && npm ci
cd web && npm run typecheck        # nuxt typecheck
cd web && npm test                 # vitest
make lint                          # golangci-lint + markdownlint + web-typecheck
make test                          # go test ./... + web-test
make build                         # npm ci + nuxt generate + copy + go build (embed validation)
./bin/gohookbridge server --address 0.0.0.0 --port 8081 --dev-admin   # smoke: UI + SSE + login
```
CI parity: build job (`make build` with Node 22), web job (`typecheck` + `test`), lint job, test job.

---

## 11. Risks & rollback

### 11.1 `go:embed` `_`/`.` exclusion (MITIGATED)
**Risk:** Go's `embed` package silently excludes files and directories whose names begin with `_` or `.`. Nuxt's default `buildAssetsDir` is `/_nuxt/`, which would be completely absent from the embedded binary — the SPA would load with no JS/CSS.
**Mitigations baked into the plan:**
- `nuxt.config.ts` sets `app.buildAssetsDir: '/assets/'` (no underscore) — see §1.4.
- `scripts/copy-to-static.mjs` asserts no `_`/`.`-prefixed entries exist at the root of `.output/public` before copying, failing the build with a clear error — see §6.3.
- `gohookbridge/web/handler_test.go` verifies the SPA handler serves hashed assets with immutable cache headers and falls back to `index.html` for deep links — see §6.1a.
- CONTRIBUTING.md documents this coupling so future maintainers know not to rename `buildAssetsDir` back to `/_nuxt/`.

### 11.2 Nuxt UI v4 API drift (MITIGATED)
**Risk:** `UTable`, `UTabs`, `USelectMenu`, `UModal`, `UFormField`, `UToast`, `UVerticalNavigation` have different prop/slot/event names than Naive UI. A wrong prop name silently fails or renders incorrectly.
**Mitigations baked into the plan:**
- §3 mapping table lists exact prop/slot names for every component (verified against Context7 Nuxt UI v4 docs).
- §3.1 mandates a per-page verification loop: `npm run typecheck` after each page port, plus visual side-by-side comparison.
- Fallback rule: if a Nuxt UI component API cannot be confirmed, use native HTML + Tailwind instead.
- CI `web` job runs `npm run typecheck` on every push — see §7.4.

### 11.3 HA Raft/NATS bootstrapping (MITIGATED)
**Risk:** The 3-node Raft cluster requires correct `--raft-peers` and `--nats-routes` strings with stable pod DNS. A typo or wrong service name prevents quorum formation, and the cluster never starts.
**Mitigations baked into the plan:**
- §8.3a provides exact `_helpers.tpl` code that deterministically computes peer/route strings from `.Values.server.replicas` — no manual string construction.
- §8.3b provides the complete StatefulSet YAML with `POD_NAME`/`POD_NAMESPACE` env, `podManagementPolicy: Parallel`, probes, and `volumeClaimTemplates`.
- §8.3g mandates a pre-deploy validation step: `helm lint` + `helm template` + manual inspection of the rendered `--raft-peers` and `--nats-routes` args.
- §8.3h provides a step-by-step rollback/recovery procedure: delete PVCs, scale to 1, bootstrap single-node, scale back to 3.

### 11.4 Static output gitignored (MITIGATED)
**Risk:** After `git rm --cached gohookbridge/web/static`, the embedded assets exist only after `make build`/`npm run build`. If the build step is skipped, `go build` fails at the embed directive.
**Mitigations baked into the plan:**
- `Makefile` `build` target has a guard: `test -f gohookbridge/web/static/index.html` before `go build` — see §6.2.
- `Dockerfile` webbuild stage has a guard: `RUN test -f /src/gohookbridge/web/static/index.html` — see §6.4.
- CI `build` job runs `make build` (which includes `web-build`) with Node 22 — see §7.4.
- CI `web` job independently validates `npm run build` succeeds.

### 11.5 Dev port ambiguity (MITIGATED)
**Risk:** CONTRIBUTING.md says the dev server runs on 3333, but the Go default is 8081. Developers might start the server on the wrong port and the Nuxt proxy would fail.
**Mitigations baked into the plan:**
- §1.5 standardizes ALL local dev on port 8081 (Go server) + 3000 (Nuxt dev server) with proxy to 8081.
- §1.4 `nuxt.config.ts` uses `vite.server.proxy` targeting `http://localhost:8081`.
- §12 CONTRIBUTING.md changes correct all 3333 references to 8081.
- Helm container port 3333 is explicitly documented as an in-cluster concern only.

### 11.6 Behavioral parity loss (ACCEPTED)
Visual design changes to Nuxt UI defaults per decision #3. All screens/features/behaviors preserved. If a feature is missing, the old Naive UI source is on `main` for reference.

### 11.7 Rollback strategy
Keep the old `web/` Vite source on `main`; revert the PR to restore. Old cached `_nuxt/`-style bookmarks are irrelevant (fresh domain). If Nuxt output has issues, `git revert` + redeploy with the previous image tag. No API contract changes; old browser bookmarks (`/#/...` were never used — the SPA used HTML5 history) still resolve via SPA fallback.

---

## 12. Documentation updates
- **CONTRIBUTING.md**
  - Project structure: replace the `web/` block with the Nuxt layout (`web/app/`, `web/nuxt.config.ts`, `web/tests/`); update `gohookbridge/web/static/` description ("Nuxt static output, gitignored, regenerated by `make build`").
  - "Running the backend": fix line 97 — the `dev-server` target starts on **port 8081** (the Go default), NOT 3333. Update the text to say `http://localhost:8081`.
  - "Running the UI": rewrite entirely:
    ```
    The Nuxt 4 SPA runs with Nuxt's dev server, which proxies API calls to the Go backend:

    cd web
    npm install
    npm run dev          # starts at http://localhost:3000

    The Nuxt dev proxy (configured in `web/nuxt.config.ts` under `vite.server.proxy`) forwards these paths to `http://localhost:8081`:

    | Frontend path | Proxied to backend |
    |---|---|
    | `/api/*` | `http://localhost:8081` |
    | `/events/*` | `http://localhost:8081` |
    | `/auth/*` | `http://localhost:8081` |

    **Recommended workflow:** open two terminals — one for `make dev-server` (backend on 8081), one for `cd web && npm run dev` (UI on 3000). Work against `http://localhost:3000` in the browser; the SPA is served from Nuxt and API calls are proxied to the Go backend on 8081.
    ```
  - "UI conventions": replace framework (Nuxt 4), routing (file-based `pages/`), component library (Nuxt UI + Tailwind CSS), add `@pinia/nuxt` stores, `nuxt typecheck` instead of `vue-tsc`, `@nuxt/test-utils` + `mountSuspended` instead of plain vitest, and `npm run build` = `nuxt generate` + copy. Add a note about the `app.buildAssetsDir: '/assets/'` coupling with `go:embed` (must not start with `_` or `.`).
  - "Building everything": step 1 becomes `cd web && npm ci && npm run build` (Nuxt generate → `gohookbridge/web/static/`). Add a note that `make build` includes a guard that fails if `gohookbridge/web/static/index.html` is missing.
  - "Pull request process": update step 5 from `cd web && npx vue-tsc --noEmit` to `cd web && npm run typecheck`.
- **README.md**: no functional text references the stack except generic "web interface"; update any "Vue"/"Naive UI" mention (none found besides a "naively written" prose word — leave it). Add a note that the admin UI is a Nuxt 4 static SPA embedded in the binary.
- **quickstart.md**: keep the HA StatefulSet section (already correct); add/adjust a Helm-based HA section pointing at `values-home.yaml`/`server.replicas=3` and the `gohookbridge-test.home.webcenter.fr` example. Update the "Local Quick Start" build step to note that `make build` now requires Node.js 22+ for the Nuxt web build.
- **design.md**: no Vue/Naive references (backend-only) — no change; optionally add one line noting the UI is a Nuxt 4 SPA.
- **AGENTS.md**: no change (generic feature rules already cover back+front+docs+tests).

---

## Ordered task list (for the implementing agent)
1. `git checkout main && git checkout -b feat/nuxt-migration`.
2. Scaffold Nuxt: rewrite `web/package.json`, `web/tsconfig.json`, `web/vitest.config.ts`, add `web/nuxt.config.ts` (with merged `nitro` block and `vite.server.proxy` per §1.4), `web/app/app.config.ts`, `web/app/assets/css/main.css`, `web/scripts/copy-to-static.mjs` (with `_`/`.` guard per §6.3); delete `web/src/**`, `web/index.html`, `web/env.d.ts`, `web/vite.config.ts`.
3. Create `web/app/` source tree per §2: `app.vue`, layouts, pages, components, stores, middleware, utils.
4. Port each view with the §3 mapping (exact prop/slot names) + §3.1 verification loop (`npm run typecheck` after each page); add `AppCodeBlock.vue` and the `[channel].vue` redirect.
5. Port/author tests (§7.1) and update vitest config.
6. Go changes: `gohookbridge/web/handler.go` (§6.1) + `gohookbridge/web/handler_test.go` (§6.1a — 5 exact test cases).
7. `Makefile` targets (§6.2 — including `build` guard), `Dockerfile` (§6.4 — including `RUN test -f` guard), `.gitignore` (§6.5); `git rm --cached` old static output.
8. CI: update `.github/workflows/go.yml` (§7.4 — add `web` job + Node 22 to `build` job).
9. Helm: `values.yaml`, StatefulSet + headless service + bootstrap ConfigMap + probes/resources (§8.3a–8.3f), `values-home.yaml`. Run pre-deploy validation (§8.3g).
10. Docs: CONTRIBUTING.md (fix port 3333→8081, rewrite UI section), README.md, quickstart.md (§12).
11. Run local checklist (§10), then deploy + validate on home cluster (§8.4–8.5). If Raft quorum fails, follow §8.3h recovery procedure.
12. Commit, push, open PR against `main` (issue #13).
