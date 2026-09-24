package repository

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testRaftTLSCfg(dir string) *RaftTLSConfig {
	return &RaftTLSConfig{
		Enabled:      true,
		Dir:          dir,
		Validity:     8760 * time.Hour,
		Organization: "gohookbridge-raft",
		ClientAuth:   true,
	}
}

func testTLSLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestCreateRaftCAWithGoca(t *testing.T) {
	certPEM, keyPEM, err := createRaftCAWithGoca("test-ca", "test-org")
	assert.NilError(t, err)
	assert.Assert(t, len(certPEM) > 0 && len(keyPEM) > 0)

	cert, err := parsePEMCert(certPEM)
	assert.NilError(t, err)
	assert.Assert(t, cert.IsCA)
	assert.Equal(t, cert.Subject.Organization[0], "test-org")
}

func TestLoadOrBuildRaftTLS_Manual(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, err := createRaftCAWithGoca("ca", "org")
	assert.NilError(t, err)
	ca, err := NewMintingCAFromPEM(caCertPEM, caKeyPEM, time.Hour)
	assert.NilError(t, err)
	leafCertPEM, leafKeyPEM, err := ca.IssuePeerCertificate("node-0", "org", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	assert.NilError(t, err)

	caPath := filepath.Join(dir, "ca.crt")
	certPath := filepath.Join(dir, "node.crt")
	keyPath := filepath.Join(dir, "node.key")
	assert.NilError(t, os.WriteFile(caPath, caCertPEM, 0o600))
	assert.NilError(t, os.WriteFile(certPath, leafCertPEM, 0o600))
	assert.NilError(t, os.WriteFile(keyPath, leafKeyPEM, 0o600))

	cfg, err := LoadOrBuildRaftTLS(&RaftTLSConfig{
		Enabled:      true,
		Organization: "org",
		CACertPath:   caPath,
		CertPath:     certPath,
		KeyPath:      keyPath,
		ClientAuth:   true,
	}, false, nil, "", nil, nil, "node-0", testTLSLogger())
	assert.NilError(t, err)
	assert.Equal(t, cfg.MinVersion, uint16(tls.VersionTLS12))
	assert.Equal(t, cfg.ClientAuth, tls.RequireAndVerifyClientCert)
	assert.Equal(t, len(cfg.Certificates), 1)
}

func TestLoadOrBuildRaftTLS_ManualMissingFiles(t *testing.T) {
	_, err := LoadOrBuildRaftTLS(&RaftTLSConfig{Enabled: true, CACertPath: "/nonexistent/ca.crt"}, false, nil, "", nil, nil, "n", testTLSLogger())
	assert.Assert(t, err != nil)
}

func TestLoadOrBuildRaftTLS_LocalSingleNode(t *testing.T) {
	dir := t.TempDir()
	cfg := testRaftTLSCfg(dir)
	cn := "node-0"
	dns, ips := PodSANs(&RaftDiscoveryConfig{HeadlessService: "headless", Namespace: "ns", ClusterDomain: "cluster.local"}, cn)

	tlsCfg, err := LoadOrBuildRaftTLS(cfg, false, nil, "", dns, ips, cn, testTLSLogger())
	assert.NilError(t, err)
	assert.Assert(t, tlsCfg != nil)

	// Second call reuses the same leaf (files exist).
	firstCert, err := os.ReadFile(filepath.Join(dir, "node.crt"))
	assert.NilError(t, err)
	_, err = LoadOrBuildRaftTLS(cfg, false, nil, "", dns, ips, cn, testTLSLogger())
	assert.NilError(t, err)
	secondCert, err := os.ReadFile(filepath.Join(dir, "node.crt"))
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(firstCert, secondCert), "leaf cert should be reused")

	// Deleting node.crt forces re-issue.
	assert.NilError(t, os.Remove(filepath.Join(dir, "node.crt")))
	_, err = LoadOrBuildRaftTLS(cfg, false, nil, "", dns, ips, cn, testTLSLogger())
	assert.NilError(t, err)
	thirdCert, err := os.ReadFile(filepath.Join(dir, "node.crt"))
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Equal(firstCert, thirdCert), "leaf cert should be re-issued after deletion")
}

