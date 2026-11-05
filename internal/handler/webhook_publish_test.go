package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// F25: a broker publish failure must surface as a 5xx, never a false 202.
func TestPublishErrorReturns5xx(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "c"}))

	broker := newNatsBroker(t, 4292)
	broker.Shutdown() // subsequent publishes fail

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhook/c", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channel", "c")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	HandleWebhookPost(broker, svc, service.NewBanTracker()).ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusBadGateway, "body: %s", w.Body.String())
}
