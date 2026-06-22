# Channel Access Token Authentication

## Overview

Add per-channel access token authentication for producer (POST `/{channel}`) and consumer (SSE GET `/events/{channel}`) endpoints. Users can keep channels public or require token auth. Tokens are hashed (SHA-256) and shown once at creation.

## Design Decisions

- **Per-channel tokens**: Each channel has its own list of tokens
- **Scoped tokens**: Each token has a scope — `"produce"` (POST only), `"consume"` (SSE only), or `"both"`. A leaked producer token cannot be used to read events, and vice versa.
- **Hashed storage**: SHA-256 hash stored, raw token shown once at creation
- **Producer auth**: Token via URL query param (`?token=xxx`) — required for GitHub webhooks
- **Consumer auth**: Token via URL query param (`?token=xxx`) OR `Authorization: Bearer xxx` header
- **Default**: `access_mode = "public"` — fully backward compatible

---

## 1. Store Layer (`gohookbridge/store/types.go`)

### Add types

```go
type ChannelAccessToken struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    TokenHash string `json:"token_hash"`
    Scope     string `json:"scope"`       // "produce", "consume", or "both"
    CreatedAt string `json:"created_at"`
}
```

### Add fields to `Channel` struct

```go
AccessMode  string                 `json:"access_mode,omitempty"`  // "public" (default) or "token"
AccessTokens []ChannelAccessToken  `json:"access_tokens,omitempty"`
```

### Add resolve helper in `types.go`

In `resolveChannelConfig()`, add:
```go
if resolved.AccessMode == "" {
    resolved.AccessMode = "public"
}
```

---

## 2. Store Layer — Token Helpers (`gohookbridge/store/tokens.go`) — NEW FILE

```go
package store

import (
    "crypto/rand"
    "crypto/sha256"
    "crypto/subtle"
    "encoding/hex"
    "time"

    gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
)

func GenerateAccessToken() (raw string, hash string) {
    b := make([]byte, 32)
    rand.Read(b)
    raw = hex.EncodeToString(b)
    h := sha256.Sum256([]byte(raw))
    hash = hex.EncodeToString(h[:])
    return raw, hash
}

func HashToken(raw string) string {
    h := sha256.Sum256([]byte(raw))
    return hex.EncodeToString(h[:])
}

func (rs *RaftStore) CreateAccessToken(channelID string, name string, scope string) (raw string, token ChannelAccessToken, err error) {
    if scope != "produce" && scope != "consume" && scope != "both" {
        scope = "both"
    }
    ch, err := rs.GetChannel(channelID)
    if err != nil {
        return "", ChannelAccessToken{}, err
    }
    raw, hash := GenerateAccessToken()
    t := ChannelAccessToken{
        ID:        gohookbridge.GenerateUUID(),
        Name:      name,
        TokenHash: hash,
        Scope:     scope,
        CreatedAt: time.Now().UTC().Format(time.RFC3339),
    }
    ch.AccessTokens = append(ch.AccessTokens, t)
    ch.AccessMode = "token"
    if err := rs.UpdateChannel(ch); err != nil {
        return "", ChannelAccessToken{}, err
    }
    return raw, t, nil
}

func (rs *RaftStore) DeleteAccessToken(channelID string, tokenID string) error {
    ch, err := rs.GetChannel(channelID)
    if err != nil {
        return err
    }
    filtered := make([]ChannelAccessToken, 0, len(ch.AccessTokens))
    for _, t := range ch.AccessTokens {
        if t.ID != tokenID {
            filtered = append(filtered, t)
        }
    }
    ch.AccessTokens = filtered
    if len(ch.AccessTokens) == 0 {
        ch.AccessMode = "public"
    }
    return rs.UpdateChannel(ch)
}

// ValidateChannelToken checks if rawToken is valid for the given channel and requiredScope.
// requiredScope is "produce" or "consume". A token with scope "both" satisfies either.
func (rs *RaftStore) ValidateChannelToken(channelID string, rawToken string, requiredScope string) bool {
    ch, err := rs.GetChannel(channelID)
    if err != nil {
        return false
    }
    hash := HashToken(rawToken)
    for _, t := range ch.AccessTokens {
        if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(hash)) == 1 {
            if t.Scope == "both" || t.Scope == requiredScope {
                return true
            }
        }
    }
    return false
}
```

