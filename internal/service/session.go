package service

import (
	"context"
	"time"

	goauthjwt "github.com/disaster37/go-auth/jwt"
)

// SessionToken is the signed session payload stored in the session cookie.
type SessionToken = goauthjwt.SessionToken

const (
	sessionSecretKDFInfo   = "gohookbridge.session.signing.v1"
	MinSessionSecretLength = goauthjwt.MinSecretLength
)

// DeriveSessionSecret derives the 32-byte HMAC-SHA256 signing key via go-auth/jwt
// using gohookbridge's domain-separation info string for cookie compatibility.
func DeriveSessionSecret(secret string) [32]byte {
	key, err := goauthjwt.DeriveSigningKey(secret, sessionSecretKDFInfo)
	if err != nil {
		panic("service: derive session secret: " + err.Error())
	}
	return key
}

// ValidateSessionSecret rejects weak session secrets (< 32 chars) via go-auth/jwt.
func ValidateSessionSecret(secret string) error {
	return goauthjwt.ValidateSecret(secret)
}

// EncodeSession signs and encodes a session token via go-auth/jwt.
func EncodeSession(token *SessionToken, secret [32]byte) (string, error) {
	return goauthjwt.EncodeSession(token, secret)
}

// DecodeSession verifies and decodes a session cookie value via go-auth/jwt.
func DecodeSession(tokenStr string, secret [32]byte) (*SessionToken, error) {
	return goauthjwt.DecodeSession(tokenStr, secret, time.Now().UTC())
}

// GetAuthEpoch returns the current session/auth epoch (0 on error).
func (s *Service) GetAuthEpoch(ctx context.Context) int64 {
	epoch, err := s.repo.GetAuthEpoch(ctx)
	if err != nil {
		return 0
	}
	return epoch
}

// BumpAuthEpoch invalidates every outstanding session.
func (s *Service) BumpAuthEpoch(ctx context.Context) error {
	return s.repo.BumpAuthEpoch(ctx)
}

// SessionEpochValid reports whether the session was minted under the current auth epoch.
func (s *Service) SessionEpochValid(ctx context.Context, tok *SessionToken) bool {
	if tok == nil {
		return false
	}
	return tok.AuthEpoch == s.GetAuthEpoch(ctx)
}

// GenerateRandomHex returns a random 16-byte value encoded as 32 hex chars.
func GenerateRandomHex() (string, error) {
	return goauthjwt.GenerateRandomHex()
}
