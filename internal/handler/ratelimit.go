package handler

import (
	"net/http"

	sgdomain "github.com/disaster37/go-secguard/domain"
	sgnethttp "github.com/disaster37/go-secguard/nethttp"
	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

func BanMiddleware(tracker *service.BanTracker, svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cfg, err := svc.GetGlobalConfig(ctx)
			if err != nil || !cfg.Server.BanEnabled {
				next.ServeHTTP(w, r)
				return
			}

			ip, err := service.GetRealIP(r, cfg.Server.BehindReverseProxy, service.ParseTrustedProxyCIDRs(cfg.Server.TrustedProxyCIDRs))
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if tracker.IsBanned(ip.String()) {
				sgnethttp.WriteError(w, sgdomain.ErrIPBanned)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RateLimitMiddleware(limiter *service.RateLimiter, svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			cfg, err := svc.GetGlobalConfig(ctx)
			if err != nil || !cfg.Server.RateLimitEnabled {
				next.ServeHTTP(w, r)
				return
			}

			ip, err := service.GetRealIP(r, cfg.Server.BehindReverseProxy, service.ParseTrustedProxyCIDRs(cfg.Server.TrustedProxyCIDRs))
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if !limiter.Allow(ip.String(), cfg.Server.RateLimitRequests, cfg.Server.RateLimitWindowSeconds) {
				sgnethttp.WriteError(w, sgdomain.ErrRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func APIBansHandler(tracker *service.BanTracker) http.HandlerFunc {
	return sgnethttp.APIBansHandler(tracker)
}

func APIUnbanHandler(tracker *service.BanTracker) http.HandlerFunc {
	return sgnethttp.APIUnbanHandler(tracker, func(r *http.Request) string {
		return chi.URLParam(r, "ip")
	})
}
