package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"github.com/webcenter-fr/gohookbridge/pkg/encryption"
	"gotest.tools/v3/assert"
)

// F36: HandleTestPayloadSend routes through the central, policy-aware
// encryption path, so a server_side channel publishes ciphertext bound to the
// channel context.
func TestSendPayloadHonorsEncryption(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	key, err := encryption.GenerateAESKey()
	assert.NilError(t, err)
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{
		ID:             "enc",
		EncryptionMode: "server_side",
		EncryptionKey:  key,
	}))

	broker := newNatsBroker(t, 4290)
	_, live := broker.Subscribe("enc", time.Time{}, 10)
	defer broker.Unsubscribe("enc", live)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/send/enc", strings.NewReader(`{"hello":"world"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channel", "enc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	HandleTestPayloadSend(broker, svc).ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusAccepted, "body: %s", w.Body.String())

	select {
	case event := <-live:
		var eventData map[string]any
		assert.NilError(t, json.Unmarshal(event, &eventData))
		bodyB, _ := eventData["bodyB"].(string)
		decoded, err := base64.StdEncoding.DecodeString(bodyB)
		assert.NilError(t, err)
		plaintext, err := encryption.AESDecryptWithContext(decoded, key, service.ChannelEncryptionContext("enc"))
		assert.NilError(t, err)
		assert.Equal(t, string(plaintext), `{"hello":"world"}`)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for encrypted event")
	}
}

// F36: a mode without a key must fail closed (503), never publish plaintext.
func TestSendPayloadMissingKeyFailsClosed(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{
		ID:             "broken",
		EncryptionMode: "server_side",
	}))

	broker := newNatsBroker(t, 4291)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/send/broken", strings.NewReader(`{"x":1}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channel", "broken")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	HandleTestPayloadSend(broker, svc).ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusServiceUnavailable, "body: %s", w.Body.String())
}
