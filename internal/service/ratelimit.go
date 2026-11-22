package service

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/disaster37/go-secguard/bantracker"
	"github.com/disaster37/go-secguard/ratelimit"
	"github.com/disaster37/go-secguard/realip"
)

// BanInfo describes an active IP ban.
type BanInfo = bantracker.BanInfo

// BanTracker counts unique credential failures per IP and bans suspicious clients.
type BanTracker = bantracker.BanTracker

// NewBanTracker returns a bounded BanTracker with default capacity (4096).
func NewBanTracker() *BanTracker {
	return bantracker.New(0, nil)
}

// RateLimiter is a bounded sliding-window per-IP request limiter.
type RateLimiter struct {
	inner *ratelimit.RateLimiter
}

// NewRateLimiter returns a bounded RateLimiter with default capacity (4096).
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{inner: ratelimit.NewRateLimiter(0, nil)}
}

// Allow reports whether ip is within maxRequests over windowSeconds.
func (rl *RateLimiter) Allow(ip string, maxRequests, windowSeconds int) bool {
	if maxRequests <= 0 {
		return false
	}
	return rl.inner.Allow(ip, maxRequests, time.Duration(clampWindow(windowSeconds))*time.Second)
}

// Sweep removes expired keys outside windowSeconds.
func (rl *RateLimiter) Sweep(windowSeconds int) {
	rl.inner.Sweep(time.Duration(clampWindow(windowSeconds)) * time.Second)
}

// Len returns the number of tracked IP keys.
func (rl *RateLimiter) Len() int {
	return rl.inner.Len()
}

// LoginThrottle bounds repeated failed logins per account.
type LoginThrottle struct {
	inner *ratelimit.LoginThrottle
}

// NewLoginThrottle returns a bounded LoginThrottle with default capacity (4096).
func NewLoginThrottle() *LoginThrottle {
	return &LoginThrottle{inner: ratelimit.NewLoginThrottle(0, nil)}
}

// Allow reports whether key may attempt another login within windowSeconds.
func (lt *LoginThrottle) Allow(key string, maxAttempts, windowSeconds int) bool {
	return lt.inner.Allow(key, maxAttempts, time.Duration(clampWindow(windowSeconds))*time.Second)
}

// Clear resets the throttle state for key after a successful login.
func (lt *LoginThrottle) Clear(key string) {
	lt.inner.Clear(key)
}

func clampWindow(seconds int) int {
	if seconds <= 0 {
		return 300
	}
	return seconds
}

// ParseTrustedProxyCIDRs parses a list of CIDRs/IPs into networks using
// go-secguard/realip, skipping malformed runtime entries individually.
func ParseTrustedProxyCIDRs(entries []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		parsed, err := realip.ParseTrustedProxyCIDRs([]string{e})
		if err == nil {
			nets = append(nets, parsed...)
		}
	}
	return nets
}

// ValidateTrustedProxyCIDRs strictly validates CIDR/IP strings (for API/config validation).
func ValidateTrustedProxyCIDRs(entries []string) error {
	_, err := realip.ParseTrustedProxyCIDRs(entries)
	return err
}

// GetRealIP extracts the client IP via go-secguard/realip (F18).
func GetRealIP(r *http.Request, behindReverseProxy bool, trustedCIDRs []*net.IPNet) (net.IP, error) {
	return realip.GetRealIPFromRequest(r, behindReverseProxy, trustedCIDRs)
}

// RecordCredentialFailure records a failed credential attempt and bans the
// client IP when unique failures reach the configured threshold.
func (s *Service) RecordCredentialFailure(ctx context.Context, tracker *BanTracker, r *http.Request, fingerprint string) {
	if tracker == nil {
		return
	}
	cfg, err := s.repo.GetGlobalConfig(ctx)
	if err != nil || !cfg.Server.BanEnabled {
		return
	}
	ip, err := GetRealIP(r, cfg.Server.BehindReverseProxy, ParseTrustedProxyCIDRs(cfg.Server.TrustedProxyCIDRs))
	if err != nil {
		return
	}
	window := time.Duration(clampWindow(cfg.Server.BanWindowSeconds)) * time.Second
	banDuration := time.Duration(cfg.Server.BanDurationSeconds) * time.Second
	tracker.RecordAndBan(ip.String(), fingerprint, cfg.Server.BanMaxUniqueFailures, banDuration, window)
}

func FingerprintLogin(username string) string {
	return bantracker.FingerprintLogin(username)
}

func FingerprintToken(token string) string {
	return bantracker.FingerprintToken(token)
}

func FingerprintSignature(channel, signatureValue string) string {
	return bantracker.FingerprintSignature(channel, signatureValue)
}

func ExtractSignatureValue(r *http.Request) string {
	return bantracker.ExtractSignatureValueFromRequest(r)
}
