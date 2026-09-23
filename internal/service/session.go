package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// SessionToken is the signed session payload stored in the session cookie.
type SessionToken struct {
	Username  string   `json:"username"`
	Method    string   `json:"method"`
	Provider  string   `json:"provider,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
	Groups    []string `json:"groups,omitempty"`
}

const sessionSecretKDFInfo = "gohookbridge.session.signing.v1"

// DeriveSessionSecret derives the HMAC-SHA256 signing key from the configured
// session secret using HKDF-SHA256 (extract+expand with a domain-separation
// info string). SHA-256 alone is not a KDF; HKDF is the standard extractor for
// turning a shared secret into keying material.
func DeriveSessionSecret(secret string) [32]byte {
	var key [32]byte
	r := hkdf.New(sha256.New, []byte(secret), nil, []byte(sessionSecretKDFInfo))
	if _, err := io.ReadFull(r, key[:]); err != nil {
		// Cannot happen for HKDF-SHA256 (returns exactly the requested bytes).
		panic("service: derive session secret: " + err.Error())
	}
	return key
}

// MinSessionSecretLength is the minimum accepted length for a configured
// session secret (32+ hex/base64 chars provide >= 128 bits of keyspace).
const MinSessionSecretLength = 32

// ValidateSessionSecret rejects weak session secrets (CWE-326). An empty
// secret is allowed (the server auto-generates a 32-byte random one). A
// 32+ character hex/base64 secret provides >= 128 bits of keyspace.
func ValidateSessionSecret(secret string) error {
	if secret == "" {
		return nil
	}
	if len(secret) < MinSessionSecretLength {
		return fmt.Errorf("session_secret must be at least %d characters (got %d)", MinSessionSecretLength, len(secret))
	}
	return nil
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
