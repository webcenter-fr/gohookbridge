# Real E2E Encryption Plan

## Goal

Replace the current per-subscriber NaCl encryption model with a **shared keypair per channel** model. One keypair is generated per channel: the **public key** is used by producers to encrypt data, the **private key** is used by clients to decrypt data. The server supports a **hybrid approach**: it can receive plaintext (validates signatures, parses JSON, then encrypts with the channel public key) OR receive pre-encrypted data from the `produce` CLI / encrypt proxy (relays as-is).

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Channel Keypair                              │
│   Public Key (producers use to encrypt)                             │
│   Private Key (clients use to decrypt)                              │
└─────────────────────────────────────────────────────────────────────┘

Mode A: Standard webhook (GitHub, GitLab, etc.)
  Producer ──plaintext──▶ Server ──validates/parses──▶ encrypts with pubKey ──▶ NATS ──▶ Client decrypts with privKey

Mode B: E2E from producer (CLI produce / encrypt proxy)
  Producer ──encrypts with pubKey──▶ Server ──relays as-is──▶ NATS ──▶ Client decrypts with privKey
```

**Hybrid detection**: Server checks `IsEncrypted(body)` on incoming POST. If already encrypted → relay as-is. If plaintext → validate, parse, then encrypt.

---

## Phase 1: Data Model Changes

### 1.1 Channel struct (`gohookbridge/store/types.go`)

Add two new fields, keep `EncryptionPubKeys` for backward-compatible migration:

```go
type Channel struct {
    // ... existing fields ...
    EncryptionMode       string   `json:"encryption_mode,omitempty"`
    EncryptionKey        string   `json:"encryption_key,omitempty"`         // AES key (server_side mode)
    EncryptionPublicKey  string   `json:"encryption_public_key,omitempty"`  // NEW: single NaCl public key (e2e mode)
    EncryptionPrivateKey string   `json:"encryption_private_key,omitempty"` // NEW: single NaCl private key (e2e mode)
    EncryptionPubKeys    []string `json:"encryption_public_keys,omitempty"` // DEPRECATED: kept for migration
}
```

### 1.2 Migration (`gohookbridge/store/types.go` — `migrateChannel`)

```go
func migrateChannel(p *Channel) {
    // existing migrations...
    // NEW: migrate from multi-key to single key
    if p.EncryptionPublicKey == "" && len(p.EncryptionPubKeys) > 0 {
        p.EncryptionPublicKey = p.EncryptionPubKeys[0]
    }
}
```

Note: The private key cannot be recovered from the old model (old keys were per-subscriber and the server never stored private keys). Users must regenerate the keypair for true E2E.

### 1.3 ResolveChannelEncryption (`gohookbridge/store/raft.go`)

Update to return the new single-key fields:

```go
func (rs *RaftStore) ResolveChannelEncryption(channelID string) (mode, key, pubKey, privKey string, err error)
```

---

## Phase 2: Server Changes

### 2.1 Key generation endpoint (`gohookbridge/server.go` — `handleGenerateEncryptionKey`)

For `mode == "e2e"`:
- Generate ONE keypair (`GenerateKeyPair()`)
- Store `EncryptionPublicKey` (base64url) and `EncryptionPrivateKey` (base64std) on the channel
- Return both keys to the caller:
  ```json
  {
    "encryption_mode": "e2e",
    "encryption_public_key": "<base64url>",
    "encryption_private_key": "<base64std>",
    "key_file": { "public_key": "<base64std>", "private_key": "<base64std>" }
  }
  ```
- Remove the old behavior of appending to `EncryptionPubKeys` list

### 2.2 Webhook POST handler (`gohookbridge/server.go` — `handleWebhookPost`)

When `encryptionMode == "e2e"`:

1. Check if body is already encrypted: `IsEncrypted(body)`
2. **If already encrypted** (from produce CLI / proxy):
   - Skip JSON validation (`json.Unmarshal` body check)
   - Skip webhook signature validation (can't validate encrypted data)
   - Use the encrypted body as `payloadBytes`
3. **If plaintext** (standard webhook):
   - Validate webhook signature as usual
   - Parse JSON as usual
   - Encrypt body with channel's public key: `Encrypt(body, channelPubKey)`
   - Use encrypted result as `payloadBytes`
4. Wrap `payloadBytes` in the standard event envelope (headers + timestamp + bodyB + event_id)
5. Publish to NATS

### 2.3 SSE handler (`gohookbridge/server.go` — `handleEventsGet`)

- Remove the per-subscriber `pubKey` query parameter handling for E2E channels
- Remove the `Encrypt(data, pubKey)` calls in the SSE loop
- Data from NATS is already encrypted → relay as-is to all subscribers
- Keep the `ProtectedChannels` check for access control (optional: require authentication for E2E channels)

### 2.4 EventBroker (`gohookbridge/server.go`)

- Simplify `Publish()`: remove per-subscriber encryption logic
- Remove `PublicKey` field from `Subscriber` struct
- `Subscribe()` no longer takes a `pubKey` parameter
- EventBroker becomes a pure relay (data in = data out)

### 2.5 ProtectedChannels (`gohookbridge/store/bridge.go`)

- Update `Has()` to check `EncryptionPublicKey` (single key) instead of `EncryptionPubKeys` (list)
- Update `IsAllowed()` — in E2E mode with shared keypair, any authenticated client is allowed (no per-key check needed)
- Simplify: `Has()` returns true if `encryption_mode == "e2e"` and `encryption_public_key != ""`

### 2.6 Event replay (`gohookbridge/server.go` — `handleEventReplay`)

- For E2E channels: replay sends a new encrypted payload (encrypt the replay marker with the channel's public key)
- The replay body `{"replayed":true,"original_event_id":"..."}` is encrypted before publishing

---

## Phase 3: CLI — `produce` Command

### 3.1 New `produce` subcommand (`gohookbridge/app.go`)

```
gohookbridge produce --pubkey <base64url-public-key> <server-url>/<channel> [payload-file]
```

- Reads payload from file arg or stdin
- Encrypts with `Encrypt(payload, channelPubKey)` using NaCl box
- POSTs encrypted body to `<server-url>/<channel>` with `Content-Type: application/json`
- Flags: `--pubkey` (required), `--pubkey-file` (alternative: path to key file), `--insecure-skip-tls-verify`

### 3.2 New flags (`gohookbridge/flags.go`)

```go
var produceFlags = []cli.Flag{
    &cli.StringFlag{Name: "pubkey", Usage: "Channel public key (base64url) for E2E encryption"},
    &cli.StringFlag{Name: "pubkey-file", Usage: "Path to keypair JSON file (uses public_key field)"},
    &cli.BoolFlag{Name: "insecure-skip-tls-verify"},
}
```

### 3.3 Produce implementation (`gohookbridge/produce.go` — new file)

```go
func produce(c *cli.Context) error {
    // 1. Load public key from --pubkey or --pubkey-file
    // 2. Read payload from file arg or stdin
    // 3. Encrypt with NaCl box: Encrypt(payload, pubKey)
    // 4. POST to server URL
    // 5. Print response
}
```

---

## Phase 4: Encrypt Proxy

### 4.1 New `proxy` subcommand (`gohookbridge/app.go`)

```
gohookbridge proxy --pubkey <key> --listen :9090 --target https://server.example.com/channel
```

- Starts a lightweight HTTP server on `--listen`
- Receives plaintext webhooks from standard providers (GitHub, etc.)
- Encrypts the body with the channel's public key
- Forwards the encrypted body to `--target`
- Passes through all headers unchanged
- Flags: `--pubkey`, `--pubkey-file`, `--listen`, `--target`, `--insecure-skip-tls-verify`

### 4.2 Proxy implementation (`gohookbridge/proxy.go` — new file)

```go
func proxy(c *cli.Context) error {
    // 1. Load public key
    // 2. Start HTTP server on --listen
    // 3. On each POST: read body → Encrypt(body, pubKey) → POST to --target with same headers
    // 4. Return the server's response to the caller
}
```

---

## Phase 5: Client Changes

### 5.1 Client decryption (`gohookbridge/client.go`)

The `--encryption-key-file` flag now loads the **channel keypair** (shared keypair). The client uses the **private key** to decrypt.

Update `prepareSubscription()`:
- Remove `pubkey` query parameter from SSE URL (no longer needed — server doesn't encrypt per-subscriber)
- Still load the private key from the key file for decryption
- The SSE URL is simply `/events/<channel>` without `?pubkey=...`

Update `clientSetup()`:
- Decryption logic stays the same: `Decrypt(msg.Data, privateKey)` when `IsEncrypted(msg.Data)`
- No changes to AES decryption path

### 5.2 Client flags (`gohookbridge/flags.go`)

No new flags needed. Existing `--encryption-key-file` works for E2E, `--encryption-key` for AES.

---

## Phase 6: UI Changes

### 6.1 ChannelDetailView (`web/src/views/ChannelDetailView.vue`)

**Settings tab:**
- Replace the "Authorized Client Public Keys" list with a single keypair display:
  - "Generate Keypair" button (calls `POST /api/channels/{id}/generate-encryption-key` with `mode: "e2e"`)
  - Display public key with copy button
  - Display private key with copy button (show/hide toggle, like webhook secret)
  - "Download key file" button (downloads `{public_key, private_key}` JSON)
- Remove `encryption_public_keys` array management (add/remove key buttons)

**Clients tab:**
- **Producer section:**
  - Show public key with copy button
  - CLI command: `gohookbridge produce --pubkey <key> <origin>/<channel>`
  - Proxy command: `gohookbridge proxy --pubkey <key> --listen :9090 --target <origin>/<channel>`
- **Client section:**
  - "Download key file" button
  - CLI command: `gohookbridge client --encryption-key-file ./gohookbridge-key-<channel>.json <origin>/<channel> http://localhost:8080`