---

## 3. Server Layer — Channel Auth Middleware (`gohookbridge/server/server.go`)

### New middleware function

```go
// channelAccessMiddleware returns a middleware that checks channel access tokens.
// requiredScope is "produce" for POST endpoints or "consume" for SSE endpoints.
func channelAccessMiddleware(rs *store.RaftStore, requiredScope string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            channel := chi.URLParam(r, "channel")
            chConfig, err := rs.ResolveChannelConfig(channel)
            if err != nil {
                next.ServeHTTP(w, r)
                return
            }

            if chConfig.AccessMode != "token" {
                next.ServeHTTP(w, r)
                return
            }

            // Check query param
            token := r.URL.Query().Get("token")

            // Check Authorization header (Bearer)
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

### Apply middleware

In `serve()`, apply scoped middleware:

For POST webhook endpoint on `restrictedRouter`:
```go
restrictedRouter.Use(channelAccessMiddleware(rs, "produce"))
```

For the SSE endpoint (`/events/{channel}`) on `mainRouter`:
```go
mainRouter.Get(eventsPath, channelAccessMiddleware(rs, "consume")(handleEventsGet(broker, protectedChannels, rs)))
```

---

## 4. API Layer — Token Management (`gohookbridge/store/api.go`)

### Register new routes

Inside `RegisterAPIHandlers`, add inside the `/{id}` channel route group:

```go
r.Post("/access-tokens", h.createAccessToken)
r.Get("/access-tokens", h.listAccessTokens)
r.Delete("/access-tokens/{tokenID}", h.deleteAccessToken)
r.Put("/access-mode", h.updateAccessMode)
```

### Handler implementations

```go
func (h *apiHandler) createAccessToken(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    var body struct {
        Name  string `json:"name"`
        Scope string `json:"scope"`
    }
    json.NewDecoder(r.Body).Decode(&body)
    if body.Name == "" {
        body.Name = "default"
    }
    if body.Scope == "" {
        body.Scope = "both"
    }
    raw, token, err := h.rs.CreateAccessToken(id, body.Name, body.Scope)
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    writeJSON(w, http.StatusCreated, map[string]any{
        "token":      raw,
        "id":         token.ID,
        "name":       token.Name,
        "scope":      token.Scope,
        "created_at": token.CreatedAt,
    })
}

func (h *apiHandler) listAccessTokens(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    ch, err := h.rs.GetChannel(id)
    if err != nil {
        writeError(w, http.StatusNotFound, "channel not found")
        return
    }
    tokens := make([]map[string]string, 0, len(ch.AccessTokens))
    for _, t := range ch.AccessTokens {
        tokens = append(tokens, map[string]string{
            "id":         t.ID,
            "name":       t.Name,
            "scope":      t.Scope,
            "created_at": t.CreatedAt,
        })
    }
    writeJSON(w, http.StatusOK, map[string]any{
        "access_mode": ch.AccessMode,
        "tokens":      tokens,
    })
}

