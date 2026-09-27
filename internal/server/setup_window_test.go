package server

import (
	"context"
	"testing"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// SEC-002: the boot-time reset must clear a persisted (possibly expired)
// setup-window deadline so a restart always yields a fresh window.
func TestResetSetupWindowOnBoot(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	// Simulate an expired window persisted by a previous run.
	expired := time.Now().Add(-time.Minute)
	assert.NilError(t, svc.SetSetupModeEndTime(ctx, expired))
	assert.Assert(t, svc.GetSetupModeEndTime(ctx).Equal(expired))

	assert.NilError(t, resetSetupWindowIfNeeded(ctx, svc))
	assert.Assert(t, svc.GetSetupModeEndTime(ctx).IsZero(), "deadline must be cleared on boot")
}
