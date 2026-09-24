package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
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
	r.Use(RequireAuth(secret, true))
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

func cookieByName(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSessionCookieSecureFlag(t *testing.T) {
	for _, secure := range []bool{true, false} {
		t.Run(fmt.Sprintf("secure=%v", secure), func(t *testing.T) {
			w := httptest.NewRecorder()
			setSessionCookie(w, "token", secure)
			c := cookieByName(t, w, sessionCookieName)
			assert.Assert(t, c != nil)
			assert.Equal(t, c.Secure, secure)
			assert.Assert(t, c.HttpOnly)
		})
	}
}

func TestClearSessionCookieSecureFlag(t *testing.T) {
	for _, secure := range []bool{true, false} {
		t.Run(fmt.Sprintf("secure=%v", secure), func(t *testing.T) {
			w := httptest.NewRecorder()
			clearSessionCookie(w, secure)
			c := cookieByName(t, w, sessionCookieName)
			assert.Assert(t, c != nil)
			assert.Equal(t, c.Secure, secure)
			assert.Assert(t, c.MaxAge < 0)
		})
	}
}

func TestOIDCStateCookieSecureFlag(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-secure-flag-32")
	provider := domain.OIDCProvider{
		ID:       "test",
		Name:     "TestProvider",
		ClientID: "test-client",
		Scopes:   []string{"openid"},
	}

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()
	provider.IssuerURL = mockServer.URL

	for _, secure := range []bool{true, false} {
		t.Run(fmt.Sprintf("secure=%v", secure), func(t *testing.T) {
			handler, err := NewOIDCHandler(provider, secret, "http://localhost:3333", secure)
			assert.NilError(t, err)

			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/oidc/test/login", nil)
			handler.LoginHandler().ServeHTTP(w, req)

			stateCookie := cookieByName(t, w, oidcStateCookieName)
			assert.Assert(t, stateCookie != nil)
			assert.Equal(t, stateCookie.Secure, secure)
			assert.Assert(t, stateCookie.HttpOnly)

			nonceCookie := cookieByName(t, w, oidcNonceCookieName)
			assert.Assert(t, nonceCookie != nil)
			assert.Equal(t, nonceCookie.Secure, secure)
			assert.Assert(t, nonceCookie.HttpOnly)
		})
	}
}

func TestLogoutHandler(t *testing.T) {
	handler := LogoutHandler(true)
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

// oidcMock is a composite OIDC test provider: it serves a full discovery
// document, a JWKS endpoint (via oidctest), plus configurable token and
// userinfo endpoints. ID tokens are signed with oidcMock.signIDToken.
type oidcMock struct {
	t          *testing.T
	priv       *rsa.PrivateKey
	keyID      string
	issuerURL  string
	tokenFn    func() map[string]any
	userinfo   map[string]any
	onUserinfo func()
}

func newOIDCMock(t *testing.T) *oidcMock {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err)
	return &oidcMock{
		t:        t,
		priv:     priv,
		keyID:    "test-key",
		userinfo: map[string]any{"sub": "user123", "email": "user@example.com"},
	}
}

func (m *oidcMock) signIDToken(claims map[string]any) string {
	m.t.Helper()
	raw, err := json.Marshal(claims)
	assert.NilError(m.t, err)
	return oidctest.SignIDToken(m.priv, m.keyID, oidc.RS256, string(raw))
}

// idTokenClaims builds standard claims (iss, aud, sub, email, exp, nonce) for
// the given nonce value.
func (m *oidcMock) idTokenClaims(nonce string) map[string]any {
	return map[string]any{
		"iss":   m.issuerURL,
		"aud":   "test-client",
		"sub":   "user123",
		"email": "user@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"nonce": nonce,
	}
}

func (m *oidcMock) start() *httptest.Server {
	m.t.Helper()
	oidcSrv := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{
			{PublicKey: m.priv.Public(), KeyID: m.keyID, Algorithm: oidc.RS256},
		},
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/auth",
				"token_endpoint":         srv.URL + "/token",
				"userinfo_endpoint":      srv.URL + "/userinfo",
				"jwks_uri":               srv.URL + "/keys",
			})
		case "/keys":
			oidcSrv.ServeHTTP(w, r)
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{"access_token": "mock-access-token", "token_type": "Bearer"}
			if m.tokenFn != nil {
				resp = m.tokenFn()
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/userinfo":
			if m.onUserinfo != nil {
				m.onUserinfo()
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m.userinfo)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	m.issuerURL = srv.URL
	return srv
}

