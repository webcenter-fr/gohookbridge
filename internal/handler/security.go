package handler

import (
	"net/http"

	sgheaders "github.com/disaster37/go-secguard/headers"
	sgnethttp "github.com/disaster37/go-secguard/nethttp"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

// SecurityHeadersMiddleware applies browser hardening headers via go-secguard (F23).

func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return sgnethttp.SecurityHeadersMiddleware(next)
}

// MutationOriginValidation rejects cross-origin mutating browser requests by
// validating Origin/Referer against the dynamic Raft-stored CORS origin (F23).
func MutationOriginValidation(svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var allowed []string
			if corsOrigin := svc.ResolveCORSOrigin(r.Context()); corsOrigin != "" {
				allowed = []string{corsOrigin}
			}
			if err := sgheaders.CheckMutationOrigin(r.Method, r.Header.Get("Origin"), r.Header.Get("Referer"), allowed); err != nil {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
