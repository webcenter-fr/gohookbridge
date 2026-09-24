package service

import (
	"context"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// BuildAuthConfig assembles the effective authentication configuration from
// the stored users and OIDC providers. It returns nil when neither local
// users nor OIDC providers are configured (authentication disabled).
func (s *Service) BuildAuthConfig(ctx context.Context) *domain.AuthConfig {
	users, err := s.repo.ListUsers(ctx)
	if err != nil || len(users) == 0 {
		providers, _ := s.repo.OIDCProviders(ctx)
		if len(providers) == 0 {
			return nil
		}
		return &domain.AuthConfig{
			Internal: domain.InternalConfig{Enabled: false},
			OIDC: domain.OIDCConfig{
				Enabled:   true,
				Providers: providers,
			},
		}
	}

	authUsers := make([]domain.InternalUser, 0, len(users))
	for _, u := range users {
		authUsers = append(authUsers, domain.InternalUser{
			Username:     u.Username,
			PasswordHash: u.PasswordHash,
		})
	}

	providers, _ := s.repo.OIDCProviders(ctx)
	oidcEnabled := len(providers) > 0

	return &domain.AuthConfig{
		Internal: domain.InternalConfig{
			Enabled: true,
			Users:   authUsers,
		},
		OIDC: domain.OIDCConfig{
			Enabled:   oidcEnabled,
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
