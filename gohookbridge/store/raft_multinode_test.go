package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"go.etcd.io/bbolt"
	"gotest.tools/v3/assert"
)

// raftClusterNode is one node in a test raft cluster.
type raftClusterNode struct {
	store *RaftStore
	r     *raft.Raft
	trans raft.Transport
	layer raft.StreamLayer
	addr  raft.ServerAddress
	db    *bbolt.DB
}

// raftTestConfig returns a fast election-timing raft config for test clusters.
// Timings are relaxed enough to stay deterministic under -race and CI CPU
// contention.
func raftTestConfig(id string) *raft.Config {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)
	config.TrailingLogs = 256
	config.SnapshotInterval = 10 * time.Minute
	config.SnapshotThreshold = 1000
	config.LogOutput = nil
	config.LogLevel = "WARN"
	config.HeartbeatTimeout = time.Second
	config.ElectionTimeout = time.Second
	config.LeaderLeaseTimeout = 500 * time.Millisecond
	config.CommitTimeout = 100 * time.Millisecond
	return config
}

// buildPlainClusterNodes builds n plaintext raft nodes over loopback TCP. The
// nodes are NOT bootstrapped (callers choose the configuration).
//
//nolint:unparam // n is kept generic for future cluster-size variants.
func buildPlainClusterNodes(t *testing.T, n int) []raftClusterNode {
	t.Helper()
	nodes := make([]raftClusterNode, n)
	for i := 0; i < n; i++ {
		layer, err := newPlainStreamLayer("127.0.0.1:0", nil)
		assert.NilError(t, err)
		trans := raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
			Stream:  layer,
			Logger:  hclog.NewNullLogger(),
			MaxPool: 10,
			Timeout: time.Second,
		})
		nodes[i] = raftClusterNode{layer: layer, trans: trans, addr: raft.ServerAddress(layer.Addr().String())}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("node-%d", i)
		db, err := newBoltDB(t.TempDir(), id)
		assert.NilError(t, err)
		fsm := NewFSM(db)
		r, err := raft.NewRaft(raftTestConfig(id), fsm, newBoltLogStore(db), newBoltStableStore(db), raft.NewInmemSnapshotStore(), nodes[i].trans)
		assert.NilError(t, err)
		nodes[i].store = &RaftStore{raft: r, fsm: fsm, db: db, transport: nodes[i].trans, nodeID: id}
		nodes[i].r = r
		nodes[i].db = db
	}
	t.Cleanup(func() {
		for i := range nodes {
			_ = nodes[i].r.Shutdown().Error()
			_ = closeRaftTransport(nodes[i].trans)
			_ = nodes[i].db.Close()
		}
	})
	return nodes
}

// buildTLSClusterNodes builds n mTLS raft nodes over loopback TCP sharing a
// goca CA. The nodes are NOT bootstrapped (callers choose the configuration).
func buildTLSClusterNodes(t *testing.T, n int) []raftClusterNode {
	t.Helper()
	caCertPEM, caKeyPEM, err := createRaftCAWithGoca("test-raft-ca", "gohookbridge-raft")
	assert.NilError(t, err)
	ca, err := NewMintingCAFromPEM(caCertPEM, caKeyPEM, time.Hour)
	assert.NilError(t, err)
	pool := x509.NewCertPool()
	assert.Assert(t, pool.AppendCertsFromPEM(caCertPEM))

	nodes := make([]raftClusterNode, n)
	for i := 0; i < n; i++ {
		cn := fmt.Sprintf("node-%d", i)
		leafCert, leafKey, err := ca.IssuePeerCertificate(cn, "gohookbridge-raft", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
		assert.NilError(t, err)
		leaf, err := tls.X509KeyPair(leafCert, leafKey)
		assert.NilError(t, err)
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{leaf},
			RootCAs:      pool,
			ClientCAs:    pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		}
		layer, err := newTLSStreamLayer("127.0.0.1:0", nil, tlsCfg)
		assert.NilError(t, err)
		trans := raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
			Stream:  layer,
			Logger:  hclog.NewNullLogger(),
			MaxPool: 10,
			Timeout: time.Second,
		})
		nodes[i] = raftClusterNode{layer: layer, trans: trans, addr: raft.ServerAddress(layer.Addr().String())}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("node-%d", i)
		db, err := newBoltDB(t.TempDir(), id)
		assert.NilError(t, err)
		fsm := NewFSM(db)
		r, err := raft.NewRaft(raftTestConfig(id), fsm, newBoltLogStore(db), newBoltStableStore(db), raft.NewInmemSnapshotStore(), nodes[i].trans)
		assert.NilError(t, err)
		nodes[i].store = &RaftStore{raft: r, fsm: fsm, db: db, transport: nodes[i].trans, nodeID: id}
		nodes[i].r = r
		nodes[i].db = db
	}
	t.Cleanup(func() {
		for i := range nodes {
			_ = nodes[i].r.Shutdown().Error()
			_ = closeRaftTransport(nodes[i].trans)
			_ = nodes[i].db.Close()
		}
	})
	return nodes
}

