// Package web embeds the compiled dashboard (a SvelteKit static SPA) into the
// pushq binary.
//
// build/ is not committed — CI, Docker, and GoReleaser run `npm run build`
// before compiling, so official binaries carry the real dashboard. Only
// build/.gitkeep is in git, so that `go build` and
// `go install github.com/blissfulrays/pushq/cmd/pushq@latest` work without
// Node installed; such a build serves the placeholder below at /ui/.
//
// Run `make web` to build the dashboard locally.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:build
var buildFS embed.FS

// HasFile reports whether the embedded build contains name (e.g.
// "docs/swagger-ui-bundle.js").
func HasFile(name string) bool {
	info, err := fs.Stat(sub(), name)
	return err == nil && !info.IsDir()
}

// Bundled reports whether this binary carries the real dashboard.
func Bundled() bool {
	_, err := fs.Stat(sub(), "index.html")
	return err == nil
}

func sub() fs.FS {
	f, err := fs.Sub(buildFS, "build")
	if err != nil {
		panic(err)
	}
	return f
}

// Handler serves the dashboard at the site root, for requests no API route
// matched. Real files (and "/") are served to anyone. Any other path gets
// index.html only for a browser page load (Accept: text/html), so client-side
// routes like /queues/x survive a reload; API clients and probes get
// notFound instead of a misleading 200 with HTML.
func Handler(notFound http.Handler) http.Handler {
	root := sub()
	bundled := Bundled()
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			notFound.ServeHTTP(w, r)
			return
		}
		wantsPage := r.URL.Path == "/" || strings.Contains(r.Header.Get("Accept"), "text/html")
		if !bundled {
			if wantsPage {
				placeholder(w, r)
				return
			}
			notFound.ServeHTTP(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if info, err := fs.Stat(root, path); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		if !wantsPage || strings.HasPrefix(path, "_app/") {
			notFound.ServeHTTP(w, r) // a missing asset is a 404, not the app
			return
		}
		r2 := new(http.Request)
		*r2 = *r
		u := *r.URL
		u.Path = "/"
		r2.URL = &u
		fileServer.ServeHTTP(w, r2)
	})
}

const placeholderHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>pushq</title>
<style>
 body{font-family:system-ui,sans-serif;display:grid;place-items:center;
      min-height:100vh;margin:0;background:#0b0d10;color:#e6e8eb;line-height:1.6}
 main{max-width:34rem;padding:2rem}
 code{background:#1a1e24;padding:.15rem .4rem;border-radius:4px;font-size:.9em}
 a{color:#7aa2f7}
</style></head>
<body><main>
<h1>Dashboard not bundled</h1>
<p>This binary was built without the web assets, so only the placeholder is
available here. The API and dispatcher are fully functional.</p>
<p>To get the dashboard, use an official release binary or the Docker image,
or build from source with <code>make build</code>.</p>
<p><a href="https://github.com/blissfulrays/pushq">github.com/blissfulrays/pushq</a></p>
</main></body></html>`

func placeholder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(placeholderHTML))
}
