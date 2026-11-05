package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"github.com/webcenter-fr/gohookbridge/pkg/nats"
	"gotest.tools/v3/assert"
)

func newNatsBroker(t *testing.T, port int) *nats.Broker {
	t.Helper()
	b, err := nats.New(nats.Config{
		NodeID:     t.Name(),
		Port:       port,
		BufferSize: 100,
	})
	assert.NilError(t, err)
	assert.Assert(t, b != nil)
	t.Cleanup(func() {
		b.Shutdown()
	})
	return b
}

func eventually(t *testing.T, predicate func() bool) bool {
	t.Helper()

	for range 50 {
		if predicate() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}

	return false
}

// syncRecorder wraps httptest.ResponseRecorder with a mutex. The SSE handler
// streams from its own goroutine while the test goroutine polls the buffered
// output, and httptest.ResponseRecorder is not safe for concurrent use.
type syncRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
}

func newSyncRecorder() *syncRecorder {
	return &syncRecorder{rec: httptest.NewRecorder()}
}

func (s *syncRecorder) Header() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Header()
}

func (s *syncRecorder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Write(p)
}

func (s *syncRecorder) WriteHeader(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.WriteHeader(code)
}

// Flush implements http.Flusher so the SSE handler can stream through the
// wrapper (HandleEventsGet type-asserts http.Flusher).
func (s *syncRecorder) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.Flush()
}

// Body returns the recorded body; safe to call while the handler goroutine
// is still writing.
func (s *syncRecorder) Body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Body.String()
}

