package uuid

import (
	"crypto/rand"
	"fmt"
)

// GenerateUUID returns a random RFC 4122 version 4 UUID as a string.
func GenerateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// MustGenerateUUID is like GenerateUUID but panics on failure. Use it only at
// call sites that cannot propagate an error (e.g. a helper that must return a
// plain string); prefer GenerateUUID everywhere else.
func MustGenerateUUID() string {
	id, err := GenerateUUID()
	if err != nil {
		panic("uuid: crypto/rand failed: " + err.Error())
	}
	return id
}
