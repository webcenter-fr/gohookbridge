package store

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"gotest.tools/v3/assert"
)

func TestHostAddr_String(t *testing.T) {
	a := hostAddr{host: "pod-0.headless.ns.svc", port: 6001}
	assert.Equal(t, a.Network(), "tcp")
	assert.Equal(t, a.String(), "pod-0.headless.ns.svc:6001")
}

func TestNewHostAddr(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		addr, err := newHostAddr("pod-0.headless.ns.svc:6001")
		assert.NilError(t, err)
		assert.Equal(t, addr.String(), "pod-0.headless.ns.svc:6001")
	})

	t.Run("missing port", func(t *testing.T) {
		_, err := newHostAddr("pod-0.headless.ns.svc")
		assert.Assert(t, err != nil)
	})

	t.Run("invalid port", func(t *testing.T) {
		_, err := newHostAddr("pod-0.headless.ns.svc:notaport")
		assert.Assert(t, err != nil)
	})
}

func TestIsConnRefused(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"refused", &net.OpError{Op: "dial", Err: testError("connect: connection refused")}, true},
		{"timeout", &net.OpError{Op: "dial", Err: testError("i/o timeout")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, isConnRefused(tc.err), tc.want)
		})
	}
}

type testError string

func (e testError) Error() string { return string(e) }

func TestRetryingStreamLayerDial_PlaintextReResolve(t *testing.T) {
	// Bind the IPv6 loopback: "localhost" resolves to ::1 first in this
	// environment, and the retrying layer dials the first resolved address.
	layer, err := newPlainStreamLayer("[::1]:0", nil)
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer func() { _ = layer.Close() }()

	stream := newRetryingStreamLayer(layer, time.Second, nil)

	accepted := make(chan error, 1)
	go func() {
		conn, err := layer.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		accepted <- err
	}()

	_, port, err := net.SplitHostPort(layer.Addr().String())
	assert.NilError(t, err)
	// Dial by DNS name (localhost) to exercise re-resolution.
	conn, err := stream.Dial(raft.ServerAddress(net.JoinHostPort("localhost", port)), time.Second)
	assert.NilError(t, err)
	_, err = conn.Write([]byte("ping"))
	assert.NilError(t, err)
	_ = conn.Close()
	assert.NilError(t, <-accepted)
}

func TestRetryingStreamLayerDial_TLS(t *testing.T) {
	cfg := testSelfSignedTLSConfig(t)
	layer, err := newTLSStreamLayer("[::1]:0", nil, cfg)
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer func() { _ = layer.Close() }()

	stream := newRetryingStreamLayer(layer, time.Second, cfg)

	accepted := make(chan error, 1)
	go func() {
		conn, err := layer.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		accepted <- err
	}()

	_, port, err := net.SplitHostPort(layer.Addr().String())
	assert.NilError(t, err)
	conn, err := stream.Dial(raft.ServerAddress(net.JoinHostPort("localhost", port)), time.Second)
	assert.NilError(t, err)
	_, err = conn.Write([]byte("ping"))
	assert.NilError(t, err)
	_ = conn.Close()
	assert.NilError(t, <-accepted)
}

func TestRetryingStreamLayerDial_RetriesOnRefused(t *testing.T) {
	// A closed port yields connection-refused, so the layer retries until the
	// budget is exhausted and returns a wrapped error.
	stream := newRetryingStreamLayer(&plainStreamLayer{listener: nil, advertise: nil}, time.Second, nil)
	_, err := stream.Dial(raft.ServerAddress("127.0.0.1:1"), 200*time.Millisecond)
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "retries exhausted")
}

// testSelfSignedTLSConfig builds a self-signed mTLS config for localhost.
func testSelfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NilError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	assert.NilError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	assert.NilError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	assert.NilError(t, err)
	pool := x509.NewCertPool()
	assert.Assert(t, pool.AppendCertsFromPEM(certPEM))
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
}
