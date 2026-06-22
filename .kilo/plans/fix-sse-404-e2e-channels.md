# Fix SSE 404 for E2E Channels — Separate Access Control from Encryption

## Problem Statement

Currently, the SSE endpoint (`GET /events/{channel}`) returns **404 Not Found** for E2E-encrypted channels when the request doesn't include a `?pubkey=` parameter. This is incorrect because:

1. **Access control** (who can connect) should be governed by **RBAC rules**, not encryption settings
2. **Encryption** (data format) is a separate concern — E2E channels should relay encrypted data as-is
3. The server should never use encryption as a gate for access control

### Current (Incorrect) Behavior

```go
// handleEventsGet in server.go
if protectedChannels.Has(channel) {
    pubkey := r.URL.Query().Get("pubkey")
    if pubkey == "" {
        rejectProtectedChannelRequest(w)  // Returns 404
        return
    }
    // ... validate pubkey ...
}
```

### Correct Behavior

- **Session-based auth** (browser UI): Check user's RBAC permissions for the channel (`channel:read`)
- **Token-based auth** (CLI): Check channel access token scope (`consume`)
- **E2E channels**: Relay encrypted data as-is (no server-side decryption)
- **Client-side decryption**: Optional feature where user provides private key in UI

---

## Implementation Plan

### Phase 1: Backend — Remove Encryption-Based Access Control

#### 1.1 Remove `protectedChannels.Has()` check from `handleEventsGet`

**File:** `gohookbridge/server/server.go`

**Changes:**
- Remove lines 602-614 (the `protectedChannels.Has(channel)` block)
- Remove the `rejectProtectedChannelRequest()` function (line 106-108)
- The handler should proceed directly to NATS subscription after validation

**Before:**
```go
func handleEventsGet(broker *nats.Broker, protectedChannels *store.ProtectedChannels, rs *store.RaftStore) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        channel := chi.URLParam(r, "channel")
        // ... validation ...

        if protectedChannels.Has(channel) {
            pubkey := r.URL.Query().Get("pubkey")
            if pubkey == "" {
                rejectProtectedChannelRequest(w)
                return
            }
            // ... validate pubkey ...
        }

        // ... SSE setup ...
    }
}
```

**After:**
```go
func handleEventsGet(broker *nats.Broker, rs *store.RaftStore) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        channel := chi.URLParam(r, "channel")
        // ... validation ...

        // No encryption-based access control here
        // Access is controlled by channelAccessMiddleware (RBAC)

        // ... SSE setup ...
    }
}
```

#### 1.2 Update `channelAccessMiddleware` to support session-based auth

**File:** `gohookbridge/server/server.go`

**Changes:**
- Modify `channelAccessMiddleware` to check session cookie first
- If session exists, validate RBAC permissions using `store.UserHasPermission(rs, username, store.PermChannelRead, channel)`
- If no session, fall back to token-based auth (existing behavior)

**Implementation:**
```go
func channelAccessMiddleware(rs *store.RaftStore, requiredScope string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            channel := chi.URLParam(r, "channel")

            // Try session-based auth first (browser UI)
            if cookie, err := r.Cookie(sessionCookieName); err == nil {
                if token, err := decodeSession(cookie.Value, sessionSecret); err == nil {
                    // Check RBAC permissions
                    perm := store.PermChannelRead
                    if requiredScope == "produce" {
                        perm = store.PermChannelWrite
                    }
                    if store.UserHasPermission(rs, token.Username, perm, channel) {
                        // Add username to context for downstream handlers
                        ctx := context.WithValue(r.Context(), store.UsernameContextKey, token.Username)
                        if len(token.Groups) > 0 {
                            ctx = context.WithValue(ctx, store.GroupsContextKey, token.Groups)
                        }
                        next.ServeHTTP(w, r.WithContext(ctx))
                        return
                    }
                    http.Error(w, "Forbidden", http.StatusForbidden)
                    return
                }
            }

            // Fall back to token-based auth (CLI clients)
            chConfig, err := rs.ResolveChannelConfig(channel)
            if err != nil {
                next.ServeHTTP(w, r)
                return
            }

            if chConfig.AccessMode != "token" {
                next.ServeHTTP(w, r)
                return
            }

            token := r.URL.Query().Get("token")
            if token == "" {
                auth := r.Header.Get("Authorization")
                if strings.HasPrefix(auth, "Bearer ") {
                    token = strings.TrimPrefix(auth, "Bearer ")
                }
            }

            if token == "" {
                http.Error(w, "Unauthorized", http.StatusUnauthorized)
                return
            }

            if !rs.ValidateChannelToken(channel, token, requiredScope) {
                http.Error(w, "Unauthorized", http.StatusUnauthorized)
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

#### 1.3 Update route registration

**File:** `gohookbridge/server/server.go`

**Changes:**
- Remove `protectedChannels` parameter from `handleEventsGet` call
- Update line 942:

**Before:**
```go
mainRouter.Get(eventsPath, channelAccessMiddleware(rs, "consume")(handleEventsGet(broker, protectedChannels, rs)).ServeHTTP)
```

**After:**
```go
mainRouter.Get(eventsPath, channelAccessMiddleware(rs, "consume")(handleEventsGet(broker, rs)).ServeHTTP)
```

#### 1.4 Remove `protectedChannels` initialization

**File:** `gohookbridge/server/server.go`

**Changes:**
- Remove line 858: `protectedChannels := store.NewProtectedChannelsDynamic(rs)`
- This variable is no longer needed

#### 1.5 Update tests

**File:** `gohookbridge/server/server_test.go`

**Changes:**
- Update `TestHandleEventsGet` to remove pubkey-related tests
- Add tests for session-based auth:
  - `TestHandleEventsGet_SessionAuth_Allowed`: User with `channel:read` permission can connect
  - `TestHandleEventsGet_SessionAuth_Forbidden`: User without permission gets 403
  - `TestHandleEventsGet_TokenAuth_Allowed`: Valid token can connect
  - `TestHandleEventsGet_TokenAuth_Unauthorized`: Invalid/missing token gets 401
- Remove tests for `rejectProtectedChannelRequest`

---

### Phase 2: Frontend — Client-Side Decryption for E2E Channels

#### 2.1 Add private key input to ChannelDetailView

**File:** `web/src/views/ChannelDetailView.vue`

**Changes:**
- Add a "Decrypt Events" section in the Data tab for E2E channels
- Input field for private key (password type, show/hide toggle)
- "Decrypt" button to enable client-side decryption
- Store private key in component state (never sent to server)

**UI Mockup:**
```vue
<n-card v-if="channel?.encryption_mode === 'e2e'" title="Client-Side Decryption" size="small">
  <n-alert type="info" title="E2E Encrypted Channel">
    Events are encrypted. Provide the private key to decrypt in your browser.
  </n-alert>
  <n-form-item label="Private Key">
    <n-input
      v-model:value="privateKey"
      type="password"
      show-password-on="click"
      placeholder="Paste private key (base64)"
    />
  </n-form-item>
  <n-button @click="enableDecryption" :disabled="!privateKey">
    Enable Decryption
  </n-button>
