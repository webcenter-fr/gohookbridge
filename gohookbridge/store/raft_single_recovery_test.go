package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"gotest.tools/v3/assert"
)

// seedMultiVoterState writes a 3-voter Raft configuration plus one committed
// FSM command into dir/nodeID, simulating a cluster that lost two of its three
// voters (e.g. a StatefulSet scaled from 3 replicas to 1).
func seedMultiVoterState(t *testing.T, dir, nodeID string) {
	t.Helper()
	db, err := newBoltDB(dir, nodeID)
	assert.NilError(t, err)
	defer func() { _ = db.Close() }()

	logStore := newBoltLogStore(db)
	stableStore := newBoltStableStore(db)
	snapStore := raft.NewInmemSnapshotStore()
	_, trans := raft.NewInmemTransport("")

	cfg := raftConfigurationFromPeers([]RaftPeer{
		{ID: nodeID, Address: "127.0.0.1:1"},
		{ID: "other-1", Address: "127.0.0.1:2"},
		{ID: "other-2", Address: "127.0.0.1:3"},
	})
	assert.NilError(t, raft.BootstrapCluster(raftTestConfig(nodeID), logStore, stableStore, snapStore, trans, cfg))

	lastIndex, err := logStore.LastIndex()
	assert.NilError(t, err)
	value, err := json.Marshal(ServerConfig{MaxBodySize: 4242})
	assert.NilError(t, err)
	data, err := json.Marshal(fsmCommand{Op: "set", Key: "/global/server/", Value: value})
	assert.NilError(t, err)
	assert.NilError(t, logStore.StoreLog(&raft.Log{
		Index: lastIndex + 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  data,
	}))
}

func TestSingleNodeRecovery_CollapsesToSelfAndPreservesData(t *testing.T) {
	dir := t.TempDir()
	seedMultiVoterState(t, dir, "node-a")

	rs, err := NewRaftStore(RaftConfig{
		Dir:                dir,
		NodeID:             "node-a",
		BindAddr:           freeTCPAddr(t),
		SingleNodeRecovery: true,
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

	global, err := rs.GetGlobalConfig()
	assert.NilError(t, err)
	assert.Equal(t, global.Server.MaxBodySize, 4242)
}

func TestSingleNodeRecovery_DisabledKeepsQuorumRequirement(t *testing.T) {
	dir := t.TempDir()
	seedMultiVoterState(t, dir, "node-a")

	rs, err := NewRaftStore(RaftConfig{
		Dir:      dir,
		NodeID:   "node-a",
		BindAddr: freeTCPAddr(t),
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
