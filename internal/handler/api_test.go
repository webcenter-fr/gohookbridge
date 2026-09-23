package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"gotest.tools/v3/assert"
)

func setupAPI(t *testing.T) (*service.Service, *chi.Mux) {
	t.Helper()
	rs := storetest.NewRaftStore(t)
	svc := service.NewService(rs, nil)
	ctx := context.Background()

	err := svc.CreateUser(ctx, &domain.User{
		ID:       "admin",
		Username: "admin",
		Roles:    []string{"admin"},
		Channels: []string{"*"},
	})
	assert.NilError(t, err)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:revive,staticcheck
			ctx := context.WithValue(r.Context(), UsernameContextKey, "admin")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	RegisterAPIHandlers(r, svc)
	return svc, r
}

func apiRequest(method, path string, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCreateChannel_ValidID(t *testing.T) {
	_, router := setupAPI(t)

	tests := []string{
		"my-channel",
		"test_hook",
		"a",
		"channel-123_test",
	}
	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": id}))
			assert.Equal(t, w.Code, http.StatusCreated, "expected 201 for id %q, got %d: %s", id, w.Code, w.Body.String())

			var ch domain.Channel
			err := json.Unmarshal(w.Body.Bytes(), &ch)
			assert.NilError(t, err)
			assert.Equal(t, ch.ID, id)
		})
	}
}

func TestCreateChannel_ValidID_63Char(t *testing.T) {
	_, router := setupAPI(t)
	id := strings.Repeat("a", 63)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": id}))
	assert.Equal(t, w.Code, http.StatusCreated)
}

func TestCreateChannel_InvalidID_Empty(t *testing.T) {
	_, router := setupAPI(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": ""}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
	assert.Assert(t, strings.Contains(w.Body.String(), "required"))
}

func TestCreateChannel_InvalidID_Spaces(t *testing.T) {
	_, router := setupAPI(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": "my channel"}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestCreateChannel_InvalidID_SpecialChars(t *testing.T) {
	_, router := setupAPI(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": "my@ch"}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestCreateChannel_InvalidID_StartsWithDash(t *testing.T) {
	_, router := setupAPI(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": "-chan"}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestCreateChannel_InvalidID_StartsWithUnderscore(t *testing.T) {
	_, router := setupAPI(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": "_test"}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestCreateChannel_InvalidID_TooLong(t *testing.T) {
	_, router := setupAPI(t)
	id := strings.Repeat("a", 65)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": id}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestCreateChannel_WithDescription(t *testing.T) {
	svc, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]any{
		"id":          "desc-channel",
		"description": "A test channel",
	}))
	assert.Equal(t, w.Code, http.StatusCreated)

	ch, err := svc.GetChannel(context.Background(), "desc-channel")
	assert.NilError(t, err)
	assert.Equal(t, ch.Description, "A test channel")
}

func TestCreateChannel_DescriptionTooLong(t *testing.T) {
	_, router := setupAPI(t)
	desc := strings.Repeat("x", 501)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]any{
		"id":          "long-desc",
		"description": desc,
	}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
}

func TestCreateChannel_Duplicate(t *testing.T) {
	_, router := setupAPI(t)

	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, apiRequest("POST", "/channels", map[string]string{"id": "dup-channel"}))
	assert.Equal(t, w1.Code, http.StatusCreated)

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, apiRequest("POST", "/channels", map[string]string{"id": "dup-channel"}))
	assert.Equal(t, w2.Code, http.StatusConflict)
}

func TestCreateChannel_DuplicateErrorIsErrAlreadyExists(t *testing.T) {
	svc, router := setupAPI(t)

	assert.NilError(t, svc.CreateChannel(context.Background(), &domain.Channel{ID: "dup-2"}))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": "dup-2"}))
	assert.Equal(t, w.Code, http.StatusConflict)

	// Prove the 409 is driven by the domain sentinel surviving the Raft Apply.
	err := svc.CreateChannel(context.Background(), &domain.Channel{ID: "dup-2"})
	assert.Assert(t, errors.Is(err, domain.ErrAlreadyExists))
}

func TestDeleteChannel_Success(t *testing.T) {
	_, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels", map[string]string{"id": "del-test"}))
	assert.Equal(t, w.Code, http.StatusCreated)

	w = httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("DELETE", "/channels/del-test", nil))
	assert.Equal(t, w.Code, http.StatusNoContent)

	w = httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("GET", "/channels/del-test", nil))
	assert.Equal(t, w.Code, http.StatusNotFound)
}

func TestDeleteChannel_Nonexistent(t *testing.T) {
	_, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("DELETE", "/channels/nonexistent", nil))
	assert.Equal(t, w.Code, http.StatusNoContent)
}

func TestGetChannel_NotFoundMapsTo404(t *testing.T) {
	_, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("GET", "/channels/nonexistent", nil))
	assert.Equal(t, w.Code, http.StatusNotFound)
}