</n-card>
```

#### 2.2 Implement client-side decryption in events store

**File:** `web/src/stores/events.ts`

**Changes:**
- Add `privateKey` parameter to `connect()` function
- If private key provided, decrypt E2E events using Web Crypto API
- Decryption happens in browser, private key never leaves client

**Implementation:**
```typescript
import { decryptE2E } from '../utils/crypto'

function connect(channel: string, token?: string, privateKey?: string) {
  disconnect()
  connecting.value = true
  events.value = []

  let url = `/events/${channel}`
  if (token) {
    url += `?token=${encodeURIComponent(token)}`
  }
  const es = new EventSource(url)

  es.onmessage = async (msg) => {
    try {
      const parsed = JSON.parse(msg.data)
      
      // Check if event is E2E encrypted
      if (parsed.encrypted && parsed.algorithm === 'NaCl' && privateKey) {
        // Decrypt using Web Crypto API
        const decrypted = await decryptE2E(parsed.ciphertext, privateKey)
        events.value.push({
          id: eventCounter++,
          data: JSON.parse(decrypted),
          timestamp: new Date().toISOString(),
          raw: decrypted,
          encrypted: false,
        })
      } else {
        // Not encrypted or no private key
        events.value.push({
          id: eventCounter++,
          data: parsed,
          timestamp: new Date().toISOString(),
          raw: msg.data,
          encrypted: parsed.encrypted === true,
        })
      }
    } catch {
      events.value.push({
        id: eventCounter++,
        data: msg.data,
        timestamp: new Date().toISOString(),
        raw: msg.data,
        encrypted: false,
      })
    }
  }
  
  // ... rest of the function
}
```

#### 2.3 Implement Web Crypto API decryption utility

**File:** `web/src/utils/crypto.ts` (new file)

**Changes:**
- Implement NaCl box decryption using Web Crypto API
- Handle base64 decoding of ciphertext and private key

**Implementation:**
```typescript
export async function decryptE2E(ciphertext: string, privateKeyBase64: string): Promise<string> {
  // Parse the encrypted envelope
  const envelope = JSON.parse(atob(ciphertext))
  const nonce = Uint8Array.from(atob(envelope.nonce), c => c.charCodeAt(0))
  const encrypted = Uint8Array.from(atob(envelope.body), c => c.charCodeAt(0))
  const ephemeralPubKey = Uint8Array.from(atob(envelope.ephemeral), c => c.charCodeAt(0))
  
  // Import private key
  const privateKey = Uint8Array.from(atob(privateKeyBase64), c => c.charCodeAt(0))
  
  // Decrypt using NaCl box (X25519 + XSalsa20-Poly1305)
  // Note: Web Crypto API doesn't support NaCl directly, so we need a library
  // Use tweetnacl-js or similar
  const decrypted = nacl.box.open(encrypted, nonce, ephemeralPubKey, privateKey)
  
  if (!decrypted) {
    throw new Error('Decryption failed')
  }
  
  return new TextDecoder().decode(decrypted)
}
```

**Note:** Web Crypto API doesn't support NaCl box directly. We'll need to use a library like `tweetnacl-js` (already used in the Go backend). Add it to `package.json`:
```json
{
  "dependencies": {
    "tweetnacl": "^1.0.3",
    "tweetnacl-util": "^0.15.1"
  }
}
```

#### 2.4 Update ChannelDetailView to pass private key

**File:** `web/src/views/ChannelDetailView.vue`

**Changes:**
- When calling `eventsStore.connect()`, pass the private key if available
- Update the connect button handler:

```typescript
function handleConnect() {
  eventsStore.connect(channelId, undefined, privateKey.value || undefined)
}
```

#### 2.5 Update EventFeed component

**File:** `web/src/components/EventFeed.vue`

**Changes:**
- Update the encrypted event display for E2E channels
- Show "E2E Encrypted" tag instead of "Encrypted"
- Show message: "Provide private key to decrypt in browser"

---

### Phase 3: Documentation Updates

#### 3.1 Update CONTRIBUTING.md

**File:** `CONTRIBUTING.md`

**Changes:**
- Update the "Channel access token authentication" section to clarify:
  - Access control is governed by RBAC rules, not encryption
  - Session-based auth checks user permissions
  - Token-based auth checks token scope
- Add a new section "Encryption vs Access Control" explaining the separation

**Add:**
```markdown
## Encryption vs Access Control