**Form state:**
- Replace `encryption_public_keys: string[]` with `encryption_public_key: string` and `encryption_private_key: string`
- Update `handleSave()` to send the new fields
- Update `handleGenerateClientKey()` → rename to `handleGenerateKeypair()`

### 6.2 EventFeed (`web/src/components/EventFeed.vue`)

- Update the encrypted event display for E2E channels:
  - Show "E2E Encrypted" tag instead of "Encrypted"
  - Show message: "This channel uses end-to-end encryption. Events are encrypted. Use the CLI client with `--encryption-key-file` to decrypt."
  - Display the encrypted envelope JSON (with ciphertext) as the event data

### 6.3 API client (`web/src/api/client.ts`)

- Update `Channel` interface: add `encryption_public_key` and `encryption_private_key`
- Update `generateEncryptionKey` return type to include `encryption_private_key`

### 6.4 ChannelsView (`web/src/views/ChannelsView.vue`)

- No changes needed (already shows encryption mode badge)

---

## Phase 7: Helm Chart

### 7.1 Chart structure

```
helm/gohookbridge/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── NOTES.txt
│   ├── server-deployment.yaml
│   ├── server-service.yaml
│   ├── server-ingress.yaml
│   ├── client-deployment.yaml
│   ├── proxy-deployment.yaml
│   ├── encryption-secret.yaml
│   └── serviceaccount.yaml
```

