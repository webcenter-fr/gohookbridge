package repository

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/disaster37/goca"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// nodeCertSafetyMargin is how close a leaf cert may be to expiry before it is
// re-issued at startup.
const nodeCertSafetyMargin = 7 * 24 * time.Hour

// RaftTLSConfig is the TLS subset of RaftConfig, passed to the builder.
type RaftTLSConfig struct {
	Enabled      bool
	Dir          string        // default <raft-dir>/tls
	Validity     time.Duration // default 8760h (1y)
	Organization string        // default "gohookbridge-raft"
	CACertPath   string        // manual mode: CA cert file
	CertPath     string        // manual mode: leaf cert file
	KeyPath      string        // manual mode: leaf key file
	CASecret     string        // auto/K8s mode: K8s Secret name for CA sharing
	CABootstrap  bool          // auto/K8s mode: this node generates+writes the CA
	ClientAuth   bool          // default true (mTLS)
	// SecretPollTimeout bounds the non-bootstrap nodes' poll for the CA Secret
	// (mirrors raft.leader_wait_timeout). 0 = 30s default.
	SecretPollTimeout time.Duration
}

// raftTLSMaterial is the loaded CA pool + leaf certificate used to build the
// transport tls.Config.
type raftTLSMaterial struct {
	caPool *x509.CertPool
	leaf   tls.Certificate
}

// LoadOrBuildRaftTLS prepares the raft TLS material per the selected mode:
//   - manual: load CA + leaf from the configured paths.
//   - auto/K8s: ensure the CA Secret exists (bootstrap node creates it via
//     goca), read it, then issue/reuse this node's leaf at <dir>/node.*.
//   - local-only: generate CA + leaf locally (single-node only).
//
// Returns the tls.Config for the raft StreamLayer, or nil when disabled.
func LoadOrBuildRaftTLS(
	cfg *RaftTLSConfig,
	isMultiNode bool,
	clientset kubernetes.Interface,
	namespace string,
	dnsNames []string,
	ipAddrs []net.IP,
	commonName string,
	logger *log.Logger,
) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Validity == 0 {
		cfg.Validity = 8760 * time.Hour
	}
	if cfg.Organization == "" {
		cfg.Organization = "gohookbridge-raft"
	}

	// Manual mode: operator pre-provisions CA + leaf PEM files.
	if cfg.CACertPath != "" || cfg.CertPath != "" || cfg.KeyPath != "" {
		return loadManualRaftTLS(cfg)
	}

	// Local-only mode cannot form a multi-node cluster: there is no way to
	// share the CA across pods. Check before generating a local CA so we do
	// not write throwaway material to disk.
	if (clientset == nil || cfg.CASecret == "") && isMultiNode {
		return nil, fmt.Errorf("raft TLS auto-mode requires K8s or manual CA files for multi-node")
	}

	caCertPEM, caKeyPEM, err := ensureRaftCA(cfg, clientset, namespace, logger)
	if err != nil {
		return nil, err
	}

	ca, err := NewMintingCAFromPEM(caCertPEM, caKeyPEM, cfg.Validity)
	if err != nil {
		return nil, fmt.Errorf("raft TLS: load CA: %w", err)
	}

	leafCertPEM, leafKeyPEM, err := issueOrReuseNodeCert(cfg, ca, commonName, cfg.Organization, dnsNames, ipAddrs)
	if err != nil {
		return nil, err
	}

	m, err := buildRaftTLSMaterial(caCertPEM, leafCertPEM, leafKeyPEM)
	if err != nil {
		return nil, err
	}
	return buildRaftTLSConfig(m, cfg.ClientAuth), nil
}

// loadManualRaftTLS loads CA + leaf from the configured manual paths.
func loadManualRaftTLS(cfg *RaftTLSConfig) (*tls.Config, error) {
	if cfg.CACertPath == "" || cfg.CertPath == "" || cfg.KeyPath == "" {
		return nil, fmt.Errorf("raft TLS: ca_cert/cert/key must all be set together")
	}
	caCertPEM, err := readPEM(cfg.CACertPath, "CA cert")
	if err != nil {
		return nil, err
	}
	leafCertPEM, err := readPEM(cfg.CertPath, "leaf cert")
	if err != nil {
		return nil, err
	}
	leafKeyPEM, err := readPEM(cfg.KeyPath, "leaf key")
	if err != nil {
		return nil, err
	}
	m, err := buildRaftTLSMaterial(caCertPEM, leafCertPEM, leafKeyPEM)
	if err != nil {
		return nil, err
	}
	return buildRaftTLSConfig(m, cfg.ClientAuth), nil
}