Gohookbridge separates **access control** from **encryption**:

- **Access control** determines who can connect to a channel:
  - Browser UI: Session-based auth with RBAC permission checks (`channel:read`)
  - CLI clients: Channel access tokens with scope validation (`consume`/`produce`)

- **Encryption** determines the data format:
  - **No encryption**: Plaintext data
  - **Server-side encryption** (`encryption_mode: "server_side"`): Server decrypts before sending
  - **E2E encryption** (`encryption_mode: "e2e"`): Server relays encrypted data as-is; client decrypts

The SSE endpoint (`GET /events/{channel}`) checks access control but does not enforce encryption rules. E2E channels can be accessed by any authorized user; the encrypted data is relayed as-is.
```

#### 3.2 Update design.md

**File:** `design.md`

**Changes:**
- Update the "SSE Endpoint" section to clarify access control vs encryption
- Remove references to `protectedChannels` and `?pubkey=` parameter

#### 3.3 Update README.md

**File:** `README.md`

**Changes:**
- Update E2E encryption examples to reflect the new behavior
- Add section on client-side decryption in the UI

---

### Phase 4: Testing

#### 4.1 Backend tests

**File:** `gohookbridge/server/server_test.go`

**Tests to add:**
- `TestHandleEventsGet_SessionAuth_Allowed`: User with `channel:read` permission can connect to E2E channel
- `TestHandleEventsGet_SessionAuth_Forbidden`: User without permission gets 403
- `TestHandleEventsGet_TokenAuth_Allowed`: Valid token can connect to E2E channel
- `TestHandleEventsGet_TokenAuth_Unauthorized`: Invalid/missing token gets 401
- `TestHandleEventsGet_E2EChannel_RelayEncrypted`: E2E channel relays encrypted data without 404

**Tests to remove:**
- `TestHandleEventsGet_RejectsMissingPublicKey`
- `TestHandleEventsGet_RejectsInvalidPublicKey`

#### 4.2 Frontend tests

**File:** `web/src/stores/events.test.ts` (new file)

**Tests to add:**
- `Test events store connects to E2E channel without pubkey parameter`
- `Test events store decrypts E2E events when private key provided`
- `Test events store shows encrypted data when no private key`

---

## Migration Notes

### Backward Compatibility

- **Existing CLI clients** using `?pubkey=` parameter: The parameter will be ignored (no error). Clients should be updated to remove the parameter.
- **Existing E2E channels**: No migration needed. Channels will continue to work; the 404 error is removed.
- **Session cookie**: Already in use for browser UI; no changes needed.

### Breaking Changes

- **None.** This is a bug fix that removes incorrect behavior.

---

## Validation Plan

### Manual Testing

1. **Create an E2E-encrypted channel** via the UI
2. **Navigate to the channel page** — verify no 404 error
3. **Connect to SSE** — verify encrypted events are displayed
4. **Provide private key** — verify events are decrypted in browser
5. **Test RBAC**: Create a user without `channel:read` permission, verify 403 error

### Automated Testing

1. Run `make test` — all backend tests pass
2. Run `cd web && npm run test` — all frontend tests pass
3. Run `make lint` — no linting errors
4. Run `cd web && npx vue-tsc --noEmit` — no TypeScript errors

---

## Implementation Order

1. **Phase 1** — Backend changes (remove encryption-based access control, add session auth)
2. **Phase 2** — Frontend changes (client-side decryption)
3. **Phase 3** — Documentation updates
4. **Phase 4** — Testing

---

## Open Questions

None. The design is clear and aligns with the existing `real-e2e-encryption.md` plan.
