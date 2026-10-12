package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"github.com/webcenter-fr/gohookbridge/pkg/nats"
)

func RetVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))
	resp := map[string]string{
		"version": strings.TrimSpace(string(Version)),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		errorIt(w, nil, http.StatusInternalServerError, err)
	}
}

// sseWriteTimeout bounds a single SSE write so a stalled client cannot pin a
// handler goroutine indefinitely (F11).
const sseWriteTimeout = 10 * time.Second

// sseConnLimiter bounds concurrent SSE connections (F11).
type sseConnLimiter struct {
	sem chan struct{}
}

func newSSEConnLimiter(max int) *sseConnLimiter {
	if max <= 0 {
		max = 1000
	}
	return &sseConnLimiter{sem: make(chan struct{}, max)}
}

func (l *sseConnLimiter) acquire() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *sseConnLimiter) release() {
	<-l.sem
}

// writeSSE writes one SSE frame under a per-write deadline (F11) and flushes.
// It returns false when the client is gone or too slow, ending the stream.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, frame string) bool {
	// Best-effort: writers without deadline support (e.g. test recorders)
	// skip the deadline rather than fail.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(sseWriteTimeout))
	if _, err := fmt.Fprint(w, frame); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func HandleEventsGet(broker *nats.Broker, svc *service.Service, maxConnections int) http.HandlerFunc {
	conns := newSSEConnLimiter(maxConnections)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			http.Error(w, "Channel name missing in URL", http.StatusBadRequest)
			return
		}
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		// Bound concurrent event streams (F11).
		if !conns.acquire() {
			http.Error(w, "too many event stream connections", http.StatusServiceUnavailable)
			return
		}
		defer conns.release()

		corsOrigin := svc.ResolveCORSOrigin(ctx)

		clientID := r.URL.Query().Get("client_id")

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		if corsOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		if !writeSSE(w, flusher, "data: "+`{"message":"connected"}`+"\n\n") {
			return
		}
		if !writeSSE(w, flusher, "data: "+`{"message":"ready"}`+"\n\n") {
			return
		}

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		clientGone := r.Context().Done()

		var since time.Time
		if clientID != "" {
			cursor, _ := svc.GetClientCursor(ctx, channel, clientID)
			if cursor != nil && cursor.LastTimestampMs > 0 {
				since = time.UnixMilli(cursor.LastTimestampMs)
			}
		}
		var lastMsgTs int64
		historical, sub := broker.Subscribe(channel, since, 100)
		defer broker.Unsubscribe(channel, sub)

		for _, data := range historical {
			if !writeSSE(w, flusher, fmt.Sprintf("data: %s\n\n", data)) {
				return
			}
		}

		sseLoop(w, flusher, sub, clientGone, ticker, &lastMsgTs)

		if clientID != "" {
			lastTs := lastMsgTs
			if lastTs == 0 {
				lastTs = time.Now().UTC().UnixMilli()
			}
			if err := svc.SetClientCursor(ctx, &domain.ClientCursor{
				Channel:         channel,
				ClientID:        clientID,
				LastTimestampMs: lastTs,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: failed to save client cursor for %s/%s: %v\n", channel, clientID, err)
			}
		}
	}
}

func sseLoop(w http.ResponseWriter, flusher http.Flusher, sub *nats.Subscriber, clientGone <-chan struct{}, ticker *time.Ticker, lastMsgTs *int64) {
	for {
		select {
		case <-clientGone:
			return
		case data, ok := <-sub.C:
			if !ok {
				return
			}
			sub.Ack(len(data))
			if !writeSSE(w, flusher, fmt.Sprintf("data: %s\n\n", data)) {
				return
			}
			if lastMsgTs != nil {
				*lastMsgTs = time.Now().UTC().UnixMilli()
			}
		case <-ticker.C:
			if !writeSSE(w, flusher, ": keepalive\n\n") {
				return
			}
		}
	}
}

// raftHealth is the subset of the Raft store the health endpoints need. The
// composition root passes the repository instance; handlers stay decoupled
// from internal/repository.
type raftHealth interface {
	IsCleanState() bool
	IsStarted() bool
}

// retReadyz reports readiness: the Raft layer must be a settled Leader/Follower
// with no un-applied committed entries.
func RetReadyz(rs raftHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if rs.IsCleanState() {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "not ready"})
	}
}

// retStartup reports whether this node has joined the Raft cluster (voter or
// leader). Used by the Kubernetes startupProbe.
func RetStartup(rs raftHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if rs.IsStarted() {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
	}
}
