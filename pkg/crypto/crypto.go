package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/nacl/box"
)

const (
	// encryptedEnvelopeVersion is the legacy, context-free envelope. It is
	// still accepted during the compatibility window but is deprecated.
	encryptedEnvelopeVersion = 1
	// encryptedEnvelopeVersionV2 binds the ciphertext to a caller-supplied
	// context (e.g. the channel ID) so a ciphertext cannot be replayed across
	// channels.
	encryptedEnvelopeVersionV2 = 2
	// contextSeparator delimits the bound context from the plaintext.
	contextSeparator = 0x00
)

type encryptedEnvelope struct {
	Encrypted  bool   `json:"encrypted"`
	Version    int    `json:"version"`
	Ephemeral  string `json:"epk"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Ctx        string `json:"ctx,omitempty"`
}

type storedKeyPair struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func GenerateKeyPair() (*[32]byte, *[32]byte, error) {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	return publicKey, privateKey, nil
}

func SaveKeyPair(path string, publicKey, privateKey *[32]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	stored := storedKeyPair{
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey[:]),
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey[:]),
	}
	encoded, err := json.Marshal(stored) //nolint:gosec // intentionally marshaling key pair for storage
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(encoded); err != nil {
		return err
	}
	return nil
}

func LoadKeyPair(path string) (*[32]byte, *[32]byte, error) {
	if path == "" {
		return nil, nil, fmt.Errorf("key file path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	return loadKeyPair(data)
}

func LoadPrivateKey(path string) (*[32]byte, error) {
	_, priv, err := LoadKeyPair(path)
	return priv, err
}

func EncodePublicKey(key *[32]byte) string {
	return base64.RawURLEncoding.EncodeToString(key[:])
}

func ParsePublicKey(encoded string) (*[32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}

	return bytesToKey(decoded)
}

// Encrypt produces the legacy (v1) context-free envelope. Kept for backward
// compatibility; new code should use EncryptWithContext.
func Encrypt(plaintext []byte, recipientPubKey *[32]byte) ([]byte, error) {
	return seal(plaintext, recipientPubKey, nil, encryptedEnvelopeVersion)
}

// EncryptWithContext produces a v2 envelope bound to ctx: it seals
// ctx||0x00||plaintext and records the context. A ciphertext produced for one
// context cannot be decrypted with another.
func EncryptWithContext(plaintext []byte, recipientPubKey *[32]byte, ctx []byte) ([]byte, error) {
	return seal(plaintext, recipientPubKey, ctx, encryptedEnvelopeVersionV2)
}

func seal(plaintext []byte, recipientPubKey *[32]byte, ctx []byte, version int) ([]byte, error) {
	ephemeralPubKey, ephemeralPrivKey, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}

	payload := plaintext
	if version == encryptedEnvelopeVersionV2 {
		payload = make([]byte, 0, len(ctx)+1+len(plaintext))
		payload = append(payload, ctx...)
		payload = append(payload, contextSeparator)
		payload = append(payload, plaintext...)
	}

	ciphertext := box.Seal(nil, payload, &nonce, recipientPubKey, ephemeralPrivKey)
	envelope := encryptedEnvelope{
		Encrypted:  true,
		Version:    version,
		Ephemeral:  base64.StdEncoding.EncodeToString(ephemeralPubKey[:]),
		Nonce:      base64.StdEncoding.EncodeToString(nonce[:]),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	if version == encryptedEnvelopeVersionV2 {
		envelope.Ctx = base64.StdEncoding.EncodeToString(ctx)
	}

	return json.Marshal(envelope)
}

// Decrypt decrypts a v1 or v2 envelope without binding context (v2 envelopes
// with a non-empty context are rejected: use DecryptWithContext).
func Decrypt(envelope []byte, privateKey *[32]byte) ([]byte, error) {
	return DecryptWithContext(envelope, privateKey, nil)
}

// DecryptWithContext decrypts a v1 or v2 envelope. For v2 the envelope context
// must match ctx and the sealed context prefix is stripped.
func DecryptWithContext(envelope []byte, privateKey *[32]byte, ctx []byte) ([]byte, error) {
	parsed, ephemeralPubKey, nonce, ciphertext, err := parseEncryptedEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	if !parsed.Encrypted {
		return nil, fmt.Errorf("payload is not encrypted")
	}

	plaintext, ok := box.Open(nil, ciphertext, nonce, ephemeralPubKey, privateKey)
	if !ok {
		return nil, fmt.Errorf("decrypt payload: invalid ciphertext")
	}

	if parsed.Version == encryptedEnvelopeVersionV2 {
		envelopeCtx, err := base64.StdEncoding.DecodeString(parsed.Ctx)
		if err != nil {
			return nil, fmt.Errorf("decode envelope context: %w", err)
		}
		if !bytes.Equal(envelopeCtx, ctx) {
			return nil, fmt.Errorf("envelope context mismatch")
		}
		prefix := make([]byte, 0, len(ctx)+1)
		prefix = append(prefix, ctx...)
		prefix = append(prefix, contextSeparator)
		if !bytes.HasPrefix(plaintext, prefix) {
			return nil, fmt.Errorf("invalid envelope context prefix")
		}
		plaintext = plaintext[len(prefix):]
	}

	return plaintext, nil
}

// ChannelContext returns the AEAD context bound to a channel. It is shared by
// the server (NaCl envelope v2 and AES-GCM AAD) and the client so both sides
// derive the same context.
func ChannelContext(channel string) []byte {
	return []byte("gohookbridge.channel." + channel)
}

func IsEncrypted(data []byte) bool {
	var e struct {
		Encrypted  bool   `json:"encrypted"`
		Ephemeral  string `json:"epk"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}
	return json.Unmarshal(data, &e) == nil && e.Encrypted && e.Ephemeral != "" && e.Nonce != "" && e.Ciphertext != ""
}

