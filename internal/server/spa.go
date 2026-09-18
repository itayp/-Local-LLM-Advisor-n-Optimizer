package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves files from root and falls back to index.html for any
// path that is not a file — which is what a single-page app with
// client-side routes needs. Hashed asset files (Vite's /assets/*) are
// cacheable for a long time; index.html is not.
func spaHandler(root fs.FS) http.Handler {
	files := http.FS(root)
	fileServer := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if p != "/" {
			if f, err := root.Open(strings.TrimPrefix(p, "/")); err == nil {
				info, statErr := f.Stat()
				_ = f.Close()
				if statErr == nil && !info.IsDir() {
					if strings.HasPrefix(p, "/assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					r.URL.Path = p
					fileServer.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
