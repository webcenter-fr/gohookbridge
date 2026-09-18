package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	gohookbridge "github.com/webcenter-fr/gohookbridge/gohookbridge"
	"go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

type RaftStore struct {
	raft      *raft.Raft
	fsm       *FSM
	db        *bbolt.DB
	transport raft.Transport
	resolver  PeerResolver
	nodeID    string
	closeOnce sync.Once
	closeErr  error
}

type RaftConfig struct {
	Dir             string
	NodeID          string
	BindAddr        string
	AdvertiseAddr   string // "" = derive from hostname+headless+port / bind host
	Peers           []string
	BootstrapPath   string
	Replicas        int
	StatefulSetName string
	HeadlessService string
	Namespace       string
	ClusterDomain   string
	RecoveryMode    bool
	// SingleNodeRecovery collapses an existing multi-voter configuration to
	// this node when the StatefulSet is intentionally scaled to one replica.
	// Raft cannot commit the configuration change that removes the missing
	// voters without their quorum, so the surviving node must force the new
	// configuration with RecoverCluster. Set by the server from the
	// StatefulSet's spec.replicas; never active for steady-state replicas > 1.
	SingleNodeRecovery    bool
	NoSnapshotRestore     bool // default false (snapshot restore on restart)
	PerformanceMultiplier float64
	ApplyTimeout          time.Duration
	Resolver              PeerResolver // nil = legacy resolver from Peers/AdvertiseAddr/BindAddr
	TLS                   *tls.Config  // nil = plaintext
}

// withDefaults fills unset config fields with production defaults. NodeID is
// intentionally left empty so resolveBootstrapState can derive it from the
// resolver (e.g. the StatefulSet pod name) before falling back to "node1".
func withDefaults(cfg *RaftConfig) {
	if cfg.Dir == "" {
		cfg.Dir = "./raft-data"
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1:6001"
	}
	if cfg.ApplyTimeout == 0 {
		cfg.ApplyTimeout = 5 * time.Second
	}
	if cfg.PerformanceMultiplier == 0 {
		cfg.PerformanceMultiplier = 1.0
	}
}

// legacyResolver builds a resolver from the legacy Peers/AdvertiseAddr/BindAddr
// fields when no explicit resolver is supplied.
func legacyResolver(cfg *RaftConfig) PeerResolver {
	return NewPeerResolver(&RaftDiscoveryConfig{
		NodeID:        cfg.NodeID,
		AdvertiseAddr: cfg.AdvertiseAddr,
		BindAddr:      cfg.BindAddr,
		Peers:         parsePeers(cfg.Peers),
	})
}

// parsePeers parses legacy "id=addr" peer entries, skipping malformed ones.
func parsePeers(entries []string) []RaftPeer {
	peers := make([]RaftPeer, 0, len(entries))
	for _, e := range entries {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		peers = append(peers, RaftPeer{ID: parts[0], Address: parts[1]})
	}
	return peers
}

// NewRaftStore constructs and starts a Raft node. It resolves this node's
// stable ID and whether it seeds the initial cluster configuration, opens the
// BoltDB log+stable store and file snapshot store, creates the transport
// (plaintext or mTLS), and bootstraps the cluster with ONLY itself when it is
// the bootstrap node. Other nodes start empty and join via the leader's
// ReconcileMembership/AddVoter (StartJoinLoop). The caller must call
// WaitForLeader before issuing writes.
func NewRaftStore(cfg RaftConfig) (*RaftStore, error) {
	withDefaults(&cfg)

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("create raft dir: %w", err)
	}

	resolver := cfg.Resolver
	if resolver == nil {
		resolver = legacyResolver(&cfg)
	}

	nodeID, shouldBootstrap, err := resolveBootstrapState(&cfg, resolver)
	if err != nil {
		return nil, err
	}

	clearStaleRaftState(&cfg, cfg.Dir, nodeID)

	db, err := newBoltDB(cfg.Dir, nodeID)
	if err != nil {
		return nil, fmt.Errorf("create bolt db: %w", err)
	}

	fsm := NewFSM(db)

	logStore := newBoltLogStore(db)
	stableStore := newBoltStableStore(db)

	snapStore, err := raft.NewFileSnapshotStore(cfg.Dir, 2, os.Stderr)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create snapshot store: %w", err)
	}

	transport, advertise, err := newStreamTransport(&cfg, os.Stderr)
	if err != nil {
		db.Close()
		return nil, err
	}

	raftCfg := &raft.Config{
		LocalID:            raft.ServerID(nodeID),
		ProtocolVersion:    raft.ProtocolVersionMax,
		HeartbeatTimeout:   1 * time.Second,
		ElectionTimeout:    1 * time.Second,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		MaxAppendEntries:   64,
		TrailingLogs:       10240,
		SnapshotInterval:   120 * time.Second,
		SnapshotThreshold:  8192,
		LogOutput:          os.Stderr,
		LogLevel:           "WARN",
	}
	// NoSnapshotRestoreOnStart defaults to false so Raft restores from the
	// latest snapshot on restart (required for correct data recovery after
	// log compaction). Only disable it when explicitly configured.
	if cfg.NoSnapshotRestore {
		raftCfg.NoSnapshotRestoreOnStart = true
	}
	ApplyPerformanceMultiplier(raftCfg, cfg.PerformanceMultiplier)

	singleVoterConfig := raftConfigurationFromPeers([]RaftPeer{{ID: nodeID, Address: advertise}})
	recovered := false
	if cfg.SingleNodeRecovery && shouldBootstrap && !cfg.RecoveryMode {
		hasState, stateErr := raft.HasExistingState(logStore, stableStore, snapStore)
		if stateErr != nil {
			_ = closeRaftTransport(transport)
			_ = db.Close()
			return nil, fmt.Errorf("check existing raft state: %w", stateErr)
		}
		if hasState {
			// The StatefulSet is scaled to one replica, so the other voters are
			// gone and their removal can never reach quorum. Force the
			// configuration to this single voter; RecoverCluster replays the
			// existing log into the FSM and snapshots it, so replicated config
			// survives.
			if err := raft.RecoverCluster(raftCfg, fsm, logStore, stableStore, snapStore, transport, singleVoterConfig); err != nil {
				_ = closeRaftTransport(transport)
				_ = db.Close()
				return nil, fmt.Errorf("single-node raft recovery: %w", err)
			}
			fmt.Fprintf(os.Stderr, "WARNING: single-node recovery: forced Raft configuration to %q (StatefulSet scaled to 1 replica)\n", nodeID)
			recovered = true
		}
	}

	if shouldBootstrap && !recovered {
		if err := raft.BootstrapCluster(raftCfg, logStore, stableStore, snapStore, transport, singleVoterConfig); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			_ = closeRaftTransport(transport)
			_ = db.Close()
			return nil, fmt.Errorf("bootstrap raft cluster: %w", err)
		}
	}

	r, err := raft.NewRaft(raftCfg, fsm, logStore, stableStore, snapStore, transport)
	if err != nil {
		_ = closeRaftTransport(transport)
		_ = db.Close()
		return nil, fmt.Errorf("new raft: %w", err)
	}

	return &RaftStore{
		raft:      r,
		fsm:       fsm,
		db:        db,
		transport: transport,
		resolver:  resolver,
		nodeID:    nodeID,
	}, nil
}