func loadKeyPair(data []byte) (*[32]byte, *[32]byte, error) {
	var stored storedKeyPair
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, nil, fmt.Errorf("unmarshal key file: %w", err)
	}

	publicKey, err := decodeStdKey(stored.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decode public key: %w", err)
	}
	privateKey, err := decodeStdKey(stored.PrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decode private key: %w", err)
	}

	return publicKey, privateKey, nil
}

func decodeStdKey(encoded string) (*[32]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	return bytesToKey(decoded)
}

func bytesToKey(decoded []byte) (*[32]byte, error) {
	if len(decoded) != 32 {
		return nil, fmt.Errorf("invalid key length %d", len(decoded))
	}

	var key [32]byte
	copy(key[:], decoded)
	return &key, nil
}

func parseEncryptedEnvelope(data []byte) (encryptedEnvelope, *[32]byte, *[24]byte, []byte, error) {
	var envelope encryptedEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return encryptedEnvelope{}, nil, nil, nil, err
	}
	if !envelope.Encrypted {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("missing encrypted marker")
	}
	if envelope.Version != encryptedEnvelopeVersion && envelope.Version != encryptedEnvelopeVersionV2 {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("unsupported envelope version %d", envelope.Version)
	}
	if envelope.Ephemeral == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("incomplete encrypted envelope")
	}

	ephemeralBytes, err := base64.StdEncoding.DecodeString(envelope.Ephemeral)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode ephemeral key: %w", err)
	}
	ephemeralKey, err := bytesToKey(ephemeralBytes)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode ephemeral key: %w", err)
	}

	nonceBytes, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode nonce: %w", err)
	}
	if len(nonceBytes) != 24 {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("invalid nonce length %d", len(nonceBytes))
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)

	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return encryptedEnvelope{}, nil, nil, nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	return envelope, ephemeralKey, &nonce, ciphertext, nil
}
