package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter is a sliding-window per-IP request limiter.
type RateLimiter struct {
	mu        sync.Mutex
	entries   map[string][]time.Time
	lastSweep time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		entries: make(map[string][]time.Time),
	}
}

// Sweep removes IP keys that have no timestamps within the last windowSeconds.
func (rl *RateLimiter) Sweep(windowSeconds int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.sweep(time.Now(), clampWindow(windowSeconds))
}

func (rl *RateLimiter) sweep(now time.Time, windowSeconds int) {
	cutoff := now.Add(-time.Duration(windowSeconds) * time.Second)
	for ip, times := range rl.entries {
		if len(times) == 0 || times[len(times)-1].Before(cutoff) {
			delete(rl.entries, ip)
		}
	}
}

func (rl *RateLimiter) maybeSweep(now time.Time, windowSeconds int) {
	if !rl.lastSweep.IsZero() && now.Sub(rl.lastSweep) < time.Duration(windowSeconds)*time.Second {
		return
	}
	rl.lastSweep = now
	rl.sweep(now, windowSeconds)
}

func (rl *RateLimiter) Allow(ip string, maxRequests int, windowSeconds int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.maybeSweep(now, clampWindow(windowSeconds))
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

// BanTracker counts credential failures per IP and bans suspicious clients.
type BanTracker struct {
	mu        sync.Mutex
	failures  map[string][]banEntry
	banned    map[string]time.Time
	lastSweep time.Time
}

func NewBanTracker() *BanTracker {
	return &BanTracker{
		failures: make(map[string][]banEntry),
		banned:   make(map[string]time.Time),
	}
}

func (bt *BanTracker) recordFailure(ip, fingerprint string, windowSeconds int) {
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

func (bt *BanTracker) IsBanned(ip string) bool {
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

func (bt *BanTracker) banIfSuspicious(ip string, maxUniqueFailures, banDurationSeconds, windowSeconds int) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	now := time.Now()

	// Clean expired failures and count unique fingerprints within window
	fingerprints := make(map[string]struct{})
	valid := bt.failures[ip][:0]
	for _, e := range bt.failures[ip] {
		if now.Sub(e.timestamp) <= time.Duration(windowSeconds)*time.Second {
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

// Sweep removes expired failure entries and expired bans.
func (bt *BanTracker) Sweep(windowSeconds int) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	now := time.Now()
	bt.lastSweep = now
	bt.sweep(now, clampWindow(windowSeconds))
}

// sweep removes expired failure entries and expired bans without touching the
// throttle state; callers must hold bt.mu.
func (bt *BanTracker) sweep(now time.Time, windowSeconds int) {
	cutoff := now.Add(-time.Duration(windowSeconds) * time.Second)
	for ip, entries := range bt.failures {
		kept := entries[:0]
		for _, e := range entries {
			if e.timestamp.After(cutoff) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(bt.failures, ip)
		} else {
			bt.failures[ip] = kept
		}
	}
	for ip, until := range bt.banned {
		if !now.Before(until) {
			delete(bt.banned, ip)
		}
	}
}

// maybeSweep runs sweep at most once per window so that the credential-failure
// hot path (webhooks, channel tokens) cannot be turned into a per-failure
// full-map scan by an attacker forcing failures.
func (bt *BanTracker) maybeSweep(now time.Time, windowSeconds int) {
	if !bt.lastSweep.IsZero() && now.Sub(bt.lastSweep) < time.Duration(windowSeconds)*time.Second {
		return
	}
	bt.lastSweep = now
	bt.sweep(now, windowSeconds)
}

// clampWindow guards against a zero/unset window config, falling back to the
// domain default (300s).
func clampWindow(seconds int) int {
	if seconds <= 0 {
		return 300 // matches domain.DefaultGlobalConfig BanWindowSeconds
	}
	return seconds
}

func (bt *BanTracker) ListBans() []BanInfo {
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

func (bt *BanTracker) Unban(ip string) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	delete(bt.banned, ip)
}

// GetRealIP extracts the client IP, honoring X-Forwarded-For/X-Real-IP only
// when behindReverseProxy is enabled.
func GetRealIP(r *http.Request, behindReverseProxy bool) (net.IP, error) {
	if behindReverseProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ips := strings.Split(xff, ",")
			clientIP := strings.TrimSpace(ips[0])
			ip := net.ParseIP(clientIP)
			if ip != nil {
				return ip, nil
			}
		}

		if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
			ip := net.ParseIP(strings.TrimSpace(xrip))
			if ip != nil {
				return ip, nil
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip := net.ParseIP(r.RemoteAddr)
		if ip != nil {
			return ip, nil
		}
		return nil, fmt.Errorf("invalid RemoteAddr %q: %w", r.RemoteAddr, err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address %q", host)
	}
	return ip, nil
}

// RecordCredentialFailure records a failed credential attempt and bans the
// client IP when the failure pattern looks like an attack.
func (s *Service) RecordCredentialFailure(ctx context.Context, tracker *BanTracker, r *http.Request, fingerprint string) {
	cfg, err := s.repo.GetGlobalConfig(ctx)
	if err != nil || !cfg.Server.BanEnabled {
		return
	}

	ip, err := GetRealIP(r, cfg.Server.BehindReverseProxy)
	if err != nil {
		return
	}

	ipStr := ip.String()
	window := clampWindow(cfg.Server.BanWindowSeconds)
	tracker.maybeSweep(time.Now(), window)
	tracker.recordFailure(ipStr, fingerprint, window)
	tracker.banIfSuspicious(ipStr, cfg.Server.BanMaxUniqueFailures, cfg.Server.BanDurationSeconds, window)
}

func FingerprintLogin(username string) string {
	h := sha256.Sum256([]byte("login:" + username))
	return hex.EncodeToString(h[:])
}

func FingerprintToken(token string) string {
	h := sha256.Sum256([]byte("token:" + token))
	return hex.EncodeToString(h[:])
}

func FingerprintSignature(channel, signatureValue string) string {
	h := sha256.Sum256([]byte("signature:" + channel + ":" + signatureValue))
	return hex.EncodeToString(h[:])
}

func ExtractSignatureValue(r *http.Request) string {
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