// resolveBootstrapState computes this node's stable ID and whether it seeds
// the initial cluster configuration.
//
// shouldBootstrap is true only for the bootstrap node (the first peer in the
// resolved voter list — ordinal 0 for DNS discovery, the first explicit peer
// for static discovery, self for single-node). Other nodes start with no
// config and join via the leader's AddVoter (joinLoop).
//
// The bootstrap node seeds the cluster with ONLY itself as the initial voter
// (single-node quorum). Once it becomes leader, the joinLoop adds the
// remaining peers via AddVoter. Including all peers in the initial
// configuration would require a majority (2 of 3) to elect a leader, but
// non-bootstrap peers have no config and may not be ready to vote when the
// election fires — this creates a deadlock where no leader is ever elected
// (CWE-693).
func resolveBootstrapState(cfg *RaftConfig, resolver PeerResolver) (nodeID string, shouldBootstrap bool, err error) {
	shouldBootstrap = true
	if resolver != nil {
		resolved, err := resolver.Resolve()
		if err != nil {
			return "", false, fmt.Errorf("resolve raft peers: %w", err)
		}
		if self, errSelf := resolver.Self(); errSelf == nil && len(resolved) > 0 {
			shouldBootstrap = self.ID == resolved[0].ID
		}
	}

	nodeID = cfg.NodeID
	if nodeID == "" && resolver != nil {
		if self, err := resolver.Self(); err == nil {
			nodeID = self.ID
		}
	}
	if nodeID == "" {
		nodeID = "node1"
	}
	return nodeID, shouldBootstrap, nil
}

// clearStaleRaftState implements recovery mode: when RecoveryMode is set the
// persisted raft state (BoltDB, snapshots, peers.json) is wiped so the node
// bootstraps a fresh cluster. Use only for manual recovery when quorum is
// lost.
func clearStaleRaftState(cfg *RaftConfig, dir, nodeID string) {
	if !cfg.RecoveryMode {
		return
	}
	if cleared := clearRaftState(dir, nodeID); cleared {
		fmt.Fprintf(os.Stderr, "WARNING: recovery mode: cleared stale raft state in %s\n", dir)
	}
}

// clearRaftState removes the BoltDB, snapshots, and peers.json from the data
// directory. Returns true if any files were removed.
func clearRaftState(dir, nodeID string) bool {
	cleared := false
	for _, p := range []string{
		filepath.Join(dir, nodeID+".db"),
		filepath.Join(dir, "peers.json"),
	} {
		if err := os.Remove(p); err == nil {
			cleared = true
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, "snapshots")); err == nil {
		cleared = true
	}
	return cleared
}

// newStreamTransport binds a transport on cfg.BindAddr. When cfg.TLS != nil it
// wraps the listener in a tlsStreamLayer (mTLS); otherwise it uses a plaintext
// stream layer. Both paths preserve the DNS name in the advertise address via
// a custom raft.StreamLayer: raft.NewTCPTransport type-asserts its advertise
// address to *net.TCPAddr, so a DNS hostAddr would fail. Returns
// (transport, advertiseAddr, error).
func newStreamTransport(cfg *RaftConfig, logOutput io.Writer) (raft.Transport, string, error) {
	host, port, err := net.SplitHostPort(cfg.BindAddr)
	if err != nil {
		return nil, "", fmt.Errorf("parse raft bind_addr %s: %w", cfg.BindAddr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	advertiseAddr := cfg.AdvertiseAddr
	if advertiseAddr == "" {
		advertiseAddr = net.JoinHostPort(host, port)
	}

	// Startup gate: retry-resolution (1s interval, 2min budget) so a fresh
	// cluster's DNS warmup delays this pod instead of CrashLooping it.
	if _, err := resolveAdvertiseAddr(advertiseAddr, defaultAdvertiseResolveTimeout, logOutput); err != nil {
		return nil, "", fmt.Errorf("resolve raft advertise addr %s: %w", advertiseAddr, err)
	}

	advertise, err := newHostAddr(advertiseAddr)
	if err != nil {
		return nil, "", fmt.Errorf("parse advertise addr %s: %w", advertiseAddr, err)
	}

	var layer raft.StreamLayer
	if cfg.TLS != nil {
		l, err := newTLSStreamLayer(cfg.BindAddr, advertise, cfg.TLS)
		if err != nil {
			return nil, "", err
		}
		layer = l
	} else {
		l, err := newPlainStreamLayer(cfg.BindAddr, advertise)
		if err != nil {
			return nil, "", err
		}
		layer = l
	}

	// MaxPool: 1 forces a fresh connection (and thus fresh DNS resolution)
	// per peer.
	stream := newRetryingStreamLayer(layer, 10*time.Second, cfg.TLS)
	transport := raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
		Stream:  stream,
		Logger:  hclog.New(&hclog.LoggerOptions{Output: logOutput, Name: "transport"}),
		MaxPool: 1,
		Timeout: 10 * time.Second,
	})
	return transport, advertise.String(), nil
}

