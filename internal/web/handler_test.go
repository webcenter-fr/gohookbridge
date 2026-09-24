package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func newTestFS(t *testing.T) fs.FS {
	t.Helper()
	dir := t.TempDir()
	assert.NilError(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!DOCTYPE html><html></html>"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "assets", "app-abc123.js"), []byte("console.log('hi')"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "logo.svg"), []byte("<svg/>"), 0o644))
	return os.DirFS(dir)
}

func TestSPAHandler_ServesIndexForDeepLink(t *testing.T) {
	h := spaHandlerFrom(newTestFS(t))

	t.Run("root serves index.html", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
		assert.Assert(t, strings.Contains(w.Body.String(), "<!DOCTYPE html>"))
	})

	t.Run("deep link serves index.html with no-cache", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/channels/my-channel", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
		assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	})

	t.Run("hashed asset returns immutable cache header", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/app-abc123.js", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
	})

	t.Run("missing asset falls back to index.html", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nonexistent", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
	})

	t.Run("static asset without assets/ prefix gets no special cache", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/logo.svg", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "", w.Header().Get("Cache-Control"))
	})
}

// TestSPAHandlerRealEmbed exercises the production embed path (requires the
// generated internal/web/static assets from `make web-build`).
func TestSPAHandlerRealEmbed(t *testing.T) {
	t.Run("serves index.html for root path", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		SPAHandler().ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})

	t.Run("serves index.html for unknown paths (SPA fallback)", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/some/unknown/path", nil)
		w := httptest.NewRecorder()
		SPAHandler().ServeHTTP(w, req)
		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})
}
