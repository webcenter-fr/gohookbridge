# Channel TTL Enforcement & Client Read Tracking

## Summary

1. Wire `Channel.MessageTTLSeconds` into the ring buffer's per-channel TTL eviction (model + buffer logic already exist, just not connected).
2. Add server-side client read cursors (stored in Raft/BoltDB) so reconnecting CLI clients resume from where they left off.
3. Remove the unused `--default-message-ttl` server flag.

## Current State

### What already exists
- `Channel.MessageTTLSeconds` — stored in Raft, editable via UI/API (`store/types.go:17`)
- `DefaultChannelConfig.MessageTTLSeconds` — global default, inherited via `resolveChannelConfig()` (`store/types.go:68`, `store/types.go:147-149`)
- `RingBuffer.channelTTLs` map + `SetChannelTTL()` + per-channel eviction logic (`nats/buffer.go:20-43`, `nats/buffer.go:125-165`)
- `evictExpired()` already checks per-channel TTL and uses the smaller of channel TTL vs global `maxAge` (`nats/buffer.go:147-153`)
- `Broker.Subscribe()` drains historical data from ring buffer on SSE connect (`nats/broker.go:127-143`)
- NATS core only (JetStream disabled) — no built-in consumer offsets

### What's missing
- **TTL wiring**: `SetChannelTTL()` on the ring buffer is never called from server code.
- **Read tracking**: No mechanism for a reconnecting client to get only unread messages.
- **Flag cleanup**: `--default-message-ttl` server flag is defined but unused.

## Design Decisions

### TTL: Per-channel only, inherited from global defaults
- Remove `--default-message-ttl` server flag (unused, redundant with global config defaults).
- Channel `MessageTTLSeconds` = 0 means "inherit from `DefaultChannelConfig.MessageTTLSeconds`" (already works via `resolveChannelConfig`).
- If both are 0, use `--nats-buffer-ttl` (ring buffer global maxAge) as fallback for eviction (current behavior, unchanged).
- No new CLI flags needed.

### Read Tracking: Server-side cursors in Raft (not client-side files)
- Each CLI client gets a unique `client_id` (auto-generated on first run, stored in `~/.gohookbridge/client-id`, overridable via `--client-id` flag).
- On SSE connect, client sends `?client_id=<id>`.
- Server looks up the cursor for `(channel, client_id)` from BoltDB.
- Server drains ring buffer from `cursor_timestamp` onward instead of `now - bufferTTL`.
- Cursor is written to Raft **only on graceful SSE disconnect** (minimizes Raft writes).
- If cursor not found or client crashed without graceful disconnect: fall back to `now - bufferTTL` (no data loss guarantee beyond buffer window — acceptable for ephemeral webhooks).

### Why Raft for cursors
- NATS core (used in this project) has no consumer offset mechanism. NATS JetStream would provide this but requires significant architectural change (JetStream enabled, streams per channel, disk persistence).
- Raft is already the source of truth for all durable state in this project.
- Cursors written only on disconnect keep Raft overhead minimal (~1 write per client session end).

## Plan

### 1. Backend: Remove `--default-message-ttl` flag

**File: `gohookbridge/flags.go`**

Remove lines 230-234:
```go
&cli.IntFlag{
    Name:    "default-message-ttl",
    Usage:   "Default message TTL in seconds for channels (0 = use nats-buffer-ttl)",
    Value:   0,
    EnvVars: []string{"GOSMEE_DEFAULT_MESSAGE_TTL"},
},
```

### 2. Backend: Wire Channel TTL to Ring Buffer

**2a. Load TTL on startup**

File: `gohookbridge/server.go`, in `serve()`, after NATS broker creation (~line 830):

```go
if broker != nil {
    channels, _ := rs.ListChannels()
    for _, ch := range channels {
        resolved, _ := rs.ResolveChannelConfig(ch.ID)
        if resolved.MessageTTLSeconds > 0 {
            broker.SetChannelTTL(ch.ID, time.Duration(resolved.MessageTTLSeconds)*time.Second)
        }
    }
}
```

**2b. Set TTL on webhook publish**

File: `gohookbridge/server.go`, in `handleWebhookPost`, after resolving channel config (~line 219):

```go
if broker != nil {
    ch, _ := rs.ResolveChannelConfig(channel)
    if ch.MessageTTLSeconds > 0 {
        broker.SetChannelTTL(channel, time.Duration(ch.MessageTTLSeconds)*time.Second)
    }
}
```

**2c. Update TTL on channel config change (API)**

File: `gohookbridge/store/api.go`

Extend `apiHandler` to hold a broker reference and a callback interface. Since `store/` package shouldn't import `nats/`, use a callback/interface pattern:

