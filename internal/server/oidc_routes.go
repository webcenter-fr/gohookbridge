package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/handler"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

// oidcMaxCacheEntries bounds the resolver cache so a stream of unique provider
// IDs cannot grow it without limit.
const oidcMaxCacheEntries = 64

// oidcCacheEntry is a cached OIDC handler plus the fingerprint of the provider
// config it was built from.
type oidcCacheEntry struct {
	fingerprint string
	handler     *handler.OIDCHandler
}

// oidcRouteResolver resolves the OIDC provider for a request at runtime and
// delegates to a cached handler. Providers added or changed through the UI
// after boot therefore get working /login and /callback routes without a
// server restart.
type oidcRouteResolver struct {
	svc           *service.Service
	sessionSecret [32]byte
	publicURL     string
	cookieSecure  bool

	mu    sync.Mutex
	cache map[string]oidcCacheEntry

	// newHandler is injectable for tests; it defaults to handler.NewOIDCHandler.
	newHandler func(provider domain.OIDCProvider, sessionSecret [32]byte, publicURL string, cookieSecure bool) (*handler.OIDCHandler, error)
}

func newOIDCRouteResolver(svc *service.Service, sessionSecret [32]byte, publicURL string, cookieSecure bool) *oidcRouteResolver {
	return &oidcRouteResolver{
		svc:           svc,
		sessionSecret: sessionSecret,
		publicURL:     publicURL,
		cookieSecure:  cookieSecure,
		cache:         make(map[string]oidcCacheEntry),
		newHandler:    handler.NewOIDCHandler,
	}
}

// LoginHandler serves GET /auth/oidc/{provider}/login.
func (o *oidcRouteResolver) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := o.resolve(w, r)
		if !ok {
			return
		}
		h.LoginHandler().ServeHTTP(w, r)
	}
}

// CallbackHandler serves GET /auth/oidc/{provider}/callback.
func (o *oidcRouteResolver) CallbackHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := o.resolve(w, r)
		if !ok {
			return
		}
		h.CallbackHandler().ServeHTTP(w, r)
	}
}

// resolve loads the provider for the request, (re)builds its handler on a cache
// miss or fingerprint change, and returns it. It writes the error response and
// returns ok=false when resolution fails.
func (o *oidcRouteResolver) resolve(w http.ResponseWriter, r *http.Request) (*handler.OIDCHandler, bool) {
	id := chi.URLParam(r, "provider")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "provider id required")
		return nil, false
	}

	providers, err := o.svc.OIDCProviders(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "oidc providers unavailable")
		return nil, false
	}

	var provider *domain.OIDCProvider
	for i := range providers {
		if providers[i].ID == id {
			provider = &providers[i]
			break
		}
	}
	if provider == nil {
		o.mu.Lock()
		o.evictLocked(providers)
		o.mu.Unlock()
		writeJSONError(w, http.StatusNotFound, "oidc provider not found")
		return nil, false
	}

	fingerprint, err := providerFingerprint(*provider)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "oidc provider fingerprint failed")
		return nil, false
	}

	o.mu.Lock()
	if entry, ok := o.cache[id]; ok && entry.fingerprint == fingerprint {
		o.mu.Unlock()
		return entry.handler, true
	}
	o.mu.Unlock()

	oidcHandler, err := o.newHandler(*provider, o.sessionSecret, o.publicURL, o.cookieSecure)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "oidc provider discovery failed")
		return nil, false
	}
	oidcHandler.Resolver = o.svc

	o.mu.Lock()
	o.evictLocked(providers)
	o.cache[id] = oidcCacheEntry{fingerprint: fingerprint, handler: oidcHandler}
	o.mu.Unlock()

	return oidcHandler, true
}

// evictLocked drops cache entries whose provider is no longer configured and
// caps the cache size. Callers must hold o.mu.
func (o *oidcRouteResolver) evictLocked(providers []domain.OIDCProvider) {
	live := make(map[string]struct{}, len(providers))
	for i := range providers {
		live[providers[i].ID] = struct{}{}
	}
	for id := range o.cache {
		if _, ok := live[id]; !ok {
			delete(o.cache, id)
		}
	}
	for id := range o.cache {
		if len(o.cache) <= oidcMaxCacheEntries {
			break
		}
		delete(o.cache, id)
	}
}

// providerFingerprint returns a stable hash of the provider config so a changed
// provider rebuilds its handler.
func providerFingerprint(provider domain.OIDCProvider) (string, error) {
	data, err := json.Marshal(provider)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// writeJSONError writes `{"error": msg}` with the given status.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}
