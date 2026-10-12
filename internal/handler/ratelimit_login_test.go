package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository/storetest"
	"github.com/webcenter-fr/gohookbridge/internal/service"
	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
)

// F19: repeated failed logins for the same account are throttled (429) even
// when the source IP is constant.
func TestLoginThrottlePerAccount(t *testing.T) {
	svc := service.NewService(storetest.NewRaftStore(t), nil)
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	assert.NilError(t, err)
	assert.NilError(t, svc.CreateUser(ctx, &domain.User{
		ID: "alice", Username: "alice", PasswordHash: string(hash),
	}))

	cfg, err := svc.GetGlobalConfig(ctx)
	assert.NilError(t, err)
	cfg.Server.LoginThrottleAttempts = 2
	cfg.Server.LoginThrottleCooldown = 60
	cfg.Server.SessionSecret = "0123456789abcdef0123456789abcdef"
	assert.NilError(t, svc.UpdateGlobalConfig(ctx, cfg))

	secret := service.DeriveSessionSecret("test-secret-for-login-throttle-32")
	throttle := service.NewLoginThrottle()
	handler := APILoginHandler(svc, secret, service.NewBanTracker(), throttle, true)

	attempt := func(password string) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/login",
			strings.NewReader(fmt.Sprintf(`{"username":"alice","password":%q}`, password)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	assert.Equal(t, attempt("wrong"), http.StatusUnauthorized)
	assert.Equal(t, attempt("wrong"), http.StatusUnauthorized)
	assert.Equal(t, attempt("wrong"), http.StatusTooManyRequests)

	// A successful login clears the throttle.
	throttle.Clear(service.FingerprintLogin("alice"))
	assert.Equal(t, attempt("correct-password"), http.StatusOK)
}
