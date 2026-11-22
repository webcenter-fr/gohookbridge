package repository

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/disaster37/go-raft/discovery"
	"github.com/disaster37/go-raft/pki"
	"github.com/disaster37/go-raft/testutil"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

// raftClusterNode is one node in a test raft cluster.
type raftClusterNode struct {
	store  *RaftStore
	addr   string
	tlsDir string
}

// buildPlainClusterNodes builds n plaintext raft nodes over loopback TCP. The
// nodes are NOT bootstrapped here: node-0 bootstraps itself during Start and
// the leader's join loop adds the rest.
//
//nolint:unparam // n is kept generic for future cluster-size variants.
func buildPlainClusterNodes(t *testing.T, n int) []raftClusterNode {
	t.Helper()
	return buildClusterNodes(t, n, false)
}

// buildTLSClusterNodes builds n mTLS raft nodes over loopback TCP sharing a
// goca CA written to each node's TLS dir.
func buildTLSClusterNodes(t *testing.T, n int) []raftClusterNode {
	t.Helper()
	return buildClusterNodes(t, n, true)
}

func buildClusterNodes(t *testing.T, n int, tlsEnabled bool) []raftClusterNode {
	t.Helper()
	addrs := make([]string, n)
	listeners := make([]net.Listener, n)
	for i := 0; i < n; i++ {
		listeners[i] = testutil.FreeListener(t)
		addrs[i] = testutil.FreeAddrFromListener(t, listeners[i])
	}
	peers := make([]discovery.Peer, n)
	for i := 0; i < n; i++ {
		peers[i] = discovery.Peer{ID: fmt.Sprintf("node-%d", i), Address: addrs[i]}
	}

	var ca *pki.MintingCA
	if tlsEnabled {
		var err error
		ca, err = pki.NewMintingCA("test-raft-ca", "gohookbridge-raft", time.Hour)
		assert.NilError(t, err)
	}

	nodes := make([]raftClusterNode, n)
	for i := 0; i < n; i++ {
		cn := fmt.Sprintf("node-%d", i)
		dir := t.TempDir()
		cfg := RaftConfig{
			Dir:                   dir,
			NodeID:                cn,
			BindAddr:              addrs[i],
			AdvertiseAddr:         addrs[i],
			Peers:                 legacyPeerStrings(peers),
			Replicas:              n,
			PerformanceMultiplier: 0.2,
			ApplyTimeout:          5 * time.Second,
			CustomListener:        listeners[i],
			Resolver: discovery.NewPeerResolver(&discovery.Config{
				NodeID:        cn,
				AdvertiseAddr: addrs[i],
				Peers:         peers,
				Replicas:      n,
			}),
			AutopilotEnabled: boolPtr(false),
		}
		tlsDir := ""
		if tlsEnabled {
			tlsDir = filepath.Join(dir, "tls")
			writeClusterTLS(t, ca, tlsDir, cn)
			cfg.TLSEnabled = true
			cfg.TLSDir = tlsDir
			cfg.TLSCACertPath = filepath.Join(tlsDir, "ca.crt")
			cfg.TLSCertPath = filepath.Join(tlsDir, "tls.crt")
			cfg.TLSKeyPath = filepath.Join(tlsDir, "tls.key")
			cfg.TLSReloadPollInterval = 25 * time.Millisecond
		}
		rs, err := NewRaftStore(cfg)
		assert.NilError(t, err)
		t.Cleanup(func() { _ = rs.Shutdown() })
		nodes[i] = raftClusterNode{store: rs, addr: addrs[i], tlsDir: tlsDir}
	}
	return nodes
}

func boolPtr(b bool) *bool { return &b }

func legacyPeerStrings(peers []discovery.Peer) []string {
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		out = append(out, p.ID+"="+p.Address)
	}
	return out
}

// writeClusterTLS mints a CA + leaf and writes ca.crt/tls.crt/tls.key into dir.
func writeClusterTLS(t *testing.T, ca *pki.MintingCA, dir, commonName string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(dir, 0o700))
	caCert, caKey, err := ca.EncodePEM()
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "ca.crt"), caCert, 0o600))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "ca.key"), caKey, 0o600))

	leafCert, leafKey, err := ca.IssuePeerCertificate(commonName, "gohookbridge-raft", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "tls.crt"), leafCert, 0o600))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "tls.key"), leafKey, 0o600))
}

// clusterStores extracts the RaftStore handles from the cluster nodes.
func clusterStores(nodes []raftClusterNode) []*RaftStore {
	stores := make([]*RaftStore, len(nodes))
	for i := range nodes {
		stores[i] = nodes[i].store
	}
	return stores
}

