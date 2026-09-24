package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

func TestAPIUnbanHandlerInvalidIP(t *testing.T) {
	bt := service.NewBanTracker()

	t.Run("rejects empty ip", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/bans/", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("ip", "")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		APIUnbanHandler(bt)(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})

	t.Run("rejects invalid ip", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/bans/not-an-ip", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("ip", "not-an-ip")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		APIUnbanHandler(bt)(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	})
}
