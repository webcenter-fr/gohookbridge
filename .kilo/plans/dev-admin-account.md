# Plan: Dev Admin Account Auto-Creation

## Goal
When no admin account is provided via bootstrap config, allow auto-creating one at startup for development/testing convenience. Avoid logging passwords; use a restricted-permission file instead.

## Approach
Add an opt-in `--dev-admin` CLI flag. When set and the FSM has no users, create an `admin` user with a generated password (or one from `--dev-admin-password`). Write the password to `raft-data/admin-password.txt` with `0600` permissions and print a one-time message to stdout pointing to that file.

## Changes

### 1. `gohookbridge/flags.go`
Add two new server flags:
- `--dev-admin` (bool) — "Auto-create an admin user on first boot when no users exist (development only). Password is written to raft-data/admin-password.txt."
- `--dev-admin-password` (string) — "Password for the dev admin user. If empty, a random password is generated."

### 2. `gohookbridge/store/raft.go` (or new helper function)
Add a method `CreateDevAdmin(password string) error` on `RaftStore` that:
1. Checks `IsSetupMode()` — only creates if true (no existing users)
2. Hashes the password with bcrypt
3. Creates a `User` with username `admin` and role `admin`
4. Applies via Raft (needs to go through Raft consensus, so use `applyCommand("set", "/users/admin/", userJSON)` and set index entries)

This needs to be compatible with how `createUser` works in `api.go` — use the same key patterns (`/users/<username>/` and `usernameIndexKey`).

### 3. `gohookbridge/server.go` — `serve()` function
After raft store bootstrap (line 635), before session secret setup (~line 683):
```go
if c.Bool("dev-admin") {
    if err := initDevAdmin(rs, c.String("dev-admin-password"), c.String("raft-dir")); err != nil {
        return fmt.Errorf("dev admin: %w", err)
    }
}
```

Helper `initDevAdmin` in `server.go` (or `store/`) that:
1. Checks `rs.IsSetupMode()` — skip if users already exist
2. Generates password if not provided (16-char random hex via `generateRandomHex`)
3. Calls `rs.CreateDevAdmin(password)`
4. Writes password to `<raftDir>/admin-password.txt` with `0600` permissions via `os.WriteFile`
5. Prints to stdout: `"Dev admin account created. Username: admin. Password saved to <raftDir>/admin-password.txt"`

### 4. Write the password file atomically
- Use `os.WriteFile` with `0600` permissions
- Path: `<raft-dir>/admin-password.txt`
- On subsequent restarts (users already exist), skip creation silently

## Security notes
- `--dev-admin` is opt-in only; never auto-creates without explicit flag
- Password never appears in logs or stdout
- File has `0600` (owner read/write only)
- Only creates when `IsSetupMode()` is true (no existing users)
- Production deployments should use bootstrap config, not this flag

### 5. `.gitignore`
Add `raft-data/admin-password.txt` to prevent accidental commits of the dev admin password file.

### 6. `CONTRIBUTING.md`
In the "Running the backend" section (~line 81), update the `go run` example to show the `--dev-admin` flag and explain what it does:
```
go run main.go server --address 0.0.0.0 --port 3333 --dev-admin
```
Add a note explaining that `--dev-admin` creates an `admin` user and writes the password to `raft-data/admin-password.txt` on first boot.

## Files to modify
| File | Change |
|------|--------|
| `gohookbridge/flags.go` | Add `--dev-admin`, `--dev-admin-password` to `serverFlags` |
| `gohookbridge/store/raft.go` | Add `CreateDevAdmin(password string) error` method |
| `gohookbridge/server.go` | Add `initDevAdmin()` helper + call in `serve()` |
| `.gitignore` | Add `raft-data/admin-password.txt` |
| `CONTRIBUTING.md` | Document `--dev-admin` flag usage in local dev section |

## Files to create
None — all changes in existing files.