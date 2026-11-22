package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/disaster37/go-auth/apitoken"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/pkg/uuid"
)

func GenerateAccessToken() (raw, hash string, err error) {
	b := make([]byte, apitoken.DefaultRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate access token: %w", err)
	}
	raw = hex.EncodeToString(b)
	return raw, apitoken.Hash(raw), nil
}

func HashToken(raw string) string {
	return apitoken.Hash(raw)
}

func (s *Service) CreateAccessToken(ctx context.Context, channelID, name, scope string) (raw string, token domain.ChannelAccessToken, err error) {
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
	id, err := uuid.GenerateUUID()
	if err != nil {
		return "", domain.ChannelAccessToken{}, err
	}
	t := domain.ChannelAccessToken{
		ID:        id,
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

func (s *Service) DeleteAccessToken(ctx context.Context, channelID, tokenID string) error {
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

func (s *Service) ValidateChannelToken(ctx context.Context, channelID, rawToken, requiredScope string) bool {
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return false
	}
	hash := apitoken.Hash(rawToken)
	for _, t := range ch.AccessTokens {
		if apitoken.CompareHashConstantTime(t.TokenHash, hash) {
			if t.Scope == "both" || t.Scope == requiredScope {
				return true
			}
		}
	}
	return false
}
