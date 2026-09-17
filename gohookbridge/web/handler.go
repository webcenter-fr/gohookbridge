package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The embed directive below works ONLY because nuxt.config.ts sets
// app.buildAssetsDir to "/assets/" (no leading underscore). Go's embed
// package silently excludes files and directories whose names begin with
// "_" or ".", so Nuxt's default "/_nuxt/" directory would be missing from
// the binary. See web/scripts/copy-to-static.mjs, which fails the build if
// such entries appear in the generated output.
//
//go:embed static/*
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
