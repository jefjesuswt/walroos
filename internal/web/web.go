// Package web serves the React UI built by web/ (bun run build → internal/web/ui/dist).
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

// ui/README keeps the embed pattern valid before the UI is built.
//
//go:embed all:ui
var ui embed.FS

// Handler serves static files and falls back to index.html for client-side routes.
// Unknown /api/ paths get a JSON 404 instead of the SPA.
func Handler() http.HandlerFunc {
	dist, err := fs.Sub(ui, "ui/dist")
	if _, statErr := fs.Stat(dist, "index.html"); err != nil || statErr != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				whttp.ErrorJSON(w, http.StatusNotFound, "endpoint not found")
				return
			}
			http.Error(w, "walroos UI not built: run `bun install && bun run build` in web/", http.StatusNotFound)
		}
	}

	files := http.FileServerFS(dist)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			whttp.ErrorJSON(w, http.StatusNotFound, "endpoint not found")
			return
		}
		if _, err := fs.Stat(dist, strings.TrimPrefix(path.Clean(r.URL.Path), "/")); err != nil {
			r.URL.Path = "/" // SPA route → index.html
		}
		// Vite hashes asset names, so they never change; index.html must be revalidated to pick up new ones.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}
}
