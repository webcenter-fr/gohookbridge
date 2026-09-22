package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/service"
)

const (
	sessionCookieName = "gohookbridge_session"
	sessionMaxAge     = 86400
)

// dummyPasswordHash is a valid bcrypt hash of an unguessable placeholder
// password. It is compared against the submitted password when the username is
// unknown so that login response timing does not reveal whether an account
// exists (CWE-204).
//
//nolint:gosec // this is a dummy hash used for timing equalization, not a credential
const dummyPasswordHash = "$2a$10$GB/spbww7shMhAoZAdOXKOcdUaA/W1gnFjKLZpLdW.xe6U0FUNBfe"

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sessionMaxAge,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func RequireAuth(secret [32]byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				if r.Method == http.MethodGet {
					redirectURL := r.URL.String()
					//nolint:gosec
					http.Redirect(w, r, "/?redirect="+redirectURL, http.StatusFound)
				} else {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
				}
				return
			}
			token, err := service.DecodeSession(cookie.Value, secret)
			if err != nil {
				clearSessionCookie(w)
				if r.Method == http.MethodGet {
					redirectURL := r.URL.String()
					//nolint:gosec
					http.Redirect(w, r, "/?redirect="+redirectURL, http.StatusFound)
				} else {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
				}
				return
			}
			//nolint:revive,staticcheck // context keys are package-level string constants by design
			ctx := context.WithValue(r.Context(), UsernameContextKey, token.Username)
			if len(token.Groups) > 0 {
				//nolint:revive,staticcheck // context keys are package-level string constants by design
				ctx = context.WithValue(ctx, GroupsContextKey, token.Groups)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func RequireAuthDynamic(svc *service.Service, secret [32]byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := svc.BuildAuthConfig(r.Context())
			if cfg == nil {
				next.ServeHTTP(w, r)
				return
			}
			RequireAuth(secret)(next).ServeHTTP(w, r)
		})
	}
}

func APIAuthMethodsHandler(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := svc.BuildAuthConfig(r.Context())
		localEnabled := false
		providers := make([]map[string]string, 0)
		if cfg != nil {
			localEnabled = cfg.Internal.Enabled
			if cfg.OIDC.Enabled {
				for _, p := range cfg.OIDC.Providers {
					providers = append(providers, map[string]string{
						"id":   p.ID,
						"name": p.Name,
					})
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"local_enabled":  localEnabled,
			"oidc_providers": providers,
		})
	}
}

func APILoginHandler(svc *service.Service, secret [32]byte, banTracker *service.BanTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cfg := svc.BuildAuthConfig(ctx)
		if cfg == nil || !cfg.Internal.Enabled {
			http.Error(w, `{"error":"local auth not enabled"}`, http.StatusNotFound)
			return
		}

		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		var valid bool
		for _, u := range cfg.Internal.Users {
			if u.Username == body.Username {
				valid = service.ValidatePassword(u.PasswordHash, body.Password)
				break
			}
		}
		if !valid {
			// Equalize the response time for unknown usernames with a dummy
			// bcrypt comparison so timing does not leak account existence.
			_ = service.ValidatePassword(dummyPasswordHash, body.Password)
			svc.RecordCredentialFailure(ctx, banTracker, r, service.FingerprintLogin(body.Username))
			http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
			return
		}

		token := &service.SessionToken{
			Username:  body.Username,
			Method:    "internal",
			ExpiresAt: time.Now().Unix() + sessionMaxAge,
		}
		encoded, err := service.EncodeSession(token, secret)
		if err != nil {
			http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, encoded)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

func APILogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		clearSessionCookie(w)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}
