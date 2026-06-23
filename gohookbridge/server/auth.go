package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "gosmee_session"
	sessionMaxAge     = 86400
)

var sessionSecret [32]byte

type sessionToken struct {
	Username  string   `json:"username"`
	Method    string   `json:"method"`
	Provider  string   `json:"provider,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
	Groups    []string `json:"groups,omitempty"`
}

func deriveSessionSecret(secret string) [32]byte {
	return sha256.Sum256([]byte(secret))
}

func encodeSession(token *sessionToken, secret [32]byte) (string, error) {
	data, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

func decodeSession(tokenStr string, secret [32]byte) (*sessionToken, error) {
	parts := strings.SplitN(tokenStr, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}
	encoded := parts[0]
	sigB64 := parts[1]

	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(encoded))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sigB64), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid token signature")
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var token sessionToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	if token.ExpiresAt < time.Now().Unix() {
		return nil, fmt.Errorf("token expired")
	}
	return &token, nil
}

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

func RequireAuth(cfg *AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				if r.Method == http.MethodGet {
					redirectURL := r.URL.String()
					http.Redirect(w, r, "/?redirect="+redirectURL, http.StatusFound)
				} else {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
				}
				return
			}
			token, err := decodeSession(cookie.Value, sessionSecret)
			if err != nil {
				clearSessionCookie(w)
				if r.Method == http.MethodGet {
					redirectURL := r.URL.String()
					http.Redirect(w, r, "/?redirect="+redirectURL, http.StatusFound)
				} else {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
				}
				return
			}
			ctx := context.WithValue(r.Context(), store.UsernameContextKey, token.Username)
			if len(token.Groups) > 0 {
				ctx = context.WithValue(ctx, store.GroupsContextKey, token.Groups)
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

func RequireAuthDynamic(rs *store.RaftStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := store.BuildAuthConfig(rs)
			if cfg == nil {
				next.ServeHTTP(w, r)
				return
			}
			secret := rs.SessionSecret()
			if secret != "" && sessionSecret == [32]byte{} {
				sessionSecret = deriveSessionSecret(secret)
			}
			RequireAuth(cfg)(next).ServeHTTP(w, r)
		})
	}
}

func ValidatePassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func generateRandomHex(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func apiAuthMethodsHandler(rs *store.RaftStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.BuildAuthConfig(rs)
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

func apiLoginHandler(rs *store.RaftStore, banTracker *banTracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.BuildAuthConfig(rs)
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
				if ValidatePassword(u.PasswordHash, body.Password) {
					valid = true
					break
				}
			}
		}
		if !valid {
			recordCredentialFailure(banTracker, rs, r, fingerprintLogin(body.Username))
			http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
			return
		}

		token := &sessionToken{
			Username:  body.Username,
			Method:    "internal",
			ExpiresAt: time.Now().Unix() + sessionMaxAge,
		}
		encoded, err := encodeSession(token, sessionSecret)
		if err != nil {
			http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, encoded)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

func apiLogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clearSessionCookie(w)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}
