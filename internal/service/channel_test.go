package service

import (
	"context"
	"errors"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

// F08: unreadable/missing policy must fail closed; require_known_channels
// turns an unknown channel into ErrNotFound instead of a synthetic default.
func TestResolveChannelConfigFailClosed(t *testing.T) {
	fake := newFakeRepository()
	svc := NewService(fake, nil)
	ctx := context.Background()

	// Legacy behavior: unknown channel resolves to a synthetic default.
	resolved, err := svc.ResolveChannelConfig(ctx, "ghost")
	assert.NilError(t, err)
	assert.Equal(t, resolved.ID, "ghost")

	cfg := fake.config
	cfg.Server.RequireKnownChannels = true
	assert.NilError(t, fake.UpdateGlobalConfig(ctx, cfg))

	_, err = svc.ResolveChannelConfig(ctx, "ghost")
	assert.Assert(t, errors.Is(err, domain.ErrNotFound), "got %v", err)
}
