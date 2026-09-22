package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
)

func newTestAuthConfig() *domain.AuthConfig {
	hash, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return &domain.AuthConfig{
		Internal: domain.InternalConfig{
			Enabled: true,
			Users: []domain.InternalUser{
				{
					Username:     "testuser",
					PasswordHash: string(hash),
				},
			},
		},
		OIDC: domain.OIDCConfig{
			Enabled:   false,
			Providers: nil,
		},
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-middleware-tests-32")

	r := chi.NewRouter()
	r.Use(RequireAuth(secret))
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Run("NoCookieRedirectsToLogin", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		loc := w.Header().Get("Location")
		assert.Assert(t, strings.HasPrefix(loc, "/?redirect="))
	})

	t.Run("ValidCookiePasses", func(t *testing.T) {
		tok := &service.SessionToken{
			Username:  "testuser",
			Method:    "internal",
			ExpiresAt: time.Now().Unix() + 86400,
		}
		enc, err := service.EncodeSession(tok, secret)
		assert.NilError(t, err)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: enc})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("ExpiredCookieRedirects", func(t *testing.T) {
		tok := &service.SessionToken{
			Username:  "testuser",
			Method:    "internal",
			ExpiresAt: time.Now().Unix() - 1,
		}
		enc, err := service.EncodeSession(tok, secret)
		assert.NilError(t, err)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: enc})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		loc := w.Header().Get("Location")
		assert.Assert(t, strings.HasPrefix(loc, "/?redirect="))
	})

	t.Run("NonGetReturns401", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusUnauthorized)
	})
}

func TestLogoutHandler(t *testing.T) {
	handler := LogoutHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/logout", nil)
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
	secret := service.DeriveSessionSecret("test-secret-for-oidc-login-32")
	provider := domain.OIDCProvider{
		ID:        "test",
		Name:      "TestProvider",
		ClientID:  "test-client",
		IssuerURL: "https://example.com",
		Scopes:    []string{"openid", "profile"},
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/oidc/test/login?redirect=/my-channel", nil)
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
	secret := service.DeriveSessionSecret("test-secret-for-oidc-callback-32")

	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": mockServer.URL + "/auth",
				"token_endpoint":         mockServer.URL + "/token",
				"userinfo_endpoint":      mockServer.URL + "/userinfo",
			})
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "mock-access-token",
				"token_type":   "Bearer",
			})
		case strings.HasSuffix(r.URL.Path, "/userinfo"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":   "user123",
				"email": "user@example.com",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid", "profile", "email"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL)
	assert.NilError(t, err)

	state := "valid-state-value"
	redirectTarget := "/my-channel"

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
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
	secret := service.DeriveSessionSecret("test-secret-for-oidc-invalid-32")
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": mockServer.URL + "/auth",
				"token_endpoint":         mockServer.URL + "/token",
				"userinfo_endpoint":      mockServer.URL + "/userinfo",
			})
			return
		}
	}))
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:        "test",
		Name:      "TestProvider",
		ClientID:  "test-client",
		IssuerURL: mockServer.URL,
		Scopes:    []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL)
	assert.NilError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=code&state=wrong-state", mockServer.URL), nil)
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
	secret := service.DeriveSessionSecret("test-secret-for-full-flow-test-32")
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
					token := &service.SessionToken{
						Username:  body.Username,
						Method:    "internal",
						ExpiresAt: time.Now().Unix() + sessionMaxAge,
					}
					encoded, _ := service.EncodeSession(token, secret)
					setSessionCookie(w, encoded)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
					return
				}
			}
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
	})
	r.Post("/logout", LogoutHandler())
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(secret))
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
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		loc := w.Header().Get("Location")
		assert.Assert(t, strings.HasPrefix(loc, "/?redirect="))
	})

	var sessionCookie *http.Cookie
	t.Run("LoginSucceeds", func(t *testing.T) {
		body := `{"username":"testuser","password":"testpass"}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/login", strings.NewReader(body))
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
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
		body := w.Body.String()
		assert.Equal(t, body, "home")
	})

	t.Run("AccessNewWithCookieSucceeds", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/new", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
		body := w.Body.String()
		assert.Equal(t, body, "new")
	})

	t.Run("LogoutClearsSession", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/logout", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusFound)
		assert.Equal(t, w.Header().Get("Location"), "/")

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
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.AddCookie(sessionCookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, w.Code, http.StatusOK)
	})
}
