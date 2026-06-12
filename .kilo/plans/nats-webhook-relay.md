# Plan: NATS-based HA Webhook Relay

## Summary

Replace the in-memory `EventBroker` (per-instance, no HA) with **Core NATS embedded cluster** for real-time webhook fanout across instances, plus a **local ring buffer with TTL** on each instance for late subscriber catch-up.

## Architecture

```
                    Load Balancer
                    /     |     \
                   v      v      v
              Instance A  Instance B  Instance C
              ┌──────────────────────────────┐
              │  gosmee HTTP server          │
              │  ├─ POST /{channel}          │
              │  └─ GET /events/{channel}    │
              │                              │
              │  NATS Broker (embedded)      │
              │  ├─ nats-server (in-process) │
              │  ├─ cluster routes           │
              │  ├─ ring buffer (TTL)        │
              │  └─ fan-out to SSE clients   │
              │                              │
              │  Raft (config only)          │
              └──────────────────────────────┘
                       │ cluster │
                       └─────────┘
```

### Data flow

```
POST /{channel}  →  nats.Publish("webhook.{channel}", payload)
                              │
              ┌───────────────┼───────────────┐
              ↓               ↓               ↓
         Instance A      Instance B      Instance C
         [ring buffer]   [ring buffer]   [ring buffer]
         [fan-out]       [fan-out]       [fan-out]
              │               ↑
              └─────── SSE client on B
                      1. drain buffer
                      2. live NATS events
```

## New Files

### 1. `gosmee/nats/broker.go` — Embedded NATS server + fan-out broker

```go
package nats

// Config holds NATS broker configuration.
type Config struct {
    NodeID      string        // unique node ID (reuse raft-node-id)
    Port        int           // client port, 0 = disabled (use EventBroker fallback)
    ClusterPort int           // cluster route port
    Routes      []string      // cluster routes: "nats://host:6222"
    BufferTTL   time.Duration // ring buffer max age
    BufferSize  int           // ring buffer max entries
    DataDir     string        // NATS store dir (for JetStream metadata, optional)
}

// Broker wraps embedded NATS server, ring buffer, and fan-out to SSE subscribers.
type Broker struct {
    nc     *nats.Conn
    server *server.Server  // embedded nats-server
    buffer *RingBuffer
    mu     sync.RWMutex
    subs   map[string]map[chan []byte]struct{} // channel -> set of subscriber chans
}

// New starts embedded NATS server, connects as client, starts ring buffer feeder.
func New(cfg Config) (*Broker, error)

// Publish sends data to all NATS nodes and local fan-out.
func (b *Broker) Publish(channel string, data []byte) error

// Subscribe registers a channel for webhook events. Returns drained historical
// data first, then a chan that receives live events.
func (b *Broker) Subscribe(channel string, drainLimit int) (historical [][]byte, live <-chan []byte)

// Unsubscribe removes the subscriber channel.
func (b *Broker) Unsubscribe(channel string, ch chan []byte)

// Shutdown gracefully stops NATS server.
func (b *Broker) Shutdown()
```

**Key design decisions:**
- Starts `nats-server` in-process with `server.Options{Port, Cluster: clusterOpts, Routes}`
- Connects as NATS client to `localhost:Port` (in-process, no network overhead)
- Subscribes to `webhook.>` to feed ring buffer + fan-out
- `Subscribe()` returns historical data from ring buffer then a live channel
- Falls back gracefully: if `Port == 0`, broker is nil and server.go uses existing EventBroker

### 2. `gosmee/nats/buffer.go` — Ring buffer with TTL eviction

```go
package nats

type entry struct {
    channel   string
    data      []byte
    timestamp time.Time
}

type RingBuffer struct {
    mu      sync.RWMutex
    entries []entry
    maxSize int
    maxAge  time.Duration
}

func NewRingBuffer(maxSize int, maxAge time.Duration) *RingBuffer

// Append adds an entry, evicting oldest if over maxSize.
func (rb *RingBuffer) Append(channel string, data []byte)

// Get returns entries for a channel newer than the given time, up to limit.
func (rb *RingBuffer) Get(channel string, since time.Time, limit int) [][]byte

// startCleanup runs a goroutine that evicts expired entries every 30s.
func (rb *RingBuffer) startCleanup(ctx context.Context)
```

### 3. `gosmee/nats/broker_test.go` — Tests

- Test embedded NATS start/shutdown
- Test publish/subscribe within single instance
- Test ring buffer TTL eviction
- Test ring buffer size limit eviction
- Test Subscribe returns historical + live

## Modified Files

### 4. `gosmee/server.go` — Integrate NATS broker