func TestLoadOrBuildRaftTLS_LocalMultiNodeRejected(t *testing.T) {
	cfg := testRaftTLSCfg(t.TempDir())
	_, err := LoadOrBuildRaftTLS(cfg, true, nil, "", nil, nil, "node-0", testTLSLogger())
	assert.Assert(t, err != nil)
}

func TestIssueOrReuseNodeCert(t *testing.T) {
	dir := t.TempDir()
	caCertPEM, caKeyPEM, err := createRaftCAWithGoca("ca", "org")
	assert.NilError(t, err)
	ca, err := NewMintingCAFromPEM(caCertPEM, caKeyPEM, time.Hour)
	assert.NilError(t, err)

	cfg := testRaftTLSCfg(dir)
	dns := make([]string, 0, 3)
	dns = append(dns, "node-0", "localhost")
	ips := []net.IP{net.ParseIP("127.0.0.1")}

	cert1, key1, err := issueOrReuseNodeCert(cfg, ca, "node-0", "org", dns, ips)
	assert.NilError(t, err)
	cert2, key2, err := issueOrReuseNodeCert(cfg, ca, "node-0", "org", dns, ips)
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(cert1, cert2) && bytes.Equal(key1, key2), "cert should be reused")

	// A missing SAN forces re-issue.
	cert3, _, err := issueOrReuseNodeCert(cfg, ca, "node-0", "org", append(dns, "extra.example"), ips)
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Equal(cert1, cert3), "cert should be re-issued when SANs change")
}

func TestReusableNodeCert(t *testing.T) {
	caCertPEM, caKeyPEM, err := createRaftCAWithGoca("ca", "org")
	assert.NilError(t, err)
	ca, err := NewMintingCAFromPEM(caCertPEM, caKeyPEM, time.Hour)
	assert.NilError(t, err)

	dns := make([]string, 0, 3)
	dns = append(dns, "node-0", "localhost")
	ips := []net.IP{net.ParseIP("127.0.0.1")}
	certPEM, _, err := ca.IssuePeerCertificate("node-0", "org", dns, ips, 8760*time.Hour)
	assert.NilError(t, err)
	cert, err := parsePEMCert(certPEM)
	assert.NilError(t, err)

	assert.Assert(t, reusableNodeCert(cert, ca, "node-0", dns, ips))
	assert.Assert(t, !reusableNodeCert(cert, ca, "other-cn", dns, ips), "CN mismatch must not be reusable")
	assert.Assert(t, !reusableNodeCert(cert, ca, "node-0", append(dns, "missing.example"), ips), "missing SAN must not be reusable")
}