// resolveAdvertiseAddr resolves advertiseAddr, retrying every second within
// the timeout budget so a cluster whose DNS is still warming up delays this
// pod instead of failing it out of the boot sequence (CrashLoopBackOff).
// The last resolution error is returned once the budget expires.
func resolveAdvertiseAddr(advertiseAddr string, timeout time.Duration, logOutput io.Writer) (*net.TCPAddr, error) {
	if timeout <= 0 {
		timeout = defaultAdvertiseResolveTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		advertise, err := net.ResolveTCPAddr("tcp", advertiseAddr)
		if err == nil {
			return advertise, nil
		}
		if !time.Now().Add(advertiseResolveRetryInterval).Before(deadline) {
			return nil, err
		}
		if logOutput != nil {
			_, _ = fmt.Fprintf(logOutput, "[WARN] raft: unable to resolve advertise addr %s yet (cluster DNS may still be starting), retrying: %v\n", advertiseAddr, err)
		}
		time.Sleep(advertiseResolveRetryInterval)
	}
}

// raftConfigurationFromPeers maps the resolved voter list to a raft
// Configuration, skipping empty entries and deduplicating IDs.
func raftConfigurationFromPeers(peers []RaftPeer) raft.Configuration {
	servers := make([]raft.Server, 0, len(peers))
	seen := make(map[raft.ServerID]bool, len(peers))
	for _, p := range peers {
		id := raft.ServerID(p.ID)
		if id == "" || p.Address == "" || seen[id] {
			continue
		}
		seen[id] = true
		servers = append(servers, raft.Server{Suffrage: raft.Voter, ID: id, Address: raft.ServerAddress(p.Address)})
	}
	return raft.Configuration{Servers: servers}
}

const (
	// defaultAdvertiseResolveTimeout bounds startup resolution of the
	// advertise address. A fresh cluster's DNS may not be serving yet (the
	// CoreDNS pods of a brand-new cluster take time to become ready);
	// failing out immediately would push this pod into a CrashLoopBackOff,
	// which delays the whole raft bootstrap sequence.
	defaultAdvertiseResolveTimeout = 2 * time.Minute
	advertiseResolveRetryInterval  = time.Second
)

// leaderPollInterval is how often WaitForLeader / WaitForSelfLeadership
// re-check leadership.
const leaderPollInterval = 50 * time.Millisecond

// WaitForLeader blocks until A leader exists in the cluster (not necessarily
// this node) or ctx expires. Use WaitForSelfLeadership to wait until this
// node is the leader (required before issuing writes).
func (rs *RaftStore) WaitForLeader(ctx context.Context) error {
	return rs.waitForLeaderCondition(ctx, "wait for raft leader", func() bool {
		return rs.raft.Leader() != ""
	})
}

// WaitForSelfLeadership blocks until this node is the leader or ctx expires.
func (rs *RaftStore) WaitForSelfLeadership(ctx context.Context) error {
	return rs.waitForLeaderCondition(ctx, "wait for self leadership", rs.IsLeader)
}

// WaitForCleanState blocks until IsCleanState reports true or ctx expires.
// The server uses it as a startup barrier: the control/data plane must not
// serve until the Raft layer is a settled Leader/Follower with no un-applied
// committed entries and no pending FSM mutations.
func (rs *RaftStore) WaitForCleanState(ctx context.Context) error {
	return rs.waitForLeaderCondition(ctx, "wait for raft clean state", rs.IsCleanState)
}

// waitForLeaderCondition polls ready until it returns true, ctx expires, or the
// raft node shuts down. Raft's LeaderCh only fires when THIS node gains/loses
// leadership, so a leader-exists wait must also poll on a ticker.
func (rs *RaftStore) waitForLeaderCondition(ctx context.Context, label string, ready func() bool) error {
	ticker := time.NewTicker(leaderPollInterval)
	defer ticker.Stop()
	for {
		if ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", label, ctx.Err())
		case _, ok := <-rs.raft.LeaderCh():
			if !ok {
				return fmt.Errorf("%s: %w", label, raft.ErrRaftShutdown)
			}
		case <-ticker.C:
		}
	}
}

// GetConfiguration returns the current raft cluster configuration.
func (rs *RaftStore) GetConfiguration() (raft.Configuration, error) {
	future := rs.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return raft.Configuration{}, fmt.Errorf("get raft configuration: %w", err)
	}
	return future.Configuration(), nil
}

// AddVoter adds a new voter to the running cluster (idempotent: re-adding an
// existing voter is a no-op). Leader-only.
func (rs *RaftStore) AddVoter(id, address string, timeout time.Duration) error {
	if err := rs.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(address), 0, timeout).Error(); err != nil {
		return fmt.Errorf("raft add voter %s: %w", id, err)
	}
	return nil
}

// RemoveServer removes a server from the running cluster. Leader-only.
func (rs *RaftStore) RemoveServer(id string, timeout time.Duration) error {
	if err := rs.raft.RemoveServer(raft.ServerID(id), 0, timeout).Error(); err != nil {
		return fmt.Errorf("raft remove server %s: %w", id, err)
	}
	return nil
}

