package gohookbridge

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/static/*
var spaAssets embed.FS

func spaHandler() http.Handler {
	sub, _ := fs.Sub(spaAssets, "web/static")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		f, err := sub.Open(path)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}