**In `serve()`:**
1. After Raft store init, init NATS broker if configured:
```go
natsCfg := nats.Config{
    NodeID:      c.String("raft-node-id"),
    Port:        c.Int("nats-port"),
    ClusterPort: c.Int("nats-cluster-port"),
    Routes:      c.StringSlice("nats-routes"),
    BufferTTL:   c.Duration("nats-buffer-ttl"),
    BufferSize:  c.Int("nats-buffer-size"),
}
broker, _ := nats.New(natsCfg)
defer broker.Shutdown()
```
2. Pass `broker` to `handleWebhookPost` and `handleEventsGet` and `handleReplayPost`.

**In `handleWebhookPost`:**
```go
// After validation + encoding, replace:
//   events.CreateStream(channel)
//   events.Publish(channel, &sse.Event{Data: reencoded})
//   eventBroker.Publish(channel, reencoded)
// With:
if broker != nil {
    broker.Publish(channel, reencoded)
} else {
    events.CreateStream(channel)
    events.Publish(channel, &sse.Event{Data: reencoded})
    eventBroker.Publish(channel, reencoded)
}
```

**In `handleEventsGet`:**
```go
// Replace eventBroker.Subscribe loop with:
if broker != nil {
    historical, live := broker.Subscribe(channel, 100)
    // Send historical first
    for _, data := range historical {
        fmt.Fprintf(w, "data: %s\n\n", data)
        flusher.Flush()
    }
    // Then live events
    for {
        select {
        case <-clientGone:
            broker.Unsubscribe(channel, live)
            return
        case data, ok := <-live:
            if !ok { return }
            fmt.Fprintf(w, "data: %s\n\n", data)
            flusher.Flush()
        case <-ticker.C:
            fmt.Fprint(w, ": keepalive\n\n")
            flusher.Flush()
        }
    }
} else {
    // existing EventBroker code
}
```

**In `handleReplayPost`:** Same pattern — `broker.Publish(channel, reencoded)` if broker is set.

**Keep EventBroker** for backward compat (single-instance mode without NATS).

### 5. `gosmee/flags.go` — Add NATS flags

```go
&cli.IntFlag{
    Name:    "nats-port",
    Usage:   "NATS embedded server port (0 = disabled, uses in-memory EventBroker)",
    Value:   0,
    EnvVars: []string{"GOSMEE_NATS_PORT"},
},
&cli.IntFlag{
    Name:    "nats-cluster-port",
    Usage:   "NATS cluster route port for inter-node communication",
    Value:   6222,
    EnvVars: []string{"GOSMEE_NATS_CLUSTER_PORT"},
},
&cli.StringSliceFlag{
    Name:    "nats-routes",
    Usage:   "NATS cluster routes (nats://host:6222). Required for HA, same hosts as raft-peers",
    EnvVars: []string{"GOSMEE_NATS_ROUTES"},
},
&cli.DurationFlag{
    Name:    "nats-buffer-ttl",
    Usage:   "How long to keep webhook data in ring buffer for late subscribers",
    Value:   1 * time.Hour,
    EnvVars: []string{"GOSMEE_NATS_BUFFER_TTL"},
},
&cli.IntFlag{
    Name:    "nats-buffer-size",
    Usage:   "Max number of webhook entries to keep in ring buffer",
    Value:   10000,
    EnvVars: []string{"GOSMEE_NATS_BUFFER_SIZE"},
},
```

### 6. `go.mod` — Add dependency

```
require (
    github.com/nats-io/nats-server/v2 v2.11.2
    github.com/nats-io/nats.go v1.41.2
)
```

### 7. `vendor/` — Vendor new dependency

Run `go mod tidy && go mod vendor`.

## Behavior matrix

| Mode | NATS Config | Transport | Persistence | HA |
|---|---|---|---|---|
| Single instance | `--nats-port 0` (default) | EventBroker (in-memory) | None | No |
| Single instance + buffer | `--nats-port 4222` | NATS (local) | Ring buffer TTL | No |
| HA cluster | `--nats-port 4222 --nats-routes ...` | NATS cluster | Ring buffer TTL | Yes |

## Implementation order

1. Add NATS dependencies, vendor
2. Create `gosmee/nats/buffer.go` + tests
3. Create `gosmee/nats/broker.go` + tests
4. Add NATS flags to `gosmee/flags.go`
5. Modify `gosmee/server.go` to init broker and wire handlers
6. Integration test: start 3 instances with NATS cluster, verify cross-instance delivery
7. Run existing tests, ensure no regressions

## Not in scope

- JetStream persistence (can add later if ring buffer TTL is insufficient)
- NATS TLS for inter-node communication (use private networks as with Raft)
- NATS authentication (private network, same as Raft)
- Client-side changes (SSE protocol unchanged)
