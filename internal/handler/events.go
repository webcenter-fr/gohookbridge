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

func HandleEventsGet(broker *nats.Broker, svc *service.Service) http.HandlerFunc {
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

		fmt.Fprintf(w, "data: %s\n\n", `{"message":"connected"}`)
		flusher.Flush()

		fmt.Fprintf(w, "data: %s\n\n", `{"message":"ready"}`)
		flusher.Flush()

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
		historical, live := broker.Subscribe(channel, since, 100)
		defer broker.Unsubscribe(channel, live)

		for _, data := range historical {
			//nolint:gosec
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		sseLoop(w, flusher, live, clientGone, ticker, &lastMsgTs)

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

func sseLoop(w http.ResponseWriter, flusher http.Flusher, events <-chan []byte, clientGone <-chan struct{}, ticker *time.Ticker, lastMsgTs *int64) {
	for {
		select {
		case <-clientGone:
			return
		case data, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			if lastMsgTs != nil {
				*lastMsgTs = time.Now().UTC().UnixMilli()
			}
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
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
