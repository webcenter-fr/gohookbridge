package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// fakeOIDCResolver maps OIDC subjects to linked users.
type fakeOIDCResolver struct {
	linked map[string]*domain.User
}

func (f *fakeOIDCResolver) GetUserByExternalIdentity(_ context.Context, _, _, subject string) (*domain.User, error) {
	if u, ok := f.linked[subject]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeOIDCResolver) GetAuthEpoch(_ context.Context) int64 { return 0 }

// F03: OIDC login resolves strictly by the bound (issuer, subject); an
// unlinked subject is rejected rather than auto-linked or matched by email.
func TestOIDCCollisionRejected(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-collision-32")

	mock := newOIDCMock(t)
	nonce := "collision-nonce"
	mock.tokenFn = func() map[string]any {
		return map[string]any{
			"access_token": "mock-access-token",
			"id_token":     mock.signIDToken(mock.idTokenClaims(nonce)),
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
		Scopes:       []string{"openid", "email"},
	}

	callback := func(resolver OIDCPrincipalResolver) *httptest.ResponseRecorder {
		handler, err := NewOIDCHandler(provider, secret, mockServer.URL, true)
		assert.NilError(t, err)
		handler.Resolver = resolver

		state := "collision-state"
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			fmt.Sprintf("%s/auth/oidc/test/callback?code=c&state=%s", mockServer.URL, state), nil)
		req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: state + "|/", Path: "/", HttpOnly: true})
		req.AddCookie(&http.Cookie{Name: oidcNonceCookieName, Value: nonce, Path: "/", HttpOnly: true})
		w := httptest.NewRecorder()
		handler.CallbackHandler().ServeHTTP(w, req)
		return w
	}

	t.Run("unlinked subject rejected", func(t *testing.T) {
		w := callback(&fakeOIDCResolver{linked: map[string]*domain.User{}})
		assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
		assert.Assert(t, cookieByName(t, w, sessionCookieName) == nil)
	})

	t.Run("linked subject accepted", func(t *testing.T) {
		w := callback(&fakeOIDCResolver{linked: map[string]*domain.User{
			"user123": {ID: "u1", Username: "linked-user"},
		}})
		assert.Equal(t, w.Code, http.StatusFound, "body: %s", w.Body.String())
		sessionCookie := cookieByName(t, w, sessionCookieName)
		assert.Assert(t, sessionCookie != nil)
		tok, err := service.DecodeSession(sessionCookie.Value, secret)
		assert.NilError(t, err)
		assert.Equal(t, tok.UserID, "u1")
		assert.Equal(t, tok.Username, "linked-user")
		assert.Equal(t, tok.Subject, "user123")
		assert.Equal(t, tok.Method, "oidc")
	})
}

// F22: the OIDC client is bounded and honors an endpoint allowlist; plain-http
// non-loopback issuers are rejected.
func TestOIDCClientBoundsAndAllowlist(t *testing.T) {
	secret := service.DeriveSessionSecret("test-secret-for-oidc-bounds-32")

	t.Run("http non-loopback issuer rejected", func(t *testing.T) {
		_, err := NewOIDCHandler(domain.OIDCProvider{
			ID: "test", ClientID: "c", IssuerURL: "http://example.com",
		}, secret, "http://localhost:3333", true)
		assert.Assert(t, err != nil)
	})

	mock := newOIDCMock(t)
	mockServer := mock.start()
	defer mockServer.Close()

	base := domain.OIDCProvider{
		ID: "test", Name: "TestProvider", ClientID: "test-client",
		ClientSecret: "s", IssuerURL: mockServer.URL, Scopes: []string{"openid"},
	}

	t.Run("disallowed endpoint rejected", func(t *testing.T) {
		p := base
		p.AllowedEndpoints = []string{"https://evil.example/token"}
		_, err := NewOIDCHandler(p, secret, mockServer.URL, true)
		assert.Assert(t, err != nil)
	})

	t.Run("allowed endpoints accepted", func(t *testing.T) {
		p := base
		p.AllowedEndpoints = []string{mockServer.URL + "/token", mockServer.URL + "/userinfo"}
		_, err := NewOIDCHandler(p, secret, mockServer.URL, true)
		assert.NilError(t, err)
	})
}
