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

func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sessionMaxAge,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // Secure reflects the effective TLS deployment, derived once at startup
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// unauthorizedResponse clears the session cookie and answers GETs with a
// redirect to the login page and other methods with 401.
func unauthorizedResponse(w http.ResponseWriter, r *http.Request, secure bool) {
	clearSessionCookie(w, secure)
	if r.Method == http.MethodGet {
		redirectURL := r.URL.String()
		//nolint:gosec
		http.Redirect(w, r, "/?redirect="+redirectURL, http.StatusFound)
		return
	}
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func RequireAuth(svc *service.Service, secret [32]byte, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				unauthorizedResponse(w, r, secure)
				return
			}
			token, err := service.DecodeSession(cookie.Value, secret)
			if err != nil {
				unauthorizedResponse(w, r, secure)
				return
			}
			// The principal must resolve to a stored user and the session must
			// belong to the current auth epoch; otherwise the session is stale
			// or forged. A nil service (unit tests) skips principal resolution.
			if svc != nil {
				user := svc.ResolvePrincipal(r.Context(), token)
				if user == nil || !svc.SessionEpochValid(r.Context(), token) {
					unauthorizedResponse(w, r, secure)
					return
				}
				next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), user, token.Groups)))
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

func LogoutHandler(secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clearSessionCookie(w, secure)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func RequireAuthDynamic(svc *service.Service, secret [32]byte, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := svc.BuildAuthConfig(r.Context())
			if cfg == nil {
				next.ServeHTTP(w, r)
				return
			}
			RequireAuth(svc, secret, secure)(next).ServeHTTP(w, r)
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

func APILoginHandler(svc *service.Service, secret [32]byte, banTracker *service.BanTracker, throttle *service.LoginThrottle, secure bool) http.HandlerFunc {
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
		// The login endpoint is unauthenticated: cap the body so a huge
		// payload cannot be buffered without limit (CWE-400, F11).
		if !decodeJSON(w, r, &body) {
			return
		}

		// Per-account throttling is always-on and keyed by a fingerprint so raw
		// usernames are never stored.
		throttleKey := service.FingerprintLogin(body.Username)
		attempts, cooldown := loginThrottleConfig(ctx, svc)
		if throttle != nil && !throttle.Allow(throttleKey, attempts, cooldown) {
			http.Error(w, `{"error":"too many login attempts; try again later"}`, http.StatusTooManyRequests)
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
			svc.RecordCredentialFailure(ctx, banTracker, r, throttleKey)
			http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
			return
		}
		if throttle != nil {
			throttle.Clear(throttleKey)
		}

		user, err := svc.GetUserByUsername(ctx, body.Username)
		if err != nil {
			http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
			return
		}
		sessionID, err := service.GenerateRandomHex()
		if err != nil {
			http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
			return
		}
		token := &service.SessionToken{
			UserID:    user.ID,
			Username:  user.Username,
			Method:    "internal",
			SessionID: sessionID,
			AuthEpoch: svc.GetAuthEpoch(ctx),
			ExpiresAt: time.Now().Unix() + sessionMaxAge,
		}
		encoded, err := service.EncodeSession(token, secret)
		if err != nil {
			http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, encoded, secure)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

// loginThrottleConfig returns the configured per-account attempt budget and
// cooldown, falling back to the domain defaults.
func loginThrottleConfig(ctx context.Context, svc *service.Service) (int, int) {
	attempts, cooldown := 10, 300
	if cfg, err := svc.GetGlobalConfig(ctx); err == nil {
		if cfg.Server.LoginThrottleAttempts > 0 {
			attempts = cfg.Server.LoginThrottleAttempts
		}
		if cfg.Server.LoginThrottleCooldown > 0 {
			cooldown = cfg.Server.LoginThrottleCooldown
		}
	}
	return attempts, cooldown
}

func APILogoutHandler(secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		clearSessionCookie(w, secure)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}
