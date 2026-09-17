package store

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/raft"
)

// hostAddr implements net.Addr with a DNS hostname instead of a resolved IP.
// Storing DNS names in the Raft cluster configuration lets peers re-resolve
// the address on each connection, handling IP changes across pod restarts
// (Kubernetes StatefulSet DNS is stable while pod IPs change).
type hostAddr struct {
	host string
	port int
}

func (a hostAddr) Network() string { return "tcp" }
func (a hostAddr) String() string  { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }

// newHostAddr parses addr as host:port and returns a hostAddr without
// resolving the hostname. Unlike net.ResolveTCPAddr the DNS name is kept
// intact so Raft peers resolve it afresh on each connection.
func newHostAddr(addr string) (net.Addr, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}
	return hostAddr{host: host, port: port}, nil
}

// plainStreamLayer implements raft.StreamLayer over a plaintext net.Listener.
type plainStreamLayer struct {
	listener  net.Listener
	advertise net.Addr
}

var _ raft.StreamLayer = (*plainStreamLayer)(nil)

// newPlainStreamLayer binds a plaintext listener on bindAddr. advertise is the
// externally routable address (nil = use the bound address).
func newPlainStreamLayer(bindAddr string, advertise net.Addr) (*plainStreamLayer, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("raft: listen %s: %w", bindAddr, err)
	}
	if advertise == nil {
		advertise = ln.Addr()
	}
	return &plainStreamLayer{listener: ln, advertise: advertise}, nil
}

func (l *plainStreamLayer) Accept() (net.Conn, error) {
	return l.listener.Accept()
}

func (l *plainStreamLayer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	return d.Dial("tcp", string(addr))
}

func (l *plainStreamLayer) Close() error {
	return l.listener.Close()
}

func (l *plainStreamLayer) Addr() net.Addr {
	return l.advertise
}

// tlsStreamLayer implements raft.StreamLayer over a TLS-wrapped net.Listener.
type tlsStreamLayer struct {
	listener  net.Listener
	config    *tls.Config
	advertise net.Addr
}

var _ raft.StreamLayer = (*tlsStreamLayer)(nil)

// newTLSStreamLayer binds a TLS listener on bindAddr. advertise is the
// externally routable address (nil = use the bound address).
func newTLSStreamLayer(bindAddr string, advertise net.Addr, config *tls.Config) (*tlsStreamLayer, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("raft TLS: listen %s: %w", bindAddr, err)
	}
	if advertise == nil {
		advertise = ln.Addr()
	}
	return &tlsStreamLayer{listener: ln, config: config, advertise: advertise}, nil
}

func (l *tlsStreamLayer) Accept() (net.Conn, error) {
	conn, err := l.listener.Accept()
	if err != nil {
		return nil, err
	}
	return tls.Server(conn, l.config), nil
}

func (l *tlsStreamLayer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: l.config}
	return d.DialContext(context.Background(), "tcp", string(addr))
}

func (l *tlsStreamLayer) Close() error {
	return l.listener.Close()
}

func (l *tlsStreamLayer) Addr() net.Addr {
	return l.advertise
}

// retryingStreamLayer wraps a raft.StreamLayer and adds DNS re-resolution with
// retry on dial failures. When a StatefulSet pod restarts and gets a new IP,
// eBPF-based DNS proxies (Cilium) may cache the old IP for up to 30 s. This
// layer resolves the hostname to an IP before each dial attempt and retries
// on connection-refused errors, which is the typical symptom of a stale DNS
// cache (the old IP has been reassigned to a pod that is not listening on
// the raft port). tlsCfg nil = plaintext dial.
type retryingStreamLayer struct {
	inner  raft.StreamLayer
	tlsCfg *tls.Config
	dialer *net.Dialer
}

var _ raft.StreamLayer = (*retryingStreamLayer)(nil)

// newRetryingStreamLayer wraps inner with DNS-re-resolving dial retry logic.
func newRetryingStreamLayer(inner raft.StreamLayer, timeout time.Duration, tlsCfg *tls.Config) *retryingStreamLayer {
	return &retryingStreamLayer{
		inner:  inner,
		tlsCfg: tlsCfg,
		dialer: &net.Dialer{Timeout: timeout},
	}
}

// maxDialRetries is the maximum number of dial retries when the initial
// attempt fails with a connection-refused error.
const maxDialRetries = 4

// dialRetryBackoff is the base backoff between dial retries.
const dialRetryBackoff = 500 * time.Millisecond

// Dial resolves the hostname in addr to an IP, then dials the IP directly
// (with TLS when configured). On connection-refused errors it re-resolves the
// hostname and retries up to maxDialRetries times. Other errors (timeout, TLS
// handshake failure, etc.) are returned immediately.
func (l *retryingStreamLayer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	addrStr := string(addr)
	host, port, err := net.SplitHostPort(addrStr)
	if err != nil {
		return nil, fmt.Errorf("parse raft dial addr %s: %w", addrStr, err)
	}

	dialer := l.dialer
	if timeout != dialer.Timeout {
		dialer = &net.Dialer{Timeout: timeout}
	}

	var lastErr error
	for attempt := 0; attempt <= maxDialRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(dialRetryBackoff * time.Duration(attempt))
		}

		// Resolve the hostname to an IP on every attempt so stale DNS
		// caches (Cilium eBPF, NodeLocal DNSCache) are bypassed when
		// the pod IP has changed.
		ips, err := net.DefaultResolver.LookupHost(context.Background(), host)
		if err != nil {
			lastErr = fmt.Errorf("resolve %s: %w", host, err)
			continue
		}
		if len(ips) == 0 {
			lastErr = fmt.Errorf("resolve %s: no addresses", host)
			continue
		}

		target := net.JoinHostPort(ips[0], port)
		var conn net.Conn
		if l.tlsCfg != nil {
			// Dial the resolved IP directly. Set ServerName to the original
			// hostname so the TLS handshake verifies the peer's certificate
			// SANs against the DNS name (not the IP).
			cfg := l.tlsCfg.Clone()
			cfg.ServerName = host
			d := &tls.Dialer{NetDialer: dialer, Config: cfg}
			conn, err = d.DialContext(context.Background(), "tcp", target)
		} else {
			conn, err = dialer.Dial("tcp", target)
		}
		if err == nil {
			return conn, nil
		}
		lastErr = err

		// Only retry on connection-refused: the peer pod is alive but
		// we got the wrong IP from a stale DNS cache. Timeouts and TLS
		// errors indicate a different problem.
		if !isConnRefused(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("dial %s: %w (retries exhausted)", addrStr, lastErr)
}

// isConnRefused reports whether err indicates the remote host actively
// refused the TCP connection (ECONNREFUSED).
func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "connection refused")
}

func (l *retryingStreamLayer) Accept() (net.Conn, error) {
	return l.inner.Accept()
}

func (l *retryingStreamLayer) Close() error {
	return l.inner.Close()
}

func (l *retryingStreamLayer) Addr() net.Addr {
	return l.inner.Addr()
}