func TestCreateUser_DuplicateMapsTo409(t *testing.T) {
	svc, router := setupAPI(t)

	create := func(t *testing.T, name string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, apiRequest("POST", "/users", map[string]string{"username": name, "password": "secret-password-1"}))
		return w
	}

	assert.Equal(t, create(t, "dup-user").Code, http.StatusCreated)
	w := create(t, "dup-user")
	assert.Equal(t, w.Code, http.StatusConflict)
	assert.Assert(t, strings.Contains(w.Body.String(), "user already exists"))

	_, err := svc.GetUser(context.Background(), "dup-user")
	assert.NilError(t, err)
}

func TestUpdateUser_CannotRemoveAdminRole(t *testing.T) {
	svc, router := setupAPI(t)

	err := svc.CreateUser(context.Background(), &domain.User{
		ID:       "user1",
		Username: "user1",
		Roles:    []string{"admin"},
		Channels: []string{},
	})
	assert.NilError(t, err)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/users/user1", map[string]any{
		"username": "user1",
		"roles":    []string{},
	}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
	assert.Assert(t, strings.Contains(w.Body.String(), "cannot remove admin role"))
}

func TestUpdateBinding_CannotRemoveAdminRole(t *testing.T) {
	svc, router := setupAPI(t)

	err := svc.CreateUser(context.Background(), &domain.User{
		ID:       "user1",
		Username: "user1",
		Roles:    []string{"admin"},
		Channels: []string{},
	})
	assert.NilError(t, err)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/rbac/bindings/user1", map[string]any{
		"roles":    []string{},
		"channels": []string{},
	}))
	assert.Equal(t, w.Code, http.StatusBadRequest)
	assert.Assert(t, strings.Contains(w.Body.String(), "cannot remove admin role"))
}

func TestDeleteUser_CanDeleteAdminWhenOtherAdminExists(t *testing.T) {
	svc, router := setupAPI(t)

	err := svc.CreateUser(context.Background(), &domain.User{
		ID:       "admin2",
		Username: "admin2",
		Roles:    []string{"admin"},
		Channels: []string{"*"},
	})
	assert.NilError(t, err)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("DELETE", "/users/admin", nil))
	assert.Equal(t, w.Code, http.StatusNoContent)
}

func TestUpdateBinding_AdminCanChangeOtherRoles(t *testing.T) {
	_, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("PUT", "/rbac/bindings/admin", map[string]any{
		"roles":    []string{"admin", "channel_admin"},
		"channels": []string{"*"},
	}))
	assert.Equal(t, w.Code, http.StatusOK)
}

func TestDeleteUser_CannotDeleteLastAdmin(t *testing.T) {
	_, router := setupAPI(t)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("DELETE", "/users/admin", nil))
	assert.Equal(t, w.Code, http.StatusBadRequest)
	assert.Assert(t, strings.Contains(w.Body.String(), "cannot delete the last admin user"))
}

func TestUpdateChannel_Valid(t *testing.T) {
	svc, router := setupAPI(t)
	ch := &domain.Channel{ID: "update-test"}
	assert.NilError(t, svc.CreateChannel(context.Background(), ch))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "PUT", "/channels/update-test", bytes.NewReader([]byte(`{"description":"new desc"}`))))
	assert.Equal(t, w.Code, http.StatusOK)

	got, err := svc.GetChannel(context.Background(), "update-test")
	assert.NilError(t, err)
	assert.Equal(t, got.Description, "new desc")
}

func TestChannelACL_ListRequiresChannelRead(t *testing.T) {
	svc, router := setupAPI(t)

	err := svc.CreateChannel(context.Background(), &domain.Channel{ID: "test-channel"})
	assert.NilError(t, err)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("GET", "/channels/test-channel/acl", nil))
	assert.Equal(t, w.Code, http.StatusOK)
}

func TestChannelACL_AddRequiresChannelWriteOrRBACWrite(t *testing.T) {
	svc, router := setupAPI(t)

	err := svc.CreateChannel(context.Background(), &domain.Channel{ID: "test-channel"})
	assert.NilError(t, err)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("POST", "/channels/test-channel/acl", map[string]string{
		"type":    "user",
		"subject": "someone",
		"role":    "read",
	}))
	assert.Equal(t, w.Code, http.StatusCreated)

	w = httptest.NewRecorder()
	router.ServeHTTP(w, apiRequest("DELETE", "/channels/test-channel/acl/nonexistent", nil))
	assert.Equal(t, w.Code, http.StatusNoContent)
}

func TestChannelACL_NonAdminCannotAddACL(t *testing.T) {
	rs := storetest.NewRaftStore(t)
	svc := service.NewService(rs, nil)
	ctx := context.Background()

	err := svc.CreateUser(ctx, &domain.User{
		ID:       "reader",
		Username: "reader",
		Roles:    []string{"channel_viewer"},
		Channels: []string{},
	})
	assert.NilError(t, err)

	err = svc.CreateChannel(ctx, &domain.Channel{ID: "test-channel"})
	assert.NilError(t, err)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:revive,staticcheck
			ctx := context.WithValue(r.Context(), UsernameContextKey, "reader")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	RegisterAPIHandlers(r, svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, apiRequest("POST", "/channels/test-channel/acl", map[string]string{
		"type":    "user",
		"subject": "someone",
		"role":    "read",
	}))
	assert.Equal(t, w.Code, http.StatusForbidden)
}
