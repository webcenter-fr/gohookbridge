package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/handler"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// newOIDCIssuer starts a minimal OIDC discovery + JWKS server so
// handler.NewOIDCHandler succeeds without network access.
func newOIDCIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err)
	oidcSrv := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{PublicKey: priv.Public(), KeyID: "k", Algorithm: oidc.RS256}},
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
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// requestWithProvider builds a request with the chi {provider} URL param set.
func requestWithProvider(id string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/oidc/"+id+"/login", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestOIDCRouteResolver_UnknownProvider(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	resolver := newOIDCRouteResolver(svc, [32]byte{}, "http://localhost", false)

	w := httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("missing"))

	assert.Equal(t, w.Code, http.StatusNotFound)
	assert.Equal(t, w.Header().Get("Content-Type"), "application/json")
}

func TestOIDCRouteResolver_DelegatesAndCaches(t *testing.T) {
	issuer := newOIDCIssuer(t)
	rs := storetest.NewRaftStore(t)
	svc := service.NewService(rs, nil)
	ctx := context.Background()
	assert.NilError(t, svc.SetOIDCProviders(ctx, []domain.OIDCProvider{{
		ID: "google", Name: "Google", ClientID: "cid", ClientSecret: "sec", IssuerURL: issuer.URL,
	}}))

	resolver := newOIDCRouteResolver(svc, [32]byte{}, issuer.URL, false)

	var constructions int32
	resolver.newHandler = func(p domain.OIDCProvider, s [32]byte, u string, c bool) (*handler.OIDCHandler, error) {
		atomic.AddInt32(&constructions, 1)
		return handler.NewOIDCHandler(p, s, u, c)
	}

	// First call builds and delegates (login redirects to the issuer).
	w := httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("google"))
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, int32(1), atomic.LoadInt32(&constructions))

	// Second call reuses the cached handler.
	w = httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("google"))
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, int32(1), atomic.LoadInt32(&constructions), "cache hit must not rebuild")

	// Changing the provider config rebuilds the handler.
	assert.NilError(t, svc.SetOIDCProviders(ctx, []domain.OIDCProvider{{
		ID: "google", Name: "Google Renamed", ClientID: "cid", ClientSecret: "sec", IssuerURL: issuer.URL,
	}}))
	w = httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("google"))
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, int32(2), atomic.LoadInt32(&constructions), "fingerprint change must rebuild")
}

func TestOIDCRouteResolver_EvictsRemovedProviders(t *testing.T) {
	issuer := newOIDCIssuer(t)
	rs := storetest.NewRaftStore(t)
	svc := service.NewService(rs, nil)
	ctx := context.Background()
	assert.NilError(t, svc.SetOIDCProviders(ctx, []domain.OIDCProvider{{
		ID: "google", Name: "Google", ClientID: "cid", ClientSecret: "sec", IssuerURL: issuer.URL,
	}}))

	resolver := newOIDCRouteResolver(svc, [32]byte{}, issuer.URL, false)

	w := httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("google"))
	assert.Equal(t, w.Code, http.StatusFound)
	assert.Equal(t, 1, len(resolver.cache))

	// Removing the provider evicts its cache entry on the next resolve.
	assert.NilError(t, svc.SetOIDCProviders(ctx, []domain.OIDCProvider{}))
	w = httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("google"))
	assert.Equal(t, w.Code, http.StatusNotFound)
	assert.Equal(t, 0, len(resolver.cache))
}

func TestOIDCRouteResolver_DiscoveryFailure(t *testing.T) {
	rs := storetest.NewRaftStore(t)
	svc := service.NewService(rs, nil)
	ctx := context.Background()
	// An https issuer that does not resolve discovery fails fast.
	assert.NilError(t, svc.SetOIDCProviders(ctx, []domain.OIDCProvider{{
		ID: "broken", Name: "Broken", ClientID: "cid", ClientSecret: "sec", IssuerURL: "https://127.0.0.1:1",
	}}))

	resolver := newOIDCRouteResolver(svc, [32]byte{}, "http://localhost", false)
	w := httptest.NewRecorder()
	resolver.LoginHandler().ServeHTTP(w, requestWithProvider("broken"))
	assert.Equal(t, w.Code, http.StatusBadGateway)
}
