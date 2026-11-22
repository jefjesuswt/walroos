// Package staticserver is `walroos static-server`: what a static site's image runs. The node
// agent copies its own binary into the image next to the built site (see agent/apps/static.go),
// so a site needs no nginx or Caddy image.
package staticserver

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	dir  string
	port int
	spa  bool
)

var Cmd = &cobra.Command{
	Use:    "static-server",
	Short:  "Serve a folder over HTTP (static sites' images run it)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		srv := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: Handler(os.DirFS(dir), spa), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-cmd.Context().Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv.Shutdown(ctx)
		}()
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&dir, "dir", ".", "folder to serve")
	Cmd.Flags().IntVar(&port, "port", 8080, "port to listen on")
	Cmd.Flags().BoolVar(&spa, "spa", false, "serve index.html for paths with no file (client-side routing)")
}

// Handler serves site: a file, a folder's index.html, then (spa) the root index.html for paths
// with no file extension, else 404.html with a 404. Dot paths (.git, .env) are never served,
// except .well-known; folders are never listed.
func Handler(site fs.FS, spa bool) http.Handler {
	files := http.FileServerFS(site)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		switch {
		case hidden(name):
		case servable(site, name):
			if name == "" || strings.HasSuffix(name, ".html") || isDir(site, name) {
				w.Header().Set("Cache-Control", "no-cache") // pages change on each deploy; hashed assets don't
			}
			files.ServeHTTP(w, r)
			return
		case spa && !strings.Contains(path.Base(name), ".") && servable(site, "index.html"):
			page(w, site, "index.html", http.StatusOK)
			return
		}
		if servable(site, "404.html") {
			page(w, site, "404.html", http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	})
}

func hidden(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") && part != ".well-known" {
			return true
		}
	}
	return false
}

// servable: a file, or a folder with an index.html.
func servable(site fs.FS, name string) bool {
	if name == "" {
		name = "."
	}
	st, err := fs.Stat(site, name)
	if err != nil {
		return false
	}
	if st.IsDir() {
		st, err = fs.Stat(site, path.Join(name, "index.html"))
		return err == nil && !st.IsDir()
	}
	return true
}

func isDir(site fs.FS, name string) bool {
	st, err := fs.Stat(site, name)
	return err == nil && st.IsDir()
}

func page(w http.ResponseWriter, site fs.FS, name string, status int) {
	f, err := site.Open(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	io.Copy(w, f)
}
