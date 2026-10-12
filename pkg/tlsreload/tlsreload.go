// Package tlsreload provides a hot-reloadable TLS provider backed by files.
// It is the single server-side certificate reload mechanism (Raft transport,
// NATS cluster routes, and leader-forwarding HTTP client all consume it).
package tlsreload

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// RenewalWindow is the period before expiry within which a missing valid
// replacement is logged loudly.
const RenewalWindow = 7 * 24 * time.Hour

type state struct {
	cert    *tls.Certificate
	pool    *x509.CertPool
	modTime time.Time
}

// Provider atomically swaps a keypair + CA pool read from files, polling for
// changes so cert rotation is hot-reloaded without a restart.
type Provider struct {
	certFile     string
	keyFile      string
	caFile       string
	pollInterval time.Duration
	logger       *log.Logger

	current atomic.Pointer[state]
}

// NewProvider constructs a provider for the given files. pollInterval <= 0
// defaults to 5s.
func NewProvider(certFile, keyFile, caFile string, pollInterval time.Duration) *Provider {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	return &Provider{
		certFile:     certFile,
		keyFile:      keyFile,
		caFile:       caFile,
		pollInterval: pollInterval,
		logger:       log.Default(),
	}
}

// Load performs the initial load and validation. It returns an error when the
// material is missing or invalid.
func (p *Provider) Load() error {
	st, err := p.readState()
	if err != nil {
		return err
	}
	p.current.Store(st)
	return nil
}

// GetCertificate is a tls.Config hook returning the current hot-reloaded leaf.
func (p *Provider) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	st := p.current.Load()
	if st == nil {
		return nil, fmt.Errorf("tlsreload: no certificate loaded")
	}
	return st.cert, nil
}

// GetClientCertificate is a tls.Config hook returning the current client cert.
func (p *Provider) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	st := p.current.Load()
	if st == nil {
		return nil, fmt.Errorf("tlsreload: no certificate loaded")
	}
	return st.cert, nil
}

// TLSConfig returns a clone of base wired to the provider's dynamic
// certificate/client-certificate and current CA pool.
func (p *Provider) TLSConfig(base *tls.Config) *tls.Config {
	if base == nil {
		base = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cfg := base.Clone()
	cfg.GetCertificate = p.GetCertificate
	cfg.GetClientCertificate = p.GetClientCertificate
	if st := p.current.Load(); st != nil {
		cfg.RootCAs = st.pool
		cfg.ClientCAs = st.pool
	}
	return cfg
}

// Start polls the watched files and swaps in new material atomically. Invalid
// material keeps the previous certificate and logs a warning. It stops when
// ctx is done.
func (p *Provider) Start(ctx context.Context) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.reload()
		}
	}
}

func (p *Provider) reload() {
	current := p.current.Load()
	modTime, err := p.latestModTime()
	if err != nil {
		p.warn("tlsreload: stat certificates: %v", err)
		return
	}
	if current != nil && !modTime.After(current.modTime) {
		p.checkRenewalWindow(current)
		return
	}
	st, err := p.readState()
	if err != nil {
		p.warn("tlsreload: reload rejected, keeping previous certificate: %v", err)
		return
	}
	p.current.Store(st)
	p.warn("tlsreload: certificate reloaded from %s", p.certFile)
}

func (p *Provider) checkRenewalWindow(st *state) {
	if st.cert == nil || st.cert.Leaf == nil {
		return
	}
	if time.Until(st.cert.Leaf.NotAfter) < RenewalWindow {
		p.warn("tlsreload: certificate for %s expires soon (%s); no valid replacement loaded",
			p.certFile, st.cert.Leaf.NotAfter.Format(time.RFC3339))
	}
}

func (p *Provider) latestModTime() (time.Time, error) {
	var latest time.Time
	for _, path := range []string{p.certFile, p.keyFile, p.caFile} {
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest, nil
}

func (p *Provider) readState() (*state, error) {
	leaf, err := tls.LoadX509KeyPair(p.certFile, p.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load key pair: %w", err)
	}
	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse leaf certificate: %w", err)
	}
	leaf.Leaf = parsed
	now := time.Now()
	if now.Before(parsed.NotBefore) {
		return nil, fmt.Errorf("certificate not yet valid (notBefore %s)", parsed.NotBefore.Format(time.RFC3339))
	}
	if now.After(parsed.NotAfter) {
		return nil, fmt.Errorf("certificate expired at %s", parsed.NotAfter.Format(time.RFC3339))
	}
	caPEM, err := os.ReadFile(filepath.Clean(p.caFile))
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no CA certificate found in %s", p.caFile)
	}
	modTime, err := p.latestModTime()
	if err != nil {
		return nil, err
	}
	return &state{cert: &leaf, pool: pool, modTime: modTime}, nil
}

func (p *Provider) warn(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}
