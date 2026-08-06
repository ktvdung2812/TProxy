package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dashboard
var dashboardFiles embed.FS

func dashboardHandler() http.Handler {
	root, err := fs.Sub(dashboardFiles, "dashboard")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/dashboard/")
		if path == "" || path == "/" || path == "index.html" {
			serveDashboardIndex(w, root)
			return
		}
		if info, err := fs.Stat(root, path); err != nil || info.IsDir() {
			// Only extensionless paths are client-side routes. Returning the SPA
			// shell for a missing asset makes browsers try to parse HTML as JSON
			// (notably for manifest.webmanifest) and hides deployment mistakes.
			if strings.Contains(path, ".") {
				http.NotFound(w, r)
				return
			}
			serveDashboardIndex(w, root)
			return
		}
		if strings.HasSuffix(path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		r.URL.Path = "/" + strings.TrimPrefix(path, "/")
		files.ServeHTTP(w, r)
	})
}

func serveDashboardIndex(w http.ResponseWriter, root fs.FS) {
	data, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
