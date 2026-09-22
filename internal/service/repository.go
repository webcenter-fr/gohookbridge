package service

import (
	"context"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

// The methods in this file are passthroughs to the domain.Repository so that
// handlers only ever depend on the service layer.

func (s *Service) GetChannel(ctx context.Context, id string) (*domain.Channel, error) {
	return s.repo.GetChannel(ctx, id)
}

func (s *Service) ListChannels(ctx context.Context) ([]*domain.Channel, error) {
	return s.repo.ListChannels(ctx)
}

func (s *Service) CreateChannel(ctx context.Context, p *domain.Channel) error {
	if err := s.repo.CreateChannel(ctx, p); err != nil {
		return err
	}
	s.notifyChannelChanged(ctx, p.ID)
	return nil
}

func (s *Service) UpdateChannel(ctx context.Context, p *domain.Channel) error {
	if err := s.repo.UpdateChannel(ctx, p); err != nil {
		return err
	}
	s.notifyChannelChanged(ctx, p.ID)
	return nil
}

func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	return s.repo.DeleteChannel(ctx, id)
}

func (s *Service) CreateChannelRoleMapping(ctx context.Context, m *domain.ChannelRoleMapping) error {
	return s.repo.CreateChannelRoleMapping(ctx, m)
}

func (s *Service) ListChannelRoleMappings(ctx context.Context, channelID string) ([]domain.ChannelRoleMapping, error) {
	return s.repo.ListChannelRoleMappings(ctx, channelID)
}

func (s *Service) DeleteChannelRoleMapping(ctx context.Context, channelID, entryID string) error {
	return s.repo.DeleteChannelRoleMapping(ctx, channelID, entryID)
}

func (s *Service) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return s.repo.GetUser(ctx, id)
}

func (s *Service) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	return s.repo.GetUserByUsername(ctx, username)
}

func (s *Service) ListUsers(ctx context.Context) ([]*domain.User, error) {
	return s.repo.ListUsers(ctx)
}

func (s *Service) CreateUser(ctx context.Context, u *domain.User) error {
	return s.repo.CreateUser(ctx, u)
}

func (s *Service) UpdateUser(ctx context.Context, u *domain.User) error {
	return s.repo.UpdateUser(ctx, u)
}

func (s *Service) DeleteUser(ctx context.Context, id string) error {
	return s.repo.DeleteUser(ctx, id)
}

func (s *Service) GetRole(ctx context.Context, name string) (*domain.Role, error) {
	return s.repo.GetRole(ctx, name)
}

func (s *Service) ListRoles(ctx context.Context) ([]domain.Role, error) {
	return s.repo.ListRoles(ctx)
}

func (s *Service) CreateRole(ctx context.Context, r domain.Role) error {
	return s.repo.CreateRole(ctx, r)
}

func (s *Service) CreateRoleMapping(ctx context.Context, m *domain.RoleMapping) error {
	return s.repo.CreateRoleMapping(ctx, m)
}

func (s *Service) ListRoleMappings(ctx context.Context) ([]domain.RoleMapping, error) {
	return s.repo.ListRoleMappings(ctx)
}

func (s *Service) DeleteRoleMapping(ctx context.Context, id string) error {
	return s.repo.DeleteRoleMapping(ctx, id)
}

func (s *Service) GetUserRoleMappings(ctx context.Context, userID string) ([]domain.RoleMapping, error) {
	return s.repo.GetUserRoleMappings(ctx, userID)
}

func (s *Service) GetGroupRoleMappings(ctx context.Context, groupName string) ([]domain.RoleMapping, error) {
	return s.repo.GetGroupRoleMappings(ctx, groupName)
}

func (s *Service) GetUserChannelRoleMappings(ctx context.Context, userID string) ([]domain.ChannelRoleMapping, error) {
	return s.repo.GetUserChannelRoleMappings(ctx, userID)
}

func (s *Service) GetGroupChannelRoleMappings(ctx context.Context, groupName string) ([]domain.ChannelRoleMapping, error) {
	return s.repo.GetGroupChannelRoleMappings(ctx, groupName)
}

func (s *Service) GetGlobalConfig(ctx context.Context) (*domain.GlobalConfig, error) {
	return s.repo.GetGlobalConfig(ctx)
}

func (s *Service) UpdateGlobalConfig(ctx context.Context, cfg *domain.GlobalConfig) error {
	if err := s.repo.UpdateGlobalConfig(ctx, cfg); err != nil {
		return err
	}
	if s.notifier != nil {
		channels, _ := s.repo.ListChannels(ctx)
		for _, ch := range channels {
			if ch.MessageTTLSeconds == 0 {
				resolved, err := s.ResolveChannelConfig(ctx, ch.ID)
				if err == nil {
					s.notifier.OnChannelChanged(ch.ID, resolved.MessageTTLSeconds)
				}
			}
		}
	}
	return nil
}

func (s *Service) OIDCProviders(ctx context.Context) ([]domain.OIDCProvider, error) {
	return s.repo.OIDCProviders(ctx)
}

func (s *Service) SetOIDCProviders(ctx context.Context, providers []domain.OIDCProvider) error {
	return s.repo.SetOIDCProviders(ctx, providers)
}

func (s *Service) GetClientCursor(ctx context.Context, channel, clientID string) (*domain.ClientCursor, error) {
	return s.repo.GetClientCursor(ctx, channel, clientID)
}

func (s *Service) SetClientCursor(ctx context.Context, cursor *domain.ClientCursor) error {
	return s.repo.SetClientCursor(ctx, cursor)
}

func (s *Service) GetSetupModeEndTime(ctx context.Context) time.Time {
	return s.repo.GetSetupModeEndTime(ctx)
}

func (s *Service) SetSetupModeEndTime(ctx context.Context, t time.Time) error {
	return s.repo.SetSetupModeEndTime(ctx, t)
}

func (s *Service) ListBindings(ctx context.Context) ([]domain.UserBinding, error) {
	users, err := s.repo.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	bindings := make([]domain.UserBinding, 0, len(users))
	for _, u := range users {
		bindings = append(bindings, domain.UserBinding{
			UserID:   u.ID,
			Roles:    u.Roles,
			Channels: u.Channels,
		})
	}
	return bindings, nil
}

func (s *Service) UpdateUserBinding(ctx context.Context, binding *domain.UserBinding) error {
	u, err := s.repo.GetUser(ctx, binding.UserID)
	if err != nil {
		return err
	}
	u.Roles = binding.Roles
	u.Channels = binding.Channels
	return s.repo.UpdateUser(ctx, u)
}

// notifyChannelChanged forwards a channel configuration change to the
// notifier with the channel's resolved message TTL.
func (s *Service) notifyChannelChanged(ctx context.Context, channelID string) {
	if s.notifier == nil {
		return
	}
	resolved, err := s.ResolveChannelConfig(ctx, channelID)
	if err == nil {
		s.notifier.OnChannelChanged(channelID, resolved.MessageTTLSeconds)
	}
}
