package handler

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

func banMiddleware(tracker *service.BanTracker, svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cfg, err := svc.GetGlobalConfig(ctx)
			if err != nil || !cfg.Server.BanEnabled {
				next.ServeHTTP(w, r)
				return
			}

			ip, err := service.GetRealIP(r, cfg.Server.BehindReverseProxy)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if tracker.IsBanned(ip.String()) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "IP address is banned due to suspicious activity",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rateLimitMiddleware(limiter *service.RateLimiter, svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cfg, err := svc.GetGlobalConfig(ctx)
			if err != nil || !cfg.Server.RateLimitEnabled {
				next.ServeHTTP(w, r)
				return
			}

			ip, err := service.GetRealIP(r, cfg.Server.BehindReverseProxy)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if !limiter.Allow(ip.String(), cfg.Server.RateLimitRequests, cfg.Server.RateLimitWindowSeconds) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func apiBansHandler(tracker *service.BanTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tracker.ListBans())
	}
}

func apiUnbanHandler(tracker *service.BanTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := chi.URLParam(r, "ip")
		if ip == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "IP address required"})
			return
		}
		if net.ParseIP(ip) == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid IP address %q", ip)})
			return
		}
		tracker.Unban(ip)
		w.WriteHeader(http.StatusNoContent)
	}
}
