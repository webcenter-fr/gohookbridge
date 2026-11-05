package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/pkg/crypto"
	"github.com/webcenter-fr/gohookbridge/pkg/encryption"
)

// EventPublisher is the minimal broker surface the publish policy needs.
type EventPublisher interface {
	Publish(channel string, data []byte) error
}

// ChannelEncryptionContext returns the AAD/context bound to a channel for both
// AES-GCM (server-side) and NaCl envelope v2 (e2e) encryption.
func ChannelEncryptionContext(channel string) []byte {
	return crypto.ChannelContext(channel)
}

// EncryptForChannel applies the channel's encryption policy to body and returns
// the bytes to embed in the event envelope. It is fail-closed: a mode that
// requires a key while none is configured is an error, never a silent
// plaintext publish.
func (s *Service) EncryptForChannel(ctx context.Context, channel string, body []byte) ([]byte, error) {
	ch, err := s.ResolveChannelConfig(ctx, channel)
	if err != nil {
		return nil, err
	}
	mode := ch.EncryptionMode
	key := ch.EncryptionKey
	pub := ch.EncryptionPublicKey
	if mode == "" {
		resolvedMode, resolvedKey, resolvedPub, rerr := s.ResolveChannelEncryption(ctx, channel)
		if rerr != nil && !errors.Is(rerr, domain.ErrNotFound) {
			return nil, rerr
		}
		mode, key, pub = resolvedMode, resolvedKey, resolvedPub
	}

	aad := ChannelEncryptionContext(channel)
	switch mode {
	case "server_side":
		if key == "" {
			return nil, fmt.Errorf("server_side channel %q has no encryption key", channel)
		}
		return encryption.AESEncryptWithContext(body, key, aad)
	case "e2e":
		if pub == "" {
			return nil, fmt.Errorf("e2e channel %q has no public key", channel)
		}
		if crypto.IsEncrypted(body) {
			return body, nil
		}
		pubKey, perr := crypto.ParsePublicKey(pub)
		if perr != nil {
			return nil, fmt.Errorf("parse channel public key: %w", perr)
		}
		return crypto.EncryptWithContext(body, pubKey, aad)
	default:
		return body, nil
	}
}

// PublishEvent applies the channel encryption policy and publishes the result
// to the broker, returning the encrypted body it published. All event
// producers route their payload through this single policy-aware entry point.
func (s *Service) PublishEvent(ctx context.Context, broker EventPublisher, channel string, body []byte) ([]byte, error) {
	if broker == nil {
		return nil, fmt.Errorf("publish: broker required")
	}
	encrypted, err := s.EncryptForChannel(ctx, channel, body)
	if err != nil {
		return nil, err
	}
	if err := broker.Publish(channel, encrypted); err != nil {
		return nil, err
	}
	return encrypted, nil
}
