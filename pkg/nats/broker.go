package nats

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsclient "github.com/nats-io/nats.go"
)

type Config struct {
	NodeID      string
	Port        int
	ClusterPort int
	Routes      []string
	BufferSize  int
	BufferBytes int
	// ClusterName is the NATS cluster name shared by all nodes of an HA
	// deployment. Without it every server generates a random name, route
	// connections between nodes are rejected with "Cluster Name Conflict",
	// and webhook fan-out only reaches subscribers on the same node.
	ClusterName string
	// ClusterUsername/ClusterPassword authenticate cluster route connections.
	ClusterUsername string
	ClusterPassword string
	// ClusterTLS, when set, wraps cluster route connections in TLS.
	ClusterTLS *tls.Config
	// DisableCluster disables the cluster listener and routes entirely
	// (single-node deployments).
	DisableCluster bool
	// MaxPayload/MaxPending bound per-message and per-client buffering.
	MaxPayload int
	MaxPending int64
}

type Broker struct {
	nc     *natsclient.Conn
	ns     *server.Server
	buffer *RingBuffer
	mu     sync.RWMutex
	subs   map[string]map[chan []byte]struct{}
	cancel context.CancelFunc
}

// buildServerOptions maps a Config to NATS server options. It is extracted so
// cluster TLS/auth/disable wiring is unit-testable without starting a server.
func buildServerOptions(cfg *Config) (*server.Options, error) {
	var routes []*url.URL
	if !cfg.DisableCluster {
		for _, r := range cfg.Routes {
			u, err := url.Parse(r)
			if err != nil {
				return nil, fmt.Errorf("invalid route URL %q: %w", r, err)
			}
			if cfg.ClusterUsername != "" {
				u.User = url.UserPassword(cfg.ClusterUsername, cfg.ClusterPassword)
			}
			routes = append(routes, u)
		}
	}

	opts := &server.Options{
		ServerName: cfg.NodeID,
		Host:       "127.0.0.1",
		Port:       cfg.Port,
		Routes:     routes,
	}
	if cfg.MaxPayload > 0 {
		opts.MaxPayload = int32(cfg.MaxPayload)
	}
	if cfg.MaxPending > 0 {
		opts.MaxPending = cfg.MaxPending
	}
	if !cfg.DisableCluster {
		opts.Cluster = server.ClusterOpts{
			Host: "0.0.0.0",
			Port: cfg.ClusterPort,
		}
		if cfg.ClusterName != "" {
			opts.Cluster.Name = cfg.ClusterName
		}
		if cfg.ClusterUsername != "" {
			opts.Cluster.Username = cfg.ClusterUsername
			opts.Cluster.Password = cfg.ClusterPassword
		}
		if cfg.ClusterTLS != nil {
			opts.Cluster.TLSConfig = cfg.ClusterTLS
			opts.Cluster.TLSTimeout = 2.0
		}
	}
	return opts, nil
}

func New(cfg Config) (*Broker, error) {
	if cfg.NodeID == "" {
		cfg.NodeID = "gohookbridge"
	}

	opts, err := buildServerOptions(&cfg)
	if err != nil {
		return nil, err
	}

	b := &Broker{
		buffer: NewRingBuffer(cfg.BufferSize, cfg.BufferBytes, DefaultMaxAge),
		subs:   make(map[string]map[chan []byte]struct{}),
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("create nats server: %w", err)
	}
	ns.ConfigureLogger()

	ns.Start()
	b.ns = ns

	if !ns.ReadyForConnections(5 * time.Second) {
		ns.Shutdown()
		return nil, fmt.Errorf("nats server not ready within 5s")
	}

	nc, err := natsclient.Connect(fmt.Sprintf("127.0.0.1:%d", cfg.Port),
		natsclient.Name(cfg.NodeID),
	)
	if err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("connect to embedded nats: %w", err)
	}
	b.nc = nc

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	go b.buffer.startCleanup(ctx)

	_, err = nc.Subscribe("webhook.>", func(msg *natsclient.Msg) {
		channel := extractChannel(msg.Subject)
		if channel == "" {
			return
		}
		b.buffer.Append(channel, msg.Data)
		b.fanout(channel, msg.Data)
	})
	if err != nil {
		cancel()
		nc.Close()
		ns.Shutdown()
		return nil, fmt.Errorf("subscribe to webhook.>: %w", err)
	}

	return b, nil
}

func extractChannel(subject string) string {
	const prefix = "webhook."
	if len(subject) <= len(prefix) {
		return ""
	}
	return subject[len(prefix):]
}

func (b *Broker) Publish(channel string, data []byte) error {
	if b.nc == nil {
		return fmt.Errorf("nats not connected")
	}
	return b.nc.Publish("webhook."+channel, data)
}

func (b *Broker) SetChannelTTL(channel string, ttl time.Duration) {
	b.buffer.SetChannelTTL(channel, ttl)
}

func (b *Broker) Subscribe(channel string, since time.Time, drainLimit int) ([][]byte, chan []byte) {
	ch := make(chan []byte, 100)

	b.mu.Lock()
	subs, ok := b.subs[channel]
	if !ok {
		subs = make(map[chan []byte]struct{})
		b.subs[channel] = subs
	}
	subs[ch] = struct{}{}
	b.mu.Unlock()

	if since.IsZero() {
		since = time.Now().Add(-b.buffer.maxAge)
	}
	historical := b.buffer.Get(channel, since, drainLimit)

	return historical, ch
}

func (b *Broker) Unsubscribe(channel string, ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs, ok := b.subs[channel]
	if !ok {
		return
	}
	delete(subs, ch)
	close(ch)
	if len(subs) == 0 {
		delete(b.subs, channel)
	}
}

func (b *Broker) fanout(channel string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.subs[channel]
	if !ok {
		return
	}

	for ch := range subs {
		select {
		case ch <- data:
		default:
		}
	}
}

func (b *Broker) Shutdown() {
	if b.cancel != nil {
		b.cancel()
	}
	if b.nc != nil {
		b.nc.Close()
	}
	if b.ns != nil {
		b.ns.Shutdown()
		b.ns.WaitForShutdown()
	}
}
