# Plan: Fix LLM Flash Review Issues

## Summary
Fix 2 CRITICAL, 7 WARNING, and 1 SUGGESTION issues identified in the security review. Each fix is a self-contained change.

---

## CRITICAL Fixes (must fix before merge)

### 1. Session secret leaked to stderr (`gosmee/server.go:707`)
**File:** `gosmee/server.go`

Remove the secret value from the log message:
- Change `fmt.Fprintf(os.Stderr, "WARNING: Generated random session secret and stored in Raft. Secret: %s\n", secret)` 
- To: `fmt.Fprintf(os.Stderr, "WARNING: Generated random session secret and stored in Raft\n")`

### 2. Replay token bypass on missing project (`gosmee/server.go:363-391`)
**File:** `gosmee/server.go` — function `handleReplayPost`

Current logic: When no Bearer token is present, it calls `ResolveProjectConfig(channel)`, and if the returned project has `ReplayToken == ""`, the request passes through unauthenticated. This also triple-reads the project from BoltDB (once for `ResolveProjectConfig`, once for `ValidateReplayToken`, once for `ResolveProjectMaxBodySize`).

**Restructure the replay handler:**
1. Remove the separate `ResolveProjectConfig` call.
2. Always fall through to `ValidateReplayToken` (which already handles project-not-found and global-default fallback correctly at `raft.go:473-490`).
3. Read the project once, then use it for both replay validation and max body size:
   ```go
   p, projectExists := rs.GetProject(channel)
   maxBodySize := rs.ResolveProjectMaxBodySize(channel) // already handles fallback
   if !rs.ValidateReplayToken(channel, token) {
       http.Error(w, "Unauthorized", http.StatusUnauthorized)
       return
   }
   ```
   But we also need to retain the token extraction from Authorization header. The key change: when `token == ""`, call `ValidateReplayToken("")` instead of the current check.
4. Remove the first `rs.ResolveProjectMaxBodySize(channel)` call (line 394 is fine, keep just one).

**Simpler fix:** Remove lines 376-391 entirely and replace with:
```go
authorizationHeader := r.Header.Get("Authorization")
token := ""
if strings.HasPrefix(authorizationHeader, "Bearer ") {
    token = strings.TrimPrefix(authorizationHeader, "Bearer ")
}
if !rs.ValidateReplayToken(channel, token) {
    http.Error(w, "Unauthorized", http.StatusUnauthorized)
    return
}
```

`ValidateReplayToken` already handles:
- Empty token + existing project with ReplayToken → rejects
- Empty token + nonexistent project + global ReplayToken → rejects
- Empty token + no ReplayToken at all → passes
- Non-empty token → validates against project or global

---

## WARNING Fixes

### 3. Static auth snapshot — API-created users can't authenticate (`gosmee/store/bridge.go:49`)
**Files:** `gosmee/store/bridge.go`, `gosmee/server.go`

**Approach:** Make `BuildAuthConfig` not needed at startup. Instead, make `RequireAuth` and `LoginHandler` dynamically read from the store.

**Changes:**
- `gosmee/server.go`: In the `startServer` function (around lines 690-802), replace the static `authConfig != nil` check with a dynamic check via `rs`. Remove `authConfig := store.BuildAuthConfig(rs)`.
- Create a new function `RequireAuthDynamic(rs *RaftStore)` in `store/acl.go` that checks auth at request time:
  ```go
  func RequireAuthDynamic(rs *RaftStore) func(http.Handler) http.Handler {
      return func(next http.Handler) http.Handler {
          return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
              users, _ := rs.ListUsers()
              providers, _ := rs.OIDCProviders()
              if len(users) == 0 && len(providers) == 0 {
                  next.ServeHTTP(w, r)
                  return
              }
              // Build auth config on the fly
              authConfig := BuildAuthConfig(rs)
              RequireAuth(authConfig)(next).ServeHTTP(w, r)
          })
      }
  }
  ```
- Modify `LoginHandler` to accept `rs *RaftStore` directly and call `BuildAuthConfig` per-request.
- Remove `BuildAuthConfig` call from `startServer` route setup — always use dynamic checks.