// ReconcileMembership ensures the cluster configuration matches the desired
// voter list: missing voters are added, stale addresses are updated, and extra
// voters removed (idempotent). Leader-only. Returns the IDs added, updated,
// and removed.
func (rs *RaftStore) ReconcileMembership(desired []RaftPeer, timeout time.Duration) (added, updated, removed []string, err error) {
	current, err := rs.GetConfiguration()
	if err != nil {
		return nil, nil, nil, err
	}
	currentByID := make(map[string]raft.Server, len(current.Servers))
	for _, srv := range current.Servers {
		currentByID[string(srv.ID)] = srv
	}
	desiredByID := make(map[string]RaftPeer, len(desired))
	for _, p := range desired {
		if p.ID != "" {
			desiredByID[p.ID] = p
		}
	}

	// Add missing voters. A voter whose pod does not exist must not be added:
	// adding a voter to a small cluster can commit through the current
	// configuration even when the new peer is unreachable, which raises the
	// quorum requirement and stalls the cluster (e.g. the join loop re-adding
	// the two deleted pods right after a single-replica recovery).
	for id, p := range desiredByID {
		if _, ok := currentByID[id]; !ok {
			if !peerResolvable(p) {
				continue
			}
			if err := rs.AddVoter(id, p.Address, timeout); err != nil {
				return added, updated, removed, err
			}
			added = append(added, id)
		}
	}

	// Update addresses for existing voters whose address changed (e.g. after a
	// pod restart assigned a new IP while the Raft config still stores the old
	// one). The resolver returns DNS names (not IPs) for StatefulSet pods, so
	// the desired address is a stable FQDN. The current address may be a stale
	// IP from a previous bootstrap. Resolve the desired DNS name to compare.
	for id, p := range desiredByID {
		srv, ok := currentByID[id]
		if !ok {
			continue
		}
		if addrChanged(string(srv.Address), p.Address) {
			// Skip a peer that is currently down: removing it would drop the
			// quorum, and its address only needs updating once its pod exists
			// again (the stream layer re-resolves DNS on every dial).
			if !peerResolvable(p) {
				continue
			}
			// Remove and re-add to update the address: raft.AddVoter is a no-op
			// for existing voters with unchanged addresses.
			if err := rs.RemoveServer(id, timeout); err != nil {
				return added, updated, removed, err
			}
			if err := rs.AddVoter(id, p.Address, timeout); err != nil {
				return added, updated, removed, err
			}
			updated = append(updated, id)
		}
	}

	// Remove extra voters.
	for id := range currentByID {
		if _, ok := desiredByID[id]; !ok {
			if err := rs.RemoveServer(id, timeout); err != nil {
				return added, updated, removed, err
			}
			removed = append(removed, id)
		}
	}
	return added, updated, removed, nil
}

// peerResolvable reports whether a desired peer currently exists, i.e. its
// address resolves. StatefulSet peers use pod FQDNs that return NXDOMAIN once
// the pod is deleted; those must never be added to the Raft configuration
// while absent. IP addresses cannot be probed and are always accepted (the
// caller's AddVoter then fails or times out exactly as before).
func peerResolvable(p RaftPeer) bool {
	host, _, err := net.SplitHostPort(p.Address)
	if err != nil {
		return true
	}
	if host == "" || net.ParseIP(host) != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	return err == nil && len(addrs) > 0
}

// addrChanged reports whether two server addresses differ. When the desired
// address is a DNS name (contains non-numeric characters in the host part),
// it is resolved to an IP for comparison with the current address (which may
// be a stale IP from a previous bootstrap). Direct string comparison is used
// when both are IPs or both are DNS names.
func addrChanged(current, desired string) bool {
	if current == desired {
		return false
	}
	host, _, err := net.SplitHostPort(desired)
	if err != nil || net.ParseIP(host) != nil {
		// Desired is an IP (or unparseable) — direct string comparison.
		return true
	}
	// Desired is a DNS name; resolve and compare with the current address.
	resolved, err := net.ResolveTCPAddr("tcp", desired)
	if err != nil {
		// Can't resolve — assume changed (will retry on next reconciliation).
		return true
	}
	return current != resolved.String()
}

// StartJoinLoop periodically reconciles the cluster membership with the
// resolver's desired voter list while this node is the leader. It covers
// scale-up, scale-down, and pod restarts with a new IP (the desired FQDN
// re-resolves to the new IP; addrChanged removes+re-adds).
func (rs *RaftStore) StartJoinLoop(ctx context.Context) {
	if rs.resolver == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !rs.IsLeader() {
				continue
			}
			desired, err := rs.resolver.Resolve()
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: joinLoop: resolve peers failed: %v\n", err)
				continue
			}
			added, updated, removed, err := rs.ReconcileMembership(desired, 10*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: joinLoop: reconcile membership failed: %v\n", err)
				continue
			}
			if len(added)+len(updated)+len(removed) > 0 {
				fmt.Fprintf(os.Stdout, "joinLoop: raft membership reconciled added=%v updated=%v removed=%v\n", added, updated, removed)
			}
		}
	}
}

// followerLastContactThreshold is how long a follower may go without hearing
// from the leader before IsCleanState reports false. Set to 3 × the default
// election timeout (5 s with multiplier 5) to avoid false positives while
// still rejecting followers that are disconnected from the cluster.
const followerLastContactThreshold = 15 * time.Second

// IsCleanState reports whether the Raft consensus layer is in a clean state:
//   - the node is a Leader or Follower (not Candidate or Shutdown)
//   - all committed log entries have been applied (commit_index == applied_index)
//   - there are no pending FSM mutations (fsm_pending == 0)
//   - followers must have recent contact with the leader (last_contact ≤ 15 s);
//     a follower whose local commit_index == applied_index but that has not
//     received heartbeats since restarting is behind the leader's log and its
//     FSM is stale.
func (rs *RaftStore) IsCleanState() bool {
	return cleanStateFromStats(rs.raft.Stats())
}

