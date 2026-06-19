# Fix Encryption Workflow: Make Both Modes Work End-to-End

## Problem Summary

1. **`provider_side` (NaCl box) is completely broken** — `server.go:533-536` returns `501 Not Implemented` when a subscriber connects with a public key, because the NATS broker doesn't support per-subscriber encryption.
2. **UI "Generate Keypair" is useless** — it generates a keypair server-side, stores the private key on the server (insecure!), and never provides a downloadable file. The user sees no visible change.
3. **Client cannot decrypt AES** — `server_side` mode encrypts with AES-256-GCM on POST, but the client (`client.go`) only handles NaCl box decryption, not AES.
4. **Confusing naming** — "provider_side" implies the webhook provider encrypts, but it doesn't. The encryption is per-subscriber end-to-end.
5. **Obscure client workflow** — requires 3 manual CLI steps with no UI integration.

## Changes

### 1. Rename `provider_side` → `e2e` (breaking change with migration)

**Files:**
- `gohookbridge/store/types.go` — Update `migrateChannel()` to map `"provider_side"` → `"e2e"` (backward compat for existing Raft data)
- `gohookbridge/store/bridge.go` — Update all `"provider_side"` string comparisons to `"e2e"`
- `gohookbridge/server.go` — Update `"provider_side"` references in `handleGenerateEncryptionKey` and `handleEventReplay`
- `gohookbridge/server_test.go` — Update test fixtures
- `gohookbridge/store/storetest/helper.go` — Update test fixtures
- `web/src/views/ChannelDetailView.vue` — Update select options and conditionals
- `web/src/views/ChannelsView.vue` — Update display logic
- `web/src/api/client.ts` — No change needed (uses string values from API)

**Migration logic in `migrateChannel()`:**
```go
func migrateChannel(p *Channel) {
    // existing webhook migration...
    if p.EncryptionMode == "" && p.EncryptionEnabled {
        p.EncryptionMode = "e2e"
    }
    if p.EncryptionMode == "provider_side" {
        p.EncryptionMode = "e2e"
    }
}
```

### 2. Fix SSE handler to support per-subscriber NaCl encryption with NATS

**File: `gohookbridge/server.go` — `handleEventsGet()`**

Remove the `501 Not Implemented` block (lines 533-536). Instead, when `pubKey != nil`, wrap the SSE output to encrypt each message before writing:

```go
// Remove lines 533-536 entirely

// In the historical messages loop:
for _, data := range historical {
    payload := data
    if pubKey != nil {
        encrypted, err := Encrypt(data, pubKey)
        if err != nil {
            // log warning, skip
            continue
        }
        payload = encrypted
    }
    fmt.Fprintf(w, "data: %s\n\n", payload)
    // ...
}

// Pass pubKey to sseLoop so live messages are also encrypted:
sseLoop(w, flusher, live, clientGone, ticker, &lastMsgTs, pubKey)
```

**Update `sseLoop` signature** to accept an optional `pubKey *[32]byte` and encrypt before writing:
```go
func sseLoop(w http.ResponseWriter, flusher http.Flusher, events <-chan []byte, clientGone <-chan struct{}, ticker *time.Ticker, lastMsgTs *int64, pubKey *[32]byte) {
    for {
        select {
        case <-clientGone:
            return
        case data, ok := <-events:
            if !ok { return }
            payload := data
            if pubKey != nil {
                encrypted, err := Encrypt(data, pubKey)
                if err != nil { continue }
                payload = encrypted
            }
            fmt.Fprintf(w, "data: %s\n\n", payload)
            // ...
        }
    }
}
```

### 3. Redesign the key generation API endpoint

**File: `gohookbridge/server.go` — `handleGenerateEncryptionKey()`**

For `e2e` mode (renamed from `provider_side`):
- Generate a NaCl keypair
- Add the public key to the channel's `EncryptionPubKeys`
- **Do NOT store the private key on the server** (remove `ch.EncryptionKey = ...`)
- Return both `public_key` and `private_key` in the response so the UI can trigger a download
- Clear `ch.EncryptionKey` for e2e channels (it should only be used for AES `server_side` mode)

Response format:
```json
{
  "encryption_mode": "e2e",
  "encryption_public_key": "base64-pub-key",
  "key_file": {
    "public_key": "base64-pub-key",
    "private_key": "base64-priv-key"
  }
}
```

### 4. Add AES decryption support to the client

**File: `gohookbridge/client.go` — `clientSetup()` and `parse()`**

In the SSE event handler (around line 756), after NaCl decryption check, add AES decryption:

```go
payload := msg.Data
if privateKey != nil && IsEncrypted(msg.Data) {
    // existing NaCl decryption...
} else if c.replayDataOpts.encryptionKey != "" && IsAESEncrypted(msg.Data) {
    decrypted, err := AESDecrypt(msg.Data, c.replayDataOpts.encryptionKey)
    if err != nil { /* log error, return */ }
    payload = decrypted
}
```

This requires passing the AES key to the client. Add a new CLI flag `--encryption-key` (for AES shared key) in `flags.go`, and add it to `replayDataOpts`.

**File: `gohookbridge/flags.go`** — Add `--encryption-key` flag to `clientFlags`.

**File: `gohookbridge/app.go`** — Wire the new flag into `replayDataOpts`.

### 5. Redesign the UI — ChannelDetailView.vue

#### Settings Tab — Encryption section

Replace the current encryption UI with clearer options:

**For `server_side` (AES):**
- Show the AES key field with "Generate Key" button (unchanged)
- Add a note: "All subscribers receive AES-encrypted payloads. Clients must use `--encryption-key` to decrypt."

