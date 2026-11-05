package crypto

import (
	"testing"

	"gotest.tools/v3/assert"
)

// F32: a v2 envelope is bound to its context; a ciphertext produced for one
// channel must not decrypt under another.
func TestEnvelopeV2ContextBinding(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	ctx := []byte("gohookbridge.channel.chan-a")
	plaintext := []byte(`{"body":"secret"}`)

	envelope, err := EncryptWithContext(plaintext, publicKey, ctx)
	assert.NilError(t, err)

	got, err := DecryptWithContext(envelope, privateKey, ctx)
	assert.NilError(t, err)
	assert.Equal(t, string(got), string(plaintext))

	// Wrong context must fail.
	_, err = DecryptWithContext(envelope, privateKey, []byte("gohookbridge.channel.chan-b"))
	assert.Assert(t, err != nil)

	// No context must fail too.
	_, err = DecryptWithContext(envelope, privateKey, nil)
	assert.Assert(t, err != nil)
}

// F32: the legacy v1 envelope and the v1 wrappers keep working.
func TestV1BackwardCompat(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	plaintext := []byte(`{"legacy":true}`)
	envelope, err := Encrypt(plaintext, publicKey)
	assert.NilError(t, err)

	got, err := Decrypt(envelope, privateKey)
	assert.NilError(t, err)
	assert.Equal(t, string(got), string(plaintext))

	got, err = DecryptWithContext(envelope, privateKey, nil)
	assert.NilError(t, err)
	assert.Equal(t, string(got), string(plaintext))

	assert.Assert(t, IsEncrypted(envelope))
}