func TestOIDCLoginHandler(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-login-32")
	provider := domain.OIDCProvider{
		ID:       "test",
		Name:     "TestProvider",
		ClientID: "test-client",
		Scopes:   []string{"openid", "profile"},
	}

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()

	provider.IssuerURL = mockServer.URL
	handler, err := NewOIDCHandler(provider, secret, "http://localhost:3333", true)
	assert.NilError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/oidc/test/login?redirect=/my-channel", nil)
	w := httptest.NewRecorder()
	handler.LoginHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)

	loc := w.Header().Get("Location")
	assert.Assert(t, strings.HasPrefix(loc, mockServer.URL+"/auth"))
	assert.Assert(t, strings.Contains(loc, "response_type=code"))
	assert.Assert(t, strings.Contains(loc, "client_id=test-client"))
	assert.Assert(t, strings.Contains(loc, "scope=openid+profile"))
	assert.Assert(t, strings.Contains(loc, "state="))
	assert.Assert(t, strings.Contains(loc, "nonce="))
	assert.Assert(t, strings.Contains(loc, "redirect_uri="))

	cookies := w.Result().Cookies()
	var stateCookie, nonceCookie *http.Cookie
	for _, c := range cookies {
		switch c.Name {
		case oidcStateCookieName:
			stateCookie = c
		case oidcNonceCookieName:
			nonceCookie = c
		}
	}
	assert.Assert(t, stateCookie != nil)
	assert.Assert(t, stateCookie.HttpOnly)
	assert.Assert(t, stateCookie.Secure)
	assert.Equal(t, stateCookie.SameSite, http.SameSiteLaxMode)
	assert.Assert(t, nonceCookie != nil)
	assert.Assert(t, nonceCookie.HttpOnly)
	assert.Assert(t, nonceCookie.Secure)
	assert.Equal(t, nonceCookie.SameSite, http.SameSiteLaxMode)
	assert.Equal(t, nonceCookie.MaxAge, 300)
}

func TestOIDCCallbackHandler(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-callback-32")

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid", "profile", "email"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
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

func TestOIDCCallback_IDTokenVerified(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-idtoken-32")

	mock := newOIDCMock(t)
	nonce := "test-nonce-value"
	mock.tokenFn = func() map[string]any {
		return map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"id_token":     mock.signIDToken(mock.idTokenClaims(nonce)),
		}
	}
	userinfoCalls := 0
	mock.onUserinfo = func() { userinfoCalls++ }
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
	assert.NilError(t, err)

	state := "valid-state-value"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state + "|/", Path: "/", HttpOnly: true})
	req.AddCookie(&http.Cookie{Name: oidcNonceCookieName, Value: nonce, Path: "/", HttpOnly: true})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)

	sessionCookie := cookieByName(t, w, sessionCookieName)
	assert.Assert(t, sessionCookie != nil)
	assert.Assert(t, sessionCookie.Value != "")

	// The session must be built from the ID token claims, without a userinfo
	// call.
	tok, err := service.DecodeSession(sessionCookie.Value, secret)
	assert.NilError(t, err)
	assert.Equal(t, tok.Username, "user@example.com")
	assert.Equal(t, userinfoCalls, 0, "id_token flow must not call userinfo")
}

func TestOIDCCallback_IDTokenNonceMismatch(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-nonce-mm-32")

	mock := newOIDCMock(t)
	mock.tokenFn = func() map[string]any {
		return map[string]any{
			"access_token": "mock-access-token",
			"id_token":     mock.signIDToken(mock.idTokenClaims("attacker-nonce")),
		}
	}
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
	assert.NilError(t, err)

	state := "valid-state-value"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state + "|/", Path: "/", HttpOnly: true})
	req.AddCookie(&http.Cookie{Name: oidcNonceCookieName, Value: "expected-nonce", Path: "/", HttpOnly: true})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusBadRequest, "nonce mismatch must be a hard reject (no userinfo fallback)")

	assert.Assert(t, cookieByName(t, w, sessionCookieName) == nil, "no session cookie on nonce mismatch")
}

