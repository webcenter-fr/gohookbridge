package repository

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/disaster37/go-raft/config"
	"github.com/disaster37/go-raft/storage"
	"github.com/disaster37/go-raft/testutil"
	hraft "github.com/hashicorp/raft"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

// seedMultiVoterState writes a 3-voter Raft configuration plus one committed
// legacy FSM command into dir/nodeID, simulating a cluster that lost two of its
// three voters (e.g. a StatefulSet scaled from 3 replicas to 1). The legacy
// {"op":"set",...} entry simultaneously proves single-node recovery AND legacy
// Raft log replay.
func seedMultiVoterState(t *testing.T, dir, nodeID string) {
	t.Helper()
	store, err := storage.NewSharedBoltStore(dir, nodeID, config.StorageConfig{}, 2, 512, io.Discard)
	assert.NilError(t, err)
	defer func() { _ = store.Close() }()

	logStore := store.LogStore()
	stableStore := store.StableStore()
	snapStore := store.SnapshotStore()
	_, trans := hraft.NewInmemTransport("")

	cfg := hraft.Configuration{Servers: []hraft.Server{
		{Suffrage: hraft.Voter, ID: hraft.ServerID(nodeID), Address: "127.0.0.1:1"},
		{Suffrage: hraft.Voter, ID: "other-1", Address: "127.0.0.1:2"},
		{Suffrage: hraft.Voter, ID: "other-2", Address: "127.0.0.1:3"},
	}}
	raftCfg := hraft.DefaultConfig()
	raftCfg.LocalID = hraft.ServerID(nodeID)
	raftCfg.LogOutput = io.Discard
	assert.NilError(t, hraft.BootstrapCluster(raftCfg, logStore, stableStore, snapStore, trans, cfg))

	lastIndex, err := logStore.LastIndex()
	assert.NilError(t, err)
	value, err := json.Marshal(domain.ServerConfig{MaxBodySize: 4242})
	assert.NilError(t, err)
	data, err := json.Marshal(legacyFSMCommand{Op: "set", Key: "/global/server/", Value: value})
	assert.NilError(t, err)
	assert.NilError(t, logStore.StoreLog(&hraft.Log{
		Index: lastIndex + 1,
		Term:  1,
		Type:  hraft.LogCommand,
		Data:  data,
	}))
}

func TestSingleNodeRecovery_CollapsesToSelfAndPreservesData(t *testing.T) {
	dir := t.TempDir()
	seedMultiVoterState(t, dir, "node-a")

	ln := testutil.FreeListener(t)
	rs, err := NewRaftStore(RaftConfig{
		Dir:                   dir,
		NodeID:                "node-a",
		BindAddr:              testutil.FreeAddrFromListener(t, ln),
		CustomListener:        ln,
		PerformanceMultiplier: 0.2,
		SingleNodeRecovery:    true,
	})
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rs.Shutdown() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assert.NilError(t, rs.WaitForLeader(ctx))
	assert.Assert(t, rs.IsLeader())

	cfg, err := rs.GetConfiguration()
	assert.NilError(t, err)
	assert.Equal(t, len(cfg.Servers), 1)
	assert.Equal(t, string(cfg.Servers[0].ID), "node-a")

	global, err := rs.GetGlobalConfig(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, global.Server.MaxBodySize, 4242)
}

func TestReconcileMembership_SkipsUnresolvableAdds(t *testing.T) {
	rs := newTestRaftStore(t)

	current, err := rs.GetConfiguration()
	assert.NilError(t, err)
	desired := make([]RaftPeer, 0, len(current.Servers)+1)
	for _, srv := range current.Servers {
		desired = append(desired, RaftPeer{ID: string(srv.ID), Address: string(srv.Address)})
	}
	desired = append(desired, RaftPeer{ID: "gone", Address: "gohookbridge-gone.invalid:6001"})

	added, updated, removed, err := rs.ReconcileMembership(desired, 3*time.Second)
	assert.NilError(t, err)
	assert.Equal(t, len(added), 0)
	assert.Equal(t, len(updated), 0)
	assert.Equal(t, len(removed), 0)

	after, err := rs.GetConfiguration()
	assert.NilError(t, err)
	assert.Equal(t, len(after.Servers), len(current.Servers))
}

func TestSingleNodeRecovery_DisabledKeepsQuorumRequirement(t *testing.T) {
	dir := t.TempDir()
	seedMultiVoterState(t, dir, "node-a")

	ln := testutil.FreeListener(t)
	rs, err := NewRaftStore(RaftConfig{
		Dir:                   dir,
		NodeID:                "node-a",
		BindAddr:              testutil.FreeAddrFromListener(t, ln),
		CustomListener:        ln,
		PerformanceMultiplier: 0.2,
	})
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rs.Shutdown() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	assert.ErrorContains(t, rs.WaitForLeader(ctx), "wait for raft leader")
	assert.Assert(t, !rs.IsLeader())

	cfg, err := rs.GetConfiguration()
	assert.NilError(t, err)
	assert.Equal(t, len(cfg.Servers), 3)
}
