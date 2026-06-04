// Package webui embeds the built Relay web front end (web/dist) and serves it
// with SPA fallback. The dist directory is produced by `make web-build`; a
// committed placeholder index.html keeps this package compiling without a build.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded SPA. Unknown, non-API paths fall back to
// index.html so client-side routes deep-link correctly. Requests under /v1 are
// never served here (they belong to the API mux); this handler 404s them so a
// missing API route does not return index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		// Serve the file if it exists; otherwise fall back to index.html.
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
