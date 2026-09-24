package uuid

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestGenerateUUID(t *testing.T) {
	id, err := GenerateUUID()
	assert.NilError(t, err)
	assert.Equal(t, len(id), 36)
	assert.Equal(t, id[8], byte('-'))
	assert.Equal(t, id[13], byte('-'))
	assert.Equal(t, id[18], byte('-'))
	assert.Equal(t, id[23], byte('-'))
}

func TestGenerateUUIDUnique(t *testing.T) {
	a, err := GenerateUUID()
	assert.NilError(t, err)
	b, err := GenerateUUID()
	assert.NilError(t, err)
	assert.Assert(t, a != b)
}

func TestMustGenerateUUID(t *testing.T) {
	assert.Equal(t, len(MustGenerateUUID()), 36)
}
