package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

const (
	// UsernameContextKey carries the display-only username.
	UsernameContextKey = "username"
	// GroupsContextKey carries the verified OIDC groups from the signed session.
	GroupsContextKey = "oidc_groups"
	// PrincipalContextKey carries the resolved *domain.User principal.
	PrincipalContextKey = "principal"
)

type contextKey string

const contextKeyChannelID contextKey = "channel_id"

func GetUsernameFromContext(ctx context.Context) string {
	username, _ := ctx.Value(UsernameContextKey).(string)
	return username
}

func GetGroupsFromContext(ctx context.Context) []string {
	groups, _ := ctx.Value(GroupsContextKey).([]string)
	return groups
}

// GetPrincipalFromContext returns the resolved principal stored by RequireAuth
// or ChannelAccessMiddleware.
func GetPrincipalFromContext(ctx context.Context) *domain.User {
	user, _ := ctx.Value(PrincipalContextKey).(*domain.User)
	return user
}

// resolveRequestUser returns the principal for the request, falling back to
// resolving the display-only username when no principal was stored (legacy
// callers and tests).
func resolveRequestUser(svc *service.Service, r *http.Request) *domain.User {
	user := GetPrincipalFromContext(r.Context())
	if user != nil {
		return user
	}
	return svc.ResolvePrincipalByUsername(r.Context(), GetUsernameFromContext(r.Context()))
}

// withPrincipal stores the resolved principal, its display username, and the
// verified groups in the request context.
//
//nolint:revive,staticcheck // context keys are package-level string constants by design
func withPrincipal(ctx context.Context, user *domain.User, groups []string) context.Context {
	ctx = context.WithValue(ctx, PrincipalContextKey, user)
	ctx = context.WithValue(ctx, UsernameContextKey, user.Username)
	if len(groups) > 0 {
		ctx = context.WithValue(ctx, GroupsContextKey, groups)
	}
	return ctx
}

// ChannelContext sets contextKeyChannelID from the "channel" URL param so
// RequirePermission can scope channel:* permissions.
func ChannelContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if channel := chi.URLParam(r, "channel"); channel != "" {
			//nolint:revive,staticcheck // context keys are package-level string constants by design
			ctx := context.WithValue(r.Context(), contextKeyChannelID, channel)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func RequirePermission(svc *service.Service, perm domain.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := resolveRequestUser(svc, r)
			if user == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			groups := GetGroupsFromContext(r.Context())

			channelID := ""
			if pid, ok := r.Context().Value(contextKeyChannelID).(string); ok {
				channelID = pid
			}

			if !svc.UserHasPermission(r.Context(), user, groups, perm, channelID) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func RequireChannelACLPermission(svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := resolveRequestUser(svc, r)
			if user == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			groups := GetGroupsFromContext(r.Context())

			channelID := ""
			if pid, ok := r.Context().Value(contextKeyChannelID).(string); ok {
				channelID = pid
			}

			if svc.UserHasPermission(r.Context(), user, groups, domain.PermChannelWrite, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			if svc.UserHasPermission(r.Context(), user, groups, domain.PermRBACWrite, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			if svc.UserHasPermission(r.Context(), user, groups, domain.PermAll, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, "Forbidden", http.StatusForbidden)
		})
	}
}

type ipRanges struct {
	networks []*net.IPNet
	ips      []net.IP
}

func parseIPRanges(ranges []string) (*ipRanges, error) {
	result := &ipRanges{}
	for _, r := range ranges {
		if strings.Contains(r, "/") {
			_, ipnet, err := net.ParseCIDR(r)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR range %q: %w", r, err)
			}
			result.networks = append(result.networks, ipnet)
		} else {
			ip := net.ParseIP(r)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP address %q", r)
			}
			result.ips = append(result.ips, ip)
		}
	}
	return result, nil
}

func (r *ipRanges) contains(ip net.IP) bool {
	if slices.ContainsFunc(r.ips, func(allowedIP net.IP) bool {
		return ip.Equal(allowedIP)
	}) {
		return true
	}

	for _, network := range r.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

const (
	ipCacheTTL = 5 * time.Minute
	ipCacheMax = 10000
)

// ipCacheEntry caches the parsed allowlist for a content hash of the allowed
// IP set, so a config change invalidates the entry even when the channel ID is
// unchanged (F20).
type ipCacheEntry struct {
	ranges *ipRanges
	at     time.Time
}

func allowedIPsHash(allowedIPs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(allowedIPs, "\x00")))
	return hex.EncodeToString(sum[:])
}