func TestOIDCCallback_IDTokenMissingNonceCookie(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-nonce-mc-32")

	mock := newOIDCMock(t)
	mock.tokenFn = func() map[string]any {
		return map[string]any{
			"access_token": "mock-access-token",
			"id_token":     mock.signIDToken(mock.idTokenClaims("whatever")),
		}
	}
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
	assert.NilError(t, err)

	state := "valid-state-value"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state + "|/", Path: "/", HttpOnly: true})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusBadRequest, "id_token without nonce cookie must be rejected")
}

func TestOIDCCallback_TamperedIDTokenRejected(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-tamper-32")

	mock := newOIDCMock(t)
	mock.tokenFn = func() map[string]any {
		return map[string]any{
			"access_token": "mock-access-token",
			"id_token":     mock.signIDToken(mock.idTokenClaims("expected-nonce")) + "tampered",
		}
	}
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
	assert.NilError(t, err)

	state := "valid-state-value"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state + "|/", Path: "/", HttpOnly: true})
	req.AddCookie(&http.Cookie{Name: oidcNonceCookieName, Value: "expected-nonce", Path: "/", HttpOnly: true})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusBadRequest, "tampered id_token must be rejected")
	assert.Assert(t, cookieByName(t, w, sessionCookieName) == nil)
}

func TestOIDCCallback_InvalidState(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-invalid-32")

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:        "test",
		Name:      "TestProvider",
		ClientID:  "test-client",
		IssuerURL: mockServer.URL,
		Scopes:    []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
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

func TestSafeRedirectPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", "/"},
		{"relative", "/channels/foo", "/channels/foo"},
		{"query", "/channels/foo?tab=1", "/channels/foo?tab=1"},
		{"absoluteURL", "https://evil.example.com/phish", "/"},
		{"protocolRelative", "//evil.example.com/phish", "/"},
		{"backslash", "/\\evil.example.com", "/"},
		{"crlf", "/safe\r\nLocation: x", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, safeRedirectPath(tt.input), tt.expected)
		})
	}
}

func TestOIDCLoginHandler_RejectsExternalRedirect(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-redirect-32")
	provider := domain.OIDCProvider{
		ID:       "test",
		Name:     "TestProvider",
		ClientID: "test-client",
		Scopes:   []string{"openid"},
	}

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()

	provider.IssuerURL = mockServer.URL
	handler, err := NewOIDCHandler(provider, secret, "http://localhost:3333", true)
	assert.NilError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/oidc/test/login?redirect=https%3A%2F%2Fevil.example.com%2Fphish", nil)
	w := httptest.NewRecorder()
	handler.LoginHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)

	// The state cookie must carry the sanitized redirect target, never the
	// attacker-controlled external URL.
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == oidcStateCookieName {
			stateCookie = c
			break
		}
	}
	assert.Assert(t, stateCookie != nil)
	parts := strings.SplitN(stateCookie.Value, "|", 2)
	assert.Equal(t, len(parts), 2)
	assert.Equal(t, parts[1], "/")
}

func TestOIDCCallbackHandler_RejectsExternalRedirect(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-cb-redirect-32")

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()

	provider := domain.OIDCProvider{
		ID:           "test",
		Name:         "TestProvider",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		IssuerURL:    mockServer.URL,
		Scopes:       []string{"openid"},
	}

	handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
	assert.NilError(t, err)

	state := "valid-state-value"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("%s/auth/oidc/test/callback?code=valid-code&state=%s", mockServer.URL, state), nil)
	req.AddCookie(&http.Cookie{
		Name:     oidcStateCookieName,
		Value:    state + "|https://evil.example.com/phish",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()
	handler.CallbackHandler().ServeHTTP(w, req)
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, w.Header().Get("Location"), "/")
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
					setSessionCookie(w, encoded, true)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
					return
				}
			}
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
	})
	r.Post("/logout", LogoutHandler(true))
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(secret, true))
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
