package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
)

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		entries: make(map[string][]time.Time),
	}
}

func (rl *rateLimiter) allow(ip string, maxRequests int, windowSeconds int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-time.Duration(windowSeconds) * time.Second)

	times := rl.entries[ip]
	valid := times[:0]
	for _, t := range times {
		if t.After(windowStart) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= maxRequests {
		rl.entries[ip] = valid
		return false
	}

	rl.entries[ip] = append(valid, now)
	return true
}

type banEntry struct {
	fingerprint string
	timestamp   time.Time
}

type BanInfo struct {
	IP             string    `json:"ip"`
	Until          time.Time `json:"until"`
	UniqueFailures int       `json:"unique_failures"`
}

type banTracker struct {
	mu       sync.Mutex
	failures map[string][]banEntry
	banned   map[string]time.Time
}

func newBanTracker() *banTracker {
	return &banTracker{
		failures: make(map[string][]banEntry),
		banned:   make(map[string]time.Time),
	}
}

func (bt *banTracker) recordFailure(ip, fingerprint string, windowSeconds int) {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-time.Duration(windowSeconds) * time.Second)

	entries := bt.failures[ip]

	// Deduplicate: silently ignore if the same fingerprint already exists within the window.
	// Same credential failing repeatedly = misconfiguration, not attack.
	for _, e := range entries {
		if e.timestamp.After(windowStart) && e.fingerprint == fingerprint {
			return
		}
	}

	// Remove expired entries
	valid := entries[:0]
	for _, e := range entries {
		if e.timestamp.After(windowStart) {
			valid = append(valid, e)
		}
	}
	valid = append(valid, banEntry{fingerprint: fingerprint, timestamp: now})
	bt.failures[ip] = valid
}

func (bt *banTracker) isBanned(ip string) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	until, ok := bt.banned[ip]
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	delete(bt.banned, ip)
	return false
}

func (bt *banTracker) banIfSuspicious(ip string, maxUniqueFailures int, banDurationSeconds int) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	now := time.Now()

	// Clean expired failures and count unique fingerprints within window
	fingerprints := make(map[string]struct{})
	valid := bt.failures[ip][:0]
	for _, e := range bt.failures[ip] {
		if now.Sub(e.timestamp) <= time.Duration(bt.getWindowSeconds(ip))*time.Second {
			valid = append(valid, e)
			fingerprints[e.fingerprint] = struct{}{}
		}
	}
	bt.failures[ip] = valid

	if len(fingerprints) >= maxUniqueFailures {
		bt.banned[ip] = now.Add(time.Duration(banDurationSeconds) * time.Second)
		return true
	}
	return false
}

// getWindowSeconds returns the window seconds for the given IP's failure tracking.
// This is a helper used internally while holding the lock, so it returns a default.
func (bt *banTracker) getWindowSeconds(_ string) int {
	return 300
}

func (bt *banTracker) listBans() []BanInfo {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	now := time.Now()
	bans := make([]BanInfo, 0)
	for ip, until := range bt.banned {
		if now.Before(until) {
			fingerprints := make(map[string]struct{})
			for _, e := range bt.failures[ip] {
				fingerprints[e.fingerprint] = struct{}{}
			}
			bans = append(bans, BanInfo{
				IP:             ip,
				Until:          until,
				UniqueFailures: len(fingerprints),
			})
		} else {
			delete(bt.banned, ip)
		}
	}
	return bans
}

func (bt *banTracker) unban(ip string) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	delete(bt.banned, ip)
}

func banMiddleware(tracker *banTracker, rs *store.RaftStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg, err := rs.GetGlobalConfig()
			if err != nil || !cfg.Server.BanEnabled {
				next.ServeHTTP(w, r)
				return
			}

			ip, err := getRealIP(r, cfg.Server.BehindReverseProxy)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if tracker.isBanned(ip.String()) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "IP address is banned due to suspicious activity",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rateLimitMiddleware(limiter *rateLimiter, rs *store.RaftStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg, err := rs.GetGlobalConfig()
			if err != nil || !cfg.Server.RateLimitEnabled {
				next.ServeHTTP(w, r)
				return
			}

			ip, err := getRealIP(r, cfg.Server.BehindReverseProxy)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if !limiter.allow(ip.String(), cfg.Server.RateLimitRequests, cfg.Server.RateLimitWindowSeconds) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func recordCredentialFailure(tracker *banTracker, rs *store.RaftStore, r *http.Request, fingerprint string) {
	cfg, err := rs.GetGlobalConfig()
	if err != nil || !cfg.Server.BanEnabled {
		return
	}

	ip, err := getRealIP(r, cfg.Server.BehindReverseProxy)
	if err != nil {
		return
	}

	ipStr := ip.String()
	tracker.recordFailure(ipStr, fingerprint, cfg.Server.BanWindowSeconds)
	tracker.banIfSuspicious(ipStr, cfg.Server.BanMaxUniqueFailures, cfg.Server.BanDurationSeconds)
}

func fingerprintLogin(username string) string {
	h := sha256.Sum256([]byte("login:" + username))
	return hex.EncodeToString(h[:])
}

func fingerprintToken(token string) string {
	h := sha256.Sum256([]byte("token:" + token))
	return hex.EncodeToString(h[:])
}

func fingerprintSignature(channel, signatureValue string) string {
	h := sha256.Sum256([]byte("signature:" + channel + ":" + signatureValue))
	return hex.EncodeToString(h[:])
}

func extractSignatureValue(r *http.Request) string {
	if v := r.Header.Get("X-Hub-Signature-256"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Gitlab-Token"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Hub-Signature"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Gitea-Signature"); v != "" {
		return v
	}
	return "unknown"
}

func apiBansHandler(tracker *banTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tracker.listBans())
	}
}

func apiUnbanHandler(tracker *banTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := chi.URLParam(r, "ip")
		if ip == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "IP address required"})
			return
		}
		if net.ParseIP(ip) == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid IP address %q", ip)})
			return
		}
		tracker.unban(ip)
		w.WriteHeader(http.StatusNoContent)
	}
}