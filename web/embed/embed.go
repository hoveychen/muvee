// Package webui embeds the compiled React frontend into the Go binary.
// The web/dist directory is populated by running `npm run build` in the web/ directory.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded frontend.
// All non-API routes fall back to index.html (SPA routing).
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		// Check if file exists in the embedded FS
		if _, err := fs.Stat(sub, path); err != nil {
			// File not found → serve index.html for SPA routing
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
