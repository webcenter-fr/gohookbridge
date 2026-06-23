package store

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestGenerateAccessToken(t *testing.T) {
	raw, hash := GenerateAccessToken()
	assert.Assert(t, len(raw) > 0)
	assert.Assert(t, len(hash) > 0)
	assert.Assert(t, raw != hash, "raw and hash should be different")
	assert.Equal(t, len(raw), 64, "raw token should be 64 hex chars")
	assert.Equal(t, len(hash), 64, "hash should be 64 hex chars")

	// Verify the hash matches
	assert.Equal(t, hash, HashToken(raw))
}

func TestHashToken_Deterministic(t *testing.T) {
	h1 := HashToken("test-token-value")
	h2 := HashToken("test-token-value")
	assert.Equal(t, h1, h2)

	h3 := HashToken("different-token")
	assert.Assert(t, h1 != h3)
}

func TestCreateAccessToken(t *testing.T) {
	rs := newTestRaftStore(t)

	assert.NilError(t, rs.CreateChannel(&Channel{ID: "test-chan"}))

	raw, token, err := rs.CreateAccessToken("test-chan", "my-token", "produce")
	assert.NilError(t, err)
	assert.Assert(t, len(raw) > 0)
	assert.Equal(t, token.Name, "my-token")
	assert.Equal(t, token.Scope, "produce")
	assert.Equal(t, token.TokenHash, HashToken(raw))

	ch, err := rs.GetChannel("test-chan")
	assert.NilError(t, err)
	assert.Equal(t, ch.AccessMode, "token")
	assert.Equal(t, len(ch.AccessTokens), 1)
	assert.Equal(t, ch.AccessTokens[0].Name, "my-token")

	t.Run("invalid scope defaults to both", func(t *testing.T) {
		ch2 := &Channel{ID: "test-chan2"}
		assert.NilError(t, rs.CreateChannel(ch2))
		_, token2, err := rs.CreateAccessToken("test-chan2", "bad-scope", "invalid")
		assert.NilError(t, err)
		assert.Equal(t, token2.Scope, "both")
	})

	t.Run("channel not found", func(t *testing.T) {
		_, _, err := rs.CreateAccessToken("nonexistent", "test", "both")
		assert.ErrorContains(t, err, "not found")
	})
}

func TestDeleteAccessToken(t *testing.T) {
	rs := newTestRaftStore(t)

	assert.NilError(t, rs.CreateChannel(&Channel{ID: "test-chan"}))

	_, token1, err := rs.CreateAccessToken("test-chan", "token-1", "produce")
	assert.NilError(t, err)
	_, token2, err := rs.CreateAccessToken("test-chan", "token-2", "consume")
	assert.NilError(t, err)

	assert.NilError(t, rs.DeleteAccessToken("test-chan", token1.ID))

	ch, err := rs.GetChannel("test-chan")
	assert.NilError(t, err)
	assert.Equal(t, len(ch.AccessTokens), 1)
	assert.Equal(t, ch.AccessTokens[0].Name, "token-2")

	t.Run("does not change access mode when last token is deleted", func(t *testing.T) {
		assert.NilError(t, rs.DeleteAccessToken("test-chan", token2.ID))
		ch, err := rs.GetChannel("test-chan")
		assert.NilError(t, err)
		assert.Equal(t, len(ch.AccessTokens), 0)
		assert.Equal(t, ch.AccessMode, "token")
	})

	t.Run("channel not found returns error", func(t *testing.T) {
		err := rs.DeleteAccessToken("nonexistent", "some-id")
		assert.ErrorContains(t, err, "not found")
	})
}

func TestValidateChannelToken(t *testing.T) {
	rs := newTestRaftStore(t)

	assert.NilError(t, rs.CreateChannel(&Channel{ID: "test-chan"}))

	produceRaw, _, err := rs.CreateAccessToken("test-chan", "produce-token", "produce")
	assert.NilError(t, err)

	consumeRaw, _, err := rs.CreateAccessToken("test-chan", "consume-token", "consume")
	assert.NilError(t, err)

	bothRaw, _, err := rs.CreateAccessToken("test-chan", "both-token", "both")
	assert.NilError(t, err)

	t.Run("valid produce token for produce scope", func(t *testing.T) {
		assert.Assert(t, rs.ValidateChannelToken("test-chan", produceRaw, "produce"))
	})

	t.Run("produce token rejected for consume scope", func(t *testing.T) {
		assert.Assert(t, !rs.ValidateChannelToken("test-chan", produceRaw, "consume"))
	})

	t.Run("valid consume token for consume scope", func(t *testing.T) {
		assert.Assert(t, rs.ValidateChannelToken("test-chan", consumeRaw, "consume"))
	})

	t.Run("consume token rejected for produce scope", func(t *testing.T) {
		assert.Assert(t, !rs.ValidateChannelToken("test-chan", consumeRaw, "produce"))
	})

	t.Run("both scope token valid for produce", func(t *testing.T) {
		assert.Assert(t, rs.ValidateChannelToken("test-chan", bothRaw, "produce"))
	})

	t.Run("both scope token valid for consume", func(t *testing.T) {
		assert.Assert(t, rs.ValidateChannelToken("test-chan", bothRaw, "consume"))
	})

	t.Run("invalid token rejected", func(t *testing.T) {
		assert.Assert(t, !rs.ValidateChannelToken("test-chan", "invalid-token-value", "produce"))
	})

	t.Run("nonexistent channel returns false", func(t *testing.T) {
		assert.Assert(t, !rs.ValidateChannelToken("nonexistent", bothRaw, "produce"))
	})
}
