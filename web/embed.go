// Package web embeds the built dashboard (SvelteKit static SPA) into the
// pushq binary. `make web` (or `cd web && npm run build`) refreshes build/.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:build
var buildFS embed.FS

// Handler serves the dashboard with an SPA fallback: unknown paths get
// index.html so client-side routing works. Mount under /ui.
func Handler() http.Handler {
	sub, err := fs.Sub(buildFS, "build")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// SPA fallback.
			r2 := new(http.Request)
			*r2 = *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
