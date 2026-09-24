package domain

import (
	"errors"
	"fmt"
	"testing"

	"gotest.tools/v3/assert"
)

func TestSentinelErrors(t *testing.T) {
	assert.Assert(t, ErrNotFound != nil)
	assert.Assert(t, ErrAlreadyExists != nil)
	assert.Assert(t, ErrInvalidArgument != nil)
}

func TestSentinelErrorsWrapAndMatch(t *testing.T) {
	wrapped := fmt.Errorf("channel %q: %w", "test", ErrNotFound)
	assert.Assert(t, errors.Is(wrapped, ErrNotFound))
	assert.Assert(t, !errors.Is(wrapped, ErrAlreadyExists))

	wrapped = fmt.Errorf("%w: channel %q", ErrAlreadyExists, "test")
	assert.Assert(t, errors.Is(wrapped, ErrAlreadyExists))
}
