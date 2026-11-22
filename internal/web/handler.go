package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	sgheaders "github.com/disaster37/go-secguard/headers"
)

// The embed directive below uses the "all:" prefix, which embeds every file
// under static/ including files and directories whose names begin with "_" or
// "." at any depth. This is required because Vite emits content-hashed chunks
// named like "assets/_LzBFbKv.js"; a plain "static/*" directive would silently
// exclude them, and the server would then return the SPA HTML fallback for the
// missing chunk (MIME type text/html), breaking dynamic imports.
//
// nuxt.config.ts still sets app.buildAssetsDir to "/assets/" (no leading
// underscore) for cleaner URLs, but with "all:" it is no longer embed-critical.
//
//go:embed all:static
var spaAssets embed.FS

// SPAHandler serves the embedded Nuxt static SPA.
func SPAHandler() http.Handler {
	sub, _ := fs.Sub(spaAssets, "static")
	return spaHandlerFrom(sub)
}

// spaHandlerFrom builds the SPA file server for the given filesystem root.
// It is extracted so tests can exercise the fallback and cache-header logic
// without the production embed.
func spaHandlerFrom(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range sgheaders.DefaultSecurityHeaders("/", r.TLS != nil) {
			w.Header().Set(name, value)
		}

		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := fsys.Open(p); err == nil {
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
