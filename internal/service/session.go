package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SessionToken is the signed session payload stored in the session cookie.
type SessionToken struct {
	Username  string   `json:"username"`
	Method    string   `json:"method"`
	Provider  string   `json:"provider,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
	Groups    []string `json:"groups,omitempty"`
}

// DeriveSessionSecret derives the HMAC key used to sign session cookies from
// the configured session secret string.
func DeriveSessionSecret(secret string) [32]byte {
	return sha256.Sum256([]byte(secret))
}

// EncodeSession signs and encodes a session token as
// base64url(payload).base64url(hmac).
func EncodeSession(token *SessionToken, secret [32]byte) (string, error) {
	data, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

// DecodeSession verifies and decodes a session cookie value.
func DecodeSession(tokenStr string, secret [32]byte) (*SessionToken, error) {
	parts := strings.SplitN(tokenStr, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}
	encoded := parts[0]
	sigB64 := parts[1]

	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(encoded))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sigB64), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid token signature")
	}

	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var token SessionToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	if token.ExpiresAt < time.Now().Unix() {
		return nil, fmt.Errorf("token expired")
	}
	return &token, nil
}

// GenerateRandomHex returns a random 16-byte value encoded as 32 hex chars.
func GenerateRandomHex() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random hex: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}
