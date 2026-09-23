package service

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gotest.tools/v3/assert"
)

func newTestSessionToken(username, method string) *SessionToken {
	return &SessionToken{
		Username:  username,
		Method:    method,
		ExpiresAt: time.Now().Unix() + 86400,
	}
}

func TestSessionTokenRoundTrip(t *testing.T) {
	secret := DeriveSessionSecret("test-secret-key-that-is-long-enough-32")
	token := newTestSessionToken("alice", "internal")

	encoded, err := EncodeSession(token, secret)
	assert.NilError(t, err)
	assert.Assert(t, encoded != "")

	decoded, err := DecodeSession(encoded, secret)
	assert.NilError(t, err)
	assert.Equal(t, decoded.Username, "alice")
	assert.Equal(t, decoded.Method, "internal")

	t.Run("InvalidSignature", func(t *testing.T) {
		parts := strings.SplitN(encoded, ".", 2)
		tampered := parts[0] + "." + "AAAA"
		_, err := DecodeSession(tampered, secret)
		assert.ErrorContains(t, err, "invalid token signature")
	})

	t.Run("ExpiredToken", func(t *testing.T) {
		expired := &SessionToken{
			Username:  "bob",
			Method:    "internal",
			ExpiresAt: time.Now().Unix() - 1,
		}
		enc, err := EncodeSession(expired, secret)
		assert.NilError(t, err)
		_, err = DecodeSession(enc, secret)
		assert.ErrorContains(t, err, "token expired")
	})

	t.Run("WrongSecret", func(t *testing.T) {
		otherSecret := DeriveSessionSecret("different-secret-that-is-also-long-enough")
		enc, err := EncodeSession(token, secret)
		assert.NilError(t, err)
		_, err = DecodeSession(enc, otherSecret)
		assert.ErrorContains(t, err, "invalid token signature")
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		_, err := DecodeSession("no-dot-separator", secret)
		assert.ErrorContains(t, err, "invalid token format")
	})
}

func TestValidatePassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.MinCost)
	assert.NilError(t, err)

	assert.Assert(t, ValidatePassword(string(hash), "correct"))
	assert.Assert(t, !ValidatePassword(string(hash), "wrong"))
	assert.Assert(t, !ValidatePassword(string(hash), ""))
}

func TestDeriveSessionSecret(t *testing.T) {
	secret1 := DeriveSessionSecret("hello")
	secret2 := DeriveSessionSecret("hello")
	secret3 := DeriveSessionSecret("world")

	assert.Equal(t, secret1, secret2)
	assert.Assert(t, secret1 != secret3)
	assert.Equal(t, len(secret1), 32)
}

func TestGenerateRandomHex(t *testing.T) {
	s1, err := GenerateRandomHex()
	assert.NilError(t, err)
	s2, err := GenerateRandomHex()
	assert.NilError(t, err)
	assert.Assert(t, s1 != "")
	assert.Assert(t, s1 != s2)
	assert.Equal(t, len(s1), 32)
}
