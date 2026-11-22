package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// TestEmbedIncludesAllStaticFiles is a regression guard for the "all:static"
// embed directive. A plain "static/*" directive silently excludes files whose
// names start with "_" or "." at any depth (e.g. Vite chunks like
// assets/_LzBFbKv.js), which makes the server return the SPA HTML fallback for
// the missing chunk and breaks dynamic imports. This test walks the on-disk
// static/ directory and asserts every file is also present in the embed FS.
func TestEmbedIncludesAllStaticFiles(t *testing.T) {
	const root = "static"
	if _, err := os.Stat(root); err != nil {
		t.Skip("internal/web/static is absent; run `make web-build` to generate it")
	}

	var checked int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		embedPath := root + "/" + filepath.ToSlash(rel)
		if _, statErr := fs.Stat(spaAssets, embedPath); statErr != nil {
			t.Errorf("file %q exists on disk but is missing from the embed FS (go:embed all:static required)", embedPath)
		}
		checked++
		return nil
	})
	assert.NilError(t, err)
	assert.Assert(t, checked > 0, "expected at least one static file to verify")
}
