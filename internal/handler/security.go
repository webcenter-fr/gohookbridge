package handler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/webcenter-fr/gohookbridge/internal/service"
)

// SecurityHeadersMiddleware applies browser hardening headers (F23).
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// MutationOriginValidation rejects cross-origin mutating browser requests by
// validating the Origin (falling back to the Referer origin) against the
// configured CORS origin (F23). Requests without either header (non-browser
// clients) pass through; the webhook listener is not covered by this
// middleware.
func MutationOriginValidation(svc *service.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			origin := requestOrigin(r)
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := svc.ResolveCORSOrigin(r.Context())
			if allowed == "" || allowed == "*" || origin == allowed {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "origin not allowed", http.StatusForbidden)
		})
	}
}

// requestOrigin returns the request's Origin header, or the origin derived
// from the Referer header when Origin is absent.
func requestOrigin(r *http.Request) string {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