func IPRestrictMiddleware(svc *service.Service) func(http.Handler) http.Handler {
	var (
		mu    sync.Mutex
		cache = make(map[string]ipCacheEntry)
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			channel := chi.URLParam(r, "channel")
			allowedIPs, _ := svc.ResolveChannelAllowedIPs(r.Context(), channel)
			if len(allowedIPs) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			behindReverseProxy := svc.ResolveBehindReverseProxy(r.Context())
			trusted := service.ParseTrustedProxyCIDRs(svc.ResolveTrustedProxyCIDRs(r.Context()))
			clientIP, err := service.GetRealIP(r, behindReverseProxy, trusted)
			if err != nil {
				http.Error(w, "Failed to determine client IP", http.StatusBadRequest)
				return
			}

			key := allowedIPsHash(allowedIPs)
			now := time.Now()
			mu.Lock()
			entry, ok := cache[key]
			if ok && now.Sub(entry.at) > ipCacheTTL {
				delete(cache, key)
				ok = false
			}
			mu.Unlock()
			if !ok {
				ranges, err := parseIPRanges(allowedIPs)
				if err != nil {
					http.Error(w, "Invalid IP configuration", http.StatusInternalServerError)
					return
				}
				entry = ipCacheEntry{ranges: ranges, at: now}
				mu.Lock()
				if len(cache) >= ipCacheMax {
					evictOldestIPCache(cache)
				}
				cache[key] = entry
				mu.Unlock()
			}

			if !entry.ranges.contains(clientIP) {
				http.Error(w, fmt.Sprintf("IP address %s not allowed", clientIP), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// evictOldestIPCache removes the single oldest entry so the cache stays
// bounded under an attacker forcing many distinct allowlists. Callers hold mu.
func evictOldestIPCache(cache map[string]ipCacheEntry) {
	oldestKey := ""
	var oldest time.Time
	for k, e := range cache {
		if oldestKey == "" || e.at.Before(oldest) {
			oldestKey, oldest = k, e.at
		}
	}
	if oldestKey != "" {
		delete(cache, oldestKey)
	}
}

func ChannelAccessMiddleware(svc *service.Service, requiredScope string, banTracker *service.BanTracker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			channel := chi.URLParam(r, "channel")
			sessionSecret := service.DeriveSessionSecret(svc.SessionSecret(r.Context()))

			// Try session-based auth first (browser UI)
			cookie, err := r.Cookie(sessionCookieName)
			if err == nil {
				token, err := service.DecodeSession(cookie.Value, sessionSecret)
				if err == nil {
					perm := domain.PermChannelRead
					if requiredScope == "produce" {
						perm = domain.PermChannelWrite
					}
					user := svc.ResolvePrincipal(r.Context(), token)
					if user != nil && svc.SessionEpochValid(r.Context(), token) &&
						svc.UserHasPermission(r.Context(), user, token.Groups, perm, channel) {
						ctx := withPrincipal(r.Context(), user, token.Groups)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
			}

			// Fall back to token-based auth (CLI clients)
			chConfig, err := svc.ResolveChannelConfig(r.Context(), channel)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					http.Error(w, "channel not found", http.StatusNotFound)
					return
				}
				http.Error(w, "channel configuration unavailable", http.StatusServiceUnavailable)
				return
			}

			if chConfig.AccessMode != "token" {
				next.ServeHTTP(w, r)
				return
			}

			token := r.URL.Query().Get("token")

			if token == "" {
				auth := r.Header.Get("Authorization")
				if strings.HasPrefix(auth, "Bearer ") {
					token = strings.TrimPrefix(auth, "Bearer ")
				}
			}

			if token == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if !svc.ValidateChannelToken(r.Context(), channel, token, requiredScope) {
				svc.RecordCredentialFailure(r.Context(), banTracker, r, service.FingerprintToken(token))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
