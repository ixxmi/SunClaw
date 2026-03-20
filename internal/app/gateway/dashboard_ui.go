package gateway

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	uiembed "github.com/smallnest/goclaw/ui"
)

const dashboardUIBasePath = "/admin"

var dashboardUIDistFS = uiembed.DistFS()

func (s *Server) mountDashboardUI(mux *http.ServeMux) {
	mux.HandleFunc(dashboardUIBasePath, s.handleDashboardUIRedirect)
	mux.Handle(dashboardUIBasePath+"/", s.dashboardUIHandler())
}

func (s *Server) handleDashboardUIRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != dashboardUIBasePath {
		http.NotFound(w, r)
		return
	}

	http.Redirect(w, r, dashboardUIBasePath+"/", http.StatusPermanentRedirect)
}

func (s *Server) dashboardUIHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		serveDashboardUIFileOrIndex(w, r, dashboardUIDistFS)
	})
}

func serveDashboardUIFileOrIndex(w http.ResponseWriter, r *http.Request, distFS fs.FS) {
	relPath := strings.TrimPrefix(r.URL.Path, dashboardUIBasePath)
	relPath = strings.TrimPrefix(relPath, "/")
	if relPath == "" {
		serveDashboardUIIndex(w, r, distFS)
		return
	}

	cleanPath := path.Clean("/" + relPath)
	assetPath := strings.TrimPrefix(cleanPath, "/")

	info, err := fs.Stat(distFS, assetPath)
	if err == nil && !info.IsDir() {
		serveDashboardUIFSPath(w, r, distFS, assetPath)
		return
	}

	if path.Ext(cleanPath) != "" {
		http.NotFound(w, r)
		return
	}

	serveDashboardUIIndex(w, r, distFS)
}

func serveDashboardUIFSPath(w http.ResponseWriter, r *http.Request, distFS fs.FS, assetPath string) {
	fileServer := http.FileServer(http.FS(distFS))
	req := r.Clone(r.Context())
	urlCopy := *req.URL
	urlCopy.Path = "/" + strings.TrimPrefix(assetPath, "/")
	req.URL = &urlCopy
	fileServer.ServeHTTP(w, req)
}

func serveDashboardUIIndex(w http.ResponseWriter, r *http.Request, distFS fs.FS) {
	content, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		http.Error(w, "embedded dashboard UI is unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	_, _ = w.Write(content)
}