func (h *apiHandler) deleteAccessToken(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    tokenID := chi.URLParam(r, "tokenID")
    if err := h.rs.DeleteAccessToken(id, tokenID); err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) updateAccessMode(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    var body struct {
        AccessMode string `json:"access_mode"`
    }
    json.NewDecoder(r.Body).Decode(&body)
    if body.AccessMode != "public" && body.AccessMode != "token" {
        writeError(w, http.StatusBadRequest, "access_mode must be 'public' or 'token'")
        return
    }
    ch, err := h.rs.GetChannel(id)
    if err != nil {
        writeError(w, http.StatusNotFound, "channel not found")
        return
    }
    ch.AccessMode = body.AccessMode
    if err := h.rs.UpdateChannel(ch); err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    writeJSON(w, http.StatusOK, map[string]string{"access_mode": ch.AccessMode})
}
```

---

## 5. Client CLI (`gohookbridge/client/`)

### Add flag in `flags.go` — `ClientFlags`

```go
&cli.StringFlag{
    Name:    "token",
    Usage:   "Access token for channel authentication",
    EnvVars: []string{"GOSMEE_TOKEN"},
},
```

### Modify `client.go` — `prepareSubscription()`

Add `token` parameter. When token is set, append `?token=xxx` to the SSE URL:

```go
func prepareSubscription(smeeURL, encryptionKeyFile string, resume bool, clientID string, token string) (...) {
    // ... existing code ...
    if token != "" {
        parsedURL, _ := url.Parse(sseURL)
        query := parsedURL.Query()
        query.Set("token", token)
        parsedURL.RawQuery = query.Encode()
        sseURL = parsedURL.String()
    }
    // ...
}
```

### Modify `command.go` — `clientAction()`

Pass `c.String("token")` to `replayDataOpts` and to `prepareSubscription`.

Add field to `replayDataOpts`:
```go
token string
```

---

## 6. Proxy/Produce CLI (`gohookbridge/proxy/`)

### Add flag in `flags.go` — `ProduceFlags` and `ProxyFlags`

```go
&cli.StringFlag{
    Name:    "token",
    Usage:   "Access token for channel authentication (sent as URL query parameter)",
    EnvVars: []string{"GOSMEE_TOKEN"},
},
```

### Modify `produce.go`

After parsing `serverURL`, append token query param:
```go
if token := c.String("token"); token != "" {
    parsedURL, _ := url.Parse(serverURL)
    q := parsedURL.Query()
    q.Set("token", token)
    parsedURL.RawQuery = q.Encode()
    serverURL = parsedURL.String()
}
```

### Modify `proxy.go`

In `startProxy()`, when building the target request, append token to the target URL:
```go
if token := c.String("token"); token != "" {
    parsedTarget, _ := url.Parse(targetURL)
    q := parsedTarget.Query()
    q.Set("token", token)
    parsedTarget.RawQuery = q.Encode()
    targetURL = parsedTarget.String()
}
```

---

## 7. Frontend (`web/src/`)

### API client (`api/client.ts`)

Add to `Channel` interface:
```typescript
access_mode?: string
access_tokens?: { id: string; name: string; scope: string; created_at: string }[]
```

Add methods to `ApiClient`:
```typescript
async createAccessToken(channelId: string, name: string, scope: string): Promise<{ token: string; id: string; name: string; scope: string; created_at: string }>
async listAccessTokens(channelId: string): Promise<{ access_mode: string; tokens: { id: string; name: string; scope: string; created_at: string }[] }>
async deleteAccessToken(channelId: string, tokenId: string): Promise<void>
async updateAccessMode(channelId: string, mode: string): Promise<{ access_mode: string }>
```

### Channel Settings UI (`views/ChannelDetailView.vue`)

In the Settings tab, add after "Encryption Mode" section:

1. **Access Mode** select: "Public" / "Token required"
2. **Access Tokens** section (visible when mode = "token"):
   - List of existing tokens (name, scope badge, created date, delete button)
   - "Generate Token" button with name input and scope selector (Produce / Consume / Both)
   - Modal/dialog to show the raw token once (with copy button)
   - Warning that token is shown only once
   - Scope shown as a badge: "Produce", "Consume", or "Both"

### Clients Tab (`views/ChannelDetailView.vue`)

Update CLI command generators to include `--token` flag when access_mode is "token":
- Client command: `gohookbridge client --token <token> ...`
- Produce command: `gohookbridge produce --token <token> ...`
- Proxy command: `gohookbridge proxy --token <token> ...`

Note: The UI should show which tokens are for producing vs consuming, so users know which token to use in which command.

### Events Store (`stores/events.ts`)

The SSE URL in `connect()` needs to include the token if the channel requires auth. The store needs to accept an optional token parameter and append it to the EventSource URL.

---

## 8. Tests

### Backend unit tests

- `gohookbridge/store/tokens_test.go`:
  - Test `GenerateAccessToken()` produces valid raw+hash pair
  - Test `HashToken()` is deterministic
  - Test `CreateAccessToken()` adds token to channel with correct scope
  - Test `DeleteAccessToken()` removes token
  - Test `ValidateChannelToken()` returns true for valid token with matching scope
  - Test `ValidateChannelToken()` returns false for token with wrong scope (e.g., produce-only token for consume)
  - Test `ValidateChannelToken()` returns true for "both" scope token for either produce or consume
  - Test `ValidateChannelToken()` returns false for invalid token

- `gohookbridge/server/server_test.go` (add to existing):
  - Test POST `/{channel}` returns 401 when access_mode=token and no token provided
  - Test POST `/{channel}` returns 401 when access_mode=token and invalid token
  - Test POST `/{channel}` returns 202 when access_mode=token and valid produce-scope token in query param
  - Test POST `/{channel}` returns 401 when token has consume-only scope
  - Test POST `/{channel}` returns 202 when access_mode=public (backward compat)
  - Test GET `/events/{channel}` returns 401 when access_mode=token and no token
  - Test GET `/events/{channel}` returns 202 with valid consume-scope token via query param
  - Test GET `/events/{channel}` returns 202 with valid "both"-scope token via Bearer header
  - Test GET `/events/{channel}` returns 401 when token has produce-only scope

### Integration tests

- Test full flow: create channel → set access_mode=token → generate produce token → generate consume token → produce with produce token → consume with consume token
- Test that produce token cannot be used for consume
- Test that consume token cannot be used for produce
- Test backward compat: channel with no access_mode works without token

### Frontend tests

- Test token generation modal shows raw token with correct scope
- Test token list displays scope badge correctly
- Test access mode toggle works
- Test scope selector in token creation form

---

## 9. Migration / Backward Compatibility

- Existing channels have `access_mode = ""` which resolves to `"public"` — no breaking changes
- The `resolveChannelConfig()` function handles the default
- No migration needed for existing data

---

## File Change Summary

| File | Change |
|---|---|
| `gohookbridge/store/types.go` | Add `AccessMode`, `AccessTokens` to Channel; add `ChannelAccessToken` type; update `resolveChannelConfig` |
| `gohookbridge/store/tokens.go` | **NEW** — Token generation, hashing, CRUD, validation |
| `gohookbridge/store/tokens_test.go` | **NEW** — Unit tests for token operations |
| `gohookbridge/store/api.go` | Add token management API handlers and routes |
| `gohookbridge/server/server.go` | Add `channelAccessMiddleware`; apply to POST and SSE routes |
| `gohookbridge/server/server_test.go` | Add tests for channel access middleware |
| `gohookbridge/flags.go` | Add `--token` flag to `ClientFlags`, `ProduceFlags`, `ProxyFlags` |
| `gohookbridge/client/client.go` | Pass token in SSE URL query param |
| `gohookbridge/client/command.go` | Read `--token` flag, pass to `prepareSubscription` |
| `gohookbridge/proxy/proxy.go` | Append token to target URL |
| `gohookbridge/proxy/produce.go` | Append token to server URL |
| `web/src/api/client.ts` | Add token API methods and Channel fields |
| `web/src/views/ChannelDetailView.vue` | Add access mode + token management UI |
| `web/src/stores/events.ts` | Accept optional token for SSE connection |
