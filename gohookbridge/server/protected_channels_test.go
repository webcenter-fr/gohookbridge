package server

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestLoadProtectedChannels(t *testing.T) {
	t.Run("empty path returns empty channels", func(t *testing.T) {
		protectedChannels, err := LoadProtectedChannels("")
		assert.NilError(t, err)
		assert.Assert(t, protectedChannels != nil)
		assert.Assert(t, !protectedChannels.Has("test-channel"))
	})
}