package service

import (
	"context"
	"fmt"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// IsInternalAuthEnabled reports whether username/password (internal) auth is
// enabled: the stored flag when explicitly set, otherwise derived from the
// presence of users.
func (s *Service) IsInternalAuthEnabled(ctx context.Context) bool {
	if flag, err := s.repo.InternalAuthEnabled(ctx); err == nil && flag != nil {
		return *flag
	}
	users, _ := s.repo.ListUsers(ctx)
	return len(users) > 0
}

// SetInternalAuthEnabled persists the internal-auth flag, refusing to disable
// internal auth when no OIDC providers remain (the "at least one provider"
// invariant).
func (s *Service) SetInternalAuthEnabled(ctx context.Context, enabled bool) error {
	if !enabled {
		providers, err := s.repo.OIDCProviders(ctx)
		if err != nil {
			return err
		}
		if len(providers) == 0 {
			return fmt.Errorf("%w: cannot disable internal auth: at least one auth provider (internal or OIDC) is required", domain.ErrInvalidArgument)
		}
	}
	return s.repo.SetInternalAuthEnabled(ctx, enabled)
}

// ValidateAuthConfig returns an error when the persisted auth configuration has
// zero providers: internal auth explicitly disabled AND no OIDC providers.
func (s *Service) ValidateAuthConfig(ctx context.Context) error {
	flag, err := s.repo.InternalAuthEnabled(ctx)
	if err != nil {
		return err
	}
	if flag != nil && !*flag {
		providers, err := s.repo.OIDCProviders(ctx)
		if err != nil {
			return err
		}
		if len(providers) == 0 {
			return fmt.Errorf("%w: at least one auth provider is required (internal auth is disabled and no OIDC providers are configured)", domain.ErrInvalidArgument)
		}
	}
	return nil
}

// BuildAuthConfig assembles the effective authentication configuration from
// the stored users and OIDC providers. It returns nil when neither local
// users nor OIDC providers are configured (authentication disabled).
func (s *Service) BuildAuthConfig(ctx context.Context) *domain.AuthConfig {
	users, _ := s.repo.ListUsers(ctx)
	providers, _ := s.repo.OIDCProviders(ctx)

	internalEnabled := s.IsInternalAuthEnabled(ctx)
	hasInternal := internalEnabled && len(users) > 0
	hasOIDC := len(providers) > 0

	if !hasInternal && !hasOIDC {
		return nil // setup mode
	}

	authUsers := make([]domain.InternalUser, 0, len(users))
	for _, u := range users {
		authUsers = append(authUsers, domain.InternalUser{
			Username:     u.Username,
			PasswordHash: u.PasswordHash,
		})
	}

	return &domain.AuthConfig{
		Internal: domain.InternalConfig{
			Enabled: hasInternal,
			Users:   authUsers,
		},
		OIDC: domain.OIDCConfig{
			Enabled:   hasOIDC,
			Providers: providers,
		},
	}
}

func ValidatePassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// CreateDevAdmin creates the bootstrap admin user when the system is in setup
// mode (no users exist yet). It is a no-op otherwise.
func (s *Service) CreateDevAdmin(ctx context.Context, password string) error {
	if !s.IsSetupMode(ctx) {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := &domain.User{
		ID:           "admin",
		Username:     "admin",
		PasswordHash: string(hash),
		Roles:        []string{"admin"},
		Channels:     []string{"*"},
	}
	return s.repo.CreateUser(ctx, user)
}

func (s *Service) IsSetupMode(ctx context.Context) bool {
	users, err := s.repo.ListUsers(ctx)
	if err != nil || len(users) == 0 {
		return true
	}
	return false
}