### 7.2 values.yaml

```yaml
server:
  enabled: true
  replicas: 1
  image:
    repository: ghcr.io/webcenter-fr/gohookbridge
    tag: main
  port: 3333
  publicURL: ""
  raftDir: /data/raft
  resources: {}
  ingress:
    enabled: false
    className: ""
    hosts: []
    tls: []

client:
  enabled: false
  replicas: 1
  image:
    repository: ghcr.io/webcenter-fr/gohookbridge
    tag: main
  channelURL: ""          # e.g., https://server.example.com/my-channel
  targetURL: ""           # e.g., http://my-service:8080
  encryptionKeyFile: ""   # path to mounted key file
  resources: {}

proxy:
  enabled: false
  replicas: 1
  image:
    repository: ghcr.io/webcenter-fr/gohookbridge
    tag: main
  listenPort: 9090
  targetURL: ""           # gohookbridge server channel URL
  publicKey: ""           # or use secret reference
  resources: {}

encryption:
  createSecret: false
  publicKey: ""           # base64url-encoded NaCl public key
  privateKey: ""          # base64std-encoded NaCl private key
  existingSecret: ""      # reference an existing Secret
```

### 7.3 Key templates

**encryption-secret.yaml**: Creates a K8s Secret with the channel keypair. Mounted by client (private key) and proxy (public key).