// ensureRaftCA returns the CA cert+key PEM, creating it on the bootstrap node
// and sharing via a K8s Secret, or loading from disk in manual/local mode.
func ensureRaftCA(
	cfg *RaftTLSConfig,
	clientset kubernetes.Interface,
	namespace string,
	logger *log.Logger,
) (caCertPEM, caKeyPEM []byte, err error) {
	if clientset != nil && cfg.CASecret != "" {
		return ensureRaftCAFromSecret(cfg, clientset, namespace, logger)
	}
	return ensureLocalRaftCA(cfg)
}

// ensureRaftCAFromSecret shares the CA via a K8s Secret: the bootstrap node
// generates + writes it, other nodes poll until it appears.
func ensureRaftCAFromSecret(cfg *RaftTLSConfig, clientset kubernetes.Interface, namespace string, logger *log.Logger) (caCertPEM, caKeyPEM []byte, err error) {
	if cfg.CABootstrap {
		certPEM, keyPEM, err := createRaftCAWithGoca("gohookbridge-raft-ca", cfg.Organization)
		if err != nil {
			return nil, nil, err
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: cfg.CASecret, Namespace: namespace},
			Data: map[string][]byte{
				"ca.crt": certPEM,
				"ca.key": keyPEM,
			},
		}
		if _, err := clientset.CoreV1().Secrets(namespace).Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return nil, nil, fmt.Errorf("raft TLS: create CA secret %s: %w", cfg.CASecret, err)
			}
			// The Secret already exists (e.g. the bootstrap pod restarted).
			// Reuse the existing CA rather than overwriting it, so every peer
			// keeps trusting the same internal CA across restarts.
			tlsLogf(logger, "raft TLS: CA secret %s already exists; reusing existing CA", cfg.CASecret)
			return readRaftCASecret(clientset, namespace, cfg.CASecret)
		}
		tlsLogf(logger, "raft TLS: generated and shared internal CA via Secret %s", cfg.CASecret)
		return certPEM, keyPEM, nil
	}

	timeout := cfg.SecretPollTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		certPEM, keyPEM, err := readRaftCASecret(clientset, namespace, cfg.CASecret)
		if err == nil {
			return certPEM, keyPEM, nil
		}
		if !apierrors.IsNotFound(err) {
			// A hard error (RBAC denied, malformed secret) should not be
			// retried as if the secret had not appeared yet.
			return nil, nil, fmt.Errorf("raft TLS: read CA secret %s: %w", cfg.CASecret, err)
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("raft TLS: CA secret %s did not appear within %s: %w", cfg.CASecret, timeout, err)
		}
		tlsLogf(logger, "raft TLS: waiting for CA secret %s", cfg.CASecret)
		time.Sleep(500 * time.Millisecond)
	}
}

// readRaftCASecret fetches the CA cert+key from the shared K8s Secret and
// validates that both keys are present.
func readRaftCASecret(clientset kubernetes.Interface, namespace, name string) (caCertPEM, caKeyPEM []byte, err error) {
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	certPEM, okCert := secret.Data["ca.crt"]
	keyPEM, okKey := secret.Data["ca.key"]
	if !okCert || !okKey {
		return nil, nil, fmt.Errorf("raft TLS: CA secret %s is missing ca.crt/ca.key keys", name)
	}
	return certPEM, keyPEM, nil
}

// ensureTLSDir creates the TLS material directory with 0700 permissions,
// tightening an existing directory (e.g. a mounted PVC) if needed.
func ensureTLSDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// #nosec G302 -- dir is a directory; 0700 is more restrictive than the 0750 directory threshold.
	return os.Chmod(dir, 0o700)
}

// ensureLocalRaftCA loads/creates the CA files under <dir> for the local-only
// (single-node) mode.
func ensureLocalRaftCA(cfg *RaftTLSConfig) (caCertPEM, caKeyPEM []byte, err error) {
	if err := ensureTLSDir(cfg.Dir); err != nil {
		return nil, nil, fmt.Errorf("raft TLS: mkdir %s: %w", cfg.Dir, err)
	}
	certPath := filepath.Join(cfg.Dir, "ca.crt")
	keyPath := filepath.Join(cfg.Dir, "ca.key")
	if fileExists(certPath) && fileExists(keyPath) {
		certPEM, err := readPEM(certPath, "CA cert")
		if err != nil {
			return nil, nil, err
		}
		keyPEM, err := readPEM(keyPath, "CA key")
		if err != nil {
			return nil, nil, err
		}
		return certPEM, keyPEM, nil
	}

	certPEM, keyPEM, err := createRaftCAWithGoca("gohookbridge-raft-ca", cfg.Organization)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, certPEM, "CA cert"); err != nil {
		return nil, nil, err
	}
	if err := writePEM(keyPath, keyPEM, "CA key"); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// readPEM reads a PEM file at path, wrapping failures with the labeled context.
