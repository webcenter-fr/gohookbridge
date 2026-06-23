package gohookbridge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type aesEncryptedPayload struct {
	Encrypted  bool   `json:"encrypted"`
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func GenerateAESKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate AES key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func AESEncrypt(plaintext []byte, keyBase64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode AES key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("AES key must be 32 bytes (got %d)", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	ciphertext := aesgcm.Seal(nil, nonce, plaintext, nil)

	payload := aesEncryptedPayload{
		Encrypted:  true,
		Version:    1,
		Algorithm:  "AES-256-GCM",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}

	return json.Marshal(payload)
}

func AESDecrypt(data []byte, keyBase64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode AES key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("AES key must be 32 bytes (got %d)", len(key))
	}

	var payload aesEncryptedPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal encrypted payload: %w", err)
	}

	if !payload.Encrypted {
		return nil, fmt.Errorf("payload is not encrypted")
	}
	if payload.Algorithm != "AES-256-GCM" {
		return nil, fmt.Errorf("unsupported algorithm: %s", payload.Algorithm)
	}
	if payload.Version != 1 {
		return nil, fmt.Errorf("unsupported version: %d", payload.Version)
	}

	nonce, err := base64.StdEncoding.DecodeString(payload.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	if len(nonce) != 12 {
		return nil, fmt.Errorf("invalid nonce length: %d", len(nonce))
	}

	ciphertext, err := base64.StdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w", err)
	}

	return plaintext, nil
}

func IsAESEncrypted(data []byte) bool {
	var p struct {
		Encrypted bool   `json:"encrypted"`
		Algorithm string `json:"algorithm"`
	}
	return json.Unmarshal(data, &p) == nil && p.Encrypted && p.Algorithm == "AES-256-GCM"
}
