package encryption

import (
	"testing"

	"gotest.tools/v3/assert"
)

// F32: AES-256-GCM binds its AAD, so a ciphertext is not interchangeable
// across channels.
func TestAADBinding(t *testing.T) {
	key, err := GenerateAESKey()
	assert.NilError(t, err)

	aad := []byte("gohookbridge.channel.chan-a")
	payload, err := AESEncryptWithContext([]byte(`{"k":"v"}`), key, aad)
	assert.NilError(t, err)

	got, err := AESDecryptWithContext(payload, key, aad)
	assert.NilError(t, err)
	assert.Equal(t, string(got), `{"k":"v"}`)

	// Wrong AAD must fail authentication.
	_, err = AESDecryptWithContext(payload, key, []byte("gohookbridge.channel.chan-b"))
	assert.Assert(t, err != nil)

	// Legacy nil-AAD decrypt must fail on an AAD-bound payload.
	_, err = AESDecrypt(payload, key)
	assert.Assert(t, err != nil)
}