// cleanStateFromStats is the pure predicate behind IsCleanState, extracted so
// unit tests can drive it table-style without a live node.
func cleanStateFromStats(stats map[string]string) bool {
	state := stats["state"]
	if state != "Leader" && state != "Follower" {
		return false
	}
	if stats["commit_index"] != stats["applied_index"] {
		return false
	}
	if stats["fsm_pending"] != "0" {
		return false
	}
	// A follower whose commit_index is still 0 has not received any data
	// (snapshot or log entries) from the leader yet — the local FSM is
	// empty. The leader may legitimately have commit_index == 0 before
	// writing the first log entry.
	if state == "Follower" && stats["commit_index"] == "0" {
		return false
	}
	if state == "Follower" {
		lastContact := stats["last_contact"]
		if lastContact == "never" || lastContact == "" {
			return false
		}
		d, err := time.ParseDuration(lastContact)
		if err != nil || d > followerLastContactThreshold {
			return false
		}
	}
	return true
}

// IsStarted returns true when the Raft node has joined the cluster (voter or
// leader). Used by the Kubernetes startupProbe (/startup endpoint).
func (rs *RaftStore) IsStarted() bool {
	return rs.IsLeader() || rs.IsVoter()
}

// IsVoter returns true if this node is a voting member of the cluster.
func (rs *RaftStore) IsVoter() bool {
	cfg, err := rs.GetConfiguration()
	if err != nil {
		return false
	}
	for _, srv := range cfg.Servers {
		if string(srv.ID) == rs.nodeID && srv.Suffrage == raft.Voter {
			return true
		}
	}
	return false
}

// StepDown causes the leader to step down to follower status by transferring
// leadership to another voter. On a single-node cluster where no transfer
// target exists, this is a no-op (the node will simply shut down). Used during
// graceful shutdown to allow a clean leadership transfer.
func (rs *RaftStore) StepDown(_ context.Context) error {
	cfg, err := rs.GetConfiguration()
	if err != nil {
		return fmt.Errorf("get configuration: %w", err)
	}
	otherVoters := 0
	for _, srv := range cfg.Servers {
		if srv.Suffrage == raft.Voter && string(srv.ID) != rs.nodeID {
			otherVoters++
		}
	}
	if otherVoters == 0 {
		// Single-node cluster: no one to transfer to, just proceed.
		return nil
	}

	future := rs.raft.LeadershipTransfer()
	if err := future.Error(); err != nil {
		if errors.Is(err, raft.ErrNotLeader) {
			return nil // already not leader
		}
		return fmt.Errorf("step down: %w", err)
	}
	return nil
}

// ApplyPerformanceMultiplier applies the multiplier to a Raft config's
// election/heartbeat/lease timeouts. Values below 1.0 are clamped to 1.0.
func ApplyPerformanceMultiplier(cfg *raft.Config, multiplier float64) {
	if multiplier < 1.0 {
		multiplier = 1.0
	}
	cfg.ElectionTimeout = time.Duration(float64(cfg.ElectionTimeout) * multiplier)
	cfg.HeartbeatTimeout = time.Duration(float64(cfg.HeartbeatTimeout) * multiplier)
	cfg.LeaderLeaseTimeout = time.Duration(float64(cfg.LeaderLeaseTimeout) * multiplier)
}

