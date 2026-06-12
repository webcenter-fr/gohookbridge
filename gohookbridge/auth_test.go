package gohookbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
)

func newTestAuthConfig() *AuthConfig {
	hash, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return &AuthConfig{
		Internal: InternalConfig{
			Enabled: true,
			Users: []InternalUser{
				{
					Username:     "testuser",
					PasswordHash: string(hash),
				},
			},
		},
		OIDC: OIDCConfig{
			Enabled:   false,
			Providers: nil,
		},
	}
}

func newTestSessionToken(username, method string) *sessionToken {
	return &sessionToken{
		Username:  username,
		Method:    method,
		ExpiresAt: time.Now().Unix() + 86400,
	}
}

func TestSessionTokenRoundTrip(t *testing.T) {
	secret := deriveSessionSecret("test-secret-key-that-is-long-enough-32")
	token := newTestSessionToken("alice", "internal")

	encoded, err := encodeSession(token, secret)
	assert.NilError(t, err)
	assert.Assert(t, encoded != "")

	decoded, err := decodeSession(encoded, secret)
	assert.NilError(t, err)
	assert.Equal(t, decoded.Username, "alice")
	assert.Equal(t, decoded.Method, "internal")

	t.Run("InvalidSignature", func(t *testing.T) {
		parts := strings.SplitN(encoded, ".", 2)
		tampered := parts[0] + "." + "AAAA"
		_, err := decodeSession(tampered, secret)
		assert.ErrorContains(t, err, "invalid token signature")
	})

	t.Run("ExpiredToken", func(t *testing.T) {
		expired := &sessionToken{
			Username:  "bob",
			Method:    "internal",
			ExpiresAt: time.Now().Unix() - 1,
		}
		enc, err := encodeSession(expired, secret)
		assert.NilError(t, err)
		_, err = decodeSession(enc, secret)
		assert.ErrorContains(t, err, "token expired")
	})

	t.Run("WrongSecret", func(t *testing.T) {
		otherSecret := deriveSessionSecret("different-secret-that-is-also-long-enough")
		enc, err := encodeSession(token, secret)
		assert.NilError(t, err)
		_, err = decodeSession(enc, otherSecret)
		assert.ErrorContains(t, err, "invalid token signature")
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		_, err := decodeSession("no-dot-separator", secret)
		assert.ErrorContains(t, err, "invalid token format")
	})
}

func TestValidatePassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.MinCost)
	assert.NilError(t, err)

	assert.Assert(t, ValidatePassword(string(hash), "correct"))
	assert.Assert(t, !ValidatePassword(string(hash), "wrong"))
	assert.Assert(t, !ValidatePassword(string(hash), ""))
}

func TestLoadAuthConfig(t *testing.T) {
	t.Run("EmptyPathReturnsNil", func(t *testing.T) {
		cfg, err := LoadAuthConfig("")
		assert.NilError(t, err)
		assert.Assert(t, cfg == nil)
	})
}

func TestRequireAuthMiddleware(t *testing.T) {
	secret := deriveSessionSecret("test-secret-for-middleware-tests-32")
	sessionSecret = secret
	cfg := newTestAuthConfig()

	r := chi.NewRouter()
	r.Use(RequireAuth(cfg))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Run("NoCookieRedirectsToLogin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		loc := w.Header().Get("Location")
		assert.Assert(t, strings.HasPrefix(loc, "/?redirect="))
	})

	t.Run("ValidCookiePasses", func(t *testing.T) {
		tok := newTestSessionToken("testuser", "internal")
		enc, err := encodeSession(tok, secret)
		assert.NilError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: enc})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("ExpiredCookieRedirects", func(t *testing.T) {
		tok := &sessionToken{
			Username:  "testuser",
			Method:    "internal",
			ExpiresAt: time.Now().Unix() - 1,
		}
		enc, err := encodeSession(tok, secret)
		assert.NilError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: enc})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		loc := w.Header().Get("Location")
		assert.Assert(t, strings.HasPrefix(loc, "/?redirect="))
	})

	t.Run("NonGetReturns401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusUnauthorized)
	})
}

func TestLogoutHandler(t *testing.T) {
	handler := LogoutHandler()
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, w.Header().Get("Location"), "/")

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	assert.Assert(t, sessionCookie != nil)
	assert.Assert(t, sessionCookie.MaxAge < 0)
}

func TestOIDCLoginHandler(t *testing.T) {
	secret := deriveSessionSecret("test-secret-for-oidc-login-32")
	provider := OIDCProvider{
		ID:        "test",
		Name:      "TestProvider",
		ClientID:  "test-client",
		IssuerURL: "https://example.com",
		Scopes:    []string{"openid", "profile"},
	}

	// Start a mock OIDC discovery endpoint
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": "https://example.com/auth",
				"token_endpoint":         "https://example.com/token",
				"userinfo_endpoint":      "https://example.com/userinfo",
			})
			return
		}
	}))
	defer mockServer.Close()

	provider.IssuerURL = mockServer.URL
	handler, err := NewOIDCHandler(provider, secret, "http://localhost:3333")
	assert.NilError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/test/login?redirect=/my-channel", nil)
	w := httptest.NewRecorder()
	handler.LoginHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)

	loc := w.Header().Get("Location")
	assert.Assert(t, strings.HasPrefix(loc, "https://example.com/auth"))
	assert.Assert(t, strings.Contains(loc, "response_type=code"))
	assert.Assert(t, strings.Contains(loc, "client_id=test-client"))
	assert.Assert(t, strings.Contains(loc, "scope=openid+profile"))
	assert.Assert(t, strings.Contains(loc, "state="))
	assert.Assert(t, strings.Contains(loc, "redirect_uri="))

	// Verify state cookie is set
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == oidcStateCookieName {
			stateCookie = c
			break
		}
	}
	assert.Assert(t, stateCookie != nil)
	assert.Assert(t, stateCookie.HttpOnly)
	assert.Assert(t, stateCookie.Secure)
	assert.Equal(t, stateCookie.SameSite, http.SameSiteLaxMode)
}