func TestHandleEventsGet(t *testing.T) {
	broker := newNatsBroker(t, 4243)
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	sessionSecret := service.DeriveSessionSecret("test-secret")
	cfg, err := svc.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	cfg.Server.SessionSecret = "test-secret"
	assert.NilError(t, svc.UpdateGlobalConfig(ctx, cfg))

	err = svc.CreateUser(ctx, &domain.User{
		ID:       "user-1",
		Username: "alice",
		Roles:    []string{"channel_viewer"},
		Channels: []string{"e2e-channel"},
	})
	assert.NilError(t, err)

	err = svc.CreateChannel(ctx, &domain.Channel{
		ID:                "e2e-channel",
		EncryptionMode:    "e2e",
		EncryptionPubKeys: []string{"dummy-key"},
		AccessMode:        "public",
	})
	assert.NilError(t, err)

	t.Run("SessionAuth_Allowed", func(t *testing.T) {
		token, err := service.EncodeSession(&service.SessionToken{
			Username:  "alice",
			Method:    "internal",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, sessionSecret)
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := newSyncRecorder()
		done := make(chan struct{})
		go func() {
			ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())(HandleEventsGet(broker, svc, 1000)).ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"message":"connected"}`)
		}))
		cancel()
		<-done
	})

	t.Run("SessionAuth_Forbidden", func(t *testing.T) {
		token, err := service.EncodeSession(&service.SessionToken{
			Username:  "alice",
			Method:    "internal",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, sessionSecret)
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/unknown-channel", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "unknown-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())(HandleEventsGet(broker, svc, 1000)).ServeHTTP(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusForbidden)
	})

	t.Run("TokenAuth_Allowed", func(t *testing.T) {
		assert.NilError(t, svc.UpdateChannel(ctx, &domain.Channel{ID: "e2e-channel", AccessMode: "token"}))
		tokenRaw, _, err := svc.CreateAccessToken(ctx, "e2e-channel", "consume-token", "consume")
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel?token="+tokenRaw, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
	})

	t.Run("TokenAuth_Unauthorized", func(t *testing.T) {
		assert.NilError(t, svc.UpdateChannel(ctx, &domain.Channel{ID: "e2e-channel", AccessMode: "token"}))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusUnauthorized)
	})

	t.Run("E2EChannel_RelayEncrypted", func(t *testing.T) {
		assert.NilError(t, svc.UpdateChannel(ctx, &domain.Channel{ID: "e2e-channel", AccessMode: "public"}))
		err := broker.Publish("e2e-channel", []byte(`{"encrypted":true,"ciphertext":"dGVzdA=="}`))
		assert.NilError(t, err)
		time.Sleep(100 * time.Millisecond)

		token, err := service.EncodeSession(&service.SessionToken{
			Username:  "alice",
			Method:    "internal",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}, sessionSecret)
		assert.NilError(t, err)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/e2e-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "e2e-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := newSyncRecorder()
		done := make(chan struct{})
		go func() {
			ChannelAccessMiddleware(svc, "consume", service.NewBanTracker())(HandleEventsGet(broker, svc, 1000)).ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"encrypted":true,"ciphertext":"dGVzdA=="}`)
		}), "E2E channel should relay encrypted data without 404")

		cancel()
		<-done
	})

	t.Run("Allows Plaintext Subscriber On Unprotected Channel", func(t *testing.T) {
		err := broker.Publish("plain-channel", []byte(`{"plain":true}`))
		assert.NilError(t, err)
		time.Sleep(100 * time.Millisecond)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/plain-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "plain-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := newSyncRecorder()
		done := make(chan struct{})
		go func() {
			HandleEventsGet(broker, svc, 1000).ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"plain":true}`)
		}))

		body := response.Body()
		assert.Assert(t, strings.Contains(body, `{"message":"connected"}`))
		assert.Assert(t, strings.Contains(body, `{"message":"ready"}`))
		assert.Assert(t, strings.Contains(body, `{"plain":true}`))
		assert.Assert(t, !strings.Contains(body, `"ciphertext"`))

		cancel()
		<-done
	})
}

func TestHandleEventsGetCORSOrigin(t *testing.T) {
	testCases := []struct {
		name           string
		corsOrigin     string
		expectedHeader string
		expectPresent  bool
	}{
		{
			name:           "Wildcard Origin",
			corsOrigin:     "*",
			expectedHeader: "*",
			expectPresent:  true,
		},
		{
			name:           "Specific Origin",
			corsOrigin:     "https://example.com",
			expectedHeader: "https://example.com",
			expectPresent:  true,
		},
		{
			name:          "Empty Origin Omits Header",
			corsOrigin:    "",
			expectPresent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			broker := newNatsBroker(t, 4244)
			svc := service.NewService(storetest.NewRaftStore(t), nil)

			assert.NilError(t, svc.UpdateGlobalConfig(context.Background(), &domain.GlobalConfig{
				Server: domain.ServerConfig{
					MaxBodySize: 26214400,
					CORSOrigin:  tc.corsOrigin,
				},
				Defaults: domain.DefaultChannelConfig{},
			}))

			router := chi.NewRouter()
			router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", HandleEventsGet(broker, svc, 1000))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/plainchannel1", nil)
			reqCtx, cancel := context.WithCancel(req.Context())
			req = req.WithContext(reqCtx)
			defer cancel()

			response := newSyncRecorder()
			done := make(chan struct{})
			go func() {
				router.ServeHTTP(response, req)
				close(done)
			}()

			assert.Assert(t, eventually(t, func() bool {
				return strings.Contains(response.Body(), `{"message":"connected"}`)
			}))

			headerValue := response.Header().Get("Access-Control-Allow-Origin")
			if tc.expectPresent {
				assert.Equal(t, headerValue, tc.expectedHeader)
			} else {
				assert.Equal(t, headerValue, "")
			}

			cancel()
			<-done
		})
	}
}

func TestRetVersion(t *testing.T) {
	Version = []byte("test-version")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()

	RetVersion(w, req)

	resp := w.Result()
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, resp.Header.Get("Content-Type"), contentType)
	assert.Equal(t, resp.Header.Get(versionHeaderName), "test-version")

	body, _ := io.ReadAll(resp.Body)
	var response map[string]string
	err := json.Unmarshal(body, &response)
	assert.NilError(t, err)
	assert.Equal(t, response["version"], "test-version")
}

func TestHandleEventsGetWithNATS(t *testing.T) {
	broker := newNatsBroker(t, 4245)
	svc := service.NewService(storetest.NewRaftStore(t), nil)

	router := chi.NewRouter()
	router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", HandleEventsGet(broker, svc, 1000))

	t.Run("Delivers historical and live events via NATS", func(t *testing.T) {
		err := broker.Publish("nats-sse-channel", []byte(`{"history":true}`))
		assert.NilError(t, err)
		time.Sleep(200 * time.Millisecond)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/nats-sse-channel", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := newSyncRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"history":true}`)
		}))

		body := response.Body()
		assert.Assert(t, strings.Contains(body, `{"message":"connected"}`))
		assert.Assert(t, strings.Contains(body, `{"message":"ready"}`))

		_ = broker.Publish("nats-sse-channel", []byte(`{"live":true}`))
		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"live":true}`)
		}))

		cancel()
		<-done
	})

	t.Run("Handles unprotected channel with NATS broker", func(t *testing.T) {
		localRouter := chi.NewRouter()
		svc3 := service.NewService(storetest.NewRaftStore(t), nil)
		localRouter.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", HandleEventsGet(broker, svc3, 1000))

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/unprotected-nats", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := newSyncRecorder()
		done := make(chan struct{})
		go func() {
			localRouter.ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"message":"connected"}`)
		}))

		cancel()
		<-done
	})
}