**client-deployment.yaml**: Mounts the encryption secret as a file at `/etc/gohookbridge/keys.json`. Passes `--encryption-key-file /etc/gohookbridge/keys.json` to the client.

**proxy-deployment.yaml**: Mounts the encryption secret. Passes `--pubkey-file /etc/gohookbridge/keys.json` to the proxy. Exposes the listen port via a Service.

---

## Phase 8: Tests

### 8.1 Backend unit tests

**`gohookbridge/crypto_test.go`** — add:
- `TestEncryptDecryptSharedKeypair`: generate one keypair, encrypt with public key, decrypt with private key
- `TestMultipleProducersSameKeypair`: multiple encryptions with same public key, all decryptable with same private key

**`gohookbridge/server_test.go`** — add:
- `TestHandleWebhookPostE2EPlaintext`: POST plaintext to E2E channel → server encrypts → stored data is encrypted
- `TestHandleWebhookPostE2EPreEncrypted`: POST pre-encrypted data to E2E channel → server relays as-is
- `TestHandleWebhookPostE2ESkipsSignatureValidation`: POST encrypted data without signature → accepted
- `TestEventBrokerRelaysEncryptedData`: EventBroker passes encrypted data through without modification

**`gohookbridge/client_test.go`** — add:
- `TestClientDecryptsE2ESharedKeypair`: client with shared private key decrypts server-encrypted data
- `TestPrepareSubscriptionNoPubkeyParam`: SSE URL does not include `?pubkey=` for E2E channels

**`gohookbridge/produce_test.go`** (new) — add:
- `TestProduceEncryptsAndPosts`: produce command encrypts payload and POSTs to server
- `TestProduceReadsFromStdin`: produce reads from stdin when no file arg
- `TestProduceLoadsPubkeyFromFile`: produce loads public key from keypair file

**`gohookbridge/proxy_test.go`** (new) — add:
- `TestProxyEncryptsAndForwards`: proxy receives plaintext, encrypts, forwards to target
- `TestProxyPassesHeaders`: proxy preserves all HTTP headers

### 8.2 Frontend unit tests

- Test ChannelDetailView keypair generation and display
- Test EventFeed encrypted event display for E2E mode

### 8.3 Integration tests

- **E2E round-trip**: produce CLI encrypts → server receives → NATS stores → client decrypts → verify plaintext matches
- **Hybrid round-trip**: standard webhook POST → server encrypts → client decrypts → verify plaintext matches
- **Proxy round-trip**: plaintext POST to proxy → proxy encrypts → server receives → client decrypts

---

## Phase 9: Documentation

- Update `CONTRIBUTING.md` with new encryption model description
- Update `README.md` with E2E encryption usage examples (produce, proxy, client)
- Add Helm chart README in `helm/gohookbridge/README.md`

---

## Implementation Order

1. **Phase 1** — Data model changes (types.go, raft.go, migration)
2. **Phase 2** — Server changes (key generation, webhook POST, SSE, EventBroker)
3. **Phase 5** — Client changes (simplified SSE subscription)
4. **Phase 3** — Produce CLI command
5. **Phase 4** — Encrypt proxy
6. **Phase 6** — UI changes
7. **Phase 7** — Helm chart
8. **Phase 8** — Tests (can be done incrementally with each phase)
9. **Phase 9** — Documentation

---

## Best Practice Checklist

- [x] Single shared keypair per channel (simple key management)
- [x] NaCl box with ephemeral keys (forward secrecy per message)
- [x] Hybrid mode supports standard webhook providers (no producer changes needed)
- [x] True E2E mode available via produce CLI / proxy (server never sees plaintext)
- [x] Private key only distributed to authorized clients (via UI download or K8s Secret)
- [x] Server stores private key for admin UI display (acceptable — admin is trusted)
- [x] HTTPS still required for transport security (E2E is defense in depth)
- [ ] Key rotation: users generate a new keypair and reconfigure producers/clients (manual for now)
- [ ] Sender authentication: not provided by shared keypair model (acceptable tradeoff for webhook relay)
