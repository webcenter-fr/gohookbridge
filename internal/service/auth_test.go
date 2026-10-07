package service

import (
	"context"
	"errors"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

func boolPtr(b bool) *bool { return &b }

func TestBuildAuthConfig(t *testing.T) {
	provider := domain.OIDCProvider{ID: "google", ClientID: "c", ClientSecret: "s", IssuerURL: "https://issuer"}

	tests := []struct {
		name         string
		users        int
		flag         *bool
		providers    []domain.OIDCProvider
		wantNil      bool
		wantInternal bool
		wantOIDC     bool
	}{
		{
			name:         "users only",
			users:        1,
			wantInternal: true,
		},
		{
			name:         "users with flag false and providers",
			users:        1,
			flag:         boolPtr(false),
			providers:    []domain.OIDCProvider{provider},
			wantInternal: false,
			wantOIDC:     true,
		},
		{
			name:         "no users flag false with providers",
			flag:         boolPtr(false),
			providers:    []domain.OIDCProvider{provider},
			wantInternal: false,
			wantOIDC:     true,
		},
		{
			name:         "users with flag true",
			users:        1,
			flag:         boolPtr(true),
			wantInternal: true,
		},
		{
			name:    "empty",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeRepository()
			svc := NewService(fake, nil)
			ctx := context.Background()

			for i := 0; i < tt.users; i++ {
				err := fake.CreateUser(ctx, &domain.User{
					ID:           "user",
					Username:     "user",
					PasswordHash: "hash",
				})
				assert.NilError(t, err)
			}
			fake.internalAuthEnabled = tt.flag
			fake.providers = tt.providers

			got := svc.BuildAuthConfig(ctx)
			if tt.wantNil {
				assert.Assert(t, got == nil)
				return
			}
			assert.Assert(t, got != nil)
			assert.Equal(t, got.Internal.Enabled, tt.wantInternal)
			assert.Equal(t, got.OIDC.Enabled, tt.wantOIDC)
		})
	}
}

func TestSetInternalAuthEnabled_Invariant(t *testing.T) {
	t.Run("disabling with zero providers is rejected", func(t *testing.T) {
		fake := newFakeRepository()
		svc := NewService(fake, nil)

		err := svc.SetInternalAuthEnabled(context.Background(), false)
		assert.Assert(t, errors.Is(err, domain.ErrInvalidArgument))
	})

	t.Run("disabling with providers succeeds", func(t *testing.T) {
		fake := newFakeRepository()
		fake.providers = []domain.OIDCProvider{{ID: "google"}}
		svc := NewService(fake, nil)

		err := svc.SetInternalAuthEnabled(context.Background(), false)
		assert.NilError(t, err)

		flag, err := fake.InternalAuthEnabled(context.Background())
		assert.NilError(t, err)
		assert.Assert(t, flag != nil)
		assert.Equal(t, *flag, false)
	})

	t.Run("enabling always succeeds", func(t *testing.T) {
		fake := newFakeRepository()
		svc := NewService(fake, nil)

		err := svc.SetInternalAuthEnabled(context.Background(), true)
		assert.NilError(t, err)
	})
}

func TestValidateAuthConfig(t *testing.T) {
	t.Run("nil when unset", func(t *testing.T) {
		fake := newFakeRepository()
		svc := NewService(fake, nil)

		assert.NilError(t, svc.ValidateAuthConfig(context.Background()))
	})

	t.Run("error when flag false and zero providers", func(t *testing.T) {
		fake := newFakeRepository()
		fake.internalAuthEnabled = boolPtr(false)
		svc := NewService(fake, nil)

		err := svc.ValidateAuthConfig(context.Background())
		assert.Assert(t, errors.Is(err, domain.ErrInvalidArgument))
	})

	t.Run("nil when flag false with providers", func(t *testing.T) {
		fake := newFakeRepository()
		fake.internalAuthEnabled = boolPtr(false)
		fake.providers = []domain.OIDCProvider{{ID: "google"}}
		svc := NewService(fake, nil)

		assert.NilError(t, svc.ValidateAuthConfig(context.Background()))
	})
}