// findLeader returns the current leader store, failing if none is elected.
func findLeader(t *testing.T, stores []*RaftStore) *RaftStore {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range stores {
			if s.IsLeader() {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader elected")
	return nil
}

// waitForClusterSettled blocks until every node's latest configuration
// contains the full voter set (len(cfg.Servers) == n).
func waitForClusterSettled(t *testing.T, stores []*RaftStore, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		settled := true
		for _, s := range stores {
			cfg, err := s.GetConfiguration()
			if err != nil || len(cfg.Servers) != n {
				settled = false
				break
			}
		}
		if settled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cluster configuration never replicated to all nodes")
}

// joinAll waits for the leader's join loop to add every node and for the
// configuration to replicate.
func joinAll(t *testing.T, nodes []raftClusterNode) *RaftStore {
	t.Helper()
	leader := findLeader(t, clusterStores(nodes))
	waitForClusterSettled(t, clusterStores(nodes), len(nodes), 30*time.Second)
	return leader
}

func TestMultiNode_BootstrapJoinElect(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	leader := joinAll(t, nodes)
	assert.Assert(t, leader.IsLeader())

	// A write on the leader commits against the full quorum and replicates.
	assert.NilError(t, leader.CreateChannel(context.Background(), &domain.Channel{ID: "joined"}))
	for i := 0; i < len(nodes); i++ {
		if nodes[i].store == leader {
			continue
		}
		waitForChannel(t, nodes[i].store, "joined")
	}
}

// waitForChannel polls a node's local FSM until the channel appears.
func waitForChannel(t *testing.T, s *RaftStore, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.GetChannel(context.Background(), id); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("channel %q never replicated", id)
}

func TestMultiNode_KillLeaderReelect(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	// Kill the leader: a new one must be elected and writes must resume.
	assert.NilError(t, leader.Shutdown())

	newLeader := findLeader(t, clusterStores(nodes))
	assert.Assert(t, newLeader != leader, "expected a different leader after failover")
	assert.NilError(t, newLeader.CreateChannel(context.Background(), &domain.Channel{ID: "failover"}))
}

func TestMultiNode_ReconcileAfterAddressChange(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	desired := []RaftPeer{
		{ID: "node-0", Address: nodes[0].addr},
		{ID: "node-1", Address: nodes[1].addr},
		{ID: "node-2", Address: "127.0.0.1:19999"},
	}
	added, updated, removed, err := leader.ReconcileMembership(desired, 5*time.Second)
	assert.NilError(t, err)
	assert.Equal(t, len(added), 0)
	assert.DeepEqual(t, updated, []string{"node-2"})
	assert.Equal(t, len(removed), 0)
}

func TestMultiNode_ScaleDownRemovesExtra(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	desired := []RaftPeer{{ID: "node-0", Address: nodes[0].addr}}
	added, updated, removed, err := leader.ReconcileMembership(desired, 5*time.Second)
	assert.NilError(t, err)
	assert.Equal(t, len(added), 0)
	assert.Equal(t, len(updated), 0)
	assert.Equal(t, len(removed), 2)
}

func TestMultiNode_TLSClusterReplication(t *testing.T) {
	nodes := buildTLSClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	assert.NilError(t, leader.CreateChannel(context.Background(), &domain.Channel{ID: "tls-joined"}))
	for i := 0; i < len(nodes); i++ {
		if nodes[i].store == leader {
			continue
		}
		waitForChannel(t, nodes[i].store, "tls-joined")
	}
}

func TestMultiNode_FollowerCleanState(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range clusterStores(nodes) {
		if s == leader {
			continue
		}
		assert.NilError(t, s.WaitForCleanState(ctx))
	}
}

// TestMultiNode_ManualCertHotReloadAndKeyringDisabled proves that multi-node
// manual certs disable the in-band keyring and that a replaced leaf on disk is
// hot-reloaded and used for subsequent replication.
func TestMultiNode_ManualCertHotReloadAndKeyringDisabled(t *testing.T) {
	nodes := buildTLSClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	for _, n := range nodes {
		assert.Assert(t, n.store.node.KeyringManager().CurrentKeyring() == nil,
			"manual certs must not enable the in-band keyring")
	}

	// Overwrite node-0's leaf with a freshly minted one signed by the cluster
	// CA and wait for the FileWatcher to hot-swap it.
	target := nodes[0]
	ca, err := pki.NewMintingCAFromPEM(readFile(t, filepath.Join(target.tlsDir, "ca.crt")), readFile(t, filepath.Join(target.tlsDir, "ca.key")), time.Hour)
	assert.NilError(t, err)
	leafCert, leafKey, err := ca.IssuePeerCertificate("node-0", "gohookbridge-raft", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(filepath.Join(target.tlsDir, "tls.crt"), leafCert, 0o600))
	assert.NilError(t, os.WriteFile(filepath.Join(target.tlsDir, "tls.key"), leafKey, 0o600))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cert, err := target.store.TLSProvider().GetCertificate(nil); err == nil && cert != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.NilError(t, leader.CreateChannel(context.Background(), &domain.Channel{ID: "post-reload"}))
	for i := 0; i < len(nodes); i++ {
		if nodes[i].store == leader {
			continue
		}
		waitForChannel(t, nodes[i].store, "post-reload")
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	return data
}
