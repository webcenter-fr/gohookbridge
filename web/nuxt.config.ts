export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: false },
  ssr: false,                                   // client-only SPA
  modules: ['@nuxt/ui', '@pinia/nuxt'],
  // @nuxt/fonts (auto-enabled by @nuxt/ui) emits a root-level `_fonts/`
  // directory, which go:embed silently excludes. The app relies on system
  // fonts, so disable the module to keep the static output embed-safe.
  ui: {
    fonts: false,
  },
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