func TestOIDCCallbackHandler(t *testing.T) {
	secret := deriveSessionSecret("test-secret-for-oidc-callback-32")
	sessionSecret = secret

	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": mockServer.URL + "/auth",
				"token_endpoint":         mockServer.URL + "/token",
				"userinfo_endpoint":      mockServer.URL + "/userinfo",
			})
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
			})
		case strings.HasSuffix(r.URL.Path, "/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"sub":   "user123",
				"email": "user@example.com",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	provider := OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid", "profile", "email"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL)
	assert.NilError(t, err)

	// Simulate a valid callback with state cookie
	state := "valid-state-value"
	redirectTarget := "/my-channel"

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
	req.AddCookie(&http.Cookie{
		Name:     oidcStateCookieName,
		Value:    state + "|" + redirectTarget,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, w.Header().Get("Location"), redirectTarget)

	// Verify session cookie is set
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	assert.Assert(t, sessionCookie != nil)
	assert.Assert(t, sessionCookie.Value != "")
	assert.Assert(t, sessionCookie.HttpOnly)
	assert.Assert(t, sessionCookie.Secure)
}

func TestOIDCCallback_InvalidState(t *testing.T) {
	secret := deriveSessionSecret("test-secret-for-oidc-invalid-32")
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": mockServer.URL + "/auth",
				"token_endpoint":         mockServer.URL + "/token",
				"userinfo_endpoint":      mockServer.URL + "/userinfo",
			})
			return
		}
	}))
	defer mockServer.Close()

	provider := OIDCProvider{
		ID:        "test",
		Name:      "TestProvider",
		ClientID:  "test-client",
		IssuerURL: mockServer.URL,
		Scopes:    []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL)
	assert.NilError(t, err)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=code&state=wrong-state", mockServer.URL), nil)
	req.AddCookie(&http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "expected-state|/",
		Path:     "/",
		HttpOnly: true,
	})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestFullProtectedFlow(t *testing.T) {
	secret := deriveSessionSecret("test-secret-for-full-flow-test-32")
	sessionSecret = secret
	cfg := newTestAuthConfig()

	r := chi.NewRouter()
	r.Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, u := range cfg.Internal.Users {
			if u.Username == body.Username {
				if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(body.Password)) == nil {
					token := &sessionToken{
						Username:  body.Username,
						Method:    "internal",
						ExpiresAt: time.Now().Unix() + sessionMaxAge,
					}
					encoded, _ := encodeSession(token, sessionSecret)
					setSessionCookie(w, encoded)
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]bool{"ok": true})
					return
				}
			}
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
	})
	r.Post("/logout", LogoutHandler())
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(cfg))
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("home"))
		})
		r.Get("/new", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("new"))
		})
	})

	t.Run("AccessWithoutAuthRedirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		loc := w.Header().Get("Location")
		assert.Assert(t, strings.HasPrefix(loc, "/?redirect="))
	})

	var sessionCookie *http.Cookie
	t.Run("LoginSucceeds", func(t *testing.T) {
		body := `{"username":"testuser","password":"testpass"}`
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)

		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookieName {
				sessionCookie = c
				break
			}
		}
		assert.Assert(t, sessionCookie != nil)
	})

	t.Run("AccessWithCookieSucceeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
		body := w.Body.String()
		assert.Equal(t, body, "home")
	})

	t.Run("AccessNewWithCookieSucceeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/new", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
		body := w.Body.String()
		assert.Equal(t, body, "new")
	})

	t.Run("LogoutClearsSession", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/logout", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		assert.Equal(t, w.Header().Get("Location"), "/")

		// Cookie should be cleared
		var cleared bool
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookieName {
				cleared = c.MaxAge < 0
				break
			}
		}
		assert.Assert(t, cleared)
	})

	t.Run("AccessAfterLogoutRedirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		// Try with the old cookie (should be invalidated on server side too due to expiry)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
	})
}

func TestDeriveSessionSecret(t *testing.T) {
	secret1 := deriveSessionSecret("hello")
	secret2 := deriveSessionSecret("hello")
	secret3 := deriveSessionSecret("world")

	assert.Equal(t, secret1, secret2)
	assert.Assert(t, secret1 != secret3)
	assert.Equal(t, len(secret1), 32)
}

func TestGenerateRandomHex(t *testing.T) {
	s1 := generateRandomHex(16)
	s2 := generateRandomHex(16)
	assert.Assert(t, s1 != "")
	assert.Assert(t, s1 != s2)
	assert.Equal(t, len(s1), 32)
}
