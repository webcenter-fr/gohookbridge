package store

import "encoding/base64"

func NewProtectedChannels(rs *RaftStore) *ProtectedChannels {
	chs, err := rs.ListChannels()
	if err != nil {
		return &ProtectedChannels{channels: make(map[string]map[string]struct{})}
	}

	channelMap := make(map[string]map[string]struct{})
	for _, ch := range chs {
		migrateChannel(ch)
		if ch.EncryptionMode == "provider_side" && len(ch.EncryptionPubKeys) > 0 {
			allowed := make(map[string]struct{}, len(ch.EncryptionPubKeys))
			for _, k := range ch.EncryptionPubKeys {
				allowed[k] = struct{}{}
			}
			channelMap[ch.ID] = allowed
		}
	}
	return &ProtectedChannels{channels: channelMap}
}

func NewProtectedChannelsDynamic(rs *RaftStore) *ProtectedChannels {
	return &ProtectedChannels{rs: rs}
}

type ProtectedChannels struct {
	rs       *RaftStore
	channels map[string]map[string]struct{}
}

func (p *ProtectedChannels) Has(channel string) bool {
	if p == nil {
		return false
	}
	if p.rs != nil {
		ch, err := p.rs.GetChannel(channel)
		if err != nil {
			return false
		}
		migrateChannel(ch)
		return ch.EncryptionMode == "provider_side" && len(ch.EncryptionPubKeys) > 0
	}
	_, ok := p.channels[channel]
	return ok
}

func (p *ProtectedChannels) IsAllowed(channel string, publicKey *[32]byte) bool {
	if p == nil || publicKey == nil {
		return false
	}
	if p.rs != nil {
		ch, err := p.rs.GetChannel(channel)
		if err != nil {
			return false
		}
		migrateChannel(ch)
		if ch.EncryptionMode != "provider_side" {
			return false
		}
		encoded := base64.RawURLEncoding.EncodeToString(publicKey[:])
		for _, k := range ch.EncryptionPubKeys {
			if k == encoded {
				return true
			}
		}
		return false
	}
	allowedKeys, ok := p.channels[channel]
	if !ok {
		return false
	}
	encoded := base64.RawURLEncoding.EncodeToString(publicKey[:])
	_, ok = allowedKeys[encoded]
	return ok
}

func BuildAuthConfig(rs *RaftStore) *AuthConfig {
	users, err := rs.ListUsers()
	if err != nil || len(users) == 0 {
		providers, _ := rs.OIDCProviders()
		if len(providers) == 0 {
			return nil
		}
		return &AuthConfig{
			Internal: InternalConfig{Enabled: false},
			OIDC: OIDCConfig{
				Enabled:   true,
				Providers: providers,
			},
		}
	}

	authUsers := make([]InternalUser, 0, len(users))
	for _, u := range users {
		authUsers = append(authUsers, InternalUser{
			Username:     u.Username,
			PasswordHash: u.PasswordHash,
		})
	}

	providers, _ := rs.OIDCProviders()
	oidcEnabled := len(providers) > 0

	return &AuthConfig{
		Internal: InternalConfig{
			Enabled: true,
			Users:   authUsers,
		},
		OIDC: OIDCConfig{
			Enabled:   oidcEnabled,
			Providers: providers,
		},
	}
}

type AuthConfig struct {
	Internal InternalConfig
	OIDC     OIDCConfig
}

type InternalConfig struct {
	Enabled bool
	Users   []InternalUser
}

type InternalUser struct {
	Username     string
	PasswordHash string
}

type OIDCConfig struct {
	Enabled   bool
	Providers []OIDCProvider
}