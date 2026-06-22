# Gohookbridge HA Webhook Relay — Architecture Design Document

## Table of Contents

1. [Global Architecture](#global-architecture)
2. [Component Details](#component-details)
   - [Raft — Configuration Consensus](#raft--configuration-consensus)
   - [NATS — Real-time Webhook Fan-out](#nats--real-time-webhook-fan-out)
   - [Ring Buffer — Late Subscriber Catch-up](#ring-buffer--late-subscriber-catch-up)
   - [SSE Endpoint — Client Subscribe/Push](#sse-endpoint--client-subscribepush)
3. [Data Flows](#data-flows)
   - [Webhook POST (Publish)](#webhook-post-publish)
   - [SSE Subscribe + Receive](#sse-subscribe--receive)
   - [Replay POST](#replay-post)
   - [Ring Buffer Eviction](#ring-buffer-eviction)
4. [HA Scenarios](#ha-scenarios)
   - [Cross-instance Delivery](#cross-instance-delivery)
   - [Late Subscriber Join](#late-subscriber-join)
   - [Node Failure](#node-failure)
5. [Configuration Mapping](#configuration-mapping)
6. [Backward Compatibility](#backward-compatibility)

---

## Global Architecture

```mermaid
graph TB
    subgraph External
        GH[GitHub / GitLab / Webhook Source]
        LB[Load Balancer]
    end

    subgraph "Instance A (leader)"
        direction TB
        HTTP_A[HTTP Server :3333]
        RAFT_A[Raft Node<br/>config consensus]
        NATS_A[NATS Server<br/>embedded :4222]
        RB_A[Ring Buffer<br/>in-memory]
    end

    subgraph "Instance B"
        direction TB
        HTTP_B[HTTP Server :3333]
        RAFT_B[Raft Node<br/>config consensus]
        NATS_B[NATS Server<br/>embedded :4222]
        RB_B[Ring Buffer<br/>in-memory]
    end

    subgraph "Instance C"
        direction TB
        HTTP_C[HTTP Server :3333]
        RAFT_C[Raft Node<br/>config consensus]
        NATS_C[NATS Server<br/>embedded :4222]
        RB_C[Ring Buffer<br/>in-memory]
    end

    CLIENT[gohookbridge client<br/>SSE subscriber]

    GH -->|POST webhook| LB
    LB --> HTTP_A
    LB --> HTTP_B
    LB --> HTTP_C

    CLIENT -->|SSE /events/channel| LB
    LB -.->|routes to one instance| HTTP_B

    RAFT_A <-->|raft protocol :6001| RAFT_B
    RAFT_B <-->|raft protocol :6001| RAFT_C

    NATS_A <-->|cluster routes :6222| NATS_B
    NATS_B <-->|cluster routes :6222| NATS_C

    HTTP_A --> NATS_A
    HTTP_B --> NATS_B
    HTTP_C --> NATS_C

    NATS_A --> RB_A
    NATS_B --> RB_B
    NATS_C --> RB_C
```

**Key insight:** Raft and NATS are two independent consensus/messaging layers with different purposes:
- **Raft** (port 6001): replicates **configuration** (projects, users, global settings). Slow, durable, strongly consistent.
- **NATS** (ports 4222/6222): distributes **webhook data** in real time. Fast, ephemeral, eventually consistent.

---

## Component Details

### Raft — Configuration Consensus

Raft is used **exclusively for configuration**. It stores projects, users, roles, and global settings in a BoltDB-backed FSM (Finite State Machine).

```mermaid
sequenceDiagram
    participant Admin as Admin UI / API
    participant L as Raft Leader
    participant F1 as Raft Follower 1
    participant F2 as Raft Follower 2
    participant FSM as BoltDB FSM

    Admin->>L: PUT /api/projects/my-project
    L->>L: json.Marshal(fsmCommand{Op: "set", Key: "/projects/my-project/", Value: ...})
    L->>L: raft.Apply(cmd)
    L->>F1: AppendEntries (replicate log entry)
    L->>F2: AppendEntries (replicate log entry)
    F1-->>L: ACK
    F2-->>L: ACK
    L->>FSM: Apply(log) → BoltDB.Put("/projects/my-project/", value)
    F1->>F1: Apply(log) → BoltDB.Put(...)
    F2->>F2: Apply(log) → BoltDB.Put(...)
    L-->>Admin: 200 OK
```

**Storage layout in BoltDB (`fsm_data` bucket):**

| Key | Value | Purpose |
|---|---|---|
| `/global/server/` | `ServerConfig` JSON | MaxBodySize, BehindReverseProxy, CORS, etc. |
| `/global/defaults/` | `DefaultProjectConfig` JSON | Default signatures, allowed IPs, replay token |
| `/projects/{id}/` | `Project` JSON | Per-project overrides |
| `/users/{id}/` | `User` JSON | Username, password hash, roles, projects |
| `/users/by-username/{name}/` | `usernameIndex` JSON | Username → UserID lookup |
| `/rbac/roles/{name}/` | `Role` JSON | Custom RBAC roles |
| `/meta/setup_end` | `time.Time` JSON | Setup mode expiry |
| `/global/auth/oidc_providers` | `[]OIDCProvider` JSON | OIDC providers list |

**Key Raft configuration (from `store/raft.go`):**

| Parameter | Value | Purpose |
|---|---|---|
| `HeartbeatTimeout` | 1s | Leader heartbeat interval |
| `ElectionTimeout` | 1s | Follower starts election after |
| `LeaderLeaseTimeout` | 500ms | Leader steps down if can't reach quorum |
| `CommitTimeout` | 50ms | Batch commit interval |
| `SnapshotInterval` | 120s | Snapshot frequency |
| `SnapshotThreshold` | 8192 entries | Trigger snapshot after this many log entries |
| `TrailingLogs` | 10240 | Log entries kept after snapshot |

**Important:** Webhook data does NOT go through Raft. Raft stays lightweight, handling only config mutations.

---

### NATS — Real-time Webhook Fan-out

Each gohookbridge instance embeds a `nats-server` in-process. The three servers form a **full mesh cluster** via route connections.

```mermaid
graph LR
    subgraph "Instance A (embedded NATS)"
        NS_A[NATS Server<br/>client: :4222<br/>cluster: :6222]
        NC_A[NATS Client<br/>conn to :4222]
    end
    subgraph "Instance B (embedded NATS)"
        NS_B[NATS Server<br/>client: :4222<br/>cluster: :6222]
        NC_B[NATS Client<br/>conn to :4222]
    end
    subgraph "Instance C (embedded NATS)"
        NS_C[NATS Server<br/>client: :4222<br/>cluster: :6222]
        NC_C[NATS Client<br/>conn to :4222]
    end

    NS_A <-->|route| NS_B
    NS_B <-->|route| NS_C
    NS_A <-->|route| NS_C

    NC_A -->|localhost:4222| NS_A
    NC_B -->|localhost:4222| NS_B
    NC_C -->|localhost:4222| NS_C
```

**NATS subject topology:**

```
webhook.>              ← wildcard subscription (feeds ring buffer on each instance)
webhook.{channelID}    ← per-channel publish/subscribe
webhook.abc123         ← example channel
webhook.xyz789         ← example channel
```

**How NATS distributes a publish across the cluster:**

```mermaid
sequenceDiagram
    participant HTTP as HTTP Handler (Instance A)
    participant NC_A as NATS Client (A)
    participant NS_A as NATS Server (A)
    participant NS_B as NATS Server (B)
    participant NC_B as NATS Client (B)
    participant RB_B as Ring Buffer (B)

    HTTP->>NC_A: Publish("webhook.abc123", payload)
    NC_A->>NS_A: PUB webhook.abc123
    NS_A->>NS_B: route forward PUB
    NS_A->>NC_A: MSG webhook.abc123 (local subscriber)
    NS_B->>NC_B: MSG webhook.abc123 (remote subscriber)
    NC_A->>NC_A: fan-out to local SSE clients
    NC_B->>RB_B: Append("abc123", payload)
    NC_B->>NC_B: fan-out to local SSE clients
```

**NATS embedded server configuration:**

```go
server.Options{
    Port:    4222,                              // client connections
    Cluster: server.ClusterOpts{
        Port:   6222,                           // cluster routes
        Routes: []*url.URL{
            url.Parse("nats://host2:6222"),     // peer routes
            url.Parse("nats://host3:6222"),
        },
    },
    JetStream: false,                           // core NATS only, no JetStream
    NoLog:    true,                             // suppress NATS logs
    NoSigs:   true,                             // gohookbridge handles signals
}
```

**Client connection (in-process, no network):**
```go
nc, _ := nats.Connect("nats://localhost:4222")
nc.Subscribe("webhook.>", func(msg *nats.Msg) {
    channel := strings.TrimPrefix(msg.Subject, "webhook.")
    buffer.Append(channel, msg.Data)     // feed ring buffer
    fanout(channel, msg.Data)            // dispatch to live SSE subscribers
})
```

---

### Ring Buffer — Late Subscriber Catch-up

A per-instance, in-memory ring buffer with time-based eviction. Provides recent webhooks to SSE clients that connect after the webhook was sent.

```mermaid
graph TB
    subgraph "Ring Buffer Internals"
        direction LR
        E1[entry<br/>ch: abc<br/>ts: 12:00:01]
        E2[entry<br/>ch: xyz<br/>ts: 12:00:02]
        E3[entry<br/>ch: abc<br/>ts: 12:00:05]
        E4[entry<br/>ch: abc<br/>ts: 12:00:10]
        E5[entry<br/>ch: xyz<br/>ts: 12:00:12]
        E6[...]
    end

    NATS_IN[NATS subscriber<br/>webhook.>] -->|Append| E6
    CLEANUP[Cleanup goroutine<br/>every 30s] -.->|Remove expired| E1

    SUB[SSE client connects<br/>channel: abc] -->|Get since now-1h| E3
    SUB -->|Get since now-1h| E4
```

**Data structure:**

```go
type entry struct {
    channel   string
    data      []byte
    timestamp time.Time
}

type RingBuffer struct {
    mu      sync.RWMutex
    entries []entry
    maxSize int           // max number of entries (default: 10000)
    maxAge  time.Duration // max entry age before eviction (default: 1h)
}
```

**Operations:**

| Operation | Behavior |
|---|---|
| `Append(channel, data)` | Append to slice. If `len > maxSize`, evict oldest entry. O(1) amortized. |
| `Get(channel, since, limit)` | Linear scan for matching channel + newer than since, return up to limit. O(n). |
| Cleanup goroutine | Every 30s, remove all entries older than `now - maxAge`. Runs as background goroutine. |

**TTL eviction flow:**

```mermaid
sequenceDiagram
    participant NATS as NATS subscriber
    participant RB as Ring Buffer
    participant GC as Cleanup goroutine
    participant SSE as SSE handler

    NATS->>RB: Append(ch, payload) t=12:00
    NATS->>RB: Append(ch, payload) t=12:05
    NATS->>RB: Append(ch, payload) t=12:10

    Note over RB: 12:30 — cleanup runs
    GC->>RB: Remove entries older than 11:30
    Note over RB: Nothing removed (all < 1h old)

    Note over RB: 13:01 — cleanup runs
    GC->>RB: Remove entries older than 12:01
    Note over RB: t=12:00 entry removed

    Note over RB: 13:11 — cleanup runs
    GC->>RB: Remove entries older than 12:11
    Note over RB: t=12:05 and t=12:10 entries removed

    SSE->>RB: Get(ch, since=now-1h, limit=100)
    Note over RB: Returns nothing if no fresh entries
```

**Memory estimation:**
- 10000 entries × ~16KB average payload = ~160MB max
- With TTL of 1h and typical webhook rate of 100/min: ~6000 entries, ~96MB typical
- Configurable via `--nats-buffer-size` and `--nats-buffer-ttl`

---

### SSE Endpoint — Client Subscribe/Push

The SSE endpoint (`GET /events/{channel}`) is how gohookbridge clients receive webhooks. The handler is modified to drain the ring buffer first, then subscribe to live NATS events.

```mermaid
sequenceDiagram
    participant Client as gohookbridge client<br/>(SSE subscriber)
    participant HTTP as HTTP handler<br/>/events/{channel}
    participant RB as Ring Buffer
    participant NATS as NATS subscription<br/>webhook.{channel}
    participant Sender as Webhook Sender

    Client->>HTTP: GET /events/abc123
    HTTP->>HTTP: Validate channel, CORS, auth (session or token)

    Note over HTTP: Phase 1: Drain historical
    HTTP->>RB: Get("abc123", since=now-1h, limit=100)
    RB-->>HTTP: [event1, event2]
    HTTP-->>Client: data: event1\n\n
    HTTP-->>Client: data: event2\n\n

    Note over HTTP: Phase 2: Live subscription
    HTTP->>NATS: Subscribe("webhook.abc123")

    HTTP-->>Client: data: {"message":"connected"}\n\n
    HTTP-->>Client: data: {"message":"ready"}\n\n

    loop Live events
        Sender->>HTTP: POST /abc123 (arrives via NATS)
        HTTP->>NATS: receives MSG
        HTTP-->>Client: data: {webhook payload}\n\n
    end

    loop Keepalive (every 30s)
        HTTP-->>Client: : keepalive\n\n
    end

    Client-->>HTTP: disconnect (close SSE)
    HTTP->>NATS: Unsubscribe
    HTTP->>HTTP: cleanup
```

**Handler logic (pseudocode):**

```go
func handleEventsGet(broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        channel := chi.URLParam(r, "channel")
        // ... validation, auth, CORS ...

        w.Header().Set("Content-Type", "text/event-stream")
        // ... SSE headers ...

        if broker != nil {
            // Phase 1: drain ring buffer
            historical, liveCh := broker.Subscribe(channel, 100)
            for _, data := range historical {
                fmt.Fprintf(w, "data: %s\n\n", data)
                flusher.Flush()
            }

            // Phase 2: live NATS events + keepalive
            fmt.Fprintf(w, "data: %s\n\n", `{"message":"connected"}`)
            fmt.Fprintf(w, "data: %s\n\n", `{"message":"ready"}`)
            flusher.Flush()

            ticker := time.NewTicker(30 * time.Second)
            defer ticker.Stop()
            for {
                select {
                case <-r.Context().Done():
                    broker.Unsubscribe(channel, liveCh)
                    return
                case data, ok := <-liveCh:
                    if !ok { return }
                    fmt.Fprintf(w, "data: %s\n\n", data)
                    flusher.Flush()
                case <-ticker.C:
                    fmt.Fprint(w, ": keepalive\n\n")
                    flusher.Flush()
                }
            }
        } else {
            // Fallback: existing EventBroker (single-instance, no NATS)
            // ... unchanged code ...
        }
    }
}
```

**Broker Subscribe internals:**

```go
func (b *Broker) Subscribe(channel string, limit int) (historical [][]byte, live <-chan []byte) {
    // 1. Drain historical from ring buffer
    historical = b.buffer.Get(channel, time.Now().Add(-b.buffer.maxAge), limit)

    // 2. Create live channel
    ch := make(chan []byte, 100)
    b.mu.Lock()
    if b.subs[channel] == nil {
        b.subs[channel] = make(map[chan []byte]struct{})
    }
    b.subs[channel][ch] = struct{}{}
    b.mu.Unlock()

    return historical, ch
}
```

---

## Data Flows

### Webhook POST (Publish)

```mermaid
sequenceDiagram
    participant Sender as GitHub / GitLab
    participant LB as Load Balancer
    participant A as Instance A (receives POST)
    participant NATS_A as NATS (A)
    participant NATS_B as NATS (B)
    participant NATS_C as NATS (C)
    participant RB as Ring Buffers (all)

    Sender->>LB: POST /abc123<br/>X-Hub-Signature-256: sha256=...
    LB->>A: POST /abc123 (routes to any instance)

    Note over A: Validate Content-Type
    Note over A: Read body (max size from config)
    Note over A: Validate webhook signature (HMAC)

    A->>A: Build payload JSON<br/>{headers, bodyB(base64), timestamp}

    alt NATS enabled
        A->>NATS_A: Publish("webhook.abc123", payload)
        NATS_A->>NATS_B: route forward
        NATS_A->>NATS_C: route forward
        NATS_B->>RB: Append("abc123", payload)
        NATS_C->>RB: Append("abc123", payload)
        NATS_A->>RB: Append("abc123", payload)
        NATS_B->>NATS_B: fan-out to local SSE subs
        NATS_C->>NATS_C: fan-out to local SSE subs
        NATS_A->>NATS_A: fan-out to local SSE subs
    else NATS disabled (single instance)
        A->>A: events.Publish (SSE lib)
        A->>A: eventBroker.Publish (in-memory)
    end

    A-->>LB: 202 Accepted<br/>{status, channel, message, version}
    LB-->>Sender: 202 Accepted
```

**Validation chain (unchanged from current):**
1. `Content-Type: application/json` check
2. `MaxBytesReader` limit (project-level or global default)
3. Webhook signature validation (GitHub HMAC-SHA256, GitLab token, Bitbucket HMAC, Gitea)
4. JSON parsing
5. IP allowlist (if configured)

After validation, the re-encoded payload is published to NATS.

---

### SSE Subscribe + Receive

Covered in [SSE Endpoint section](#sse-endpoint--client-subscribepush) above. Diagram repeated here for flow completeness.

```mermaid
sequenceDiagram
    participant Client as gohookbridge client
    participant B as Instance B (SSE handler)
    participant RB as Ring Buffer (B)
    participant NATS as NATS subscription (B)
    participant A as Instance A (receives webhook)
    participant NATS_A as NATS (A)

    Client->>B: GET /events/abc123
    B->>RB: Get("abc123", since=now-1h, 100)
    RB-->>B: [history...]
    B-->>Client: data: history\n\n
    B->>NATS: Subscribe("webhook.abc123")
    B-->>Client: data: {"message":"connected"}\n\n
    B-->>Client: data: {"message":"ready"}\n\n

    Note over Client: Client is ready, waiting for events

    A->>NATS_A: Publish("webhook.abc123", payload)
    NATS_A->>NATS: route → MSG
    NATS-->>B: payload
    B-->>Client: data: {payload}\n\n

    Note over Client: Client processes webhook<br/>(replay to local service, exec command, save)
```

---

### Replay POST

Replay (`POST /replay/{channel}`) uses the same NATS publish path. The replay endpoint validates the Bearer token from the project/global config, then publishes to NATS.

```mermaid
sequenceDiagram
    participant Replayer as External Replayer
    participant B as Instance B (replay handler)
    participant NATS as NATS
    participant Subs as SSE Subscribers

    Replayer->>B: POST /replay/abc123<br/>Authorization: Bearer <token>
    B->>B: Validate replay token<br/>(project-level or global default)
    B->>B: Build payload JSON<br/>(same as webhook POST format)
    B->>NATS: Publish("webhook.abc123", payload)
    NATS->>Subs: fan-out to all subscribers
    B-->>Replayer: 200 "replayed"
```

**Difference from webhook POST:** Replay skips signature validation and IP allowlist, requires Bearer token instead.

---

### Ring Buffer Eviction

```mermaid
flowchart TD
    APPEND[Entry appended] --> CHECK_SIZE{len > maxSize?}
    CHECK_SIZE -->|Yes| EVICT_OLDEST[Remove oldest entry]
    CHECK_SIZE -->|No| DONE[Entry stored]
    EVICT_OLDEST --> DONE

    TICKER[Every 30 seconds] --> SCAN[Scan all entries]
    SCAN --> CHECK_AGE{entry.age > maxAge?}
    CHECK_AGE -->|Yes| REMOVE[Remove entry]
    CHECK_AGE -->|No| KEEP[Keep entry]
    REMOVE --> NEXT{More entries?}
    KEEP --> NEXT
    NEXT -->|Yes| CHECK_AGE
    NEXT -->|No| SLEEP[Sleep 30s]
    SLEEP --> TICKER
```

**Eviction is dual-threshold:**
1. **Size-based**: When `len(entries) > maxSize`, oldest entry removed on append (instant)
2. **Time-based**: Cleanup goroutine removes entries older than `maxAge` every 30 seconds

---

## HA Scenarios

### Cross-instance Delivery

```mermaid
sequenceDiagram
    participant Sender as GitHub
    participant LB as Load Balancer
    participant A as Instance A
    participant NATS as NATS Cluster
    participant B as Instance B
    participant Client as gohookbridge client

    Note over Client: SSE connected to Instance B

    Sender->>LB: POST /abc123
    LB->>A: routes to Instance A

    A->>A: validate signature, parse
    A->>NATS: Publish("webhook.abc123", payload)

    Note over NATS: NATS cluster distributes
    NATS->>A: local subscriber gets copy
    NATS->>B: route → remote subscriber gets copy

    B->>B: Append to ring buffer
    B->>B: Fan-out to SSE subscriber chans

    B-->>Client: SSE data: {payload}

    Note over Client: ✓ Webhook received on different<br/>instance than where POST arrived
```

**Key insight:** The load balancer can route POST to any instance, and the SSE client can connect to any (potentially different) instance. NATS ensures the message reaches all instances.

### Late Subscriber Join

```mermaid
sequenceDiagram
    participant Sender as GitHub
    participant NATS as NATS Cluster
    participant RB as Ring Buffer (Instance B)
    participant Client as gohookbridge client

    Note over Client: NOT connected yet

    Sender->>NATS: webhook.abc123 t=12:00
    NATS->>RB: Append("abc123", payload1) t=12:00

    Sender->>NATS: webhook.abc123 t=12:02
    NATS->>RB: Append("abc123", payload2) t=12:02

    Sender->>NATS: webhook.abc123 t=12:05
    NATS->>RB: Append("abc123", payload3) t=12:05

    Note over Client: Client connects at t=12:06

    Client->>RB: GET /events/abc123
    RB-->>Client: drain: [payload1, payload2, payload3]
    Client->>Client: process historical webhooks

    Note over Client: Now live

    Sender->>NATS: webhook.abc123 t=12:10
    NATS-->>Client: live: payload4
```

**Ring buffer Get logic:**
- `since` defaults to `now - maxAge` (1 hour)
- Returns entries in chronological order (appended order)
- Limits to `limit` entries (default 100)

### Node Failure

```mermaid
flowchart TB
    subgraph "Normal Operation"
        A1[Instance A<br/>Raft leader + NATS]
        B1[Instance B<br/>Raft follower + NATS]
        C1[Instance C<br/>Raft follower + NATS]
    end

    subgraph "Instance B Fails"
        A2[Instance A<br/>Raft leader + NATS]
        C2[Instance C<br/>Raft follower + NATS]
        B2[Instance B<br/>❌ DOWN]
    end

    subgraph "Impact"
        RAFT_OK[Raft: quorum maintained ✓<br/>2/3 nodes, leader still active]
        NATS_OK[NATS: Instance B removed from cluster ✓<br/>A and C continue messaging]
        SSE_LOST[SSE clients on B: disconnected ✗<br/>Client reconnects to A or C via LB]
        BUFFER_LOST[Ring buffer on B: lost ✗<br/>No impact — other instances have their own]
    end
```

**Recovery when Instance B comes back:**
1. Raft: catches up via AppendEntries from leader
2. NATS: reconnects to cluster, syncs routes
3. Ring buffer: starts fresh (empty)
4. SSE clients: reconnect via load balancer health checks

**No data loss:** Webhooks are ephemeral by nature. The sender (GitHub/GitLab) will retry if no 202 response is received. Late subscribers on surviving instances get webhooks from their ring buffers.

---

## Configuration Mapping

### Server flags (HA deployment example)

```bash
# Instance 1
gohookbridge server \
  --address 0.0.0.0 --port 3333 \
  --public-url https://webhook.example.com \
  --raft-dir /data/raft \
  --raft-node-id node1 \
  --raft-bind-addr 10.0.0.1:6001 \
  --raft-peers node2=10.0.0.2:6001,node3=10.0.0.3:6001 \
  --nats-port 4222 \
  --nats-cluster-port 6222 \
  --nats-routes nats://10.0.0.2:6222,nats://10.0.0.3:6222 \
  --nats-buffer-ttl 1h \
  --nats-buffer-size 10000 \
  --bootstrap-config-file /etc/gohookbridge/bootstrap.yaml

# Instance 2
gohookbridge server \
  --address 0.0.0.0 --port 3333 \
  --public-url https://webhook.example.com \
  --raft-dir /data/raft \
  --raft-node-id node2 \
  --raft-bind-addr 10.0.0.2:6001 \
  --raft-peers node1=10.0.0.1:6001,node3=10.0.0.3:6001 \
  --nats-port 4222 \
  --nats-cluster-port 6222 \
  --nats-routes nats://10.0.0.1:6222,nats://10.0.0.3:6222 \
  --bootstrap-config-file /etc/gohookbridge/bootstrap.yaml

# Instance 3 — same pattern
```

### Port layout

| Port | Protocol | Purpose |
|---|---|---|
| 3333 | HTTP/HTTPS | Webhook ingestion + SSE + Admin UI + API |
| 6001 | TCP (Raft) | Configuration consensus between Raft nodes |
| 4222 | TCP (NATS client) | NATS client connections (in-process, localhost only) |
| 6222 | TCP (NATS cluster) | NATS inter-node cluster routes |

---

## Backward Compatibility

```mermaid
flowchart TD
    START[gohookbridge server starts] --> CHECK{NATS configured?<br/>--nats-port > 0?}
    CHECK -->|No| LEGACY[Single-instance mode]
    CHECK -->|Yes| NATS_MODE[NATS mode]

    LEGACY --> EB[Use EventBroker<br/>in-memory pub/sub]
    LEGACY --> SSE_OLD[SSE: subscribe to EventBroker channel]
    LEGACY --> POST_OLD[POST: events.Publish + EventBroker.Publish]

    NATS_MODE --> NATS_INIT[Start embedded NATS server]
    NATS_MODE --> NATS_CONN[Connect as NATS client]
    NATS_MODE --> NATS_SUB[Subscribe to webhook.> for ring buffer]
    NATS_MODE --> SSE_NEW[SSE: drain ring buffer + NATS subscribe]
    NATS_MODE --> POST_NEW[POST: NATS Publish]

    NATS_MODE --> CLUSTER_CHECK{NATS routes configured?}
    CLUSTER_CHECK -->|Yes| HA[HA mode: full mesh cluster]
    CLUSTER_CHECK -->|No| SINGLE[Single-instance with NATS<br/>buffer only, no HA]
```

**Zero-config default:** Without `--nats-port`, behavior is identical to current. No breaking changes. No new required dependencies for existing deployments.

**Single-instance with NATS:** Set `--nats-port 4222` without `--nats-routes` for buffer-only mode (late subscriber catch-up without HA clustering).

---

## Error Handling

### NATS unavailable at publish time
- NATS client has built-in reconnect with backoff
- If NATS is down, publish returns error → HTTP handler returns 503
- Sender (GitHub) will retry the webhook

### NATS server crash
- Embedded server process is the same as gohookbridge → crash kills gohookbridge
- Systemd/Kubernetes restarts the pod
- Raft and NATS both recover from their persisted state

### Ring buffer full
- Oldest entries evicted on append (FIFO)
- SSE client may not receive all historical messages
- Acceptable: webhooks are best-effort, sender retries

### SSE client disconnect during live stream
- `r.Context().Done()` fires → handler calls `broker.Unsubscribe()`
- Channel closed, goroutine exits
- No resource leak
