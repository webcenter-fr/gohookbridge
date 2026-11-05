package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

// setupAPIForActor provisions a router authenticated as actor with a small
// RBAC fixture: a full admin, a delegated users:write manager, and a viewer.
func setupAPIForActor(t *testing.T, actor string) (*service.Service, *chi.Mux) {
	t.Helper()
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	assert.NilError(t, svc.CreateRole(ctx, domain.Role{
		Name:        "user_manager",
		Permissions: []string{"users:read", "users:write", "channel:read"},
	}))
	for _, u := range []*domain.User{
		{ID: "admin", Username: "admin", Roles: []string{"admin"}, Channels: []string{"*"}},
		{ID: "delegated", Username: "delegated", Roles: []string{"user_manager"}, Channels: []string{"*"}},
		{ID: "viewer", Username: "viewer", Roles: []string{"channel_viewer"}, Channels: []string{"chan-a"}},
	} {
		assert.NilError(t, svc.CreateUser(ctx, u))
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			//nolint:revive,staticcheck
			c := context.WithValue(req.Context(), UsernameContextKey, actor)
			next.ServeHTTP(w, req.WithContext(c))
		})
	})
	RegisterAPIHandlers(r, svc)
	return svc, r
}

// F01: user mutations require users:write, not merely users:read.
func TestUserMutationsRequireWritePermission(t *testing.T) {
	_, router := setupAPIForActor(t, "viewer")

	tests := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/users", map[string]any{"username": "x", "password": "pw"}},
		{"PUT", "/users/viewer", map[string]any{"roles": []string{}}},
		{"DELETE", "/users/viewer", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, apiRequest(tt.method, tt.path, tt.body))
			assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
		})
	}
}

// F01: a delegated users:write manager may not grant a role it does not hold.
func TestCreateUserRejectsElevation(t *testing.T) {
	_, router := setupAPIForActor(t, "delegated")

	t.Run("cannot create admin", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("POST", "/users", map[string]any{
			"username": "attacker", "password": "pw", "roles": []string{"admin"},
		}))
		assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
	})

	t.Run("can create viewer", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("POST", "/users", map[string]any{
			"username": "newbie", "password": "pw", "roles": []string{"channel_viewer"},
		}))
		assert.Equal(t, w.Code, http.StatusCreated, "body: %s", w.Body.String())
	})
}

// F01: a delegated admin cannot self-promote to a wildcard role.
func TestDelegatedAdminCannotEscalate(t *testing.T) {
	_, router := setupAPIForActor(t, "delegated")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/users/delegated", map[string]any{
		"roles": []string{"admin"},
	}))
	assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
}

// F02: a delegated user manager must not grant channel membership outside its
// own channel scope (e.g. wildcard or a channel it cannot reach).
func TestDelegatedAdminCannotEscalateChannels(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	assert.NilError(t, svc.CreateRole(ctx, domain.Role{
		Name:        "user_manager",
		Permissions: []string{"users:read", "users:write", "channel:read"},
	}))
	for _, u := range []*domain.User{
		{ID: "admin", Username: "admin", Roles: []string{"admin"}, Channels: []string{"*"}},
		// Delegated manager scoped to chan-a only.
		{ID: "delegated", Username: "delegated", Roles: []string{"user_manager"}, Channels: []string{"chan-a"}},
	} {
		assert.NilError(t, svc.CreateUser(ctx, u))
	}
	assert.NilError(t, svc.CreateChannel(ctx, &domain.Channel{ID: "chan-a", CreatedBy: "admin"}))

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			//nolint:revive,staticcheck
			c := context.WithValue(req.Context(), UsernameContextKey, "delegated")
			next.ServeHTTP(w, req.WithContext(c))
		})
	})
	RegisterAPIHandlers(r, svc)

	t.Run("cannot grant wildcard channels", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, apiRequest("POST", "/users", map[string]any{
			"username": "attacker", "password": "pw", "roles": []string{"channel_viewer"}, "channels": []string{"*"},
		}))
		assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
	})

	t.Run("cannot grant channels outside own scope", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, apiRequest("POST", "/users", map[string]any{
			"username": "attacker2", "password": "pw", "roles": []string{"channel_viewer"}, "channels": []string{"chan-b"},
		}))
		assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
	})

	t.Run("can grant channels within own scope", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, apiRequest("POST", "/users", map[string]any{
			"username": "newbie", "password": "pw", "roles": []string{"channel_viewer"}, "channels": []string{"chan-a"},
		}))
		assert.Equal(t, w.Code, http.StatusCreated, "body: %s", w.Body.String())
	})
}

func TestDelegatedAdminCannotEscalateChannelsViaUpdate(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	assert.NilError(t, svc.CreateRole(ctx, domain.Role{
		Name:        "user_manager",
		Permissions: []string{"users:read", "users:write", "channel:read"},
	}))
	for _, u := range []*domain.User{
		{ID: "admin", Username: "admin", Roles: []string{"admin"}, Channels: []string{"*"}},
		{ID: "delegated", Username: "delegated", Roles: []string{"user_manager"}, Channels: []string{"chan-a"}},
		{ID: "target", Username: "target", Roles: []string{"channel_viewer"}, Channels: []string{"chan-a"}},
	} {
		assert.NilError(t, svc.CreateUser(ctx, u))
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			//nolint:revive,staticcheck
			c := context.WithValue(req.Context(), UsernameContextKey, "delegated")
			next.ServeHTTP(w, req.WithContext(c))
		})
	})
	RegisterAPIHandlers(r, svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, apiRequest("PUT", "/users/target", map[string]any{"channels": []string{"*"}}))
	assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())

	w = httptest.NewRecorder()
	r.ServeHTTP(w, apiRequest("PUT", "/rbac/bindings/target", map[string]any{"roles": []string{"channel_viewer"}, "channels": []string{"*"}}))
	assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
}

// F02: object authorization is scoped to the channel named in the URL; a
// viewer of chan-a may not read or write chan-b.
func TestChannelObjectAuthMatrix(t *testing.T) {
	svc, router := setupAPIForActor(t, "viewer")
	assert.NilError(t, svc.CreateChannel(context.Background(), &domain.Channel{ID: "chan-a", CreatedBy: "admin"}))

	t.Run("own channel read allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("GET", "/channels/chan-a", nil))
		assert.Equal(t, w.Code, http.StatusOK, "body: %s", w.Body.String())
	})

	t.Run("other channel read forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("GET", "/channels/chan-b", nil))
		assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
	})

	t.Run("other channel write forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("PUT", "/channels/chan-b", map[string]any{"description": "x"}))
		assert.Equal(t, w.Code, http.StatusForbidden, "body: %s", w.Body.String())
	})
}
