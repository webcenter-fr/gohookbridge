package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// F20: changing a channel's allowed IPs must invalidate the middleware cache
// even though the channel ID is unchanged.
func TestIPCacheInvalidatedOnUpdate(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{
		ID:         "c",
		AllowedIPs: []string{"10.0.0.0/8"},
	}))

	mw := IPRestrictMiddleware(svc)
	call := func(remote string) int {
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook/c", nil)
		req.RemoteAddr = remote
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "c")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	assert.Equal(t, call("10.1.2.3:1234"), http.StatusOK)

	ch, err := svc.GetChannel(ctx, "c")
	assert.NilError(t, err)
	ch.AllowedIPs = []string{"192.168.0.0/16"}
	assert.NilError(t, svc.UpdateChannel(ctx, ch))

	assert.Equal(t, call("10.1.2.3:1234"), http.StatusForbidden, "cache must be invalidated after the allowlist changes")
	assert.Equal(t, call("192.168.1.5:1234"), http.StatusOK)
}
