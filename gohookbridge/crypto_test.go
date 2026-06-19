package gohookbridge

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	plaintext := []byte(`{"body":"hello"}`)
	encrypted, err := Encrypt(plaintext, publicKey)
	assert.NilError(t, err)
	assert.Assert(t, IsEncrypted(encrypted))

	decrypted, err := Decrypt(encrypted, privateKey)
	assert.NilError(t, err)
	assert.DeepEqual(t, decrypted, plaintext)
}

func TestDecryptWrongKeyFails(t *testing.T) {
	publicKey, _, err := GenerateKeyPair()
	assert.NilError(t, err)
	_, wrongPrivateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	encrypted, err := Encrypt([]byte(`{"body":"secret"}`), publicKey)
	assert.NilError(t, err)

	_, err = Decrypt(encrypted, wrongPrivateKey)
	assert.Assert(t, err != nil)
}

func TestIsEncryptedRejectsLookalikePayloads(t *testing.T) {
	assert.Assert(t, !IsEncrypted([]byte(`{"encrypted":true}`)))
	assert.Assert(t, !IsEncrypted([]byte(`{"encrypted":true,"version":99}`)))
	assert.Assert(t, !IsEncrypted([]byte(`{"body":{"encrypted":true}}`)))
}

func TestSaveAndLoadKeyPair(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "keys.json")

	publicKey, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)
	assert.NilError(t, SaveKeyPair(keyPath, publicKey, privateKey))

	info, err := os.Stat(keyPath)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600))

	loadedPublicKey, loadedPrivateKey, err := LoadKeyPair(keyPath)
	assert.NilError(t, err)
	assert.DeepEqual(t, loadedPublicKey, publicKey)
	assert.DeepEqual(t, loadedPrivateKey, privateKey)
}

func TestEncryptDecryptSharedKeypair(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	plaintext := []byte(`{"body":"shared keypair test"}`)
	encrypted, err := Encrypt(plaintext, publicKey)
	assert.NilError(t, err)

	decrypted, err := Decrypt(encrypted, privateKey)
	assert.NilError(t, err)
	assert.DeepEqual(t, decrypted, plaintext)
}

func TestMultipleProducersSameKeypair(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	messages := []string{
		`{"event":"push","repo":"owner/repo1"}`,
		`{"event":"pull_request","action":"opened"}`,
		`{"event":"deployment","env":"production"}`,
	}

	for _, msg := range messages {
		encrypted, err := Encrypt([]byte(msg), publicKey)
		assert.NilError(t, err)

		decrypted, err := Decrypt(encrypted, privateKey)
		assert.NilError(t, err)
		assert.Equal(t, string(decrypted), msg)
	}
}

func TestEncodePublicKeyRoundTrip(t *testing.T) {
	publicKey, _, err := GenerateKeyPair()
	assert.NilError(t, err)

	encoded := EncodePublicKey(publicKey)
	decoded, err := ParsePublicKey(encoded)
	assert.NilError(t, err)
	assert.DeepEqual(t, decoded, publicKey)
}

func TestLoadPrivateKeyOnly(t *testing.T) {
	_, privateKey, err := GenerateKeyPair()
	assert.NilError(t, err)

	keyPath := filepath.Join(t.TempDir(), "client-key.json")
	assert.NilError(t, SaveKeyPair(keyPath, privateKey, privateKey))

	loaded, err := LoadPrivateKey(keyPath)
	assert.NilError(t, err)
	assert.DeepEqual(t, loaded, privateKey)
}
