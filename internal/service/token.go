package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/pkg/uuid"
)

func GenerateAccessToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate access token: %w", err)
	}
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(h[:])
	return raw, hash, nil
}

func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (s *Service) CreateAccessToken(ctx context.Context, channelID string, name string, scope string) (raw string, token domain.ChannelAccessToken, err error) {
	if scope != "produce" && scope != "consume" && scope != "both" {
		scope = "both"
	}
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return "", domain.ChannelAccessToken{}, err
	}
	raw, hash, err := GenerateAccessToken()
	if err != nil {
		return "", domain.ChannelAccessToken{}, err
	}
	t := domain.ChannelAccessToken{
		ID:        uuid.GenerateUUID(),
		Name:      name,
		TokenHash: hash,
		Scope:     scope,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	ch.AccessTokens = append(ch.AccessTokens, t)
	ch.AccessMode = "token"
	if err := s.repo.UpdateChannel(ctx, ch); err != nil {
		return "", domain.ChannelAccessToken{}, err
	}
	return raw, t, nil
}

func (s *Service) DeleteAccessToken(ctx context.Context, channelID string, tokenID string) error {
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	filtered := make([]domain.ChannelAccessToken, 0, len(ch.AccessTokens))
	for _, t := range ch.AccessTokens {
		if t.ID != tokenID {
			filtered = append(filtered, t)
		}
	}
	ch.AccessTokens = filtered
	return s.repo.UpdateChannel(ctx, ch)
}

func (s *Service) ValidateChannelToken(ctx context.Context, channelID string, rawToken string, requiredScope string) bool {
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return false
	}
	hash := HashToken(rawToken)
	for _, t := range ch.AccessTokens {
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(hash)) == 1 {
			if t.Scope == "both" || t.Scope == requiredScope {
				return true
			}
		}
	}
	return false
}