```go
type ChannelChangeNotifier interface {
    OnChannelChanged(channelID string, ttlSeconds int)
}

type apiHandler struct {
    rs              *RaftStore
    channelNotifier ChannelChangeNotifier
}

func RegisterAPIHandlers(r chi.Router, rs *RaftStore, notifier ChannelChangeNotifier) {
    h := &apiHandler{rs: rs, channelNotifier: notifier}
    // ... existing routes ...
}
```

After successful `CreateChannel` and `UpdateChannel` in the API handler, call:
```go
if h.channelNotifier != nil {
    ch, _ := h.rs.ResolveChannelConfig(ch.ID)
    h.channelNotifier.OnChannelChanged(ch.ID, ch.MessageTTLSeconds)
}
```

File: `gohookbridge/server.go`

Implement `ChannelChangeNotifier` on a wrapper or pass broker directly:

```go
type brokerTTLNotifier struct {
    broker *nats.Broker
    rs     *store.RaftStore
}

func (n *brokerTTLNotifier) OnChannelChanged(channelID string, ttlSeconds int) {
    if ttlSeconds > 0 {
        n.broker.SetChannelTTL(channelID, time.Duration(ttlSeconds)*time.Second)
    }
}
```

Update `serve()`:
```go
notifier := &brokerTTLNotifier{broker: broker, rs: rs}
store.RegisterAPIHandlers(apiRouter, rs, notifier)
```

### 3. Backend: Server-Side Client Read Cursors

**3a. Cursor types and RaftStore methods**

File: `gohookbridge/store/types.go`

Add cursor type:
```go
type ClientCursor struct {
    Channel        string `json:"channel"`
    ClientID       string `json:"client_id"`
    LastTimestampMs int64  `json:"last_timestamp_ms"`
}
```

File: `gohookbridge/store/raft.go`

Add cursor methods:
```go
func (rs *RaftStore) GetClientCursor(channel, clientID string) (*ClientCursor, error) {
    key := "/cursors/" + channel + "/" + clientID + "/"
    val, err := getFSMValue(rs.db, key)
    if err != nil || val == nil {
        return nil, nil
    }
    var c ClientCursor
    if err := json.Unmarshal(val, &c); err != nil {
        return nil, err
    }
    return &c, nil
}

func (rs *RaftStore) SetClientCursor(cursor *ClientCursor) error {
    key := "/cursors/" + cursor.Channel + "/" + cursor.ClientID + "/"
    val, err := json.Marshal(cursor)
    if err != nil {
        return err
    }
    return rs.applyCommand("set", key, val)
}
```

**3b. Accept `client_id` on SSE endpoint**

File: `gohookbridge/server.go`, in `handleEventsGet`:

Add `client_id` query param parsing. If present, look up cursor for `since`:

```go
var since time.Time
clientID := r.URL.Query().Get("client_id")
if clientID != "" {
    cursor, _ := rs.GetClientCursor(channel, clientID)
    if cursor != nil && cursor.LastTimestampMs > 0 {
        since = time.UnixMilli(cursor.LastTimestampMs)
    }
}
```

**3c. Extend `Broker.Subscribe` to accept `since`**

File: `gohookbridge/nats/broker.go`:

```go
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
```

Update the SSE handler call:
```go
historical, live := broker.Subscribe(channel, since, 100)
```

**3d. Save cursor on SSE disconnect**

In `handleEventsGet`, after the SSE loop exits (client disconnected), save the cursor:

```go
if broker != nil {
    historical, live := broker.Subscribe(channel, since, 100)
    defer broker.Unsubscribe(channel, live)

    // Drain historical...
    for _, data := range historical {
        fmt.Fprintf(w, "data: %s\n\n", data)
        flusher.Flush()
    }

    // SSE loop...
    sseLoop(w, flusher, live, clientGone, ticker)

    // Save cursor on disconnect
    if clientID != "" {
        lastTs := time.Now().UTC().UnixMilli()
        rs.SetClientCursor(&store.ClientCursor{
            Channel:         channel,
            ClientID:        clientID,
            LastTimestampMs: lastTs,
        })
    }
} else {
    // existing EventBroker path (unchanged)
}
```

To track the actual last message timestamp delivered (not just disconnect time), extend `sseLoop` or the handler to capture the last message's timestamp from the parsed payload. Simpler approach: use `time.Now()` at disconnect time — this means the client will re-read messages between the last actual message and disconnect time, which is harmless (idempotent delivery).

### 4. CLI Client: Client ID & Resume

**4a. Client ID management**

File: `gohookbridge/client.go`

Add client ID generation/persistence:
```go
func getOrCreateClientID() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil {
        home = "."
    }
    dir := filepath.Join(home, ".gohookbridge")
    os.MkdirAll(dir, 0700)
    
    idFile := filepath.Join(dir, "client-id")
    if data, err := os.ReadFile(idFile); err == nil {
        return strings.TrimSpace(string(data)), nil
    }
    
    id := generateUUID()
    os.WriteFile(idFile, []byte(id), 0600)
    return id, nil
}
```