**Alternative simpler approach:** Keep `BuildAuthConfig` at startup but rebuild it in `RequireAuth` middleware on each request (it's cheap — one BoltDB scan per request).

### 4. Setup mode lockout after first user creation (`gosmee/store/api.go:406`)
**File:** `gosmee/store/api.go` — function `SetupModeMiddleware`

**Problem:** After creating the first user via API, `IsSetupMode()` returns false. The next request hits `SetupModeMiddleware` which rejects with "Setup mode expired". No auth middleware is installed yet, so the admin is locked out.

**Fix:** In `SetupModeMiddleware`, when `IsSetupMode()` returns false (i.e., users now exist but setup mode just ended), check if auth should now be viable and either:
   a. Call through `next.ServeHTTP` (not just reject), so the request can be handled — this allows the admin to GET /api/me and confirm their user exists.
   b. Better: Check for auth config dynamically and serve `RequireAuth` as middleware.

**Implementation:** Change the `SetupModeMiddleware` function to fall through to `RequireAuth` when setup mode has expired but users exist:
```go
func (rs *RaftStore) SetupModeMiddleware(next http.Handler) http.Handler {
    setupHandler := func(w http.ResponseWriter, r *http.Request) {
        // Check if setup mode is active and within the time window
        if rs.IsSetupMode() {
            setupEnd := rs.GetSetupModeEndTime()
            if setupEnd.IsZero() {
                setupEnd = time.Now().Add(5 * time.Minute)
                rs.SetSetupModeEndTime(setupEnd)
            }
            if time.Now().Before(setupEnd) {
                next.ServeHTTP(w, r)
                return
            }
        }
        // If setup mode ended, don't reject — fall through to auth
        // (the router should have RequireAuth installed, but as safety net:)
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
    }
    
    // Caller should wrap both setup and auth, but if not, just pass through
    return http.HandlerFunc(setupHandler)
}
```

Plus in `server.go` lines 795-801: After initial setup (first user created), the authConfig becomes valid. We need to detect this. The minimal fix: in `SetupModeMiddleware`, when `IsSetupMode()` returns false but users exist, just pass through to the next handler. The router setup should use a combined middleware that first checks setup mode, then auth.

**Easiest correct fix:** In `server.go` lines 794-801, always use a combined middleware:
```go
apiRouter := chi.NewRouter()
apiRouter.Use(AdaptiveAuthMiddleware(rs))
```

Where `AdaptiveAuthMiddleware` checks:
- If setup mode active → serve request directly
- If auth available (users exist) → apply RequireAuth
- Otherwise → reject

### 5. Static ProtectedChannels snapshot (`gosmee/store/bridge.go:5`)
**Files:** `gosmee/store/bridge.go`, `gosmee/server.go`

**Fix:** Change `ProtectedChannels` to accept a `*RaftStore` reference instead of a static map, and compute `Has`/`IsAllowed` on each call by reading from the store. Or rebuild the snapshot on each query.

**Implementation:** Change `NewProtectedChannels` to `NewProtectedChannelsDynamic` which stores a `*RaftStore` reference:
```go
type ProtectedChannels struct {
    rs *RaftStore
}

func NewProtectedChannelsDynamic(rs *RaftStore) *ProtectedChannels {
    return &ProtectedChannels{rs: rs}
}

func (p *ProtectedChannels) Has(channel string) bool {
    if p == nil || p.rs == nil { return false }
    project, err := p.rs.GetProject(channel)
    if err != nil { return false }
    return project.EncryptionEnabled && len(project.EncryptionPubKeys) > 0
}

func (p *ProtectedChannels) IsAllowed(channel string, publicKey *[32]byte) bool {
    if p == nil || p.rs == nil || publicKey == nil { return false }
    project, err := p.rs.GetProject(channel)
    if err != nil { return false }
    if !project.EncryptionEnabled { return false }
    encoded := base64.RawURLEncoding.EncodeToString(publicKey[:])
    for _, k := range project.EncryptionPubKeys {
        if k == encoded { return true }
    }
    return false
}
```

Update server.go to use `store.NewProtectedChannelsDynamic(rs)` instead.

### 6. HasData error swallowing (`gosmee/store/raft.go:196`)
**File:** `gosmee/store/raft.go` — function `HasData`

**Fix:** Return the error and handle it at call sites.

Change `HasData` to return `(bool, error)`:
```go
func (rs *RaftStore) HasData() (bool, error) {
    keys, err := listFSMKeys(rs.db, "/")
    if err != nil {
        return false, err
    }
    return len(keys) > 0, nil
}
```

Update callers in `raft.go:103` and `bootstrap.go:58`:
- `raft.go:103` (`bootstrap`): If error, return it immediately.
- `bootstrap.go:58` (`ApplyBootstrap`): If error, return it instead of `fmt.Errorf("cannot apply bootstrap: FSM already has data")`.

**Note:** Both call sites currently call it for "is this a fresh FSM?" check. With error bubbling, intermittent BoltDB errors will cause startup failure instead of silently treating as empty.

### 7. Removed CLI flags — no migration tooling (`gosmee/flags.go:139`)
**File:** `gosmee/flags.go`, new file `gosmee/migrate.go`

**Fix:** Add `gosmee server migrate-config` subcommand that reads old env vars / a legacy config file and outputs a `bootstrap.yaml`.

**Implementation:**
- Create `gosmee/migrate.go` with a `MigrateFlagsToBootstrap()` function.
- Add the `migrate-config` subcommand to the `server` command in `gosmee/server.go` or `main.go`.
- The subcommand reads old env vars (`GOSMEE_WEBHOOK_SECRET`, `GOSMEE_ALLOWED_IPS`, `GOSMEE_REPLAY_TOKEN`, `GOSMEE_MAX_BODY_SIZE`, `GOSMEE_CORS_ORIGIN`, etc.) and generates YAML output.
- Alternatively, at startup detect if old-style env vars are set and print a warning pointing to the migration guide/subcommand.

**Scope:** Keep it minimal — the subcommand reads known old env var names and writes a basic `bootstrap.yaml` with a `global` section containing the defaults.

### 8. ListUsers scans index entries (`gosmee/store/raft.go:395`)
**File:** `gosmee/store/raft.go` — function `ListUsers`

**Problem:** Lists ALL keys under `/users/` including `/users/by-username/` index entries. While deduplication works via `seen` map, it wastes reads and masks per-key unmarshal errors with `continue`.

**Fix:** Change the prefix scan to skip index entries:
- Option A: In the key iteration loop, skip keys containing `/users/by-username/`:
  ```go
  if strings.Contains(key, "/by-username/") {
      continue
  }
  ```
- Option B: Use a different key structure — store users at `/users/data/` instead of `/users/`, so `ListUsers` scans `/users/data/`. This would require changing all write paths too.
- Option C (simplest): After splitting the key, check `parts[0] == "by-username"` and skip:
  ```go
  if parts[0] == "by-username" {
      continue
  }
  ```

**Recommendation:** Option C — minimal change, no key layout migration needed.

### 9. isValidChannelID dead code (`gosmee/server.go:145`)
**File:** `gosmee/server.go`

Remove the unused function and its associated regex/variable if any.

Also check: is `validChannelID` only used by `isValidChannelID`? Let's verify:
```go
func isValidChannelID(channel string) bool {
    return validChannelID.MatchString(channel)
}
```
Remove both the function and the `validChannelID` variable if it's only used here.

### 10. Duplicated username index logic (`gosmee/store/fsm.go:187-194` + `raft.go:439-444`)
**Files:** `gosmee/store/fsm.go`, `gosmee/store/raft.go`

**Fix:** Extract the index writing into a helper method on `RaftStore` and reuse it in `CreateUser`.

`raft.go:439-444`:
```go
idx := usernameIndex{UserID: u.ID}
idxVal, err := json.Marshal(idx)
if err != nil {
    return err
}
return rs.applyCommand("set", "/users/by-username/"+u.Username+"/", idxVal)
```

`fsm.go:187-194` (in bootstrap):
```go
idx := usernameIndex{UserID: u.Username}
idxVal, err := json.Marshal(idx)
if err != nil {
    return err
}
if err := writeString("/users/by-username/"+u.Username+"/", string(idxVal)); err != nil {
    return err
}
```

These are in different layers (FSM vs RaftStore), so they use different write primitives (`writeString` vs `applyCommand`). However, a helper function can accept a writer callback. Simplest: create a helper that takes username and userID and returns the key+value pair:
```go
func usernameIndexEntry(username, userID string) (key string, value []byte, err error) {
    idx := usernameIndex{UserID: userID}
    val, err := json.Marshal(idx)
    if err != nil {
        return "", nil, err
    }
    return "/users/by-username/" + username + "/", val, nil
}
```
Use this helper in both FSM and RaftStore.

### 11. Duplicated project-scope authorization check (`gosmee/store/api.go:377` + `acl.go:122`)
**Files:** `gosmee/store/api.go`, `gosmee/store/acl.go`

`isProjectAllowed` (api.go:377) and `hasProjectAccess` (acl.go:122) are functionally identical.

**Fix:** Remove `isProjectAllowed` from `api.go` and use `hasProjectAccess` from `acl.go` instead. Update the one call site in `listProjects` handler.

---

## SUGGESTION Fix

### 12. Triple-read of same project per replay request (`gosmee/server.go:363-394`)
**File:** `gosmee/server.go` — function `handleReplayPost`

Already addressed by the CRITICAL fix #2 restructuring. The restructured handler reads `GetProject` once (or not at all using `ValidateReplayToken`).

The current 3 reads:
1. `ResolveProjectConfig(channel)` → calls `GetProject` → BoltDB read
2. `ValidateReplayToken(channel, token)` → calls `GetProject` → BoltDB read
3. `ResolveProjectMaxBodySize(channel)` → calls `GetProject` → BoltDB read

With the restructured handler from fix #2, we only call `ResolveProjectMaxBodySize` once, and `ValidateReplayToken` handles its own read. So it goes from 3 to 2 reads. 

Further optimization: Read project once and pass to both:
```go
p, _ := rs.GetProject(channel)
// Compute maxBodySize from p or global fallback
// Pass token validation against p
```
But the simpler approach from fix #2 is sufficient.

---

## Implementation Order

1. Session secret leak (CRITICAL — 1-line fix)
2. Replay token bypass (CRITICAL — restructure handler)
3. HasData error swallowing (WARNING — API change, update callers)
4. isValidChannelID dead code (WARNING — remove function)
5. ListUsers index scan waste (WARNING — skip by-username in loop)
6. Duplicated username index (WARNING — extract helper)
7. Duplicated project-scope check (WARNING — deduplicate to hasProjectAccess)
8. Static auth snapshot (WARNING — RequireAuthDynamic wrapper)
9. Setup mode lockout (WARNING — AdaptiveAuthMiddleware)
10. Static ProtectedChannels snapshot (WARNING — dynamic ProtectedChannels)
11. Triple-read optimization (SUGGESTION — addressed by fix #2)
12. Migration subcommand (WARNING — new file and subcommand)