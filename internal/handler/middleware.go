package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

const UsernameContextKey = "username"
const GroupsContextKey = "oidc_groups"

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
			username := GetUsernameFromContext(r.Context())
			if username == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			channelID := ""
			if pid, ok := r.Context().Value(contextKeyChannelID).(string); ok {
				channelID = pid
			}

			if !svc.UserHasPermission(r.Context(), username, perm, channelID) {
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
			username := GetUsernameFromContext(r.Context())
			if username == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			channelID := ""
			if pid, ok := r.Context().Value(contextKeyChannelID).(string); ok {
				channelID = pid
			}

			if svc.UserHasPermission(r.Context(), username, domain.PermChannelWrite, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			if svc.UserHasPermission(r.Context(), username, domain.PermRBACWrite, channelID) {
				next.ServeHTTP(w, r)
				return
			}

			if svc.UserHasPermission(r.Context(), username, domain.PermAll, channelID) {
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

func IPRestrictMiddleware(svc *service.Service) func(http.Handler) http.Handler {
	var (
		mu    sync.Mutex
		cache = make(map[string]*ipRanges)
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
			clientIP, err := service.GetRealIP(r, behindReverseProxy)
			if err != nil {
				http.Error(w, "Failed to determine client IP", http.StatusBadRequest)
				return
			}

			mu.Lock()
			ranges, ok := cache[channel]
			mu.Unlock()
			if !ok {
				ranges, err = parseIPRanges(allowedIPs)
				if err != nil {
					http.Error(w, "Invalid IP configuration", http.StatusInternalServerError)
					return
				}
				mu.Lock()
				cache[channel] = ranges
				mu.Unlock()
			}

			if !ranges.contains(clientIP) {
				http.Error(w, fmt.Sprintf("IP address %s not allowed", clientIP), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
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
					if svc.UserHasPermission(r.Context(), token.Username, perm, channel) {
						//nolint:revive,staticcheck // context keys are package-level string constants by design
						ctx := context.WithValue(r.Context(), UsernameContextKey, token.Username)
						if len(token.Groups) > 0 {
							//nolint:revive,staticcheck // context keys are package-level string constants by design
							ctx = context.WithValue(ctx, GroupsContextKey, token.Groups)
						}
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
				next.ServeHTTP(w, r)
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
