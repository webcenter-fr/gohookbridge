package server

import (
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// TestLoadAtRestKeeper_MultiNodeNoKeyReturnsNilInterface is the exact
// regression guard: a multi-node deployment without --at-rest-key-file must
// return a nil interface, not a typed-nil *storage.AESGCMKeeper (which would be
// non-nil when stored in an interface and panic on Seal/Open).
func TestLoadAtRestKeeper_MultiNodeNoKeyReturnsNilInterface(t *testing.T) {
	keeper, err := loadAtRestKeeper("", 3, t.TempDir())
	assert.NilError(t, err)
	assert.Assert(t, keeper == nil, "multi-node without a key file must return a nil interface")
}

// TestLoadAtRestKeeper_SingleNodeGeneratesKey proves the single-node dev path
// auto-generates a key file and returns a working keeper.
func TestLoadAtRestKeeper_SingleNodeGeneratesKey(t *testing.T) {
	keeper, err := loadAtRestKeeper("", 1, t.TempDir())
	assert.NilError(t, err)
	assert.Assert(t, keeper != nil)

	sealed, err := keeper.SealString("x")
	assert.NilError(t, err)
	assert.Assert(t, sealed != "x", "the value must be sealed")

	opened, err := keeper.OpenString(sealed)
	assert.NilError(t, err)
	assert.Equal(t, opened, "x")
}

// TestLoadAtRestKeeper_InvalidFile proves a missing key file is an error.
func TestLoadAtRestKeeper_InvalidFile(t *testing.T) {
	_, err := loadAtRestKeeper(filepath.Join(t.TempDir(), "missing.json"), 1, t.TempDir())
	assert.Assert(t, err != nil)
}
