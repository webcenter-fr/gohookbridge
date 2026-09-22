package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
)

type fakeLeaderInfo struct {
	leader bool
	addr   string
}

func (f *fakeLeaderInfo) IsLeader() bool        { return f.leader }
func (f *fakeLeaderInfo) LeaderAddress() string { return f.addr }

func TestLeaderForwardMiddleware(t *testing.T) {
	t.Run("leader serves the request locally", func(t *testing.T) {
		var called bool
		handler := leaderForwardMiddleware(&fakeLeaderInfo{leader: true, addr: "leader.example:6001"}, 3333)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusTeapot)
			}),
		)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/channels/", nil))

		assert.Assert(t, called)
		assert.Equal(t, http.StatusTeapot, rec.Code)
	})

	t.Run("read-only requests are not forwarded on a follower", func(t *testing.T) {
		var called bool
		handler := leaderForwardMiddleware(&fakeLeaderInfo{leader: false, addr: "leader.example:6001"}, 3333)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}),
		)

		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, "/api/channels/", nil))
			assert.Equal(t, http.StatusOK, rec.Code)
		}
		assert.Assert(t, called)
	})

	t.Run("follower forwards a mutating request to the leader", func(t *testing.T) {
		var gotMethod, gotPath, gotBody, gotForwarded string
		leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			gotForwarded = r.Header.Get(leaderForwardedHeader)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer leader.Close()

		leaderURL, err := url.Parse(leader.URL)
		assert.NilError(t, err)
		leaderPort, err := strconv.Atoi(leaderURL.Port())
		assert.NilError(t, err)

		handler := leaderForwardMiddleware(&fakeLeaderInfo{leader: false, addr: "127.0.0.1:6001"}, leaderPort)(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("handler must not be called on a follower")
			}),
		)

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/channels/", strings.NewReader(`{"id":"test"}`))
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, `{"ok":true}`, rec.Body.String())
		assert.Equal(t, http.MethodPost, gotMethod)
		assert.Equal(t, "/api/channels/", gotPath)
		assert.Equal(t, `{"id":"test"}`, gotBody)
		assert.Equal(t, "1", gotForwarded)
	})

	t.Run("follower returns 503 when no leader is known", func(t *testing.T) {
		handler := leaderForwardMiddleware(&fakeLeaderInfo{leader: false}, 3333)(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("handler must not be called on a follower")
			}),
		)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/channels/test", nil))

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("already forwarded request is served locally", func(t *testing.T) {
		var called atomic.Bool
		handler := leaderForwardMiddleware(&fakeLeaderInfo{leader: false, addr: "leader.example:6001"}, 3333)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called.Store(true)
				w.WriteHeader(http.StatusOK)
			}),
		)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/channels/", nil)
		req.Header.Set(leaderForwardedHeader, "1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Assert(t, called.Load())
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}