func TestHandleEventsGetWithClientID(t *testing.T) {
	broker := newNatsBroker(t, 4247)
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	err := broker.Publish("cursor-channel", []byte(`{"old":true}`))
	assert.NilError(t, err)
	time.Sleep(100 * time.Millisecond)

	cursorTime := time.Now()
	err = svc.SetClientCursor(ctx, &domain.ClientCursor{
		Channel:         "cursor-channel",
		ClientID:        "test-client",
		LastTimestampMs: cursorTime.UnixMilli(),
	})
	assert.NilError(t, err)

	time.Sleep(50 * time.Millisecond)
	err = broker.Publish("cursor-channel", []byte(`{"recent":true}`))
	assert.NilError(t, err)
	time.Sleep(200 * time.Millisecond)

	router := chi.NewRouter()
	router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", HandleEventsGet(broker, svc, 1000))

	t.Run("Delivers only events after cursor", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/cursor-channel?client_id=test-client", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		defer cancel()

		response := newSyncRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		assert.Assert(t, eventually(t, func() bool {
			return strings.Contains(response.Body(), `{"recent":true}`)
		}), "recent event should be delivered")

		body := response.Body()
		assert.Assert(t, !strings.Contains(body, `{"old":true}`), "old event should NOT be delivered")

		cancel()
		<-done

		cursor, _ := svc.GetClientCursor(ctx, "cursor-channel", "test-client")
		assert.Assert(t, cursor != nil)
		assert.Assert(t, cursor.LastTimestampMs > cursorTime.UnixMilli())
	})
}

func TestLivezEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	RetVersion(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/livez", nil))
	assert.Equal(t, w.Code, http.StatusOK)
}

type fakeHealth struct {
	clean   bool
	started bool
}

func (f *fakeHealth) IsCleanState() bool { return f.clean }
func (f *fakeHealth) IsStarted() bool    { return f.started }

func TestReadyzEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	RetReadyz(&fakeHealth{clean: true})(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
	assert.Equal(t, w.Code, http.StatusOK)
	assert.Assert(t, strings.Contains(w.Body.String(), "ready"))

	w = httptest.NewRecorder()
	RetReadyz(&fakeHealth{clean: false})(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
	assert.Equal(t, w.Code, http.StatusServiceUnavailable)
}

func TestStartupEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	RetStartup(&fakeHealth{started: true})(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/startup", nil))
	assert.Equal(t, w.Code, http.StatusOK)
	assert.Assert(t, strings.Contains(w.Body.String(), "started"))

	w = httptest.NewRecorder()
	RetStartup(&fakeHealth{started: false})(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/startup", nil))
	assert.Equal(t, w.Code, http.StatusServiceUnavailable)
}

// F11: concurrent SSE connections are bounded by --max-sse-connections; a
// connection beyond the cap is rejected with 503 while a slot is held.
func TestHandleEventsGetConnectionCap(t *testing.T) {
	broker := newNatsBroker(t, 4253)
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	h := HandleEventsGet(broker, svc, 1)

	newReq := func(ctx context.Context) *http.Request {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/events/cap-channel", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "cap-channel")
		return req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	}

	// Hold the single connection slot with a streaming request.
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := newSyncRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(first, newReq(reqCtx))
		close(done)
	}()
	assert.Assert(t, eventually(t, func() bool {
		return strings.Contains(first.Body(), `{"message":"connected"}`)
	}))

	// The second connection is rejected while the first holds the slot.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, newReq(context.Background()))
	assert.Equal(t, w.Result().StatusCode, http.StatusServiceUnavailable)

	cancel()
	<-done

	// Once the slot is released, a new connection is accepted again.
	reqCtx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	second := newSyncRecorder()
	done2 := make(chan struct{})
	go func() {
		h.ServeHTTP(second, newReq(reqCtx2))
		close(done2)
	}()
	assert.Assert(t, eventually(t, func() bool {
		return strings.Contains(second.Body(), `{"message":"connected"}`)
	}))
	cancel2()
	<-done2
}

// deadlineRecorder records the write deadline set through
// http.NewResponseController (F11).
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	set      bool
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	d.set = true
	return nil
}

// F11: every SSE write runs under a per-write deadline so a stalled client
// cannot pin the handler goroutine indefinitely.
func TestWriteSSESetsWriteDeadline(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	assert.Assert(t, writeSSE(rec, rec, "data: {}\n\n"))
	assert.Assert(t, rec.set, "writeSSE must set a per-write deadline")
	remaining := time.Until(rec.deadline)
	assert.Assert(t, remaining > 0 && remaining <= sseWriteTimeout,
		"deadline must be within the write timeout, got %v", remaining)
	assert.Assert(t, strings.Contains(rec.Body.String(), "data: {}"))
}

// failWriter fails every write, simulating a stalled or broken client.
type failWriter struct{ header http.Header }

func (f *failWriter) Header() http.Header       { return f.header }
func (f *failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (f *failWriter) WriteHeader(int)           {}
func (f *failWriter) Flush()                    {}

// F11: a failed SSE write ends the stream instead of looping forever.
func TestWriteSSEWriteErrorEndsStream(t *testing.T) {
	w := &failWriter{header: http.Header{}}
	assert.Assert(t, !writeSSE(w, w, "data: {}\n\n"))
}
