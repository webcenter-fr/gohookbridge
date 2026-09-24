package domain

import (
	"context"
	"time"
)

// ChannelChangeNotifier is notified when a channel's configuration changes so
// brokers can refresh per-channel state (e.g. message TTL).
type ChannelChangeNotifier interface {
	OnChannelChanged(channelID string, ttlSeconds int)
}

type ChannelRepository interface {
	GetChannel(ctx context.Context, id string) (*Channel, error)
	ListChannels(ctx context.Context) ([]*Channel, error)
	CreateChannel(ctx context.Context, p *Channel) error
	UpdateChannel(ctx context.Context, p *Channel) error
	DeleteChannel(ctx context.Context, id string) error
	CreateChannelRoleMapping(ctx context.Context, m *ChannelRoleMapping) error
	ListChannelRoleMappings(ctx context.Context, channelID string) ([]ChannelRoleMapping, error)
	DeleteChannelRoleMapping(ctx context.Context, channelID, entryID string) error
}

type UserRepository interface {
	GetUser(ctx context.Context, id string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
	CreateUser(ctx context.Context, u *User) error
	UpdateUser(ctx context.Context, u *User) error
	DeleteUser(ctx context.Context, id string) error
}

type RBACRepository interface {
	GetRole(ctx context.Context, name string) (*Role, error)
	ListRoles(ctx context.Context) ([]Role, error)
	CreateRole(ctx context.Context, r Role) error
	CreateRoleMapping(ctx context.Context, m *RoleMapping) error
	ListRoleMappings(ctx context.Context) ([]RoleMapping, error)
	DeleteRoleMapping(ctx context.Context, id string) error
	GetUserRoleMappings(ctx context.Context, userID string) ([]RoleMapping, error)
	GetGroupRoleMappings(ctx context.Context, groupName string) ([]RoleMapping, error)
	GetUserChannelRoleMappings(ctx context.Context, userID string) ([]ChannelRoleMapping, error)
	GetGroupChannelRoleMappings(ctx context.Context, groupName string) ([]ChannelRoleMapping, error)
}

type ConfigRepository interface {
	GetGlobalConfig(ctx context.Context) (*GlobalConfig, error)
	UpdateGlobalConfig(ctx context.Context, cfg *GlobalConfig) error
	OIDCProviders(ctx context.Context) ([]OIDCProvider, error)
	SetOIDCProviders(ctx context.Context, providers []OIDCProvider) error
	GetClientCursor(ctx context.Context, channel, clientID string) (*ClientCursor, error)
	SetClientCursor(ctx context.Context, cursor *ClientCursor) error
	GetSetupModeEndTime(ctx context.Context) time.Time
	SetSetupModeEndTime(ctx context.Context, t time.Time) error
}

// Repository is the persistence contract every repository implementation
// satisfies. The domain layer defines it; internal/repository implements it.
type Repository interface {
	ChannelRepository
	UserRepository
	RBACRepository
	ConfigRepository
}