// bootstrapSingle seeds node-0 with itself as the only voter.
func bootstrapSingle(t *testing.T, nodes []raftClusterNode) {
	t.Helper()
	servers := []raft.Server{{
		Suffrage: raft.Voter,
		ID:       raft.ServerID("node-0"),
		Address:  nodes[0].addr,
	}}
	assert.NilError(t, nodes[0].r.BootstrapCluster(raft.Configuration{Servers: servers}).Error())
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
// contains the full voter set (len(cfg.Servers) == n). This proves the
// configuration entry has been committed and replicated before a test kills
// the leader.
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

// joinAll adds every non-bootstrap node to the cluster via the leader's
// AddVoter (simulating the joinLoop) and waits for the configuration to
// replicate.
func joinAll(t *testing.T, nodes []raftClusterNode) *RaftStore {
	t.Helper()
	leader := findLeader(t, clusterStores(nodes))
	for i := 1; i < len(nodes); i++ {
		assert.NilError(t, leader.AddVoter(fmt.Sprintf("node-%d", i), string(nodes[i].addr), 5*time.Second))
	}
	waitForClusterSettled(t, clusterStores(nodes), len(nodes), 30*time.Second)
	return leader
}

func TestMultiNode_BootstrapJoinElect(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	bootstrapSingle(t, nodes)

	leader := joinAll(t, nodes)
	assert.Assert(t, leader.IsLeader())

	// A write on the leader commits against the full quorum and replicates.
	assert.NilError(t, leader.CreateChannel(&Channel{ID: "joined"}))
	for i := 1; i < len(nodes); i++ {
		waitForChannel(t, nodes[i].store, "joined")
	}
}

// waitForChannel polls a node's local FSM until the channel appears.
func waitForChannel(t *testing.T, s *RaftStore, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.GetChannel(id); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("channel %q never replicated", id)
}

func TestMultiNode_KillLeaderReelect(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	bootstrapSingle(t, nodes)
	leader := joinAll(t, nodes)

	// Kill the leader (raft + transport): a new one must be elected and
	// writes must resume.
	assert.NilError(t, leader.raft.Shutdown().Error())
	_ = closeRaftTransport(leader.transport)

	newLeader := findLeader(t, clusterStores(nodes))
	assert.Assert(t, newLeader != leader, "expected a different leader after failover")
	assert.NilError(t, newLeader.CreateChannel(&Channel{ID: "failover"}))
}

func TestMultiNode_ReconcileAfterAddressChange(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	bootstrapSingle(t, nodes)
	leader := joinAll(t, nodes)

	desired := []RaftPeer{
		{ID: "node-0", Address: string(nodes[0].addr)},
		{ID: "node-1", Address: string(nodes[1].addr)},
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
	bootstrapSingle(t, nodes)
	leader := joinAll(t, nodes)

	desired := []RaftPeer{{ID: "node-0", Address: string(nodes[0].addr)}}
	added, updated, removed, err := leader.ReconcileMembership(desired, 5*time.Second)
	assert.NilError(t, err)
	assert.Equal(t, len(added), 0)
	assert.Equal(t, len(updated), 0)
	assert.Equal(t, len(removed), 2)
}

func TestMultiNode_TLSClusterReplication(t *testing.T) {
	nodes := buildTLSClusterNodes(t, 3)
	bootstrapSingle(t, nodes)
	leader := joinAll(t, nodes)

	assert.NilError(t, leader.CreateChannel(&Channel{ID: "tls-joined"}))
	for i := 1; i < len(nodes); i++ {
		waitForChannel(t, nodes[i].store, "tls-joined")
	}
}

func TestMultiNode_FollowerCleanState(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	bootstrapSingle(t, nodes)
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