**4b. Add `--resume` and `--client-id` flags**

File: `gohookbridge/flags.go`, in `clientFlags`:

```go
&cli.BoolFlag{
    Name:    "resume",
    Aliases: []string{"r"},
    Usage:   "Resume from last seen position on reconnect (uses client-id)",
    Value:   false,
},
&cli.StringFlag{
    Name:    "client-id",
    Usage:   "Client identifier for resume tracking (auto-generated if not set)",
    EnvVars: []string{"GOSMEE_CLIENT_ID"},
},
```

**4c. Pass `client_id` in SSE URL**

File: `gohookbridge/client.go`, in `prepareSubscription()` or `clientSetup()`:

When `--resume` is set:
```go
if c.replayDataOpts.resume {
    clientID := c.replayDataOpts.clientID
    if clientID == "" {
        clientID, _ = getOrCreateClientID()
    }
    // Append ?client_id= to SSE URL
    parsedURL, _ := url.Parse(sseURL)
    query := parsedURL.Query()
    query.Set("client_id", clientID)
    parsedURL.RawQuery = query.Encode()
    sseURL = parsedURL.String()
}
```

Update `replayDataOpts` to include `resume` and `clientID` fields.

### 5. Frontend: TTL Info Display

**File: `web/src/components/EventFeed.vue`**

Add a small info text showing the channel's TTL if available:

```html
<n-text v-if="messageTTL" depth="3" style="font-size: 12px; margin-top: 4px;">
  Messages retained for {{ formatTTL(messageTTL) }}
</n-text>
```

Add `messageTTL` prop (optional number, seconds).

**File: `web/src/views/ChannelDetailView.vue`**

Pass `messageTTL` to `EventFeed` from channel config.

### 6. Tests

**6a. Backend unit tests**

File: `gohookbridge/nats/broker_test.go`:
- `TestRingBufferPerChannelTTLEvictionMixed`: Verify entries from different channels with different TTLs are evicted correctly when interleaved
- `TestBrokerSubscribeWithSince`: Verify `Subscribe(channel, since, limit)` returns only entries newer than `since`
- `TestBrokerSubscribeWithClientID`: Verify cursor lookup and drain behavior

File: `gohookbridge/nats/buffer_test.go` (new or extend existing):
- `TestRingBufferChannelTTLUpdate`: Set TTL, append, change TTL, verify new eviction applies

File: `gohookbridge/store/raft_test.go`:
- `TestClientCursorCRUD`: Create, read, update, delete client cursor

File: `gohookbridge/server_test.go`:
- `TestHandleEventsGetWithClientID`: SSE endpoint with `?client_id=` returns correct historical data
- `TestChannelTTLPropagation`: Channel TTL is set on broker after create/update

**6b. Integration tests**

File: `gohookbridge/server_test.go` or new integration test file:
- Publish webhooks → verify ring buffer has entries with channel TTL → wait for TTL expiry → verify entries purged
- Publish webhooks → client connects with `client_id` → publishes more → disconnect → client reconnects with same `client_id` → verify only new messages delivered

**6c. Client tests**

File: `gohookbridge/client_test.go`:
- `TestClientIDPersistence`: Client ID is generated and persisted across restarts
- `TestResumeFlagAppendsClientIDToURL`: SSE URL includes `?client_id=` when `--resume` is set

### Files Changed Summary

| File | Change |
|------|--------|
| `gohookbridge/flags.go` | Remove `--default-message-ttl` server flag; add `--resume`, `--client-id` client flags |
| `gohookbridge/server.go` | Wire TTL on startup/publish; add `client_id` param to SSE; save cursor on disconnect; wire `ChannelChangeNotifier` |
| `gohookbridge/nats/broker.go` | Extend `Subscribe()` to accept `since` param |
| `gohookbridge/nats/buffer.go` | No changes (already supports per-channel TTL) |
| `gohookbridge/nats/broker_test.go` | Add `TestBrokerSubscribeWithSince`, `TestRingBufferPerChannelTTLEvictionMixed` |
| `gohookbridge/store/types.go` | Add `ClientCursor` type |
| `gohookbridge/store/raft.go` | Add `GetClientCursor`, `SetClientCursor` methods |
| `gohookbridge/store/api.go` | Add `ChannelChangeNotifier` interface; call notifier on create/update channel |
| `gohookbridge/store/raft_test.go` | Add `TestClientCursorCRUD` |
| `gohookbridge/client.go` | Add client ID generation/persistence; add `--resume`/`--client-id` support; pass `?client_id=` in SSE URL |
| `gohookbridge/client_test.go` | Add client ID and resume tests |
| `gohookbridge/server_test.go` | Add SSE `client_id` and TTL propagation tests |
| `web/src/components/EventFeed.vue` | Add optional TTL info display |
| `web/src/views/ChannelDetailView.vue` | Pass channel TTL to EventFeed |
