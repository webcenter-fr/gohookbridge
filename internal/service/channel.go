package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

// globalOrDefault returns the stored global config, or an error other than
// ErrNotFound (fail closed: a transiently unreadable config must not silently
// downgrade security policy).
func (s *Service) globalOrFail(ctx context.Context) (*domain.GlobalConfig, error) {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve global config: %w", err)
	}
	return global, nil
}

// ResolveChannelConfig resolves a channel's effective configuration. A missing
// channel returns domain.ErrNotFound when require_known_channels is enabled;
// otherwise it returns a synthetic channel built from the global defaults
// (legacy behavior). Repository errors other than ErrNotFound are propagated.
func (s *Service) ResolveChannelConfig(ctx context.Context, id string) (*domain.Channel, error) {
	ch, err := s.repo.GetChannel(ctx, id)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		global, gerr := s.globalOrFail(ctx)
		if gerr != nil {
			return nil, gerr
		}
		if global.Server.RequireKnownChannels {
			return nil, fmt.Errorf("%w: channel %q", domain.ErrNotFound, id)
		}
		return &domain.Channel{
			ID:                id,
			MaxBodySize:       global.Server.MaxBodySize,
			WebhookSecret:     global.Defaults.WebhookSecret,
			AllowedIPs:        global.Defaults.AllowedIPs,
			MessageTTLSeconds: global.Defaults.MessageTTLSeconds,
		}, nil
	}
	global, gerr := s.globalOrFail(ctx)
	if gerr != nil {
		return nil, gerr
	}
	return domain.ResolveChannelConfig(ch, global), nil
}

func (s *Service) ResolveChannelWebhookSecret(ctx context.Context, channelID string) (string, error) {
	p, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return "", err
		}
		global, gerr := s.globalOrFail(ctx)
		if gerr != nil {
			return "", gerr
		}
		return global.Defaults.WebhookSecret, nil
	}
	domain.MigrateChannel(p)
	if p.WebhookSecret != "" {
		return p.WebhookSecret, nil
	}
	global, gerr := s.globalOrFail(ctx)
	if gerr != nil {
		return "", gerr
	}
	return global.Defaults.WebhookSecret, nil
}

func (s *Service) ResolveChannelAllowedIPs(ctx context.Context, channelID string) ([]string, error) {
	p, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		global, gerr := s.globalOrFail(ctx)
		if gerr != nil {
			return nil, gerr
		}
		return global.Defaults.AllowedIPs, nil
	}
	if len(p.AllowedIPs) > 0 {
		return p.AllowedIPs, nil
	}
	global, gerr := s.globalOrFail(ctx)
	if gerr != nil {
		return nil, gerr
	}
	return global.Defaults.AllowedIPs, nil
}

func (s *Service) ResolveChannelMaxBodySize(ctx context.Context, channelID string) (int, error) {
	p, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return 0, err
		}
		global, gerr := s.globalOrFail(ctx)
		if gerr != nil {
			return 0, gerr
		}
		return global.Server.MaxBodySize, nil
	}
	if p.MaxBodySize > 0 {
		return p.MaxBodySize, nil
	}
	global, gerr := s.globalOrFail(ctx)
	if gerr != nil {
		return 0, gerr
	}
	return global.Server.MaxBodySize, nil
}

func (s *Service) ResolveChannelEncryption(ctx context.Context, channelID string) (string, string, string, error) {
	p, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return "", "", "", err
	}
	domain.MigrateChannel(p)
	return p.EncryptionMode, p.EncryptionKey, p.EncryptionPublicKey, nil
}

func (s *Service) ResolveCORSOrigin(ctx context.Context) string {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		return "*"
	}
	return global.Server.CORSOrigin
}

func (s *Service) ResolveBehindReverseProxy(ctx context.Context) bool {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		return false
	}
	return global.Server.BehindReverseProxy
}

// ResolveTrustedProxyCIDRs returns the configured trusted reverse-proxy
// networks whose forwarded headers may be honored.
func (s *Service) ResolveTrustedProxyCIDRs(ctx context.Context) []string {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		return nil
	}
	return global.Server.TrustedProxyCIDRs
}

func (s *Service) ResolveFooter(ctx context.Context) string {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		return ""
	}
	return global.Server.Footer
}

func (s *Service) SessionSecret(ctx context.Context) string {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		return ""
	}
	if global.Server.SessionSecret != "" {
		return global.Server.SessionSecret
	}
	return ""
}

func (s *Service) SetSessionSecret(ctx context.Context, secret string) error {
	global, err := s.repo.GetGlobalConfig(ctx)
	if err != nil {
		global = domain.DefaultGlobalConfig()
	}
	global.Server.SessionSecret = secret
	return s.repo.UpdateGlobalConfig(ctx, global)
}

// ProtectedChannels tracks which channels require E2E encryption and which
// client public keys are allowed to post encrypted payloads to them.
type ProtectedChannels struct {
	svc      *Service
	channels map[string]struct{}
}

// NewProtectedChannels snapshots the current set of E2E-protected channels.
func (s *Service) NewProtectedChannels(ctx context.Context) *ProtectedChannels {
	chs, err := s.repo.ListChannels(ctx)
	if err != nil {
		return &ProtectedChannels{channels: make(map[string]struct{})}
	}

	channelMap := make(map[string]struct{})
	for _, ch := range chs {
		domain.MigrateChannel(ch)
		if ch.EncryptionMode == "e2e" && ch.EncryptionPublicKey != "" {
			channelMap[ch.ID] = struct{}{}
		}
	}
	return &ProtectedChannels{channels: channelMap}
}

// NewProtectedChannelsDynamic resolves protected-channel membership on demand
// from the repository.
func (s *Service) NewProtectedChannelsDynamic() *ProtectedChannels {
	return &ProtectedChannels{svc: s}
}

func (p *ProtectedChannels) Has(ctx context.Context, channel string) bool {
	if p == nil {
		return false
	}
	if p.svc != nil {
		ch, err := p.svc.repo.GetChannel(ctx, channel)
		if err != nil {
			return false
		}
		domain.MigrateChannel(ch)
		return ch.EncryptionMode == "e2e" && ch.EncryptionPublicKey != ""
	}
	_, ok := p.channels[channel]
	return ok
}

func (p *ProtectedChannels) IsAllowed(ctx context.Context, channel string, publicKey *[32]byte) bool {
	if p == nil || publicKey == nil {
		return false
	}
	if p.svc != nil {
		ch, err := p.svc.repo.GetChannel(ctx, channel)
		if err != nil {
			return false
		}
		domain.MigrateChannel(ch)
		if ch.EncryptionMode != "e2e" {
			return false
		}
		encoded := base64.RawURLEncoding.EncodeToString(publicKey[:])
		return ch.EncryptionPublicKey == encoded
	}
	_, ok := p.channels[channel]
	return ok
}
