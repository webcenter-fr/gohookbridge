# Rename gosmee identity leftovers → gohookbridge (hard rename) + home-cluster redeploy

## 1. Objective & scope

Hard-rename every remaining `gosmee` identity leftover to `gohookbridge`. No
backward-compatibility aliases, no dual-read fallbacks. Old `GOSMEE_*` names are
removed entirely **except** the migration/deprecation paths in §4 (which exist
specifically to detect/migrate the old names).

- **In scope:** CLI flag env vars, exec-script env vars, client env vars, replay
  script templates, session cookie, version header, internal `goSmee` type, shell
  completion templates, docs (`README.md`, `SECURITY.md`, `misc/README.md`), and
  all test references.
- **Out of scope (KEEP):** `smee.io` support, `SmeeChannel`/`smeeChannel`/
  `smeeURL` identifiers (smee.io protocol/concept, no `go` prefix), `README.md:52`
  blog-post link, `NOTICE` upstream attribution, `.opencode/plans/remove-orphan-files.md`
  (historical archive), `X-Gohookbridge-Leader-Forwarded` (already correct), and
  the §4 migration/deprecation paths.
- **Target branch:** `fix/remove-orphan-files` (PR #16). New commits go on this
  branch. Do **not** create a new branch.

## 2. Complete rename mapping

**Uniform rule:** `GOSMEE_` → `GOHOOKBRIDGE_` (prefix swap; suffix unchanged).
`gosmee` (lowercase, in templates/cookie) → `gohookbridge`. `goSmee` type →
`hookBridge`. `X-Gosmee-Version` → `X-Gohookbridge-Version`.

### A. CLI flag env vars — `gohookbridge/flags.go` (42 unique names, `GOSMEE_`→`GOHOOKBRIDGE_`)

| Old | New | Lines |
|---|---|---|
| `GOSMEE_SAVEDIR` | `GOHOOKBRIDGE_SAVEDIR` | 33 |
| `GOSMEE_TARGET_TIMEOUT` | `GOHOOKBRIDGE_TARGET_TIMEOUT` | 38 |
| `GOSMEE_EXEC` | `GOHOOKBRIDGE_EXEC` | 60 |
| `GOSMEE_EXEC_ENV_VARS` | `GOHOOKBRIDGE_EXEC_ENV_VARS` | 70 |
| `GOSMEE_ENCRYPTION_KEY_FILE` | `GOHOOKBRIDGE_ENCRYPTION_KEY_FILE` | 103, 145, 179, 197 |
| `GOSMEE_HEALTH_PORT` | `GOHOOKBRIDGE_HEALTH_PORT` | 134 |
| `GOSMEE_SSE_BUFFER_SIZE` | `GOHOOKBRIDGE_SSE_BUFFER_SIZE` | 140 |
| `GOSMEE_ENCRYPTION_KEY` | `GOHOOKBRIDGE_ENCRYPTION_KEY` | 150 |
| `GOSMEE_CLIENT_ID` | `GOHOOKBRIDGE_CLIENT_ID` | 161 |
| `GOSMEE_TOKEN` | `GOHOOKBRIDGE_TOKEN` | 166, 184, 212 |
| `GOSMEE_ENCRYPTION_PUBLIC_KEY` | `GOHOOKBRIDGE_ENCRYPTION_PUBLIC_KEY` | 174, 192 |
| `GOSMEE_TLS_CERT` | `GOHOOKBRIDGE_TLS_CERT` | 241 |
| `GOSMEE_TLS_KEY` | `GOHOOKBRIDGE_TLS_KEY` | 246 |
| `GOSMEE_RAFT_DIR` | `GOHOOKBRIDGE_RAFT_DIR` | 252 |
| `GOSMEE_RAFT_NODE_ID` | `GOHOOKBRIDGE_RAFT_NODE_ID` | 258 |
| `GOSMEE_RAFT_BIND_ADDR` | `GOHOOKBRIDGE_RAFT_BIND_ADDR` | 264 |
| `GOSMEE_RAFT_PEERS` | `GOHOOKBRIDGE_RAFT_PEERS` | 269 |
| `GOSMEE_RAFT_ADVERTISE_ADDR` | `GOHOOKBRIDGE_RAFT_ADVERTISE_ADDR` | 274 |
| `GOSMEE_RAFT_REPLICAS` | `GOHOOKBRIDGE_RAFT_REPLICAS` | 280 |
| `GOSMEE_RAFT_STATEFULSET_NAME` | `GOHOOKBRIDGE_RAFT_STATEFULSET_NAME` | 285 |
| `GOSMEE_RAFT_HEADLESS_SERVICE` | `GOHOOKBRIDGE_RAFT_HEADLESS_SERVICE` | 290 |
| `GOSMEE_RAFT_NAMESPACE` | `GOHOOKBRIDGE_RAFT_NAMESPACE` | 295 |
| `GOSMEE_RAFT_CLUSTER_DOMAIN` | `GOHOOKBRIDGE_RAFT_CLUSTER_DOMAIN` | 301 |
| `GOSMEE_RAFT_LEADER_WAIT_TIMEOUT` | `GOHOOKBRIDGE_RAFT_LEADER_WAIT_TIMEOUT` | 307 |
| `GOSMEE_RAFT_RECOVERY_MODE` | `GOHOOKBRIDGE_RAFT_RECOVERY_MODE` | 312 |
| `GOSMEE_RAFT_NO_SNAPSHOT_RESTORE` | `GOHOOKBRIDGE_RAFT_NO_SNAPSHOT_RESTORE` | 317 |
| `GOSMEE_RAFT_PERFORMANCE_MULTIPLIER` | `GOHOOKBRIDGE_RAFT_PERFORMANCE_MULTIPLIER` | 323 |
| `GOSMEE_RAFT_TLS_ENABLED` | `GOHOOKBRIDGE_RAFT_TLS_ENABLED` | 328 |
| `GOSMEE_RAFT_TLS_CA_SECRET` | `GOHOOKBRIDGE_RAFT_TLS_CA_SECRET` | 333 |
| `GOSMEE_RAFT_TLS_CA_BOOTSTRAP` | `GOHOOKBRIDGE_RAFT_TLS_CA_BOOTSTRAP` | 338 |
| `GOSMEE_RAFT_TLS_DIR` | `GOHOOKBRIDGE_RAFT_TLS_DIR` | 343 |
| `GOSMEE_RAFT_TLS_VALIDITY` | `GOHOOKBRIDGE_RAFT_TLS_VALIDITY` | 349 |
| `GOSMEE_RAFT_TLS_CLIENT_AUTH` | `GOHOOKBRIDGE_RAFT_TLS_CLIENT_AUTH` | 355 |
| `GOSMEE_RAFT_TLS_CA_CERT` | `GOHOOKBRIDGE_RAFT_TLS_CA_CERT` | 360 |
| `GOSMEE_RAFT_TLS_CERT` | `GOHOOKBRIDGE_RAFT_TLS_CERT` | 365 |
| `GOSMEE_RAFT_TLS_KEY` | `GOHOOKBRIDGE_RAFT_TLS_KEY` | 370 |
| `GOSMEE_BOOTSTRAP_CONFIG_FILE` | `GOHOOKBRIDGE_BOOTSTRAP_CONFIG_FILE` | 375 |
| `GOSMEE_NATS_PORT` | `GOHOOKBRIDGE_NATS_PORT` | 381 |
| `GOSMEE_NATS_CLUSTER_PORT` | `GOHOOKBRIDGE_NATS_CLUSTER_PORT` | 387 |
| `GOSMEE_NATS_ROUTES` | `GOHOOKBRIDGE_NATS_ROUTES` | 392 |
| `GOSMEE_NATS_CLUSTER_NAME` | `GOHOOKBRIDGE_NATS_CLUSTER_NAME` | 398 |
| `GOSMEE_NATS_BUFFER_SIZE` | `GOHOOKBRIDGE_NATS_BUFFER_SIZE` | 404 |

Also `flags.go:59` usage text: `$GOSMEE_PAYLOAD_FILE` → `$GOHOOKBRIDGE_PAYLOAD_FILE`,
`$GOSMEE_HEADERS_FILE` → `$GOHOOKBRIDGE_HEADERS_FILE`.

### B. Exec env vars — `gohookbridge/client/client.go` (lines 389–394)

| Old | New |
|---|---|
| `GOSMEE_EVENT_TYPE` | `GOHOOKBRIDGE_EVENT_TYPE` |
| `GOSMEE_EVENT_ID` | `GOHOOKBRIDGE_EVENT_ID` |
| `GOSMEE_CONTENT_TYPE` | `GOHOOKBRIDGE_CONTENT_TYPE` |
| `GOSMEE_TIMESTAMP` | `GOHOOKBRIDGE_TIMESTAMP` |
| `GOSMEE_PAYLOAD_FILE` | `GOHOOKBRIDGE_PAYLOAD_FILE` |
| `GOSMEE_HEADERS_FILE` | `GOHOOKBRIDGE_HEADERS_FILE` |

### C. Client env vars

| File | Old | New |
|---|---|---|
| `gohookbridge/client/command.go:55-57` | `GOSMEE_URL`, `GOSMEE_TARGET_URL` | `GOHOOKBRIDGE_URL`, `GOHOOKBRIDGE_TARGET_URL` |
| `gohookbridge/client/replay.go:180-181` | `GOSMEE_TARGET_URL` | `GOHOOKBRIDGE_TARGET_URL` |

### D. Replay script templates

| File | Old | New |
|---|---|---|
| `gohookbridge/client/templates/replay_script.tmpl.bash:53,90,91` | `GOSMEE_DEBUG_SERVICE` | `GOHOOKBRIDGE_DEBUG_SERVICE` |
| `gohookbridge/client/templates/replay_script.tmpl.httpie.bash:52,91,92` | `GOSMEE_DEBUG_SERVICE` | `GOHOOKBRIDGE_DEBUG_SERVICE` |

### E. Migration/deprecation env vars — **KEEP** (see §4)

### F. Session cookie

`gohookbridge/server/auth.go:20`: `"gosmee_session"` → `"gohookbridge_session"`.

### G. Version header

| File | Old | New |
|---|---|---|
| `gohookbridge/server/server.go:40` | `"X-Gosmee-Version"` | `"X-Gohookbridge-Version"` |
| `gohookbridge/client/client.go:516` (read) | `"X-Gosmee-Version"` | `"X-Gohookbridge-Version"` |
| `gohookbridge/client/client.go:820` (set) | `"X-Gosmee-Version"` | `"X-Gohookbridge-Version"` |
| `gohookbridge/client/client_test.go:788,801,817,840,896,923,965` | `"X-Gosmee-Version"` | `"X-Gohookbridge-Version"` |

### H. Internal type `goSmee` → `hookBridge`

| File | Old | New |
|---|---|---|
| `gohookbridge/client/client.go:51` | `type goSmee struct {` | `type hookBridge struct {` |
| `gohookbridge/client/client.go:93` | `func (c goSmee) parse(` | `func (c hookBridge) parse(` |
| `gohookbridge/client/client.go:657` | `func (c goSmee) clientSetup() error {` | `func (c hookBridge) clientSetup() error {` |
| `gohookbridge/client/command.go:95` | `cfg := goSmee{` | `cfg := hookBridge{` |
| `gohookbridge/client/client_test.go:37,54,63,74,84,95` | `p := goSmee{` | `p := hookBridge{` |
| `gohookbridge/client/client_test.go:978` | `gs *goSmee` | `gs *hookBridge` |
| `gohookbridge/client/client_test.go:1096,1120,1163,1188,1248,1281` | `gs := &goSmee{` | `gs := &hookBridge{` |
| `gohookbridge/client/client_test.go:1298` | `gs := goSmee{` | `gs := hookBridge{` |

**Test function names (additional, required for the `grep -i gosmee` gate):**

| Line | Old | New |
|---|---|---|
| 36 | `func TestGoSmeeGood(` | `func TestHookBridgeGood(` |
| 53 | `func TestGoSmeeBad(` | `func TestHookBridgeBad(` |
| 62 | `func TestGoSmeeBodyB(` | `func TestHookBridgeBodyB(` |
| 73 | `func TestGoSmeeBadTimestamp(` | `func TestHookBridgeBadTimestamp(` |
| 83 | `func TestGoSmeeMissingHeaders(` | `func TestHookBridgeMissingHeaders(` |
| 94 | `func TestGoSmeeEventID(` | `func TestHookBridgeEventID(` |

### I. Shell completion templates

| File | Old | New |
|---|---|---|
| `gohookbridge/templates/zsh_completion.zsh:1` | `#compdef gosmee` | `#compdef gohookbridge` |
| `gohookbridge/templates/zsh_completion.zsh:23` | `compdef _cli_zsh_autocomplete gosmee` | `compdef _cli_zsh_autocomplete gohookbridge` |
| `gohookbridge/templates/bash_completion.bash:3` | `PROG=gosmee` | `PROG=gohookbridge` |

### J. Docs

| File:line | Old | New |
|---|---|---|
| `README.md:286` | `$GOSMEE_PAYLOAD_FILE` | `$GOHOOKBRIDGE_PAYLOAD_FILE` |
| `README.md:293-298` | 6× `GOSMEE_*` (EVENT_TYPE/EVENT_ID/CONTENT_TYPE/TIMESTAMP/PAYLOAD_FILE/HEADERS_FILE) | `GOHOOKBRIDGE_*` |
| `README.md:306` | `GOSMEE_EXEC_ENV_VARS` | `GOHOOKBRIDGE_EXEC_ENV_VARS` |
| `README.md:313` | `$GOSMEE_PAYLOAD_FILE` | `$GOHOOKBRIDGE_PAYLOAD_FILE` |
| `README.md:345` | `GOSMEE_DEBUG_SERVICE` | `GOHOOKBRIDGE_DEBUG_SERVICE` |
| `SECURITY.md:270` | `$GOSMEE_PAYLOAD_FILE`, `$GOSMEE_HEADERS_FILE` | `$GOHOOKBRIDGE_PAYLOAD_FILE`, `$GOHOOKBRIDGE_HEADERS_FILE` |
| `misc/README.md:3` | `Somne` | `Some` |

`SECURITY.md:133` (`GOSMEE_WEBHOOK_SIGNATURE`) — see §4 open decision point (recommend KEEP).

### K. Test references (all in `gohookbridge/client/client_test.go`)

| Lines | Old | New |
|---|---|---|
| 151, 161-162 | `GOSMEE_DEBUG_SERVICE` (in `shellScriptTmplContent`) | `GOHOOKBRIDGE_DEBUG_SERVICE` |
| 1410, 1459, 1515, 1550, 1571 | `$GOSMEE_PAYLOAD_FILE` (in `execCommand`) | `$GOHOOKBRIDGE_PAYLOAD_FILE` |
| 1472 | `$GOSMEE_HEADERS_FILE` | `$GOHOOKBRIDGE_HEADERS_FILE` |
| 1487 | `$GOSMEE_PAYLOAD_FILE $GOSMEE_HEADERS_FILE` | `$GOHOOKBRIDGE_PAYLOAD_FILE $GOHOOKBRIDGE_HEADERS_FILE` |
| 1423 | `env | grep GOSMEE_` | `env | grep GOHOOKBRIDGE_` |
| 1431 | `GOSMEE_EVENT_TYPE=push` | `GOHOOKBRIDGE_EVENT_TYPE=push` |
| 1432 | `GOSMEE_EVENT_ID=delivery-123` | `GOHOOKBRIDGE_EVENT_ID=delivery-123` |
| 1433 | `GOSMEE_CONTENT_TYPE=application/json` | `GOHOOKBRIDGE_CONTENT_TYPE=application/json` |
| 1434 | `GOSMEE_TIMESTAMP=2023-10-27T10.00.01.000` | `GOHOOKBRIDGE_TIMESTAMP=2023-10-27T10.00.01.000` |
| 1435 | `GOSMEE_PAYLOAD_FILE=` | `GOHOOKBRIDGE_PAYLOAD_FILE=` |
| 1436 | `GOSMEE_HEADERS_FILE=` | `GOHOOKBRIDGE_HEADERS_FILE=` |

(Plus the version-header and type/function-name changes in §G/§H.)

### L. KEEP (do NOT rename)

- `README.md:52` — blog-post link (`.../gosmee-webhook-forwarder-relayer`) — background attribution.
- `NOTICE` lines 4-5 — `derived from gosmee (https://github.com/chmouel/gosmee)` — **legal attribution of the upstream project name**. (Not listed in the original inventory; added here as a KEEP with rationale — renaming would falsify the upstream attribution.)
- `.opencode/plans/remove-orphan-files.md` — historical plan archive.
- `smee.io` string references (flags.go:122 `Usage`, app.go:125-126, client/client.go:609-611, client/client_test.go:1300-1309, README).
- `SmeeChannel = "messages"` (flags.go:11), `smeeChannel = "messages"` (client/client.go:46), `smeeURL` variable (client/command.go:52, client/client.go).
- `X-Gohookbridge-Leader-Forwarded` — already correct.
- §E migration/deprecation paths.

## 3. Exact code changes per file

> Implementation note: for `flags.go`, the change is a pure prefix swap
> `GOSMEE_` → `GOHOOKBRIDGE_` applied to every occurrence (the `EnvVars` strings
> and the line-59 usage string). No suffix changes. Use a single global
> find-replace in that file.

### 3.1 `gohookbridge/flags.go`
- Replace every `GOSMEE_` with `GOHOOKBRIDGE_` (all 50 `EnvVars` entries listed in §2.A, plus the two `$GOSMEE_*` references in the line-59 `Usage` string).
- Do **not** touch `SmeeChannel = "messages"` (line 11) or the `smee.io` wording in the `--channel` usage (line 122).

### 3.2 `gohookbridge/client/client.go`
- Line 51: `type goSmee struct {` → `type hookBridge struct {`
- Line 93: `func (c goSmee) parse(now time.Time, data []byte) (payloadMsg, error) {` → `func (c hookBridge) parse(...)` (same params/return).
- Lines 389-394: `"GOSMEE_EVENT_TYPE="` → `"GOHOOKBRIDGE_EVENT_TYPE="`, `"GOSMEE_EVENT_ID="` → `"GOHOOKBRIDGE_EVENT_ID="`, `"GOSMEE_CONTENT_TYPE="` → `"GOHOOKBRIDGE_CONTENT_TYPE="`, `"GOSMEE_TIMESTAMP="` → `"GOHOOKBRIDGE_TIMESTAMP="`, `"GOSMEE_PAYLOAD_FILE="` → `"GOHOOKBRIDGE_PAYLOAD_FILE="`, `"GOSMEE_HEADERS_FILE="` → `"GOHOOKBRIDGE_HEADERS_FILE="`.
- Line 516: `resp.Header.Get("X-Gosmee-Version")` → `resp.Header.Get("X-Gohookbridge-Version")`.
- Line 657: `func (c goSmee) clientSetup() error {` → `func (c hookBridge) clientSetup() error {`.
- Line 820: `w.Header().Set("X-Gosmee-Version", Version)` → `w.Header().Set("X-Gohookbridge-Version", Version)`.

### 3.3 `gohookbridge/client/command.go`
- Lines 55-57: `os.Getenv("GOSMEE_URL")` → `os.Getenv("GOHOOKBRIDGE_URL")` (×2), `os.Getenv("GOSMEE_TARGET_URL")` → `os.Getenv("GOHOOKBRIDGE_TARGET_URL")` (×2).
- Line 95: `cfg := goSmee{` → `cfg := hookBridge{`.

### 3.4 `gohookbridge/client/replay.go`
- Lines 180-181: `os.Getenv("GOSMEE_TARGET_URL")` → `os.Getenv("GOHOOKBRIDGE_TARGET_URL")` (×2).

### 3.5 `gohookbridge/client/templates/replay_script.tmpl.bash`
- Line 53: `echo "  GOSMEE_DEBUG_SERVICE  Alternative target URL"` → `echo "  GOHOOKBRIDGE_DEBUG_SERVICE  Alternative target URL"`.
- Line 90: `elif [[ -n "${GOSMEE_DEBUG_SERVICE:-}" ]]; then` → `elif [[ -n "${GOHOOKBRIDGE_DEBUG_SERVICE:-}" ]]; then`.
- Line 91: `targetURL="${GOSMEE_DEBUG_SERVICE}"` → `targetURL="${GOHOOKBRIDGE_DEBUG_SERVICE}"`.

### 3.6 `gohookbridge/client/templates/replay_script.tmpl.httpie.bash`
- Line 52: `echo "  GOSMEE_DEBUG_SERVICE  Alternative target URL"` → `echo "  GOHOOKBRIDGE_DEBUG_SERVICE  Alternative target URL"`.
- Line 91: `elif [[ -n "${GOSMEE_DEBUG_SERVICE:-}" ]]; then` → `elif [[ -n "${GOHOOKBRIDGE_DEBUG_SERVICE:-}" ]]; then`.
- Line 92: `targetURL="${GOSMEE_DEBUG_SERVICE}"` → `targetURL="${GOHOOKBRIDGE_DEBUG_SERVICE}"`.

### 3.7 `gohookbridge/server/auth.go`
- Line 20: `sessionCookieName = "gosmee_session"` → `sessionCookieName = "gohookbridge_session"`.

### 3.8 `gohookbridge/server/server.go`
- Line 40: `versionHeaderName = "X-Gosmee-Version"` → `versionHeaderName = "X-Gohookbridge-Version"`.
- Lines 841-849: **DO NOT TOUCH** the `deprecatedEnvVars` map (see §4).

### 3.9 `gohookbridge/templates/zsh_completion.zsh`
- Line 1: `#compdef gosmee` → `#compdef gohookbridge`.
- Line 23: `compdef _cli_zsh_autocomplete gosmee` → `compdef _cli_zsh_autocomplete gohookbridge`.

### 3.10 `gohookbridge/templates/bash_completion.bash`
- Line 3: `PROG=gosmee` → `PROG=gohookbridge`.

### 3.11 `gohookbridge/client/client_test.go`
- Rename type + test function names per §H.
- Version header strings per §G (7 occurrences).
- `shellScriptTmplContent` string: lines 151, 161, 162 `GOSMEE_DEBUG_SERVICE` → `GOHOOKBRIDGE_DEBUG_SERVICE`.
- `TestRunExecCommand` body: lines 1410, 1423, 1431-1436, 1459, 1472, 1487, 1515, 1550, 1571 per §K.
- Do **not** touch `smeeURL: "https://smee.io/test-channel"` (line 1300), the `"https://smee.io"` assertion (line 1309), or the `TestClientSetupKeyFileWithSmeeIOFails` function name (contains `SmeeIO`, not `gosmee`).

### 3.12 Docs
- `README.md`: apply §J changes (lines 286, 293-298, 306, 313, 345). Leave line 52 alone.
- `SECURITY.md`: line 270 → `$GOHOOKBRIDGE_PAYLOAD_FILE` / `$GOHOOKBRIDGE_HEADERS_FILE`. Line 133 — see §4.
- `misc/README.md`: line 3 `Somne` → `Some`.

## 4. Migration-tooling exception (KEEP old `GOSMEE_*` names)

**Recommendation: KEEP the old `GOSMEE_*` names in the migration/deprecation
paths.** These names are historical *inputs*, not runtime contracts — their
entire purpose is to detect and migrate old deployments. Renaming them would
break the migration path and remove the safety guard that prevents silent
misconfiguration.

Files kept untouched:

- `gohookbridge/server/migrate.go` lines 18-55 — the `migrate-config` command reads
  `GOSMEE_MAX_BODY_SIZE`, `GOSMEE_CORS_ORIGIN`, `GOSMEE_TRUST_PROXY`,
  `GOSMEE_FOOTER`, `GOSMEE_AUTH_SESSION_SECRET`, `GOSMEE_WEBHOOK_SIGNATURE`,
  `GOSMEE_ALLOWED_IPS` and emits `bootstrap.yaml`.
- `gohookbridge/server/server.go` lines 841-849 — the `deprecatedEnvVars` map
  (adds `GOSMEE_ENCRYPTED_CHANNELS_FILE`, `GOSMEE_AUTH_CONFIG_FILE`) that FATAL-exits
  with migration instructions if any old var is set.
- `gohookbridge/server/auth_config.go:20` — error text mentions `GOSMEE_AUTH_CONFIG_FILE`.
- `gohookbridge/server/command.go:27` — `migrate-config` command description lists the old names.

**Consequence of the alternative (renaming these too):** an operator running the
old `GOSMEE_WEBHOOK_SIGNATURE` (etc.) would see the env var silently ignored —
no FATAL, no migration output, and `migrate-config` would report "Nothing to
migrate". This removes exactly the guard the §E code was written to provide.

**Open decision point (flag to user):** `SECURITY.md:133` currently reads
"Secrets can also be set via `GOSMEE_WEBHOOK_SIGNATURE` (comma-separated)." This
documents a deprecated env var that is in the §E keep-list. The task's §J listed
line 133 as a rename target, which conflicts with §E. **Recommended action:** keep
`GOSMEE_WEBHOOK_SIGNATURE` at `SECURITY.md:133` (consistent with §E — the code
still reads that exact name), and optionally append a short note that this is a
deprecated name migrated via `gohookbridge server migrate-config`. If the user
prefers a rename here, the doc line must instead point to the current
`defaults.webhook_secret` bootstrap key — but the literal `GOSMEE_WEBHOOK_SIGNATURE`
env var name must not be renamed in the migration code.

## 5. Test updates

All edits are in `gohookbridge/client/client_test.go` (§G, §H, §K). The existing
`TestRunExecCommand` "environment variables are set" subtest (line 1420) and the
`TestRunExecCommand` file-based subtests already assert the env-var wiring; after
the rename they assert the **new** names, so no functional coverage is lost.

**New test (required):** add `gohookbridge/flags_test.go` to assert that no flag
defines a `GOSMEE_*` env var and that every non-`NO_COLOR` env var uses the
`GOHOOKBRIDGE_` prefix. This permanently guards the rename (the `grep` gate is
one-time).

```go
package gohookbridge

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

// TestEnvVarPrefixes guards the gosmee→gohookbridge env-var rename: no flag may
// expose a GOSMEE_* env var, and every env var must be NO_COLOR or GOHOOKBRIDGE_*.
func TestEnvVarPrefixes(t *testing.T) {
	all := [][]cli.Flag{CommonFlags, ReplayFlags, KeygenFlags, ClientFlags,
		ProduceFlags, ProxyFlags, ServerFlags}

	seen := 0
	for _, group := range all {
		for _, fl := range group {
			envs := flagEnvVars(fl)
			for _, e := range envs {
				seen++
				if strings.Contains(strings.ToUpper(e), "GOSMEE") {
					t.Fatalf("flag %q exposes legacy GOSMEE env var %q", flagName(fl), e)
				}
				if e != "NO_COLOR" && !strings.HasPrefix(e, "GOHOOKBRIDGE_") {
					t.Fatalf("flag %q has non-conforming env var %q", flagName(fl), e)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("expected at least one env var across all flag groups")
	}
}

func flagEnvVars(fl cli.Flag) []string {
	switch f := fl.(type) {
	case *cli.StringFlag:
		return f.EnvVars
	case *cli.IntFlag:
		return f.EnvVars
	case *cli.BoolFlag:
		return f.EnvVars
	case *cli.StringSliceFlag:
		return f.EnvVars
	case *cli.DurationFlag:
		return f.EnvVars
	case *cli.Float64Flag:
		return f.EnvVars
	default:
		return nil
	}
}

func flagName(fl cli.Flag) string {
	for _, n := range fl.Names() {
		if len(n) > 1 {
			return n
		}
	}
	return ""
}
```

## 6. Doc updates (exact)

- `README.md`:
  - 286: `gohookbridge client --exec 'jq . $GOSMEE_PAYLOAD_FILE' https://smee.io/aBcDeF http://localhost:8080` → `... $GOHOOKBRIDGE_PAYLOAD_FILE ...`
  - 293-298: rename the six table cells to `GOHOOKBRIDGE_EVENT_TYPE`, `GOHOOKBRIDGE_EVENT_ID`, `GOHOOKBRIDGE_CONTENT_TYPE`, `GOHOOKBRIDGE_TIMESTAMP`, `GOHOOKBRIDGE_PAYLOAD_FILE`, `GOHOOKBRIDGE_HEADERS_FILE`.
  - 306: `GOSMEE_EXEC_ENV_VARS` → `GOHOOKBRIDGE_EXEC_ENV_VARS`.
  - 313: `$GOSMEE_PAYLOAD_FILE` → `$GOHOOKBRIDGE_PAYLOAD_FILE`.
  - 345: `GOSMEE_DEBUG_SERVICE` → `GOHOOKBRIDGE_DEBUG_SERVICE`.
- `SECURITY.md`:
  - 270: `$GOSMEE_PAYLOAD_FILE` → `$GOHOOKBRIDGE_PAYLOAD_FILE`; `$GOSMEE_HEADERS_FILE` → `$GOHOOKBRIDGE_HEADERS_FILE`.
  - 133: keep `GOSMEE_WEBHOOK_SIGNATURE` (see §4).
- `misc/README.md`:
  - 3: `Somne system integrations` → `Some system integrations`.

## 7. Local verification checklist (exact order)

Run from repo root. The web build regenerates the gitignored `gohookbridge/web/static/`.

```bash
# 0. Confirm branch
git branch --show-current            # expect: fix/remove-orphan-files
git status                           # only intended files modified

# 1. Web build (regenerates gohookbridge/web/static/)
make web-build

# 2. Lint (Go + markdown + nuxt typecheck)
make lint

# 3. Tests (runs web-build + web-test + go test ./...)
make test

# 4. Builds
make build        # full binary (server+client+proxy+keygen), embeds UI
make build-all    # full + client + proxy

# 5. Functional: env vars honored (pick two)
GOHOOKBRIDGE_SAVEDIR=/tmp/gohb-save ./bin/gohookbridge client --help   # saveDir reflects the env
GOHOOKBRIDGE_TOKEN=abc ./bin/gohookbridge client --help                 # token flag picks up env
# (assert via: ./bin/gohookbridge client --help 2>&1 | grep -i gohookbridge and
#  that the env var is listed; the flag library binds GOHOOKBRIDGE_* env vars)

# 6. Functional: completion emits gohookbridge
./bin/gohookbridge completion bash | grep -E 'PROG=gohookbridge'    # or head -5
./bin/gohookbridge completion zsh | grep 'compdef _cli_zsh_autocomplete gohookbridge'

# 7. Final grep gate — only intentional KEEP items remain
grep -rn -i gosmee --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=.opencode .
# Expected remaining matches (and ONLY these):
#   README.md:52 (blog link)
#   NOTICE:4-5 (upstream attribution)
#   gohookbridge/server/migrate.go (7 GOSMEE_* reads)
#   gohookbridge/server/server.go:841-849 (deprecatedEnvVars map)
#   gohookbridge/server/auth_config.go:20 (GOSMEE_AUTH_CONFIG_FILE)
#   gohookbridge/server/command.go:27 (migrate-config description)
#   SECURITY.md:133 (GOSMEE_WEBHOOK_SIGNATURE, if kept per §4)
```

Commit (on `fix/remove-orphan-files`) with a message such as:
`chore: hard-rename gosmee identifiers to gohookbridge` (keep PR #16 open).

## 8. Deployment plan (ghcr.io, home cluster)

Facts: kubeconfig `/home/user/.kube/home` (context `home`), namespace
`gohookbridge`, release name `gohookbridge`, chart `./helm/gohookbridge`,
gitignored values `helm/gohookbridge/values-home.yaml`, public URL
`https://gohookbridge-test.home.webcenter.fr`, admin `admin` /
`<admin-password>` (stored in the gitignored `AGENTS.local.md`), HA 3 replicas,
k3s single node `kube-00`.
Current values-home image: `gohookbridge:raft-ha-5` (local containerd import).

### 8.1 Pre-flight

```bash
export KUBECONFIG=/home/user/.kube/home
docker version
docker login ghcr.io                                   # or:
# echo "$CR_PAT" | docker login ghcr.io -u webcenter-fr --password-stdin
# gh auth token                                        # if using GitHub CLI
kubectl get nodes -o wide                              # note ARCHITECTURE (amd64/arm64)
helm version
helm status gohookbridge -n gohookbridge               # record current revision for rollback
```

### 8.2 Image build & push (unique tag `gosmee-rename-1`)

```bash
# Build for the node's architecture (amd64 typical; adjust if ARCHITECTURE says arm64)
docker build -t ghcr.io/webcenter-fr/gohookbridge:gosmee-rename-1 .
docker push ghcr.io/webcenter-fr/gohookbridge:gosmee-rename-1
```

**Fallback if push fails** (no network / registry unreachable) — local build +
k3s containerd import, matching the existing `raft-ha-5` pattern:

```bash
docker build -t gohookbridge:gosmee-rename-1 .
docker save gohookbridge:gosmee-rename-1 -o /tmp/gohookbridge-gosmee-rename-1.tar
# (if node is remote, scp the tar to the node, then:)
sudo k3s ctr images import /tmp/gohookbridge-gosmee-rename-1.tar
# Then use values-home image: repository=gohookbridge, tag=gosmee-rename-1, pullPolicy=IfNotPresent
```

### 8.3 Values update (`helm/gohookbridge/values-home.yaml`)

Edit lines 10-13 (currently `repository: gohookbridge`, `tag: raft-ha-5`,
`pullPolicy: IfNotPresent`) to:

```yaml
  image:
    repository: ghcr.io/webcenter-fr/gohookbridge
    tag: gosmee-rename-1
    pullPolicy: Always
```

(`pullPolicy: Always` guarantees the freshly-pushed image is pulled even if a
stale `gosmee-rename-1` tag exists from a prior attempt. For the fallback path,
set `repository: gohookbridge`, `tag: gosmee-rename-1`, `pullPolicy: IfNotPresent`.)

### 8.4 Deploy

```bash
helm --kubeconfig /home/user/.kube/home upgrade --install gohookbridge ./helm/gohookbridge \
  -f helm/gohookbridge/values-home.yaml \
  --namespace gohookbridge \
  --wait --timeout 10m
```

(The chart's default `values.yaml` is auto-loaded; `values-home.yaml` overrides it.)

### 8.5 Validation (concrete)

```bash
export KUBECONFIG=/home/user/.kube/home

# 1. 3 pods Running/Ready
kubectl -n gohookbridge get pods -o wide          # expect 3/3 Running READY

# 2. Raft leader elected (grep all 3 pods)
for p in gohookbridge-server-0 gohookbridge-server-1 gohookbridge-server-2; do
  echo "== $p =="; kubectl -n gohookbridge logs $p 2>&1 | grep -iE 'leader|raft' | tail -5
done

# 3. UI reachable (HTTP 200) + new version header
curl -sS -o /dev/null -w '%{http_code}\n' https://gohookbridge-test.home.webcenter.fr/
curl -sSI https://gohookbridge-test.home.webcenter.fr/version | grep -i 'x-gohookbridge-version'

# 4. Admin login (expect {"ok":true} + Set-Cookie: gohookbridge_session=...)
curl -sS -c /tmp/gohb-cookies.txt -D - -X POST https://gohookbridge-test.home.webcenter.fr/api/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}"
```

**Local echo target** (run once per port in a separate terminal) — prints the
decrypted body received from the client:

```bash
python3 - <<'EOF'
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
port = int(sys.argv[1]) if len(sys.argv) > 1 else 8080
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        print("RECEIVED:", self.rfile.read(n).decode(), flush=True)
        self.send_response(200); self.end_headers()
    def log_message(self, *a): pass
HTTPServer(('0.0.0.0', port), H).serve_forever()
EOF
```

**Fully encrypted (E2E)** — `encryption_mode: e2e` with a NaCl keypair:

```bash
# 1. Generate keypair; stdout prints the base64url public key
./bin/gohookbridge keygen --key-file /tmp/e2e-key.json
PUBKEY=$(./bin/gohookbridge keygen --key-file /tmp/e2e-key.json)

# 2. Create E2E channel (authenticated session)
curl -sS -b /tmp/gohb-cookies.txt -X POST https://gohookbridge-test.home.webcenter.fr/api/channels/ \
  -H 'Content-Type: application/json' \
  -d "{\"id\":\"rename-e2e-test\",\"encryption_mode\":\"e2e\",\"encryption_public_key\":\"$PUBKEY\"}"

# 3. Start client (decrypts with the private key) → local echo :8080
./bin/gohookbridge client --encryption-key-file /tmp/e2e-key.json \
  https://gohookbridge-test.home.webcenter.fr/rename-e2e-test http://localhost:8080

# 4. Send webhook (server encrypts with the channel public key at ingest)
curl -sS -X POST https://gohookbridge-test.home.webcenter.fr/rename-e2e-test \
  -H 'Content-Type: application/json' -d '{"hello":"world"}'
# EXPECT: echo server prints {"hello":"world"} (proves E2E decrypt)
```

**Partially encrypted (server-side)** — `encryption_mode: server_side` with AES-256-GCM:

```bash
# 1. Create plain channel, then generate + set the AES key (returns encryption_key)
curl -sS -b /tmp/gohb-cookies.txt -X POST https://gohookbridge-test.home.webcenter.fr/api/channels/ \
  -H 'Content-Type: application/json' -d '{"id":"rename-srv-test"}'
KEY=$(curl -sS -b /tmp/gohb-cookies.txt -X POST https://gohookbridge-test.home.webcenter.fr/api/channels/rename-srv-test/generate-encryption-key \
  -H 'Content-Type: application/json' -d '{"mode":"server_side"}' | jq -r .encryption_key)

# 2. Start client (AES key) → local echo :8081
./bin/gohookbridge client --encryption-key "$KEY" \
  https://gohookbridge-test.home.webcenter.fr/rename-srv-test http://localhost:8081

# 3. Send webhook (server AES-encrypts at ingest)
curl -sS -X POST https://gohookbridge-test.home.webcenter.fr/rename-srv-test \
  -H 'Content-Type: application/json' -d '{"hello":"world"}'
# EXPECT: echo server prints {"hello":"world"} (proves server-side decrypt path)
```

### 8.6 Rollback

```bash
helm --kubeconfig /home/user/.kube/home rollback gohookbridge -n gohookbridge
helm --kubeconfig /home/user/.kube/home status gohookbridge -n gohookbridge   # confirm revision -1
kubectl --kubeconfig /home/user/.kube/home -n gohookbridge get pods           # 3/3 Running Ready
curl -sS -o /dev/null -w '%{http_code}\n' https://gohookbridge-test.home.webcenter.fr/
```

(The prior revision uses the local `gohookbridge:raft-ha-5` image, still present in
k3s containerd, so rollback does not require registry access.)

## 9. Risks & rollback

- **Session invalidation:** renaming `gosmee_session` → `gohookbridge_session`
  invalidates every existing browser session (accepted by the user). During the
  rolling update there is a transient mix of old/new cookie names; once all pods
  are on the new revision it is consistent. Session *secret* is unchanged (Raft
  store persists), so no admin lockout — just a re-login.
- **Env-var breakage for existing users:** any user/script setting `GOSMEE_*`
  (except §E migration vars) will silently stop working after the hard rename —
  this is the intended behavior. The §E FATAL guard remains for the deprecated
  set so old configs fail loudly rather than silently.
- **Version header during rollout:** old clients read `X-Gosmee-Version`, new
  server sets `X-Gohookbridge-Version`; `checkServerVersion` falls back to the
  JSON `/version` body when the header is missing, so a mixed-version window is
  non-breaking (no crash, only a degraded version check).
- **Image-push failure:** fall back to local build + `k3s ctr images import`
  (§8.2), matching the existing `raft-ha-5` pattern.
- **Raft data is preserved** across `helm upgrade` (PVC `raft-data`), so bootstrap
  re-runs only if the FSM were empty (it is not on an existing install).
- **Rollback** is a single `helm rollback` (§8.6); no data migration is involved.

## 10. Definition of done

- [ ] All §3 code edits applied; `go test ./...` passes (`make test`).
- [ ] `make lint` passes (Go + markdown + nuxt typecheck).
- [ ] `make build` and `make build-all` succeed.
- [ ] New `gohookbridge/flags_test.go` test present and green.
- [ ] `gohookbridge completion bash|zsh` emits `gohookbridge`.
- [ ] `GOHOOKBRIDGE_*` env vars are honored by the built binary (functional check).
- [ ] `grep -rn -i gosmee` (excluding node_modules/.git/.opencode) returns **only**
      the §4/§L KEEP items (README:52, NOTICE:4-5, migrate.go, server.go:841-849,
      auth_config.go:20, command.go:27, SECURITY.md:133-if-kept).
- [ ] Commit(s) on `fix/remove-orphan-files`; PR #16 remains the target.
- [ ] Image `ghcr.io/webcenter-fr/gohookbridge:gosmee-rename-1` built and pushed
      (or local containerd import fallback).
- [ ] `values-home.yaml` image updated; `helm upgrade --install` succeeded with
      `--wait`.
- [ ] 3 pods Running/Ready; Raft leader elected.
- [ ] UI HTTP 200 at `https://gohookbridge-test.home.webcenter.fr`; admin login OK.
- [ ] Simulated webhook consumed **fully encrypted (e2e)** and **partially
      encrypted (server_side)**, each forwarding decrypted `{"hello":"world"}` to
      the local echo service.
