package repository

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// atRestContext is the AAD bound to every at-rest ciphertext.
const atRestContext = "gohookbridge.at-rest.v1"

// EncryptedValue is the JSON envelope of one at-rest encrypted value.
type EncryptedValue struct {
	KID string `json:"kid"` // key ID
	Alg string `json:"alg"` // "A256GCM"
	Ver int    `json:"ver"` // 1
	Non string `json:"non"` // base64 12-byte nonce
	Ctx string `json:"ctx"` // base64 AAD
	Ct  string `json:"ct"`  // base64 ciphertext
}

// CryptoKeeper holds the at-rest encryption key set.
type CryptoKeeper struct {
	keys    map[string][]byte
	primary string
}

type cryptoKeyFile struct {
	Primary string            `json:"primary"`
	Keys    map[string]string `json:"keys"`
}

// LoadCryptoKeeper loads {"primary":"k1","keys":{"k1":"<b64 32B>"}}.
func LoadCryptoKeeper(keyFile string) (*CryptoKeeper, error) {
	data, err := os.ReadFile(filepath.Clean(keyFile))
	if err != nil {
		return nil, fmt.Errorf("read at-rest key file: %w", err)
	}
	var parsed cryptoKeyFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse at-rest key file: %w", err)
	}
	if parsed.Primary == "" || len(parsed.Keys) == 0 {
		return nil, fmt.Errorf("at-rest key file must define primary and keys")
	}
	keys := make(map[string][]byte, len(parsed.Keys))
	for kid, b64 := range parsed.Keys {
		key, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode key %q: %w", kid, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("key %q must be 32 bytes (got %d)", kid, len(key))
		}
		keys[kid] = key
	}
	if _, ok := keys[parsed.Primary]; !ok {
		return nil, fmt.Errorf("primary key %q not present in keys", parsed.Primary)
	}
	return &CryptoKeeper{keys: keys, primary: parsed.Primary}, nil
}

// NewCryptoKeeper builds a keeper from raw 32-byte keys (used by tests and the
// dev auto-generator).
func NewCryptoKeeper(primary string, keys map[string][]byte) *CryptoKeeper {
	return &CryptoKeeper{keys: keys, primary: primary}
}

// Encrypt seals plaintext under the primary key.
func (k *CryptoKeeper) Encrypt(plain []byte) (EncryptedValue, error) {
	key, ok := k.keys[k.primary]
	if !ok {
		return EncryptedValue{}, fmt.Errorf("primary at-rest key %q missing", k.primary)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return EncryptedValue{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedValue{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return EncryptedValue{}, err
	}
	ct := gcm.Seal(nil, nonce, plain, []byte(atRestContext))
	return EncryptedValue{
		KID: k.primary,
		Alg: "A256GCM",
		Ver: 1,
		Non: base64.StdEncoding.EncodeToString(nonce),
		Ctx: base64.StdEncoding.EncodeToString([]byte(atRestContext)),
		Ct:  base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// Decrypt opens an EncryptedValue using its key ID (rotation-safe).
func (k *CryptoKeeper) Decrypt(ev EncryptedValue) ([]byte, error) {
	if ev.Alg != "A256GCM" || ev.Ver != 1 {
		return nil, fmt.Errorf("unsupported at-rest envelope alg=%q ver=%d", ev.Alg, ev.Ver)
	}
	key, ok := k.keys[ev.KID]
	if !ok {
		return nil, fmt.Errorf("unknown at-rest key %q", ev.KID)
	}
	nonce, err := base64.StdEncoding.DecodeString(ev.Non)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(ev.Ct)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	ctx, err := base64.StdEncoding.DecodeString(ev.Ctx)
	if err != nil {
		return nil, fmt.Errorf("decode context: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, ctx)
	if err != nil {
		return nil, fmt.Errorf("decrypt at-rest value: %w", err)
	}
	return plain, nil
}

// GenerateKeyFile writes a fresh single-key at-rest key file (dev bootstrap).
func GenerateKeyFile(path string) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	doc := cryptoKeyFile{Primary: "k1", Keys: map[string]string{"k1": base64.StdEncoding.EncodeToString(key)}}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
