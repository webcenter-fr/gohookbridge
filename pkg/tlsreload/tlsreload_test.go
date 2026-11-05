package tlsreload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func writeCert(t *testing.T, dir, cn string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{cn},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	assert.NilError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	assert.NilError(t, err)

	certPath = filepath.Join(dir, cn+".crt")
	keyPath = filepath.Join(dir, cn+".key")
	assert.NilError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	assert.NilError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certPath, keyPath
}

// F13/F14: rotating the files on disk swaps the served certificate without a
// restart.
func TestReloadOnRotation(t *testing.T) {
	dir := t.TempDir()

	firstCrt, firstKey := writeCert(t, dir, "first.example")
	caPath := filepath.Join(dir, "ca.crt")
	caBytes, err := os.ReadFile(firstCrt)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(caPath, caBytes, 0o600))

	// Serve the first cert under the canonical names.
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	copyFile(t, firstCrt, certPath)
	copyFile(t, firstKey, keyPath)

	p := NewProvider(certPath, keyPath, caPath, 10*time.Millisecond)
	assert.NilError(t, p.Load())

	got, err := p.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, got.Leaf.Subject.CommonName, "first.example")

	// Rotate to a new certificate and force a distinct mtime.
	time.Sleep(20 * time.Millisecond)
	secondCrt, secondKey := writeCert(t, dir, "second.example")
	copyFile(t, secondCrt, certPath)
	copyFile(t, secondKey, keyPath)
	secondCA, err := os.ReadFile(secondCrt)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(caPath, secondCA, 0o600))

	p.reload()

	got, err = p.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, got.Leaf.Subject.CommonName, "second.example")
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(dst, data, 0o600))
}

// Invalid replacement material must not clobber the currently-served cert.
func TestInvalidReplacementKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	crt, key := writeCert(t, dir, "good.example")
	caPath := filepath.Join(dir, "ca.crt")
	caBytes, err := os.ReadFile(crt)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(caPath, caBytes, 0o600))

	p := NewProvider(crt, key, caPath, 10*time.Millisecond)
	assert.NilError(t, p.Load())

	time.Sleep(20 * time.Millisecond)
	assert.NilError(t, os.WriteFile(key, []byte("not a key"), 0o600))
	p.reload()

	got, err := p.GetCertificate(nil)
	assert.NilError(t, err)
	assert.Equal(t, got.Leaf.Subject.CommonName, "good.example")
}