// closeRaftTransport closes a raft transport when it exposes Close.
func closeRaftTransport(t raft.Transport) error {
	if c, ok := t.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

func (rs *RaftStore) IsLeader() bool {
	return rs.raft.State() == raft.Leader
}

// LeaderAddress returns the Raft transport address of the current cluster
// leader, or an empty string when no leader has been elected yet. The address
// is the one advertised in the Raft configuration (e.g. a pod DNS name).
func (rs *RaftStore) LeaderAddress() string {
	address, _ := rs.raft.LeaderWithID()
	return string(address)
}

func (rs *RaftStore) Apply(cmd []byte) (interface{}, error) {
	if !rs.IsLeader() {
		return nil, fmt.Errorf("not the leader")
	}
	future := rs.raft.Apply(cmd, 10*time.Second)
	if err := future.Error(); err != nil {
		return nil, err
	}
	return future.Response(), nil
}

func (rs *RaftStore) applyCommand(op, key string, value []byte) error {
	cmd := fsmCommand{
		Op:  op,
		Key: key,
	}
	if value != nil {
		cmd.Value = value
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = rs.Apply(data)
	return err
}

func (rs *RaftStore) HasData() (bool, error) {
	keys, err := listFSMKeys(rs.db, "/")
	if err != nil {
		return false, err
	}
	return len(keys) > 0, nil
}

// Close shuts the raft node and closes the transport and BoltDB. Idempotent.
func (rs *RaftStore) Close() error {
	rs.closeOnce.Do(func() {
		if rs.raft != nil {
			if err := rs.raft.Shutdown().Error(); err != nil && !errors.Is(err, raft.ErrRaftShutdown) {
				rs.closeErr = err
			}
		}
		if rs.transport != nil {
			_ = closeRaftTransport(rs.transport)
		}
		if rs.db != nil {
			_ = rs.db.Close()
		}
	})
	return rs.closeErr
}

// Shutdown is the final teardown alias for Close (idempotent).
func (rs *RaftStore) Shutdown() error {
	return rs.Close()
}

func (rs *RaftStore) GetChannel(id string) (*Channel, error) {
	if id == "" {
		return nil, fmt.Errorf("channel ID required")
	}
	val, err := getFSMValue(rs.db, "/channels/"+id+"/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("channel %q not found", id)
	}
	var ch Channel
	if err := json.Unmarshal(val, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

func (rs *RaftStore) ListChannels() ([]*Channel, error) {
	keys, err := listFSMKeys(rs.db, "/channels/")
	if err != nil {
		return nil, err
	}
	var channels []*Channel
	seen := make(map[string]bool)
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/channels/"), "/")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		id := parts[0]
		if seen[id] {
			continue
		}
		seen[id] = true
		val, err := getFSMValue(rs.db, "/channels/"+id+"/")
		if err != nil || val == nil {
			continue
		}
		var ch Channel
		if err := json.Unmarshal(val, &ch); err != nil {
			continue
		}
		migrateChannel(&ch)
		channels = append(channels, &ch)
	}
	return channels, nil
}

func (rs *RaftStore) CreateChannel(p *Channel) error {
	if p.ID == "" {
		return fmt.Errorf("channel ID required")
	}
	val, err := json.Marshal(p)
	if err != nil {
		return err
	}
	cmd := fsmCommand{
		Op:    "create-channel",
		Key:   "/channels/" + p.ID + "/",
		Value: val,
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	resp, err := rs.Apply(data)
	if err != nil {
		return err
	}
	if resp != nil {
		return fmt.Errorf("%v", resp)
	}
	return nil
}

func (rs *RaftStore) UpdateChannel(p *Channel) error {
	if p.ID == "" {
		return fmt.Errorf("channel ID required")
	}
	val, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/channels/"+p.ID+"/", val)
}

func (rs *RaftStore) DeleteChannel(id string) error {
	return rs.applyCommand("delete", "/channels/"+id+"/", nil)
}

func (rs *RaftStore) GetGlobalConfig() (*GlobalConfig, error) {
	cfg := defaultGlobalConfig()

	val, err := getFSMValue(rs.db, "/global/server/")
	if err != nil {
		return nil, err
	}
	if val != nil {
		var sc ServerConfig
		if err := json.Unmarshal(val, &sc); err == nil {
			cfg.Server = sc
		}
	}

	val, err = getFSMValue(rs.db, "/global/defaults/")
	if err != nil {
		return nil, err
	}
	if val != nil {
		var dc DefaultChannelConfig
		if err := json.Unmarshal(val, &dc); err == nil {
			cfg.Defaults = dc
		}
	}

	return cfg, nil
}

func (rs *RaftStore) UpdateGlobalConfig(cfg *GlobalConfig) error {
	if cfg == nil {
		return fmt.Errorf("config required")
	}
	//nolint:gosec
	scVal, err := json.Marshal(cfg.Server)
	if err != nil {
		return err
	}
	if err := rs.applyCommand("set", "/global/server/", scVal); err != nil {
		return err
	}
	dcVal, err := json.Marshal(cfg.Defaults)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/global/defaults/", dcVal)
}

func (rs *RaftStore) ResolveChannelConfig(id string) (*Channel, error) {
	ch, err := rs.GetChannel(id)
	if err != nil {
		global, globalErr := rs.GetGlobalConfig()
		if globalErr != nil {
			global = defaultGlobalConfig()
		}
		return &Channel{
			ID:                id,
			MaxBodySize:       global.Server.MaxBodySize,
			WebhookSecret:     global.Defaults.WebhookSecret,
			AllowedIPs:        global.Defaults.AllowedIPs,
			MessageTTLSeconds: global.Defaults.MessageTTLSeconds,
		}, nil
	}
	global, globalErr := rs.GetGlobalConfig()
	if globalErr != nil {
		global = defaultGlobalConfig()
	}
	return resolveChannelConfig(ch, global), nil
}

func (rs *RaftStore) GetUser(id string) (*User, error) {
	val, err := getFSMValue(rs.db, "/users/"+id+"/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("user %q not found", id)
	}
	var u User
	if err := json.Unmarshal(val, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

type usernameIndex struct {
	UserID string `json:"user_id"`
}

func usernameIndexKey(username string) string {
	return "/users/by-username/" + username
}

func usernameIndexValue(userID string) []byte {
	idx := usernameIndex{UserID: userID}
	val, err := json.Marshal(idx)
	if err != nil {
		return nil
	}
	return val
}

func (rs *RaftStore) GetUserByUsername(username string) (*User, error) {
	idxVal, err := getFSMValue(rs.db, "/users/by-username/"+username+"/")
	if err != nil || idxVal == nil {
		return nil, fmt.Errorf("user %q not found", username)
	}
	var idx usernameIndex
	if err := json.Unmarshal(idxVal, &idx); err != nil {
		return nil, fmt.Errorf("user %q not found", username)
	}
	return rs.GetUser(idx.UserID)
}

func (rs *RaftStore) ListUsers() ([]*User, error) {
	keys, err := listFSMKeys(rs.db, "/users/")
	if err != nil {
		return nil, err
	}
	var users []*User
	seen := make(map[string]bool)
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/users/"), "/")
		if len(parts) < 1 || parts[0] == "" || parts[0] == "by-username" {
			continue
		}
		id := parts[0]
		if seen[id] {
			continue
		}
		seen[id] = true
		val, err := getFSMValue(rs.db, "/users/"+id+"/")
		if err != nil || val == nil {
			continue
		}
		var u User
		if err := json.Unmarshal(val, &u); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

func (rs *RaftStore) CreateUser(u *User) error {
	if u.ID == "" {
		u.ID = u.Username
	}
	if u.ID == "" {
		return fmt.Errorf("user ID required")
	}
	val, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if err := rs.applyCommand("set", "/users/"+u.ID+"/", val); err != nil {
		return err
	}
	return rs.applyCommand("set", usernameIndexKey(u.Username)+"/", usernameIndexValue(u.ID))
}

func (rs *RaftStore) UpdateUser(u *User) error {
	old, err := rs.GetUser(u.ID)
	oldUsername := ""
	if err == nil && old.Username != u.Username && old.Username != "" {
		oldUsername = old.Username
	}
	if err := rs.CreateUser(u); err != nil {
		return err
	}
	if oldUsername != "" {
		return rs.applyCommand("delete", "/users/by-username/"+oldUsername+"/", nil)
	}
	return nil
}

func (rs *RaftStore) DeleteUser(id string) error {
	u, err := rs.GetUser(id)
	if err != nil {
		return err
	}
	if err := rs.applyCommand("delete", "/users/"+id+"/", nil); err != nil {
		return err
	}
	return rs.applyCommand("delete", "/users/by-username/"+u.Username+"/", nil)
}

func (rs *RaftStore) GetSetupModeEndTime() time.Time {
	val, err := getFSMValue(rs.db, "/meta/setup_end")
	if err != nil || val == nil {
		return time.Time{}
	}
	var t time.Time
	if err := json.Unmarshal(val, &t); err != nil {
		return time.Time{}
	}
	return t
}

func (rs *RaftStore) SetSetupModeEndTime(t time.Time) error {
	val, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/meta/setup_end", val)
}

func (rs *RaftStore) OIDCProviders() ([]OIDCProvider, error) {
	val, err := getFSMValue(rs.db, "/global/auth/oidc_providers")
	if err != nil || val == nil {
		return nil, err
	}
	var providers []OIDCProvider
	if err := json.Unmarshal(val, &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

func (rs *RaftStore) SetOIDCProviders(providers []OIDCProvider) error {
	//nolint:gosec
	val, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	return rs.applyCommand("set-json", "/global/auth/oidc_providers", val)
}

func (rs *RaftStore) GetRole(name string) (*Role, error) {
	val, err := getFSMValue(rs.db, "/rbac/roles/"+name+"/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		for _, r := range DefaultRoles {
			if r.Name == name {
				return &r, nil
			}
		}
		return nil, fmt.Errorf("role %q not found", name)
	}
	var r Role
	if err := json.Unmarshal(val, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (rs *RaftStore) ListRoles() ([]Role, error) {
	roles := make([]Role, 0, len(DefaultRoles))
	roles = append(roles, DefaultRoles...)

	keys, err := listFSMKeys(rs.db, "/rbac/roles/")
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/rbac/roles/"), "/")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		name := parts[0]
		if isDefaultRole(name) {
			continue
		}
		val, err := getFSMValue(rs.db, key)
		if err != nil || val == nil {
			continue
		}
		var r Role
		if err := json.Unmarshal(val, &r); err != nil {
			continue
		}
		roles = append(roles, r)
	}
	return roles, nil
}

func (rs *RaftStore) CreateRole(r Role) error {
	if isDefaultRole(r.Name) {
		return fmt.Errorf("role %q already exists (default role)", r.Name)
	}
	val, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/rbac/roles/"+r.Name+"/", val)
}

func isDefaultRole(name string) bool {
	for _, r := range DefaultRoles {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (rs *RaftStore) GetUserBinding(userID string) (*UserBinding, error) {
	u, err := rs.GetUser(userID)
	if err != nil {
		return nil, err
	}
	return &UserBinding{
		UserID:   u.ID,
		Roles:    u.Roles,
		Channels: u.Channels,
	}, nil
}

func (rs *RaftStore) UpdateUserBinding(binding *UserBinding) error {
	u, err := rs.GetUser(binding.UserID)
	if err != nil {
		return err
	}
	u.Roles = binding.Roles
	u.Channels = binding.Channels
	return rs.UpdateUser(u)
}

func (rs *RaftStore) ListBindings() ([]UserBinding, error) {
	users, err := rs.ListUsers()
	if err != nil {
		return nil, err
	}
	bindings := make([]UserBinding, 0, len(users))
	for _, u := range users {
		bindings = append(bindings, UserBinding{
			UserID:   u.ID,
			Roles:    u.Roles,
			Channels: u.Channels,
		})
	}
	return bindings, nil
}

func (rs *RaftStore) IsSetupMode() bool {
	users, err := rs.ListUsers()
	if err != nil || len(users) == 0 {
		return true
	}
	return false
}

func (rs *RaftStore) CreateDevAdmin(password string) error {
	if !rs.IsSetupMode() {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	user := &User{
		ID:           "admin",
		Username:     "admin",
		PasswordHash: string(hash),
		Roles:        []string{"admin"},
		Channels:     []string{"*"},
	}
	return rs.CreateUser(user)
}

func (rs *RaftStore) ResolveChannelWebhookSecret(channelID string) (string, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, globalErr := rs.GetGlobalConfig()
		if globalErr != nil {
			global = defaultGlobalConfig()
		}
		return global.Defaults.WebhookSecret, nil
	}
	migrateChannel(p)
	if p.WebhookSecret != "" {
		return p.WebhookSecret, nil
	}
	global, globalErr := rs.GetGlobalConfig()
	if globalErr != nil {
		global = defaultGlobalConfig()
	}
	return global.Defaults.WebhookSecret, nil
}

func (rs *RaftStore) ResolveChannelAllowedIPs(channelID string) ([]string, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, globalErr := rs.GetGlobalConfig()
		if globalErr != nil {
			global = defaultGlobalConfig()
		}
		return global.Defaults.AllowedIPs, nil
	}
	if len(p.AllowedIPs) > 0 {
		return p.AllowedIPs, nil
	}
	global, globalErr := rs.GetGlobalConfig()
	if globalErr != nil {
		global = defaultGlobalConfig()
	}
	return global.Defaults.AllowedIPs, nil
}

func (rs *RaftStore) ResolveChannelMaxBodySize(channelID string) (int, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, globalErr := rs.GetGlobalConfig()
		if globalErr != nil {
			global = defaultGlobalConfig()
		}
		return global.Server.MaxBodySize, nil
	}
	if p.MaxBodySize > 0 {
		return p.MaxBodySize, nil
	}
	global, globalErr := rs.GetGlobalConfig()
	if globalErr != nil {
		global = defaultGlobalConfig()
	}
	return global.Server.MaxBodySize, nil
}

func (rs *RaftStore) ResolveChannelEncryption(channelID string) (string, string, string, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		return "", "", "", err
	}
	migrateChannel(p)
	return p.EncryptionMode, p.EncryptionKey, p.EncryptionPublicKey, nil
}

func (rs *RaftStore) SessionSecret() string {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return ""
	}
	if global.Server.SessionSecret != "" {
		return global.Server.SessionSecret
	}
	return ""
}

func (rs *RaftStore) SetSessionSecret(secret string) error {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		global = defaultGlobalConfig()
	}
	global.Server.SessionSecret = secret
	return rs.UpdateGlobalConfig(global)
}

func (rs *RaftStore) ResolveCORSOrigin() string {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return "*"
	}
	return global.Server.CORSOrigin
}

func (rs *RaftStore) ResolveBehindReverseProxy() bool {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return false
	}
	return global.Server.BehindReverseProxy
}

func (rs *RaftStore) ResolveFooter() string {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return ""
	}
	return global.Server.Footer
}

func (rs *RaftStore) GetClientCursor(channel, clientID string) (*ClientCursor, error) {
	key := "/cursors/" + channel + "/" + clientID + "/"
	val, err := getFSMValue(rs.db, key)
	if err != nil || val == nil {
		return nil, err
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

func (rs *RaftStore) CreateRoleMapping(m *RoleMapping) error {
	// Check for existing identical mapping to make this idempotent
	existing, _ := rs.ListRoleMappings()
	for _, e := range existing {
		if e.Type == m.Type && e.Subject == m.Subject && e.Role == m.Role && e.ChannelScope == m.ChannelScope {
			return nil // already exists, idempotent
		}
	}
	m.ID = gohookbridge.GenerateUUID()
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/rbac/mappings/"+m.ID+"/", val)
}

func (rs *RaftStore) ListRoleMappings() ([]RoleMapping, error) {
	keys, err := listFSMKeys(rs.db, "/rbac/mappings/")
	if err != nil {
		return nil, err
	}
	mappings := make([]RoleMapping, 0)
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/rbac/mappings/"), "/")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		val, err := getFSMValue(rs.db, key)
		if err != nil || val == nil {
			continue
		}
		var m RoleMapping
		if err := json.Unmarshal(val, &m); err != nil {
			continue
		}
		mappings = append(mappings, m)
	}
	return mappings, nil
}

func (rs *RaftStore) DeleteRoleMapping(id string) error {
	return rs.applyCommand("delete", "/rbac/mappings/"+id+"/", nil)
}

func (rs *RaftStore) GetUserRoleMappings(userID string) ([]RoleMapping, error) {
	all, err := rs.ListRoleMappings()
	if err != nil {
		return nil, err
	}
	var result []RoleMapping
	for _, m := range all {
		if m.Type == "user" && m.Subject == userID {
			result = append(result, m)
		}
	}
	return result, nil
}

func (rs *RaftStore) GetGroupRoleMappings(groupName string) ([]RoleMapping, error) {
	all, err := rs.ListRoleMappings()
	if err != nil {
		return nil, err
	}
	var result []RoleMapping
	for _, m := range all {
		if m.Type == "group" && m.Subject == groupName {
			result = append(result, m)
		}
	}
	return result, nil
}

func (rs *RaftStore) CreateChannelRoleMapping(m *ChannelRoleMapping) error {
	// Check for existing identical mapping to make this idempotent
	existing, _ := rs.ListChannelRoleMappings(m.ChannelID)
	for _, e := range existing {
		if e.Type == m.Type && e.Subject == m.Subject && e.Role == m.Role {
			return nil // already exists, idempotent
		}
	}
	m.ID = gohookbridge.GenerateUUID()
	if m.ChannelID == "" {
		return fmt.Errorf("channel_id required")
	}
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/channels/"+m.ChannelID+"/acl/"+m.ID+"/", val)
}

func (rs *RaftStore) ListChannelRoleMappings(channelID string) ([]ChannelRoleMapping, error) {
	keys, err := listFSMKeys(rs.db, "/channels/"+channelID+"/acl/")
	if err != nil {
		return nil, err
	}
	var mappings []ChannelRoleMapping
	for _, key := range keys {
		val, err := getFSMValue(rs.db, key)
		if err != nil || val == nil {
			continue
		}
		var m ChannelRoleMapping
		if err := json.Unmarshal(val, &m); err != nil {
			continue
		}
		mappings = append(mappings, m)
	}
	if mappings == nil {
		mappings = make([]ChannelRoleMapping, 0)
	}
	return mappings, nil
}

func (rs *RaftStore) DeleteChannelRoleMapping(channelID, entryID string) error {
	return rs.applyCommand("delete", "/channels/"+channelID+"/acl/"+entryID+"/", nil)
}

func (rs *RaftStore) GetUserChannelRoleMappings(userID string) ([]ChannelRoleMapping, error) {
	channels, err := rs.ListChannels()
	if err != nil {
		return nil, err
	}
	var result []ChannelRoleMapping
	for _, ch := range channels {
		acls, err := rs.ListChannelRoleMappings(ch.ID)
		if err != nil {
			continue
		}
		for _, a := range acls {
			if a.Type == "user" && a.Subject == userID {
				result = append(result, a)
			}
		}
	}
	return result, nil
}

func (rs *RaftStore) GetGroupChannelRoleMappings(groupName string) ([]ChannelRoleMapping, error) {
	channels, err := rs.ListChannels()
	if err != nil {
		return nil, err
	}
	var result []ChannelRoleMapping
	for _, ch := range channels {
		acls, err := rs.ListChannelRoleMappings(ch.ID)
		if err != nil {
			continue
		}
		for _, a := range acls {
			if a.Type == "group" && a.Subject == groupName {
				result = append(result, a)
			}
		}
	}
	return result, nil
}
