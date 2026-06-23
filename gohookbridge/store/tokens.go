package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
)

func GenerateAccessToken() (raw string, hash string) {
	b := make([]byte, 32)
	rand.Read(b)
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(h[:])
	return raw, hash
}

func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (rs *RaftStore) CreateAccessToken(channelID string, name string, scope string) (raw string, token ChannelAccessToken, err error) {
	if scope != "produce" && scope != "consume" && scope != "both" {
		scope = "both"
	}
	ch, err := rs.GetChannel(channelID)
	if err != nil {
		return "", ChannelAccessToken{}, err
	}
	raw, hash := GenerateAccessToken()
	t := ChannelAccessToken{
		ID:        gohookbridge.GenerateUUID(),
		Name:      name,
		TokenHash: hash,
		Scope:     scope,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	ch.AccessTokens = append(ch.AccessTokens, t)
	ch.AccessMode = "token"
	if err := rs.UpdateChannel(ch); err != nil {
		return "", ChannelAccessToken{}, err
	}
	return raw, t, nil
}

func (rs *RaftStore) DeleteAccessToken(channelID string, tokenID string) error {
	ch, err := rs.GetChannel(channelID)
	if err != nil {
		return err
	}
	filtered := make([]ChannelAccessToken, 0, len(ch.AccessTokens))
	for _, t := range ch.AccessTokens {
		if t.ID != tokenID {
			filtered = append(filtered, t)
		}
	}
	ch.AccessTokens = filtered
	return rs.UpdateChannel(ch)
}

func (rs *RaftStore) ValidateChannelToken(channelID string, rawToken string, requiredScope string) bool {
	ch, err := rs.GetChannel(channelID)
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
