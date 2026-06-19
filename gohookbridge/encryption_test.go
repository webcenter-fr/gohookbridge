package gohookbridge

import (
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"
)

func TestAESEncryptDecrypt(t *testing.T) {
	key, err := GenerateAESKey()
	assert.NilError(t, err)
	assert.Assert(t, len(key) > 0)

	plaintext := []byte(`{"hello":"world","number":42}`)

	encrypted, err := AESEncrypt(plaintext, key)
	assert.NilError(t, err)
	assert.Assert(t, len(encrypted) > 0)
	assert.Assert(t, IsAESEncrypted(encrypted))

	var payload map[string]any
	err = json.Unmarshal(encrypted, &payload)
	assert.NilError(t, err)
	assert.Equal(t, payload["encrypted"], true)
	assert.Equal(t, payload["algorithm"], "AES-256-GCM")

	decrypted, err := AESDecrypt(encrypted, key)
	assert.NilError(t, err)
	assert.DeepEqual(t, decrypted, plaintext)
}

func TestAESDecryptWithWrongKey(t *testing.T) {
	key1, err := GenerateAESKey()
	assert.NilError(t, err)
	key2, err := GenerateAESKey()
	assert.NilError(t, err)

	plaintext := []byte(`{"test":"data"}`)
	encrypted, err := AESEncrypt(plaintext, key1)
	assert.NilError(t, err)

	_, err = AESDecrypt(encrypted, key2)
	assert.ErrorContains(t, err, "decrypt")
}

func TestAESEncryptDecryptEmptyData(t *testing.T) {
	key, err := GenerateAESKey()
	assert.NilError(t, err)

	encrypted, err := AESEncrypt([]byte{}, key)
	assert.NilError(t, err)

	decrypted, err := AESDecrypt(encrypted, key)
	assert.NilError(t, err)
	assert.Equal(t, len(decrypted), 0)
}

func TestIsAESEncrypted(t *testing.T) {
	assert.Assert(t, !IsAESEncrypted([]byte(`{"plain":true}`)))
	assert.Assert(t, !IsAESEncrypted([]byte(`{}`)))
	assert.Assert(t, !IsAESEncrypted([]byte(`not json`)))
}

func TestGenerateAESKey(t *testing.T) {
	key1, err := GenerateAESKey()
	assert.NilError(t, err)
	assert.Assert(t, len(key1) > 0)

	key2, err := GenerateAESKey()
	assert.NilError(t, err)
	assert.Assert(t, key1 != key2)
}