**For `e2e` (NaCl box, renamed from `provider_side`):**
- Remove the "Generate Keypair" button from Settings (moved to Clients tab)
- Show the list of authorized public keys with delete buttons
- Add a note: "Each subscriber gets a unique keypair. Generate keys in the Clients tab."

#### Clients Tab — Complete redesign

Replace the current 3-step CLI instructions with an integrated workflow:

**Step 1: Generate & Download Key**
- "Generate Client Key" button → calls `POST /api/channels/{id}/generate-encryption-key` with `{ mode: "e2e" }`
- On success, triggers a browser download of `gohookbridge-key-{channelId}.json` containing `{ public_key, private_key }`
- Shows a success message with the public key

**Step 2: Copy Client Command**
- Show the client command with the key file path:
  ```
  gohookbridge client --encryption-key-file ./gohookbridge-key-{channelId}.json {origin}/{channelId} http://localhost:8080
  ```
- Copy button

**For `server_side` (AES) channels:**
- Show the AES key (masked) with copy button
- Show the client command:
  ```
  gohookbridge client --encryption-key {aes-key} {origin}/{channelId} http://localhost:8080
  ```

#### Authorized Keys List
- Show each public key with a truncated display
- Delete button per key (removes from `encryption_public_keys` array, requires Save)

### 6. Update frontend API client

**File: `web/src/api/client.ts`**

Update `generateEncryptionKey` return type to include the key file data:
```typescript
async generateEncryptionKey(channelId: string, mode: string): Promise<{
  encryption_mode: string
  encryption_public_key?: string
  encryption_key?: string
  key_file?: { public_key: string; private_key: string }
}>
```

### 7. Handle admin UI SSE for encrypted channels

**File: `web/src/stores/events.ts`**

For `server_side` AES channels, the admin UI connects to SSE without a key, so it receives AES-encrypted payloads. The events store should detect AES-encrypted payloads and show a warning: "This channel uses server-side encryption. Events are encrypted in transit. Use the CLI client with `--encryption-key` to decrypt."

For `e2e` channels, the admin UI connects without a pubkey, so it receives plaintext (the server only encrypts for subscribers who present a registered pubkey). This is the current behavior and is acceptable — the admin UI sees plaintext because it's an authenticated admin.

### 8. Update the `keygen` CLI command

**File: `gohookbridge/app.go`**

The existing `keygen` command works correctly (generates locally, saves to file, prints pubkey). No changes needed, but update the help text to mention `e2e` mode.

### 9. Tests

#### Backend tests

- **`server_test.go`**: Add test for `handleEventsGet` with a pubkey subscriber — verify that historical and live messages are NaCl-encrypted when `pubKey != nil`
- **`server_test.go`**: Update `handleGenerateEncryptionKey` test for `e2e` mode — verify private key is NOT stored on server, response includes `key_file`
- **`server_test.go`**: Add test for `handleEventsGet` with `e2e` channel — verify subscriber without pubkey gets plaintext, subscriber with pubkey gets encrypted
- **`client_test.go`**: Add test for AES decryption in the client event handler
- **`encryption_test.go`**: Existing tests are sufficient
- **`crypto_test.go`**: Existing tests are sufficient
- **`store/types_test.go`** or inline: Test `migrateChannel` maps `"provider_side"` → `"e2e"`

#### Frontend tests

- **`ChannelDetailView.vue`**: Test that "Generate Client Key" triggers a file download
- **`ChannelDetailView.vue`**: Test that the encryption mode select shows correct labels
- **`client.ts`**: Test the updated `generateEncryptionKey` return type

#### Integration tests

- **End-to-end e2e encryption**: Create channel with `e2e` mode → generate key via API → POST webhook → connect SSE with pubkey → verify decrypted payload matches original
- **End-to-end AES encryption**: Create channel with `server_side` mode → POST webhook → connect SSE → verify payload is AES-encrypted → decrypt with key → verify matches original

## File Change Summary

| File | Change |
|---|---|
| `gohookbridge/store/types.go` | Add `"provider_side"` → `"e2e"` migration |
| `gohookbridge/store/bridge.go` | Replace `"provider_side"` with `"e2e"` |
| `gohookbridge/server.go` | Fix SSE encryption for NATS, redesign key generation endpoint, update mode strings |
| `gohookbridge/client.go` | Add AES decryption support in SSE handler |
| `gohookbridge/flags.go` | Add `--encryption-key` flag for AES client decryption |
| `gohookbridge/app.go` | Wire new `--encryption-key` flag |
| `gohookbridge/server_test.go` | Add/update tests for SSE encryption, key generation |
| `gohookbridge/client_test.go` | Add AES decryption test |
| `gohookbridge/store/storetest/helper.go` | Update test fixture mode string |
| `web/src/views/ChannelDetailView.vue` | Redesign encryption UI, add key download |
| `web/src/views/ChannelsView.vue` | Update mode display labels |
| `web/src/api/client.ts` | Update `generateEncryptionKey` return type |
| `web/src/stores/events.ts` | Handle encrypted payloads in admin UI |

## Execution Order

1. Backend: rename `provider_side` → `e2e` with migration (types, bridge, server, tests)
2. Backend: fix SSE handler to encrypt per-subscriber with NATS
3. Backend: redesign key generation endpoint (no private key storage, return key_file)
4. Backend: add AES client decryption support (client.go, flags.go, app.go)
5. Frontend: update API client types
6. Frontend: redesign ChannelDetailView encryption UI
7. Frontend: update ChannelsView display
8. Frontend: handle encrypted payloads in events store
9. Tests: add all new tests
10. Run `make lint && make test && cd web && npx vue-tsc --noEmit`
