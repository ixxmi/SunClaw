package gateway

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServeDashboardUIFileOrIndex(t *testing.T) {
	distDir := t.TempDir()
	assetsDir := filepath.Join(distDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("INDEX"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	distFS := os.DirFS(distDir)

	tests := []struct {
		name       string
		target     string
		statusCode int
		body       string
	}{
		{name: "root", target: "/admin/", statusCode: http.StatusOK, body: "INDEX"},
		{name: "spa route", target: "/admin/sessions", statusCode: http.StatusOK, body: "INDEX"},
		{name: "asset", target: "/admin/assets/app.js", statusCode: http.StatusOK, body: "console.log('ok')"},
		{name: "missing asset", target: "/admin/assets/missing.js", statusCode: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			rec := httptest.NewRecorder()

			serveDashboardUIFileOrIndex(rec, req, distFS)

			if rec.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.statusCode)
			}
			if tt.body != "" && rec.Body.String() != tt.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestEmbeddedDashboardUIFSHasIndex(t *testing.T) {
	if _, err := fs.Stat(dashboardUIDistFS, "index.html"); err != nil {
		t.Fatalf("embedded ui missing index.html: %v", err)
	}
}
