# Plan: Raft-on-Kubernetes HA hardening for gohookbridge

**Branch:** `feat/nuxt-migration` (commit `a995020`; PR #14). **Deliverable is this file only — no application code.**
**Reference (source of truth):** `/projects/dagger-cache` module `github.com/disaster/dagger-kubernetes` (`internal/repository/raft_*.go`, `cmd/api/main.go`, `deploy/helm/dagger-kubernetes/templates/*`).
**Both projects pin `github.com/hashicorp/raft v1.7.3`, so the reference API usage (`NetworkTransportConfig{Stream, Logger, MaxPool, Timeout}`, custom `raft.StreamLayer`, `raft.LeadershipTransfer`, `raft.Raft.Stats()`) is verified exact for gohookbridge.**

## 0. Decisions locked in (from planning interview)

1. **Migration:** one-time **fresh bootstrap via `--raft-recovery-mode`** (FSM holds only config — channels/users/global — re-created from `bootstrap.yaml`; webhooks are ephemeral).
2. **Raft transport:** add **mTLS now** (plaintext no longer shipped for multi-node).
3. **mTLS provisioning:** **embedded CA + K8s Secret** (reference parity) → new deps `github.com/disaster37/goca`, `k8s.io/client-go`, `k8s.io/apimachinery`.
4. **Leader routing:** **keep `leaderForwardMiddleware`** (webhook POST + SSE stay load-balanced for NATS fan-out). No `observeLeadership` / leader-label Service / pod-patch RBAC.

---

## 1. Target design

### 1.1 PeerResolver abstraction (adopted, ported from reference)

Port `raft_discovery.go` into `gohookbridge/store/raft_discovery.go` (new file). Exact types:

```go
type RaftPeer struct {
    ID      string
    Address string
}

// PeerResolver computes the ordered voter list (including self) and identifies self.
type PeerResolver interface {
    Resolve() ([]RaftPeer, error)
    Self() (RaftPeer, error)
}

// RaftDiscoveryConfig is the discovery subset used by the server to build a resolver.
type RaftDiscoveryConfig struct {
    NodeID          string
    AdvertiseAddr   string // host:port; "" = derive
    BindAddr        string
    Peers           []RaftPeer
    Replicas        int
    StatefulSetName string
    HeadlessService string
    Namespace       string
    ClusterDomain   string
    RaftPort        int
}

func NewPeerResolver(cfg *RaftDiscoveryConfig) PeerResolver
func DeriveAdvertiseAddr(cfg *RaftDiscoveryConfig, hostname string) (string, error)
func PodSANs(cfg *RaftDiscoveryConfig, hostname string) (dnsNames []string, ipAddrs []net.IP)
```

Resolver selection (`NewPeerResolver`), exactly like the reference:
- `len(cfg.Peers) > 0` → `staticPeerResolver` (explicit `--raft-peers` override).
- `cfg.StatefulSetName != "" && cfg.HeadlessService != ""` → `dnsPeerResolver` (K8s DNS discovery).
- else → `singleNodeResolver` (self only; local dev).

Port verbatim the reference helpers (logic, FQDN-vs-short-name rationale, `podOrdinal`, `podAddress`, `deriveBindHost`, `clusterDomain`, `raftPort`, static self-matching/synthesize-self, `PodSANs`). Differences only:
- `defaultRaftPort` = **6001** (gohookbridge), not 8081.
- keep the `defaultRaftPort` constant name; add `const defaultRaftPort = 6001`.

### 1.2 `store.RaftConfig` changes (`gohookbridge/store/raft.go`)

Replace the current `RaftConfig` with:

```go
type RaftConfig struct {
    Dir               string
    NodeID            string
    BindAddr          string
    AdvertiseAddr     string        // "" = derive from hostname+headless+port / bind host
    Peers             []string      // legacy "id=addr" entries; parsed into static resolver
    BootstrapPath     string
    Replicas          int
    StatefulSetName   string
    HeadlessService   string
    Namespace         string
    ClusterDomain     string
    RecoveryMode      bool
    NoSnapshotRestore bool          // default false (fixes today's data-loss bug)
    PerformanceMultiplier float64   // default 1.0 (Helm sets 5.0)
    ApplyTimeout      time.Duration
    Resolver          PeerResolver  // nil = legacy resolver from Peers/AdvertiseAddr/BindAddr
    TLS               *tls.Config   // nil = plaintext
}
```

`NewRaftStore(cfg RaftConfig) (*RaftStore, error)` — **keep by-value signature** (minimizes call-site churn). Inside: `withDefaults(&cfg)`, `os.MkdirAll(dir, 0o750)`, build `resolver` (use `cfg.Resolver`, else a legacy resolver), `resolveBootstrapState`, `clearStaleRaftState` (recovery mode), open BoltDB stores, open snapshot store, `newStreamTransport`, `raft.NewRaft`, and (if `shouldBootstrap`) `raft.BootstrapCluster(...)` seeding **only self**.

`RaftStore` gains fields: `transport raft.Transport`, `resolver PeerResolver`, `nodeID string`, `closeOnce sync.Once`, `closeErr error`.

**Callers to update** (signature unchanged, but `WaitForLeader`/`bootstrap` internals change): `gohookbridge/server/server.go:859`, `gohookbridge/store/storetest/helper.go` (`NewRaftStore`, `NewRaftStoreWithConfig`), `gohookbridge/store/raft_test.go:newTestRaftStore`.

### 1.3 New / changed CLI flags (`gohookbridge/flags.go`)

Add to `ServerFlags` (all optional, backward compatible; single-node local dev requires none):

| Flag | Type | Default | Env var | Purpose |
|---|---|---|---|---|
| `raft-advertise-addr` | string | `""` | `GOSMEE_RAFT_ADVERTISE_ADDR` | explicit advertise host:port; derive pod FQDN when empty |
| `raft-replicas` | int | `1` | `GOSMEE_RAFT_REPLICAS` | voter count for DNS discovery |
| `raft-statefulset-name` | string | `""` | `GOSMEE_RAFT_STATEFULSET_NAME` | StatefulSet name (`<sts>-<i>` pod names) |
| `raft-headless-service` | string | `""` | `GOSMEE_RAFT_HEADLESS_SERVICE` | headless Service name |
| `raft-namespace` | string | `""` | `GOSMEE_RAFT_NAMESPACE` | namespace; empty = `POD_NAMESPACE` env |
| `raft-cluster-domain` | string | `"cluster.local"` | `GOSMEE_RAFT_CLUSTER_DOMAIN` | cluster DNS suffix; empty = end at `.svc` |
| `raft-leader-wait-timeout` | duration | `60s` | `GOSMEE_RAFT_LEADER_WAIT_TIMEOUT` | WaitForLeader / WaitForCleanState budget |
| `raft-recovery-mode` | bool | `false` | `GOSMEE_RAFT_RECOVERY_MODE` | clear stale raft state + fresh bootstrap |
| `raft-no-snapshot-restore` | bool | `false` | `GOSMEE_RAFT_NO_SNAPSHOT_RESTORE` | map to `NoSnapshotRestoreOnStart` |
| `raft-performance-multiplier` | float | `1.0` | `GOSMEE_RAFT_PERFORMANCE_MULTIPLIER` | scale election/heartbeat/lease (Helm: 5.0) |
| `raft-tls-enabled` | bool | `false` | `GOSMEE_RAFT_TLS_ENABLED` | enable raft mTLS |
| `raft-tls-ca-secret` | string | `""` | `GOSMEE_RAFT_TLS_CA_SECRET` | K8s Secret name for CA sharing (multi-node) |
| `raft-tls-ca-bootstrap` | bool | `false` | `GOSMEE_RAFT_TLS_CA_BOOTSTRAP` | force this node to mint+share the CA (else ordinal 0) |
| `raft-tls-dir` | string | `""` | `GOSMEE_RAFT_TLS_DIR` | TLS material dir; default `<raft-dir>/tls` |
| `raft-tls-validity` | duration | `8760h` | `GOSMEE_RAFT_TLS_VALIDITY` | leaf cert validity |
| `raft-tls-client-auth` | bool | `true` | `GOSMEE_RAFT_TLS_CLIENT_AUTH` | mTLS (RequireAndVerifyClientCert) |
| `raft-tls-ca-cert` | string | `""` | `GOSMEE_RAFT_TLS_CA_CERT` | manual CA cert path |
| `raft-tls-cert` | string | `""` | `GOSMEE_RAFT_TLS_CERT` | manual leaf cert path |
| `raft-tls-key` | string | `""` | `GOSMEE_RAFT_TLS_KEY` | manual leaf key path |

**Backward compatibility:** `raft-dir`, `raft-node-id` (default `node1`), `raft-bind-addr` (default `127.0.0.1:6001`), `raft-peers`, `bootstrap-config-file` unchanged. Single-node local dev: no new flags; `singleNodeResolver` derives self from `raft-bind-addr` (or `127.0.0.1:6001` default).

---

## 2. Bootstrap saga

### 2.1 Which node bootstraps / what it seeds

`resolveBootstrapState(cfg *RaftConfig, resolver PeerResolver) (nodeID string, shouldBootstrap bool, err error)` (ported from reference, adapted to by-value cfg):

```go
shouldBootstrap = true
if resolver != nil {
    resolved, err := resolver.Resolve(); if err != nil { return "", false, fmt.Errorf("resolve raft peers: %w", err) }
    if self, errSelf := resolver.Self(); errSelf == nil && len(resolved) > 0 {
        shouldBootstrap = self.ID == resolved[0].ID   // ONLY first peer (ordinal 0 / first static peer / self)
    }
}
nodeID = cfg.NodeID
if nodeID == "" && resolver != nil { if self, err := resolver.Self(); err == nil { nodeID = self.ID } }
if nodeID == "" { nodeID = "node1" }  // gohookbridge default (stable: StatefulSet pod name via --raft-node-id)
```

In `NewRaftStore`, after building `transport` + `advertise`:

```go
if shouldBootstrap {
    configuration := raftConfigurationFromPeers([]RaftPeer{{ID: nodeID, Address: advertise}}) // seed ONLY self
    if err := raft.BootstrapCluster(raftCfg, logStore, stableStore, snapStore, transport, configuration); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
        _ = closeRaftTransport(transport); _ = db.Close(); return nil, fmt.Errorf("bootstrap raft cluster: %w", err)
    }
}
```

- `raftConfigurationFromPeers([]RaftPeer) raft.Configuration` — maps peers to `raft.Server{Suffrage: raft.Voter, ID, Address}`, skipping empty IDs/addresses, deduping IDs.
- **Others start empty and join** via the leader's `ReconcileMembership`/`AddVoter` (joinLoop, §4). They never seed themselves.
- **Critical fix:** remove `ShutdownOnRemove: true` from the `raft.Config`. `ReconcileMembership` updates addresses via remove+re-add; with `ShutdownOnRemove` a peer would shut itself down mid-update. (Default `false` is correct.)
- **Critical fix:** remove `NoSnapshotRestoreOnStart: true`; set from `cfg.NoSnapshotRestore` (default `false`) so snapshot restore on restart is not silently disabled (data-loss after log compaction).

### 2.2 `WaitForLeader` waits for ANY leader

```go
func (rs *RaftStore) WaitForLeader(ctx context.Context) error {
    return rs.waitForLeaderCondition(ctx, "wait for raft leader", func() bool { return rs.raft.Leader() != "" })
}
func (rs *RaftStore) WaitForSelfLeadership(ctx context.Context) error {
    return rs.waitForLeaderCondition(ctx, "wait for self leadership", rs.IsLeader)
}
func (rs *RaftStore) WaitForCleanState(ctx context.Context) error {
    return rs.waitForLeaderCondition(ctx, "wait for raft clean state", rs.IsCleanState)
}
```

`waitForLeaderCondition` polls a 50ms ticker, also selecting on `rs.raft.LeaderCh()` (which fires on THIS node's leadership transitions) and `ctx.Done()`; returns `raft.ErrRaftShutdown` when `LeaderCh()` is closed. **No follower CrashLoop**: followers no longer wait for self-leadership.

### 2.3 Bootstrap-config-file applied once, after a leader exists

- **Remove** the `rs.bootstrap(cfg)` call (and the `bootstrap`/`bootstrapConfiguration` functions) from `NewRaftStore`. `NewRaftStore` no longer applies `bootstrap.yaml` and no longer calls `MigrateRBAC`.
- In `serve()` (see §6.4), after `WaitForLeader` + `WaitForCleanState`, apply bootstrap exactly once:

```go
func applyBootstrapOnce(rs *store.RaftStore, path string) error {
    if path == "" || !rs.IsLeader() { return nil }
    hasData, err := rs.HasData(); if err != nil { return err }
    if hasData { return nil }                 // idempotent: only when FSM empty
    cfg, err := store.LoadBootstrap(path); if err != nil { return fmt.Errorf("load bootstrap: %w", err) }
    if err := rs.ApplyBootstrap(cfg); err != nil { return fmt.Errorf("apply bootstrap: %w", err) }
    return nil
}
```

`ApplyBootstrap` already guards `HasData()` internally; the leader-only + once semantics guarantee the config is applied a single time by the single leader.

---

## 3. Advertise/bind separation + DNS resilience (plaintext + TLS)

### 3.1 Stream layers (`gohookbridge/store/raft_streamlayer.go`, new)

Generalize the reference's `retryingStreamLayer` to plaintext **and** TLS:

```go
// plainStreamLayer: plaintext raft.StreamLayer over a net.Listener.
type plainStreamLayer struct {
    listener  net.Listener
    advertise net.Addr
}
func newPlainStreamLayer(bindAddr string, advertise net.Addr) (*plainStreamLayer, error)  // net.Listen("tcp", bindAddr)
func (l *plainStreamLayer) Accept() (net.Conn, error)                                       // l.listener.Accept()
func (l *plainStreamLayer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) // net.Dialer{Timeout}.Dial("tcp", string(addr))
func (l *plainStreamLayer) Close() error
func (l *plainStreamLayer) Addr() net.Addr

// tlsStreamLayer: TLS-wrapped raft.StreamLayer (port from reference raft_tls.go §452-493).
// retryingStreamLayer: DNS re-resolve per dial + retry (port reference; tlsCfg nil = plaintext).
type retryingStreamLayer struct {
    inner  raft.StreamLayer
    tlsCfg *tls.Config // nil = plaintext dial
    dialer *net.Dialer
}
func newRetryingStreamLayer(inner raft.StreamLayer, timeout time.Duration, tlsCfg *tls.Config) *retryingStreamLayer

const maxDialRetries = 4
const dialRetryBackoff = 500 * time.Millisecond

// hostAddr preserves the DNS name in raft config (port from reference).
type hostAddr struct{ host string; port int }
func (a hostAddr) Network() string { return "tcp" }
func (a hostAddr) String() string  { return net.JoinHostPort(a.host, strconv.Itoa(a.port)) }
func newHostAddr(addr string) (net.Addr, error) // SplitHostPort + Atoi, no resolution
```

`retryingStreamLayer.Dial` logic (adapt reference lines 50–103): split host/port; for `attempt 0..maxDialRetries` (sleep `dialRetryBackoff*attempt` before retries), `net.LookupHost(host)`, dial `ips[0]:port`; if `tlsCfg != nil` clone + `ServerName=host` + `tls.DialWithDialer`, else `dialer.Dial("tcp", ...)`; retry only on `isConnRefused(err)` (substring `"connection refused"`), otherwise return immediately.

### 3.2 `newStreamTransport` (in `raft.go`)

```go
func newStreamTransport(cfg *RaftConfig, logOutput io.Writer) (raft.Transport, string, error) {
    host, port, err := net.SplitHostPort(cfg.BindAddr)  // normalize ""/0.0.0.0/:: → "127.0.0.1"
    advertiseAddr := cfg.AdvertiseAddr
    if advertiseAddr == "" { advertiseAddr = net.JoinHostPort(host, port) }

    // Startup gate: retry-resolution (1s interval, 2min budget) so a fresh cluster's
    // DNS warmup delays this pod instead of CrashLooping it.
    resolved, err := resolveAdvertiseAddr(advertiseAddr, 2*time.Minute, logOutput)
    if err != nil { return nil, "", fmt.Errorf("resolve raft advertise addr %s: %w", advertiseAddr, err) }
    _ = resolved // gate only; the advertise stored in config is the DNS name below

    advertise, err := newHostAddr(advertiseAddr)   // DNS name preserved
    if err != nil { return nil, "", fmt.Errorf("parse advertise addr %s: %w", advertiseAddr, err) }

    if cfg.TLS != nil {
        layer, err := newTLSStreamLayer(cfg.BindAddr, advertise, cfg.TLS)
        stream := newRetryingStreamLayer(layer, 10*time.Second, cfg.TLS)
        transport := raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
            Stream:  stream,
            Logger:  hclog.New(&hclog.LoggerOptions{Output: logOutput, Name: "transport"}),
            MaxPool: 1,
            Timeout: 10 * time.Second,
        })
        return transport, advertise.String(), nil
    }

    layer, err := newPlainStreamLayer(cfg.BindAddr, advertise)
    stream := newRetryingStreamLayer(layer, 10*time.Second, nil)
    transport := raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
        Stream:  stream,
        Logger:  hclog.New(&hclog.LoggerOptions{Output: logOutput, Name: "transport"}),
        MaxPool: 1,
        Timeout: 10 * time.Second,
    })
    return transport, advertise.String(), nil
}
```

> **Why a custom `raft.StreamLayer`:** `raft.NewTCPTransport` type-asserts its advertise address to `*net.TCPAddr`, so passing a DNS `hostAddr` panics/fails. `raft.NewNetworkTransportWithConfig` with a custom `Stream` whose `Addr()` returns `hostAddr` accepts the DNS name. `MaxPool: 1` forces a fresh connection (and thus fresh DNS resolution) per peer.

`resolveAdvertiseAddr(advertiseAddr string, timeout time.Duration, logOutput io.Writer) (*net.TCPAddr, error)` — port reference lines 446–464 (1s retry; log `[WARN] raft: unable to resolve advertise addr %s yet (cluster DNS may still be starting), retrying: %v`; return last error on budget expiry). Constants `defaultAdvertiseResolveTimeout = 2 * time.Minute`, `advertiseResolveRetryInterval = time.Second`.

**Chart:** `--raft-bind-addr 0.0.0.0:6001` + pod-FQDN advertise via `--raft-advertise-addr $(POD_NAME).<headless>.<ns>.svc.<clusterDomain>:6001` (or auto-derived from hostname + `--raft-statefulset-name`/`--raft-headless-service`). See §9.

---

## 4. Membership reconciliation

Port from reference `raft_store.go` (lines 499–655) into `raft.go`, with gohookbridge's `applyTimeout`/`hclog`→`log` adaptations:

```go
func (rs *RaftStore) GetConfiguration() (raft.Configuration, error)
func (rs *RaftStore) AddVoter(id, address string, timeout time.Duration) error
func (rs *RaftStore) RemoveServer(id string, timeout time.Duration) error

// ReconcileMembership: add missing voters, update changed addresses (remove+re-add), remove extra voters. Leader-only, idempotent.
func (rs *RaftStore) ReconcileMembership(desired []RaftPeer, timeout time.Duration) (added, updated, removed []string, err error)

// addrChanged: when desired is a DNS name, resolve and compare with current (which may be a stale IP); direct string compare otherwise.
func addrChanged(current, desired string) bool
```

Exact algorithm (port verbatim): build `currentByID`/`desiredByID` maps; (1) `AddVoter` for missing IDs; (2) for existing IDs where `addrChanged(currentAddr, desiredAddr)` → `RemoveServer(id)` then `AddVoter(id, desiredAddr)`; (3) `RemoveServer` for IDs in current but not desired.

### 4.1 Leader-only joinLoop

```go
// on RaftStore (resolver stored at construction)
func (rs *RaftStore) StartJoinLoop(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if !rs.IsLeader() { continue }
            desired, err := rs.resolver.Resolve()
            if err != nil { fmt.Fprintf(os.Stderr, "WARNING: joinLoop: resolve peers failed: %v\n", err); continue }
            added, updated, removed, err := rs.ReconcileMembership(desired, 10*time.Second)
            if err != nil { fmt.Fprintf(os.Stderr, "WARNING: joinLoop: reconcile membership failed: %v\n", err); continue }
            if len(added)+len(updated)+len(removed) > 0 {
                fmt.Fprintf(os.Stdout, "joinLoop: raft membership reconciled added=%v updated=%v removed=%v\n", added, updated, removed)
            }
        }
    }
}
```

Covers scale-up, scale-down, and pod restart with a new IP (desired FQDN re-resolves to the new IP; `addrChanged` removes+re-adds).

---

## 5. Graceful restart / leader reelection

### 5.1 `StepDown` (port reference lines 832–861)

```go
func (rs *RaftStore) StepDown(ctx context.Context) error {
    cfg, err := rs.GetConfiguration(); if err != nil { return fmt.Errorf("get configuration: %w", err) }
    otherVoters := 0
    for _, srv := range cfg.Servers { if srv.Suffrage == raft.Voter && string(srv.ID) != rs.nodeID { otherVoters++ } }
    if otherVoters == 0 { return nil } // single-node: no transfer target
    future := rs.raft.LeadershipTransfer()
    if err := future.Error(); err != nil { if errors.Is(err, raft.ErrNotLeader) { return nil }; return fmt.Errorf("step down: %w", err) }
    return nil
}
```

### 5.2 Shutdown path

Keep `Shutdown()` as final teardown, add `Close()` alias semantics (idempotent `sync.Once`): `raft.Shutdown()`, `closeRaftTransport(transport)`, `db.Close()`. Wire graceful leave in `serve()`'s signal handling: call `rs.StepDown(ctx)` (transfer leadership) before `rs.Shutdown()`. (No `LeaveCluster` self-removal — `ShutdownOnRemove` is false and scale-down is handled by joinLoop `RemoveServer` on the remaining leader.)

### 5.3 Helm (see §9): `terminationGracePeriodSeconds: 60`, `preStop` (kill `-TERM 1`, sleep 2), PDB, startupProbe/readinessProbe/livenessProbe.

---

## 6. Readiness / clean-state barrier

### 6.1 `IsCleanState` + `WaitForCleanState` (port reference lines 741–789)

```go
const followerLastContactThreshold = 15 * time.Second

func (rs *RaftStore) IsCleanState() bool {
    stats := rs.raft.Stats()
    state := stats["state"]
    if state != "Leader" && state != "Follower" { return false }
    if stats["commit_index"] != stats["applied_index"] { return false }
    if stats["fsm_pending"] != "0" { return false }
    if state == "Follower" && stats["commit_index"] == "0" { return false }
    if state == "Follower" {
        lastContact := stats["last_contact"]
        if lastContact == "never" || lastContact == "" { return false }
        d, err := time.ParseDuration(lastContact)
        if err != nil || d > followerLastContactThreshold { return false }
    }
    return true
}
```

Extract `cleanStateFromStats(stats map[string]string) bool` so unit tests can drive it table-style without a live node.

### 6.2 `IsStarted` / `IsVoter` (port reference lines 863–881)

```go
func (rs *RaftStore) IsStarted() bool { return rs.IsLeader() || rs.IsVoter() }
func (rs *RaftStore) IsVoter() bool   { /* GetConfiguration scan for self as Voter */ }
```

### 6.3 Probe endpoints (`server/server.go`)

- `/livez` — **unchanged** (`retVersion`): pure liveness.
- `/health` — **unchanged** (`retVersion`): backward-compatible.
- `/readyz` — **new**: `200 {"status":"ready"}` iff `rs.IsCleanState()`, else `503 {"status":"not ready"}`.
- `/startup` — **new**: `200 {"status":"started"}` iff `rs.IsStarted()`, else `503 {"status":"starting"}`.

Add `mainRouter.Get("/readyz", ...)` and `mainRouter.Get("/startup", ...)` adjacent to the existing `/health`/`/livez` routes (server.go ~line 956).

### 6.4 `serve()` reorder (exact new sequence)

Replace the block at server.go ~859–895 with:

1. Build `store.RaftConfig` from flags (§1.3) + `ApplyTimeout: 10*time.Second`.
2. Build discovery config + resolver + advertise + TLS:
   ```go
   hostname, _ := os.Hostname()
   discovery := store.RaftDiscoveryConfig{ NodeID, AdvertiseAddr, BindAddr, Peers: toRaftPeers(c.StringSlice("raft-peers")), Replicas, StatefulSetName, HeadlessService, Namespace: effectiveNamespace(c), ClusterDomain, RaftPort: 6001 }
   resolver := store.NewPeerResolver(&discovery)
   cfg.Resolver = resolver
   advertise, err := store.DeriveAdvertiseAddr(&discovery, hostname); if err != nil { return ... }
   cfg.AdvertiseAddr = advertise
   cfg.TLS, err = buildRaftTLSConfig(c, discovery, hostname)  // *tls.Config or nil (see §7)
   ```
   (If `cfg.NodeID == ""` and `--raft-node-id` defaulted, the chart always passes `--raft-node-id $(POD_NAME)`.)
3. `rs, err := store.NewRaftStore(cfg)`; `defer rs.Shutdown()`.
4. `WaitForLeader(ctx, cfg.LeaderWaitTimeout)` → error out on failure.
5. `go rs.StartJoinLoop(ctx)`.
6. `WaitForCleanState(ctx, cfg.LeaderWaitTimeout)` → error out on failure.
7. `applyBootstrapOnce(rs, c.String("bootstrap-config-file"))`.
8. If `rs.IsLeader()`: `store.MigrateRBAC(rs)` (keep warn-only on error).
9. Session secret block (rework): if `secret == ""` and `rs.IsLeader()` → generate 32-byte hex + `rs.SetSessionSecret`; if follower and still empty → poll `rs.SessionSecret()` every 100ms up to `cfg.LeaderWaitTimeout` for the replicated value; only then, if still empty and `len(users)>0 || len(providers)>0`, return the existing "no session secret" error.
10. NATS broker init + `ListChannels` TTL + `dev-admin` (moved to after clean-state so followers have replicated FSM).

> Rationale for the reorder: NATS/channel bootstrap and dev-admin read the FSM; followers must reach clean state first, and bootstrap/admin writes must land on the leader.

---

## 7. Raft mTLS (embedded CA + K8s Secret)

### 7.1 New deps

- `go get github.com/disaster37/goca` (direct)
- `go get k8s.io/client-go` + `k8s.io/apimachinery` (direct)
- promote `github.com/hashicorp/go-hclog` to direct (used by `NetworkTransportConfig.Logger`).

### 7.2 `gohookbridge/store/raft_tls.go` (new)

Port from reference `raft_tls.go` + `ca.go`, minus the domain-specific client-cert minting (gohookbridge only needs the raft peer/server certs):

```go
type RaftTLSConfig struct {
    Enabled      bool
    Dir          string        // default <raft-dir>/tls
    Validity     time.Duration // default 8760h
    Organization string        // default "gohookbridge-raft"
    CACertPath, CertPath, KeyPath string // manual mode
    CASecret     string
    CABootstrap  bool
    ClientAuth   bool          // default true
    SecretPollTimeout time.Duration
}

func LoadOrBuildRaftTLS(cfg *RaftTLSConfig, isMultiNode bool, clientset kubernetes.Interface, namespace string, dnsNames []string, ipAddrs []net.IP, commonName string, logger *log.Logger) (*tls.Config, error)
```

Port: `loadManualRaftTLS`, `ensureRaftCA`/`ensureRaftCAFromSecret`/`ensureLocalRaftCA`, `readRaftCASecret`, `createRaftCAWithGoca`, `issueOrReuseNodeCert`, `reusableNodeCert`, `coversSANs`, `buildRaftTLSMaterial`, `buildRaftTLSConfig`, `parsePEMCert`, `readPEM`/`writePEM`/`ensureTLSDir`, `MintingCA{IssuePeerCertificate, CACertificate}` (goca-backed), `tlsStreamLayer` + `newTLSStreamLayer`. `Organization` default `"gohookbridge-raft"`; CA CN `"gohookbridge-raft-ca"`.

`buildRaftTLSConfig`:
```go
clientAuthType := tls.NoClientCert
if clientAuth { clientAuthType = tls.RequireAndVerifyClientCert }
return &tls.Config{ Certificates: []tls.Certificate{m.leaf}, RootCAs: m.caPool, ClientCAs: m.caPool, ClientAuth: clientAuthType, MinVersion: tls.VersionTLS12 }
```

### 7.3 `gohookbridge/server/raft_tls.go` (new)

`buildRaftTLSConfig(c *cli.Context, discovery store.RaftDiscoveryConfig, hostname string) (*tls.Config, error)`:
- if `!c.Bool("raft-tls-enabled")` → `nil, nil`.
- manual mode if any of `raft-tls-ca-cert`/`raft-tls-cert`/`raft-tls-key` set.
- else: `isMultiNode := c.Int("raft-replicas") > 1 || len(c.StringSlice("raft-peers")) > 1`; `caBootstrap := c.Bool("raft-tls-ca-bootstrap") || (isMultiNode && strings.HasSuffix(hostname, "-0")) || !isMultiNode`; `commonName := c.String("raft-node-id")` (else resolver self ID); `dnsNames, ipAddrs := store.PodSANs(&discovery, hostname)`; `clientset := newK8sClientset()` (nil on failure → log warn, TLS will fail only if Secret mode is required); call `store.LoadOrBuildRaftTLS(...)`.
- multi-node + Secret mode + nil clientset → error `"raft TLS auto-mode for multi-node requires K8s (or manual CA files)"`.

### 7.4 `gohookbridge/server/k8s.go` (new)

`newK8sClientset() (kubernetes.Interface, error)` — `rest.InClusterConfig()`, fall back to `clientcmd` kubeconfig (port reference `main.go` lines 1238–1255). Nil-safe: callers tolerate a nil clientset (warn + disable Secret-based CA).

### 7.5 Helm RBAC (see §9.6): a Role/ClusterRole granting `secrets` `create`/`get` in the release namespace, bound to the server ServiceAccount, gated on `server.raft.tls.enabled`.

---

## 8. NATS HA assessment

**Decision: no code change.** Justification:

- NATS routes are configured via `--nats-routes nats://<pod>.<headless>.<ns>.svc:6222` (DNS names, from the `gohookbridge.natsRoutes` helper). The embedded `nats-server` dials each route from its explicit route URL on every (re)connect attempt; it does not cache resolved IPs across reconnect attempts — a pod restart with a new IP is handled by NATS's built-in route reconnect/backoff + re-resolution.
- The headless Service already publishes the `nats-cluster` port and sets `publishNotReadyAddresses: true`, so the route DNS resolves before readiness.
- Webhooks are ephemeral; a brief route outage during a pod restart is acceptable (senders retry on non-202).
- The in-process client connects to `127.0.0.1:<nats-port>` (unchanged) — no DNS involved.

Actions: none in `gohookbridge/nats/`. Add a manual K8s validation step (§10.4) that restarts a pod and confirms NATS routes re-form (`kubectl logs … | grep -i route`). Document the rationale in `design.md` (§13).

---

## 9. Helm changes

### 9.1 `values.yaml`

```yaml
server:
  enabled: true
  replicas: 3
  # ... image/address/port/raftPort/natsPort/natsClusterPort unchanged ...
  raft:
    clusterDomain: "cluster.local"      # NEW; empty = short .svc names
    leaderWaitTimeout: "60s"            # NEW
    performanceMultiplier: 5.0          # NEW (flag default stays 1.0)
    recoveryMode: false                 # NEW
    tls:
      enabled: true                     # NEW (multi-node mTLS)
      caSecret: ""                      # NEW; default "<fullname>-raft-ca" when empty
      clientAuth: true                  # NEW
  probes:
    startup:                            # NEW
      httpGet: { path: /startup, port: http }
    liveness:
      httpGet: { path: /livez, port: http }
    readiness:
      httpGet: { path: /readyz, port: http }   # CHANGED from /health
  terminationGracePeriodSeconds: 60     # NEW
  podDisruptionBudget:                  # NEW
    enabled: true
    minAvailable: 2                     # quorum floor for 3 replicas
serviceAccount:
  create: true                          # CHANGED from false (needed for CA Secret RBAC)
```

### 9.2 `_helpers.tpl` (add helpers)

Add (before existing helpers):

```gotemplate
{{/* Raft peers: "server-0=server-0.<headless>.<ns>.svc:<port>,server-1=..." (DNS names, cluster-domain aware) */}}
{{- define "gohookbridge.raftPeers" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $replicas := int .Values.server.replicas -}}
{{- $domain := .Values.server.raft.clusterDomain | default "cluster.local" -}}
{{- range $i := until $replicas -}}
{{- if $i }},{{ end -}}
{{- $host := printf "%s-server-%d.%s-server-headless.%s.svc" $name $i $name $ns -}}
{{- if $domain }}{{- $host = printf "%s.%s" $host $domain }}{{ end -}}
{{ $name }}-server-{{ $i }}={{ $host }}:{{ $.Values.server.raftPort }}
{{- end -}}
{{- end }}

{{/* NATS routes (unchanged semantics, cluster-domain aware) */}}
{{- define "gohookbridge.natsRoutes" -}} ... {{- end }}

{{/* Raft advertise FQDN for pod hostname */}}
{{- define "gohookbridge.raftAdvertise" -}}
{{- $name := include "gohookbridge.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $domain := .Values.server.raft.clusterDomain | default "cluster.local" -}}
{{- $host := printf "%s-server-headless.%s.svc" $name $ns -}}
{{- if $domain }}{{- $host = printf "%s.%s" $host $domain }}{{ end -}}
{{- printf "$(POD_NAME).%s:%s" $host .Values.server.raftPort -}}
{{- end }}

{{/* Raft CA Secret name */}}
{{- define "gohookbridge.raftCASecret" -}}
{{- .Values.server.raft.tls.caSecret | default (printf "%s-raft-ca" (include "gohookbridge.fullname" .)) -}}
{{- end }}
```

### 9.3 `server-statefulset.yaml`

- `podManagementPolicy: Parallel` (already present).
- Add `terminationGracePeriodSeconds: {{ .Values.server.terminationGracePeriodSeconds | default 60 }}`.
- Change args:
  - `--raft-bind-addr 0.0.0.0:{{ .Values.server.raftPort }}` (bind only).
  - **add** `--raft-advertise-addr {{ include "gohookbridge.raftAdvertise" . | quote }}`.
  - **add** `--raft-replicas "{{ .Values.server.replicas }}"`.
  - **add** `--raft-statefulset-name {{ include "gohookbridge.fullname" . }}-server`.
  - **add** `--raft-headless-service {{ include "gohookbridge.fullname" . }}-server-headless`.
  - **add** `--raft-namespace $(POD_NAMESPACE)`.
  - **add** `--raft-cluster-domain {{ .Values.server.raft.clusterDomain | default "cluster.local" | quote }}`.
  - **add** `--raft-leader-wait-timeout {{ .Values.server.raft.leaderWaitTimeout | default "60s" | quote }}`.
  - **add** `--raft-performance-multiplier {{ .Values.server.raft.performanceMultiplier | default 5.0 | quote }}`.
  - **add** (conditional) recovery-mode + TLS flags:
    ```
    {{- if .Values.server.raft.tls.enabled }}
    - --raft-tls-enabled
    - --raft-tls-ca-secret
    - {{ include "gohookbridge.raftCASecret" . | quote }}
    {{- end }}
    {{- if .Values.server.raft.recoveryMode }}
    - --raft-recovery-mode
    {{- end }}
    ```
  - Keep `--raft-peers {{ include "gohookbridge.raftPeers" . | quote }}` (static fallback, backward-compatible; the dnsPeerResolver is authoritative).
- Add `lifecycle.preStop` (exec `kill -TERM 1; sleep 2`) so SIGTERM reaches PID 1 and the server runs `StepDown` before exit.
- Add `startupProbe` (`/startup`, `initialDelaySeconds: 10, periodSeconds: 5, failureThreshold: 60, timeoutSeconds: 5`), `readinessProbe` (`/readyz`, `initialDelaySeconds: 5, periodSeconds: 5, failureThreshold: 3, timeoutSeconds: 5`), `livenessProbe` (`/livez`, `initialDelaySeconds: 30, periodSeconds: 10, failureThreshold: 3, timeoutSeconds: 5`).
- Add `terminationGracePeriodSeconds` (pod spec level).
- Add `serviceAccountName` (already conditional on `serviceAccount.create`).

### 9.4 `server-headless-service.yaml`

Already has `publishNotReadyAddresses: true`. No change (raft + nats-cluster ports remain). Keep.

### 9.5 New `templates/pdb.yaml`

```yaml
{{- if and .Values.server.enabled .Values.server.podDisruptionBudget.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "gohookbridge.fullname" . }}-server
  labels: {{- include "gohookbridge.labels" . | nindent 4 }}
spec:
  {{- if .Values.server.podDisruptionBudget.minAvailable }}
  minAvailable: {{ .Values.server.podDisruptionBudget.minAvailable }}
  {{- else }}
  maxUnavailable: {{ .Values.server.podDisruptionBudget.maxUnavailable | default 1 }}
  {{- end }}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ include "gohookbridge.name" . }}
      app.kubernetes.io/component: server
{{- end }}
```

### 9.6 New `templates/rbac.yaml` (gated on `server.raft.tls.enabled`)

```yaml
{{- if and .Values.server.enabled .Values.serviceAccount.create .Values.server.raft.tls.enabled }}
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "gohookbridge.fullname" . }}-server
  labels: {{- include "gohookbridge.labels" . | nindent 4 }}
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["create", "get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "gohookbridge.fullname" . }}-server
  labels: {{- include "gohookbridge.labels" . | nindent 4 }}
roleRef: { apiGroup: rbac.authorization.k8s.io, kind: Role, name: {{ include "gohookbridge.fullname" . }}-server }
subjects:
- kind: ServiceAccount
  name: {{ include "gohookbridge.fullname" . }}
  namespace: {{ .Release.Namespace }}
{{- end }}
```

### 9.7 Exact rendered args (3 replicas, release `gohookbridge`, ns `gohookbridge`, domain `cluster.local`)

```
server
--address 0.0.0.0
--port 3333
--public-url "https://gohookbridge-test.home.webcenter.fr"
--raft-dir /data/raft
--raft-node-id $(POD_NAME)
--raft-bind-addr 0.0.0.0:6001
--raft-advertise-addr "$(POD_NAME).gohookbridge-server-headless.gohookbridge.svc.cluster.local:6001"
--raft-replicas 3
--raft-statefulset-name gohookbridge-server
--raft-headless-service gohookbridge-server-headless
--raft-namespace $(POD_NAMESPACE)
--raft-cluster-domain cluster.local
--raft-leader-wait-timeout 60s
--raft-performance-multiplier 5.0
--raft-peers "gohookbridge-server-0=gohookbridge-server-0.gohookbridge-server-headless.gohookbridge.svc.cluster.local:6001,gohookbridge-server-1=...,gohookbridge-server-2=..."
--raft-tls-enabled
--raft-tls-ca-secret gohookbridge-raft-ca
--nats-port 4222
--nats-cluster-port 6222
--nats-routes "nats://gohookbridge-server-0.gohookbridge-server-headless.gohookbridge.svc.cluster.local:6222,..."
--nats-cluster-name gohookbridge
--bootstrap-config-file /etc/gohookbridge/bootstrap.yaml
```

---

## 10. Testing

### 10.1 Backend unit tests (new files)

**`gohookbridge/store/raft_discovery_test.go`** (new): `TestDNSResolver_Resolve` (FQDN), `TestDNSResolver_Resolve_NoClusterDomain` (short `.svc`), `TestDNSResolver_Self`, `TestPodOrdinal` (table: valid / empty sts / non-numeric / negative), `TestStaticResolver_MatchSelf`, `TestStaticResolver_SynthesizeSelf`, `TestSingleNodeResolver`, `TestDeriveAdvertiseAddr` (explicit / pod-ordinal / bind fallback), `TestDeriveBindHost`, `TestPodSANs`. (~10 funcs)

**`gohookbridge/store/raft_streamlayer_test.go`** (new): `TestRetryingStreamLayerDial_PlaintextReResolve`, `TestRetryingStreamLayerDial_TLS`, `TestRetryingStreamLayerDial_RetriesOnRefused`, `TestIsConnRefused`, `TestHostAddr_String`, `TestNewHostAddr`. (~6 funcs)

**`gohookbridge/store/raft_test.go`** (modify + add): update existing `bootstrapConfiguration` tests → replace with `TestResolveBootstrapState_OrdinalZeroBootstraps` / `_OthersDoNot`; add `TestReconcileMembership` (add / update / remove, table-driven using a `fakeLeaderRaft`? no — use a real 3-node in-memory cluster helper from `raft_multinode_test.go`), `TestAddrChanged` (IP/IP, DNS/IP, DNS/DNS, unparseable), `TestCleanStateFromStats` (table-driven over `cleanStateFromStats`), `TestClearRaftState`, `TestAdvertiseResolveRetry` (fails-then-succeeds within budget; errors on budget expiry), `TestStepDown_SingleNodeNoop`. (~8 funcs)

**`gohookbridge/store/raft_tls_test.go`** (new): `TestLoadOrBuildRaftTLS_Manual`, `TestLoadOrBuildRaftTLS_LocalSingleNode`, `TestCreateRaftCAWithGoca`, `TestIssueOrReuseNodeCert`, `TestReusableNodeCert`, `TestCoversSANs`, `TestBuildRaftTLSConfig`. (~7 funcs)

**`gohookbridge/store/raft_multinode_test.go`** (new, multi-node integration over loopback TCP): port `raft_test_helpers_test.go` patterns (`raftTestConfig`, `buildClusterNodes` with `plainStreamLayer`/`tlsStreamLayer`, `bootstrapSingle`, `findLeader`, `waitForClusterSettled`). Tests: `TestMultiNode_BootstrapJoinElect`, `TestMultiNode_KillLeaderReelect`, `TestMultiNode_ReconcileAfterAddressChange`, `TestMultiNode_ScaleDownRemovesExtra`, `TestMultiNode_FollowerCleanState`, `TestMultiNode_TLSClusterReplication`. (~6 funcs)

**`gohookbridge/server/server_test.go`** (modify/add): `TestReadyzEndpoint` (clean leader → 200; a fake not-clean store → 503), `TestStartupEndpoint` (voter/leader → 200), `TestLivezEndpoint` (always 200). Use `storetest.NewRaftStore(t)` (single-node becomes leader + clean). (~3 funcs)

### 10.2 Frontend

No frontend code changes (API surface unchanged; leader forwarding + readiness are transparent to the SPA). Note this explicitly.

### 10.3 Helm lint/template assertions

```bash
helm lint ./helm/gohookbridge
helm template gohookbridge ./helm/gohookbridge --namespace gohookbridge --values helm/gohookbridge/values-home.yaml \
  | grep -A1 'raft-advertise-addr\|raft-peers\|raft-replicas\|raft-tls-ca-secret\|nats-routes\|path: /readyz\|path: /startup'
```
Assert: `--raft-bind-addr 0.0.0.0:6001`; advertise/peers use `svc.cluster.local` FQDNs; `/readyz`+`/startup` present; PDB + rbac.yaml render only when enabled.

### 10.4 Manual K8s validation (home cluster, `KUBECONFIG=/home/user/.kube/home`)

1. Rolling restart: `kubectl rollout restart statefulset/gohookbridge-server -n gohookbridge` → all pods Ready, one leader.
2. Scale 3→1→3: `kubectl scale statefulset gohookbridge-server -n gohookbridge --replicas=1` then `--replicas=3` → joinLoop re-adds voters; `kubectl logs` shows `raft membership reconciled added=[...]`.
3. Pod restart with new IP: `kubectl delete pod gohookbridge-server-2 -n gohookbridge` → new IP; cluster self-heals via re-resolution + `addrChanged`.
4. Webhook E2E (full E2E + server-side encryption) per `AGENTS.local.md` §4; UI login available.
5. NATS route re-form after pod restart (verify `gohookbridge/nats` assessment).

---

## 11. Migration / rollout (existing 3-PVC cluster)

**Detection** (before cutover): the existing raft config stores resolved IPs. Confirm with `kubectl exec gohookbridge-server-0 -n gohookbridge -- sh -c 'strings /data/raft/*.db | grep -o "[0-9.]*:6001"'` (or observe "connection refused" between pods after a rolling restart).

**Cutover (one-time, fresh bootstrap — decided):**

```bash
export KUBECONFIG=/home/user/.kube/home
# 1. Scale to 0 to stop the old cluster (prevents split-brain while we reset)
kubectl scale statefulset gohookbridge-server -n gohookbridge --replicas=0
# 2. Delete per-pod PVCs to clear the stale all-peers/IP config
kubectl delete pvc -n gohookbridge -l app.kubernetes.io/component=server
# 3. Re-deploy with the new chart + --raft-recovery-mode set (belt-and-suspenders;
#    clears any residual raft.db/snapshots/node state on first boot)
helm upgrade --install gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge --values helm/gohookbridge/values-home.yaml \
  --set server.raft.recoveryMode=true
# 4. Verify leader election on ordinal 0 and followers joining via joinLoop
kubectl logs -n gohookbridge gohookbridge-server-0 | grep -i 'raft'
# 5. Remove recovery mode for steady-state (idempotent; config now stores DNS names)
helm upgrade --install gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge --values helm/gohookbridge/values-home.yaml \
  --set server.raft.recoveryMode=false
```

- **Fresh bootstrap required?** Yes, once, because stored IPs cannot be rewritten without quorum (§0.1). Config data (channels/users/global) is re-created from `bootstrap.yaml`; webhooks are ephemeral.
- **After migration**, the config stores DNS names; all future pod restarts self-heal via `retryingStreamLayer` re-resolution + `ReconcileMembership`/`addrChanged` — no further recovery-mode needed.
- **Rollback (before deleting PVCs):** keep the old PVCs until the new cluster is verified; if the cutover fails, re-scale the old StatefulSet (old image) back up against the retained PVCs. Deleting PVCs is the point of no return — do it only after step 3 succeeds.

---

## 12. Risks / edge cases / rollback

1. **Ordinal-0 deleted before bootstrap** — DNS discovery always names `-0` as first peer, so `-0` is the bootstrap node. If `-0`'s PVC is lost, `--raft-recovery-mode` on the StatefulSet re-bootstraps a fresh cluster (config re-applied from bootstrap.yaml). If `-0` is merely restarted, its PVC persists and it reloads the existing (DNS-name) config.
2. **Split-brain** — mitigated by: single-node seed (`shouldBootstrap` seeds only self), `ShutdownOnRemove=false` (no self-kill on remove+re-add), PDB `minAvailable: 2`, and `StepDown` on shutdown. A split requires deleting 2 of 3 pods/PVCs simultaneously, which is a documented recovery-mode event.
3. **DNS not ready at first boot** — `resolveAdvertiseAddr` retries 1s for 2min; `publishNotReadyAddresses: true` on the headless Service; FQDN (not short) names to keep the NXDOMAIN cache window ≤5s.
4. **PVC reuse with stale state** — recovery mode clears `raft.db`/`snapshots`; `raft.BootstrapCluster` returns `ErrCantBootstrap` on non-empty config (handled), so a reused-but-valid PVC keeps its membership.
5. **Cluster-domain differences** — `--raft-cluster-domain` (default `cluster.local`; empty = `.svc` short names) is threaded through `podAddress`/`PodSANs`/Helm helpers consistently.
6. **Single-node unaffected** — `singleNodeResolver` + `127.0.0.1:6001` default + `performanceMultiplier` default 1.0 keep local dev identical; TLS optional and off by default.
7. **`--raft-peers` backward compat** — kept as the static resolver; still parsed into `[]RaftPeer`. When `--raft-statefulset-name`/`--raft-headless-service` are set, DNS discovery takes precedence.
8. **mTLS CA rotation / stale leaf** — `issueOrReuseNodeCert` re-issues the leaf when it no longer covers the required SANs or the CA changed (no PVC deletion needed).
9. **cleartext fallback** — if `--raft-tls-enabled=false` on a multi-node deployment, log a `WARN` (password hashes + session secret + encryption keys in cleartext, CWE-319/311).

---

## 13. Docs updates

- **`CONTRIBUTING.md`** — update store package layout (add `raft_discovery.go`, `raft_streamlayer.go`, `raft_tls.go`, `raft_multinode_test.go`); document the new raft flags (table in §1.3) and the multi-node test helper pattern; add the mTLS dependency note.
- **`README.md`** — document new flags; note the readiness/startup endpoints; add a "Raft HA + mTLS" subsection.
- **`quickstart.md`** — update the HA section: `--raft-bind-addr 0.0.0.0:6001` + advertise FQDN + `--raft-replicas`/`--raft-statefulset-name`/`--raft-headless-service`; add the migration runbook (§11) and recovery-mode commands.
- **`design.md`** — update "Raft — Configuration Consensus" (bootstrap saga, advertise/bind separation, membership reconciliation, clean-state barrier, mTLS); update "Configuration Mapping" flag examples; add the NATS DNS-re-resolution rationale (§8).
- **`helm/gohookbridge/values.yaml`** — add inline comments for each new `server.raft.*` and probe key (see §9.1).
- **`SECURITY.md`** — note that multi-node raft now defaults to mTLS and the cleartext warning applies only when `raft-tls-enabled=false`.

---

## 14. Branch / PR strategy

**Recommendation: add commits to `feat/nuxt-migration` (PR #14), not a separate branch.**

Justification:
- The StatefulSet + headless Service + `raftPeers`/`natsRoutes` Helm helpers already exist **only** on `feat/nuxt-migration` (they are absent from `main` `621a36d`). A `fix/raft-ha-saga` branch cut from `main` would lack the entire Helm StatefulSet layer and would need to re-add it, producing a large conflict with PR #14 when both land.
- The raft code (`gohookbridge/store/raft.go`) is untouched by the Nuxt work, so the Go changes apply cleanly on top.
- Sequencing: commit 1 = store raft hardening (discovery + stream layer + bootstrap saga + reconciliation + clean-state + StepDown + tests); commit 2 = raft mTLS (deps + `raft_tls.go` + `server/raft_tls.go` + `k8s.go` + tests); commit 3 = server wiring + probe endpoints + serve() reorder; commit 4 = Helm (values, statefulset, headless, PDB, RBAC, helpers) + docs. Keep each commit green (`make lint`, `go test ./...`, `helm lint`).

---

## Deliverable summary

- **Plan path:** `.opencode/plans/raft-ha-hardening.md`
- **Branch:** `feat/nuxt-migration` (PR #14).
- **Go files to create:** `gohookbridge/store/raft_discovery.go`, `gohookbridge/store/raft_streamlayer.go`, `gohookbridge/store/raft_tls.go`, `gohookbridge/server/raft_tls.go`, `gohookbridge/server/k8s.go`; plus test files `gohookbridge/store/raft_discovery_test.go`, `raft_streamlayer_test.go`, `raft_tls_test.go`, `raft_multinode_test.go`.
- **Go files to modify:** `gohookbridge/store/raft.go`, `gohookbridge/store/bolt.go` (only if snapshot-dir perms/chmod desired — otherwise untouched), `gohookbridge/flags.go`, `gohookbridge/server/server.go`, `gohookbridge/server/command.go` (no change expected), `gohookbridge/store/storetest/helper.go`, `gohookbridge/store/raft_test.go`, `gohookbridge/server/server_test.go`, `go.mod`/`go.sum`.
- **Helm files to create:** `helm/gohookbridge/templates/pdb.yaml`, `helm/gohookbridge/templates/rbac.yaml`.
- **Helm files to modify:** `helm/gohookbridge/values.yaml`, `templates/server-statefulset.yaml`, `templates/_helpers.tpl` (headless service unchanged).
- **Docs to modify:** `CONTRIBUTING.md`, `README.md`, `quickstart.md`, `design.md`, `SECURITY.md`, `values.yaml` comments.
- **New tests:** ~35 unit/integration test functions (4 new `*_test.go` files + 2 modified).
- **Top 5 risks:** (1) stale-IP migration requires one-time fresh bootstrap (data = config only); (2) mTLS CA/Secret bootstrap depends on correct ordinal-0 detection + RBAC; (3) `ShutdownOnRemove`/`NoSnapshotRestoreOnStart` must be flipped or peers self-kill / data is lost after compaction; (4) DNS warmup/cache windows on fresh clusters (mitigated by FQDN + 2min retry + `publishNotReadyAddresses`); (5) multi-node cleartext fallback leaks credentials if TLS misconfigured.