func TestCoversSANs(t *testing.T) {
	caCertPEM, caKeyPEM, err := createRaftCAWithGoca("ca", "org")
	assert.NilError(t, err)
	ca, err := NewMintingCAFromPEM(caCertPEM, caKeyPEM, time.Hour)
	assert.NilError(t, err)
	certPEM, _, err := ca.IssuePeerCertificate("node-0", "org", []string{"node-0", "localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	assert.NilError(t, err)
	cert, err := parsePEMCert(certPEM)
	assert.NilError(t, err)

	assert.Assert(t, coversSANs(cert, []string{"node-0"}, []net.IP{net.ParseIP("127.0.0.1")}))
	assert.Assert(t, !coversSANs(cert, []string{"node-0", "missing"}, nil))
	assert.Assert(t, !coversSANs(cert, nil, []net.IP{net.ParseIP("10.0.0.1")}))
}

func TestBuildRaftTLSConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := testRaftTLSCfg(dir)
	tlsCfg, err := LoadOrBuildRaftTLS(cfg, false, nil, "", nil, nil, "node-0", testTLSLogger())
	assert.NilError(t, err)
	assert.Equal(t, tlsCfg.MinVersion, uint16(tls.VersionTLS12))
	assert.Equal(t, tlsCfg.ClientAuth, tls.RequireAndVerifyClientCert)

	cfg.ClientAuth = false
	tlsCfg2, err := LoadOrBuildRaftTLS(cfg, false, nil, "", nil, nil, "node-0", testTLSLogger())
	assert.NilError(t, err)
	assert.Equal(t, tlsCfg2.ClientAuth, tls.NoClientCert)
}

func TestEnsureRaftCASecretBootstrapAndPoll(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := RaftTLSConfig{Enabled: true, Dir: t.TempDir(), Organization: "org", CASecret: "raft-ca", CABootstrap: true, SecretPollTimeout: time.Second}
	certPEM, keyPEM, err := ensureRaftCA(&cfg, clientset, "ns", testTLSLogger())
	assert.NilError(t, err)
	assert.Assert(t, len(certPEM) > 0 && len(keyPEM) > 0)

	secret, err := clientset.CoreV1().Secrets("ns").Get(context.Background(), "raft-ca", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(secret.Data["ca.crt"]) > 0 && len(secret.Data["ca.key"]) > 0)

	// Non-bootstrap node polls and reads the existing secret.
	cfg2 := RaftTLSConfig{Enabled: true, Dir: t.TempDir(), Organization: "org", CASecret: "raft-ca", SecretPollTimeout: time.Second}
	certPEM2, keyPEM2, err := ensureRaftCA(&cfg2, clientset, "ns", testTLSLogger())
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(certPEM2, certPEM) && bytes.Equal(keyPEM2, keyPEM))
}

func TestEnsureRaftCASecretBootstrapRestart(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := RaftTLSConfig{Enabled: true, Dir: t.TempDir(), Organization: "org", CASecret: "raft-ca", CABootstrap: true, SecretPollTimeout: time.Second}

	firstCert, firstKey, err := ensureRaftCA(&cfg, clientset, "ns", testTLSLogger())
	assert.NilError(t, err)
	secondCert, secondKey, err := ensureRaftCA(&cfg, clientset, "ns", testTLSLogger())
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(secondCert, firstCert) && bytes.Equal(secondKey, firstKey), "restart must reuse the existing CA")
}

func TestEnsureRaftCASecretPollTimeout(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cfg := RaftTLSConfig{Enabled: true, Dir: t.TempDir(), Organization: "org", CASecret: "missing-ca", SecretPollTimeout: 200 * time.Millisecond}
	_, _, err := ensureRaftCA(&cfg, clientset, "ns", testTLSLogger())
	assert.Assert(t, err != nil)
}

func TestEnsureRaftCASecretMissingKeys(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-ca", Namespace: "ns"},
		Data:       map[string][]byte{"wrong": []byte("x")},
	})
	cfg := RaftTLSConfig{Enabled: true, Dir: t.TempDir(), Organization: "org", CASecret: "bad-ca", SecretPollTimeout: time.Second}
	_, _, err := ensureRaftCA(&cfg, clientset, "ns", testTLSLogger())
	assert.Assert(t, err != nil)
}

func TestEnsureRaftCALocalReuse(t *testing.T) {
	dir := t.TempDir()
	cfg := testRaftTLSCfg(dir)
	cert1, key1, err := ensureRaftCA(cfg, nil, "", testTLSLogger())
	assert.NilError(t, err)
	cert2, key2, err := ensureRaftCA(cfg, nil, "", testTLSLogger())
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(cert1, cert2) && bytes.Equal(key1, key2), "local CA should be reused")
}

func TestTLSFilesPermissions(t *testing.T) {
	dir := t.TempDir()
	cfg := testRaftTLSCfg(dir)
	_, err := LoadOrBuildRaftTLS(cfg, false, nil, "", nil, nil, "node-0", testTLSLogger())
	assert.NilError(t, err)

	info, err := os.Stat(dir)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o700))
	for _, name := range []string{"ca.crt", "ca.key", "node.crt", "node.key"} {
		info, err := os.Stat(filepath.Join(dir, name))
		assert.NilError(t, err)
		assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600), "%s perm", name)
	}
}