func readPEM(path, what string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // paths from config
	if err != nil {
		return nil, fmt.Errorf("raft TLS: read %s: %w", what, err)
	}
	return data, nil
}

// writePEM writes data to path with 0600 permissions, wrapping failures with
// the labeled context.
func writePEM(path string, data []byte, what string) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("raft TLS: write %s: %w", what, err)
	}
	return nil
}

// createRaftCAWithGoca builds a new self-signed raft CA via goca and returns
// its PEM encoding.
func createRaftCAWithGoca(name, organization string) (certPEM, keyPEM []byte, err error) {
	ca, err := goca.New(name, goca.Identity{
		Organization:       organization,
		OrganizationalUnit: "engineering",
		Country:            "US",
		Locality:           "San Francisco",
		Province:           "California",
		Valid:              3650, // 10 years
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create raft CA: %w", err)
	}
	return []byte(ca.GetCertificate()), []byte(ca.GetPrivateKey()), nil
}

// issueOrReuseNodeCert issues a per-node leaf cert signed by the CA, persisted
// at <dir>/node.crt + node.key (0600). A persisted cert is reused only if it is
// still valid for the CURRENT relationship it protects:
//   - not within the expiry safety margin (and not yet valid — clock skew),
//   - signed by the current CA (the Secret may have been re-created, in which
//     case a stale cert would be rejected by every peer — CWE-295),
//   - the CN matches and the cert still COVERS the SAN set that would be
//     issued now (a superset is fine).
//
// On any mismatch the leaf is re-issued under the current CA so peers always
// agree on the trust chain after CA recreation or URI changes.
func issueOrReuseNodeCert(
	cfg *RaftTLSConfig,
	ca *MintingCA,
	commonName, organization string,
	dnsNames []string,
	ipAddrs []net.IP,
) (certPEM, keyPEM []byte, err error) {
	if err := ensureTLSDir(cfg.Dir); err != nil {
		return nil, nil, fmt.Errorf("raft TLS: mkdir %s: %w", cfg.Dir, err)
	}
	certPath := filepath.Join(cfg.Dir, "node.crt")
	keyPath := filepath.Join(cfg.Dir, "node.key")

	if fileExists(certPath) && fileExists(keyPath) {
		existingCert, err := readPEM(certPath, "node cert")
		if err == nil {
			if cert, parseErr := parsePEMCert(existingCert); parseErr == nil &&
				reusableNodeCert(cert, ca, commonName, dnsNames, ipAddrs) {
				key, keyErr := readPEM(keyPath, "node key")
				if keyErr == nil {
					return existingCert, key, nil
				}
			}
		}
	}

	certPEM, keyPEM, err = ca.IssuePeerCertificate(commonName, organization, dnsNames, ipAddrs, cfg.Validity)
	if err != nil {
		return nil, nil, fmt.Errorf("raft TLS: issue node cert: %w", err)
	}
	if err := writePEM(certPath, certPEM, "node cert"); err != nil {
		return nil, nil, err
	}
	if err := writePEM(keyPath, keyPEM, "node key"); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// reusableNodeCert reports whether a persisted leaf may be reused as the raft
// transport certificate: valid window, signed by the current CA, matching CN,
// and covering the required DNS/IP SAN set (extras allowed).
func reusableNodeCert(cert *x509.Certificate, ca *MintingCA, commonName string, dnsNames []string, ipAddrs []net.IP) bool {
	now := time.Now()
	if cert.NotBefore.After(now) || time.Until(cert.NotAfter) <= nodeCertSafetyMargin {
		return false
	}
	caCert := ca.CACertificate()
	if caCert == nil || cert.CheckSignatureFrom(caCert) != nil {
		return false
	}
	if cert.Subject.CommonName != commonName {
		return false
	}
	return coversSANs(cert, dnsNames, ipAddrs)
}

// coversSANs reports whether the cert's SANs cover every required name/ip
// (superset allowed: extra DNS names or IPs on the cert are ignored, they only
// name this same pod).
func coversSANs(cert *x509.Certificate, dnsNames []string, ipAddrs []net.IP) bool {
	certDNS := make(map[string]struct{}, len(cert.DNSNames))
	for _, n := range cert.DNSNames {
		certDNS[n] = struct{}{}
	}
	for _, n := range dnsNames {
		if _, ok := certDNS[n]; !ok {
			return false
		}
	}

	for _, want := range normalizeIPs(ipAddrs) {
		found := false
		for _, have := range cert.IPAddresses {
			if have.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// normalizeIPs dedupes and canonicalizes an IP slice (16-byte form).
func normalizeIPs(in []net.IP) []net.IP {
	out := make([]net.IP, 0, len(in))
	for _, ip := range in {
		dup := false
		for _, existing := range out {
			if existing.Equal(ip) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, ip)
		}
	}
	return out
}

// buildRaftTLSMaterial assembles the CA pool + leaf tls.Certificate.
func buildRaftTLSMaterial(caCertPEM, leafCertPEM, leafKeyPEM []byte) (*raftTLSMaterial, error) {
	leaf, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("raft TLS: parse leaf key pair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("raft TLS: no CA cert found in CA PEM")
	}
	return &raftTLSMaterial{caPool: pool, leaf: leaf}, nil
}

// buildRaftTLSConfig assembles the *tls.Config for the raft StreamLayer.
func buildRaftTLSConfig(m *raftTLSMaterial, clientAuth bool) *tls.Config {
	clientAuthType := tls.NoClientCert
	if clientAuth {
		clientAuthType = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{
		Certificates: []tls.Certificate{m.leaf},
		RootCAs:      m.caPool,
		ClientCAs:    m.caPool,
		ClientAuth:   clientAuthType,
		MinVersion:   tls.VersionTLS12,
	}
}

// parsePEMCert parses the first CERTIFICATE PEM block.
func parsePEMCert(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// fileExists reports whether path exists (any error other than not-exist is
// treated as existing so callers fail loudly on read).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// tlsLogf logs a formatted message when a logger is configured.
func tlsLogf(logger *log.Logger, format string, args ...any) {
	if logger != nil {
		logger.Printf(format, args...)
	}
}

// MintingCA wraps a goca CA for raft peer certificate issuance.
type MintingCA struct {
	gocaCA        *goca.CA
	clientCertTTL time.Duration
}

// NewMintingCAFromPEM loads an existing CA from PEM-encoded certificate and
// private key.
func NewMintingCAFromPEM(certPEM, keyPEM []byte, clientCertTTL time.Duration) (*MintingCA, error) {
	ca := &goca.CA{}
	if err := ca.LoadCAFromPEM(certPEM, keyPEM); err != nil {
		fixedKey, fixErr := fixPKCS1KeyWithWrongHeader(keyPEM)
		if fixErr != nil {
			return nil, fmt.Errorf("load minting CA from PEM: %w", err)
		}
		if err2 := ca.LoadCAFromPEM(certPEM, fixedKey); err2 != nil {
			return nil, fmt.Errorf("load minting CA from PEM: %w", err)
		}
	}
	return &MintingCA{gocaCA: ca, clientCertTTL: clientCertTTL}, nil
}

func fixPKCS1KeyWithWrongHeader(keyPEM []byte) ([]byte, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("decode key PEM")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("key PEM type is %q, not PKCS#8", block.Type)
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return nil, fmt.Errorf("key is valid PKCS#8")
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return nil, fmt.Errorf("key is neither PKCS#1 nor PKCS#8: %w", err)
	}
	fixed := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: block.Bytes})
	return fixed, nil
}

// CACertificate returns the parsed CA certificate.
func (ca *MintingCA) CACertificate() *x509.Certificate {
	return ca.gocaCA.GoCertificate()
}

// IssuePeerCertificate signs a TLS mTLS peer certificate with DNS + IP SANs.
// The certificate is backdated 5 minutes for clock-skew tolerance.
func (ca *MintingCA) IssuePeerCertificate(commonName, organization string, dnsNames []string, ipAddrs []net.IP, ttl time.Duration) (certPEM, keyPEM []byte, err error) {
	cert, err := ca.gocaCA.IssueCertificate(commonName, goca.Identity{
		Organization:       organization,
		OrganizationalUnit: "engineering",
		Country:            "US",
		Locality:           "San Francisco",
		Province:           "California",
		Type:               "server-client",
		ValidDuration:      ttl,
		Backdate:           5 * time.Minute,
		DNSNames:           dnsNames,
		IPAddresses:        ipAddrs,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("issue peer cert: %w", err)
	}
	return []byte(cert.Certificate), []byte(cert.PrivateKey), nil
}
