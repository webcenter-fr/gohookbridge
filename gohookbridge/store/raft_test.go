package store

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestResolveBootstrapState_OrdinalZeroBootstraps(t *testing.T) {
	cfg := RaftDiscoveryConfig{
		StatefulSetName: "sts",
		HeadlessService: "headless",
		Namespace:       "ns",
		ClusterDomain:   "cluster.local",
		Replicas:        3,
		RaftPort:        6001,
	}
	resolver := &dnsPeerResolver{cfg: cfg, clusterDomain: cfg.ClusterDomain, hostname: "sts-0"}

	nodeID, shouldBootstrap, err := resolveBootstrapState(&RaftConfig{NodeID: "sts-0"}, resolver)
	assert.NilError(t, err)
	assert.Equal(t, nodeID, "sts-0")
	assert.Assert(t, shouldBootstrap, "ordinal 0 must bootstrap")
}

func TestResolveBootstrapState_OthersDoNot(t *testing.T) {
	cfg := RaftDiscoveryConfig{
		StatefulSetName: "sts",
		HeadlessService: "headless",
		Namespace:       "ns",
		ClusterDomain:   "cluster.local",
		Replicas:        3,
		RaftPort:        6001,
	}
	resolver := &dnsPeerResolver{cfg: cfg, clusterDomain: cfg.ClusterDomain, hostname: "sts-1"}

	nodeID, shouldBootstrap, err := resolveBootstrapState(&RaftConfig{NodeID: "sts-1"}, resolver)
	assert.NilError(t, err)
	assert.Equal(t, nodeID, "sts-1")
	assert.Assert(t, !shouldBootstrap, "non-first peer must not bootstrap")
}

func TestResolveBootstrapState_DefaultsNodeID(t *testing.T) {
	resolver := &singleNodeResolver{cfg: RaftDiscoveryConfig{BindAddr: "127.0.0.1:6001"}}
	nodeID, shouldBootstrap, err := resolveBootstrapState(&RaftConfig{}, resolver)
	assert.NilError(t, err)
	assert.Equal(t, nodeID, "node1")
	assert.Assert(t, shouldBootstrap)
}

func TestAddrChanged(t *testing.T) {
	tests := []struct {
		name    string
		current string
		desired string
		want    bool
	}{
		{"identical ip", "127.0.0.1:6001", "127.0.0.1:6001", false},
		{"different ip", "127.0.0.1:6001", "127.0.0.1:6002", true},
		{"dns resolves to same ip", "127.0.0.1:6001", "localhost:6001", false},
		{"dns resolves to different ip", "10.0.0.1:6001", "localhost:6001", true},
		{"unparseable desired", "127.0.0.1:6001", "not-an-addr", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, addrChanged(tc.current, tc.desired), tc.want)
		})
	}
}

func TestCleanStateFromStats(t *testing.T) {
	tests := []struct {
		name  string
		stats map[string]string
		want  bool
	}{
		{
			name:  "leader clean",
			stats: map[string]string{"state": "Leader", "commit_index": "5", "applied_index": "5", "fsm_pending": "0"},
			want:  true,
		},
		{
			name:  "leader with zero commit is clean",
			stats: map[string]string{"state": "Leader", "commit_index": "0", "applied_index": "0", "fsm_pending": "0"},
			want:  true,
		},
		{
			name:  "follower clean",
			stats: map[string]string{"state": "Follower", "commit_index": "5", "applied_index": "5", "fsm_pending": "0", "last_contact": "1s"},
			want:  true,
		},
		{
			name:  "candidate not clean",
			stats: map[string]string{"state": "Candidate", "commit_index": "5", "applied_index": "5", "fsm_pending": "0"},
			want:  false,
		},
		{
			name:  "unapplied entries",
			stats: map[string]string{"state": "Leader", "commit_index": "5", "applied_index": "4", "fsm_pending": "0"},
			want:  false,
		},
		{
			name:  "pending fsm",
			stats: map[string]string{"state": "Leader", "commit_index": "5", "applied_index": "5", "fsm_pending": "1"},
			want:  false,
		},
		{
			name:  "follower zero commit",
			stats: map[string]string{"state": "Follower", "commit_index": "0", "applied_index": "0", "fsm_pending": "0", "last_contact": "1s"},
			want:  false,
		},
		{
			name:  "follower never contacted",
			stats: map[string]string{"state": "Follower", "commit_index": "5", "applied_index": "5", "fsm_pending": "0", "last_contact": "never"},
			want:  false,
		},
		{
			name:  "follower stale contact",
			stats: map[string]string{"state": "Follower", "commit_index": "5", "applied_index": "5", "fsm_pending": "0", "last_contact": "30s"},
			want:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, cleanStateFromStats(tc.stats), tc.want)
		})
	}
}

func TestClearRaftState(t *testing.T) {
	seed := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range []string{"test-node.db", "peers.json"} {
			assert.NilError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
		}
		assert.NilError(t, os.Mkdir(filepath.Join(dir, "snapshots"), 0o700))
		return dir
	}

	t.Run("recovery mode off is a no-op", func(t *testing.T) {
		dir := seed(t)
		clearStaleRaftState(&RaftConfig{Dir: dir}, dir, "test-node")
		for _, name := range []string{"test-node.db", "peers.json"} {
			_, err := os.Stat(filepath.Join(dir, name))
			assert.NilError(t, err)
		}
		_, err := os.Stat(filepath.Join(dir, "snapshots"))
		assert.NilError(t, err)
	})

	t.Run("recovery mode on clears state", func(t *testing.T) {
		dir := seed(t)
		clearStaleRaftState(&RaftConfig{Dir: dir, RecoveryMode: true}, dir, "test-node")
		for _, name := range []string{"test-node.db", "peers.json"} {
			_, err := os.Stat(filepath.Join(dir, name))
			assert.Assert(t, os.IsNotExist(err), "%s should be removed", name)
		}
		_, err := os.Stat(filepath.Join(dir, "snapshots"))
		assert.Assert(t, os.IsNotExist(err), "snapshots should be removed")
	})
}

func TestAdvertiseResolveRetry(t *testing.T) {
	t.Run("resolvable address succeeds", func(t *testing.T) {
		addr, err := resolveAdvertiseAddr("127.0.0.1:6001", time.Second, io.Discard)
		assert.NilError(t, err)
		assert.Assert(t, addr.IP.Equal(net.ParseIP("127.0.0.1")))
	})

	t.Run("unresolvable address errors within budget", func(t *testing.T) {
		start := time.Now()
		_, err := resolveAdvertiseAddr("pod-0.headless.ns.svc.cluster.local:6001", 300*time.Millisecond, io.Discard)
		assert.Assert(t, err != nil, "expected resolution error")
		assert.Assert(t, time.Since(start) < 5*time.Second, "retry budget not enforced")
	})
}

func TestStepDown_SingleNodeNoop(t *testing.T) {
	rs := newTestRaftStore(t)
	assert.NilError(t, rs.StepDown(context.Background()))
}

func TestReconcileMembership(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	bootstrapSingle(t, nodes)
	leader := joinAll(t, nodes)

	// Add a fourth (not running) voter: AddVoter only records the config
	// entry and the 3 running nodes still satisfy the old quorum.
	desired := []RaftPeer{
		{ID: "node-0", Address: string(nodes[0].addr)},
		{ID: "node-1", Address: string(nodes[1].addr)},
		{ID: "node-2", Address: string(nodes[2].addr)},
		{ID: "node-3", Address: "127.0.0.1:19998"},
	}
	added, updated, removed, err := leader.ReconcileMembership(desired, 5*time.Second)
	assert.NilError(t, err)
	assert.DeepEqual(t, added, []string{"node-3"})
	assert.Equal(t, len(updated), 0)
	assert.Equal(t, len(removed), 0)
}

func newTestRaftStore(t *testing.T) *RaftStore {
	t.Helper()
	rs, err := NewRaftStore(RaftConfig{
		Dir:      t.TempDir(),
		NodeID:   "test-node",
		BindAddr: freeTCPAddr(t),
	})
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rs.Shutdown() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assert.NilError(t, rs.WaitForLeader(ctx))
	return rs
}

// freeTCPAddr returns a currently-free loopback host:port.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	addr := l.Addr().String()
	assert.NilError(t, l.Close())
	return addr
}

func TestNewRaftStore_SingleNode(t *testing.T) {
	rs := newTestRaftStore(t)
	assert.Assert(t, rs.IsLeader())
	hasData, err := rs.HasData()
	assert.NilError(t, err)
	assert.Assert(t, !hasData)
}

func TestRaftStore_HasData(t *testing.T) {
	rs := newTestRaftStore(t)
	hasData, err := rs.HasData()
	assert.NilError(t, err)
	assert.Assert(t, !hasData)

	err = rs.CreateChannel(&Channel{ID: "test"})
	assert.NilError(t, err)
	hasData, err = rs.HasData()
	assert.NilError(t, err)
	assert.Assert(t, hasData)
}

func TestRaftStore_CreateProject(t *testing.T) {
	rs := newTestRaftStore(t)

	p := &Channel{ID: "proj1"}
	err := rs.CreateChannel(p)
	assert.NilError(t, err)

	got, err := rs.GetChannel("proj1")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "proj1")

	err = rs.CreateChannel(&Channel{ID: "proj1"})
	assert.ErrorContains(t, err, "already exists")
}

func TestRaftStore_UpdateProject(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(&Channel{ID: "proj1"})
	assert.NilError(t, err)

	err = rs.UpdateChannel(&Channel{ID: "proj1", MaxBodySize: 999})
	assert.NilError(t, err)

	got, err := rs.GetChannel("proj1")
	assert.NilError(t, err)
	assert.Equal(t, got.MaxBodySize, 999)
}

func TestRaftStore_DeleteProject(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(&Channel{ID: "proj1"})
	assert.NilError(t, err)

	err = rs.DeleteChannel("proj1")
	assert.NilError(t, err)

	_, err = rs.GetChannel("proj1")
	assert.ErrorContains(t, err, "not found")
}

func TestRaftStore_ListProjects(t *testing.T) {
	rs := newTestRaftStore(t)

	ids := []string{"p1", "p2", "p3"}
	for _, id := range ids {
		err := rs.CreateChannel(&Channel{ID: id})
		assert.NilError(t, err)
	}

	channels, err := rs.ListChannels()
	assert.NilError(t, err)
	assert.Equal(t, len(channels), 3)
}

func TestRaftStore_GetGlobalConfig(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg, err := rs.GetGlobalConfig()
	assert.NilError(t, err)
	assert.Equal(t, cfg.Server.MaxBodySize, 26214400)
	assert.Equal(t, cfg.Server.CORSOrigin, "*")
	assert.Assert(t, !cfg.Server.BehindReverseProxy)
	assert.Equal(t, cfg.Server.Footer, "")
	assert.Equal(t, cfg.Server.SessionSecret, "")
	assert.Equal(t, cfg.Defaults.WebhookSecret, "")
	assert.Assert(t, len(cfg.Defaults.AllowedIPs) == 0)
}

func TestRaftStore_UpdateGlobalConfig(t *testing.T) {
	rs := newTestRaftStore(t)

	newCfg := &GlobalConfig{
		Server: ServerConfig{
			MaxBodySize:        100,
			BehindReverseProxy: true,
			CORSOrigin:         "https://example.com",
			Footer:             "custom footer",
		},
		Defaults: DefaultChannelConfig{
			WebhookSecret: "sig1",
			AllowedIPs:    []string{"10.0.0.0/8"},
		},
	}
	err := rs.UpdateGlobalConfig(newCfg)
	assert.NilError(t, err)

	cfg, err := rs.GetGlobalConfig()
	assert.NilError(t, err)
	assert.Equal(t, cfg.Server.MaxBodySize, 100)
	assert.Assert(t, cfg.Server.BehindReverseProxy)
	assert.Equal(t, cfg.Server.CORSOrigin, "https://example.com")
	assert.Equal(t, cfg.Server.Footer, "custom footer")
	assert.Equal(t, cfg.Defaults.WebhookSecret, "sig1")
	assert.DeepEqual(t, cfg.Defaults.AllowedIPs, []string{"10.0.0.0/8"})
}

func TestRaftStore_CRUD_Users(t *testing.T) {
	rs := newTestRaftStore(t)

	u := &User{
		ID:       "user1",
		Username: "testuser",
		Roles:    []string{"admin"},
		Channels: []string{"proj1"},
	}
	err := rs.CreateUser(u)
	assert.NilError(t, err)

	got, err := rs.GetUser("user1")
	assert.NilError(t, err)
	assert.Equal(t, got.Username, "testuser")
	assert.DeepEqual(t, got.Roles, []string{"admin"})
	assert.DeepEqual(t, got.Channels, []string{"proj1"})

	got.Roles = []string{"channel_admin"}
	err = rs.UpdateUser(got)
	assert.NilError(t, err)

	updated, err := rs.GetUser("user1")
	assert.NilError(t, err)
	assert.DeepEqual(t, updated.Roles, []string{"channel_admin"})

	err = rs.DeleteUser("user1")
	assert.NilError(t, err)

	_, err = rs.GetUser("user1")
	assert.ErrorContains(t, err, "not found")

	users, err := rs.ListUsers()
	assert.NilError(t, err)
	assert.Equal(t, len(users), 0)
}

func TestRaftStore_GetUserByUsername(t *testing.T) {
	rs := newTestRaftStore(t)

	u := &User{
		ID:       "uid-1",
		Username: "johndoe",
		Roles:    []string{"admin"},
	}
	err := rs.CreateUser(u)
	assert.NilError(t, err)

	err = rs.CreateUser(&User{
		ID:       "uid-2",
		Username: "janedoe",
		Roles:    []string{"channel_viewer"},
	})
	assert.NilError(t, err)

	got, err := rs.GetUserByUsername("johndoe")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "uid-1")
	assert.Equal(t, got.Username, "johndoe")

	got, err = rs.GetUserByUsername("janedoe")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "uid-2")

	_, err = rs.GetUserByUsername("nonexistent")
	assert.ErrorContains(t, err, "not found")
}

func TestRaftStore_ProjectConfigFallback(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.UpdateGlobalConfig(&GlobalConfig{
		Server: ServerConfig{
			MaxBodySize: 100,
		},
		Defaults: DefaultChannelConfig{
			WebhookSecret: "global-sig",
			AllowedIPs:    []string{"10.0.0.0/8"},
		},
	})
	assert.NilError(t, err)

	resolved, err := rs.ResolveChannelConfig("nonexistent-project")
	assert.NilError(t, err)
	assert.Equal(t, resolved.ID, "nonexistent-project")
	assert.Equal(t, resolved.MaxBodySize, 100)
	assert.Equal(t, resolved.WebhookSecret, "global-sig")
	assert.DeepEqual(t, resolved.AllowedIPs, []string{"10.0.0.0/8"})

	err = rs.CreateChannel(&Channel{ID: "minimal-project"})
	assert.NilError(t, err)

	resolved, err = rs.ResolveChannelConfig("minimal-project")
	assert.NilError(t, err)
	assert.Equal(t, resolved.MaxBodySize, 100)
	assert.Equal(t, resolved.WebhookSecret, "global-sig")
	assert.DeepEqual(t, resolved.AllowedIPs, []string{"10.0.0.0/8"})

	err = rs.UpdateChannel(&Channel{
		ID:            "minimal-project",
		MaxBodySize:   999,
		WebhookSecret: "project-sig",
	})
	assert.NilError(t, err)

	resolved, err = rs.ResolveChannelConfig("minimal-project")
	assert.NilError(t, err)
	assert.Equal(t, resolved.MaxBodySize, 999)
	assert.Equal(t, resolved.WebhookSecret, "project-sig")
	assert.DeepEqual(t, resolved.AllowedIPs, []string{"10.0.0.0/8"})
}

func TestClientCursorCRUD(t *testing.T) {
	rs := newTestRaftStore(t)

	cursor := &ClientCursor{
		Channel:         "test-channel",
		ClientID:        "test-client",
		LastTimestampMs: 1234567890000,
	}
	err := rs.SetClientCursor(cursor)
	assert.NilError(t, err)

	got, err := rs.GetClientCursor("test-channel", "test-client")
	assert.NilError(t, err)
	assert.Assert(t, got != nil)
	assert.Equal(t, got.Channel, "test-channel")
	assert.Equal(t, got.ClientID, "test-client")
	assert.Equal(t, got.LastTimestampMs, int64(1234567890000))

	got, err = rs.GetClientCursor("nonexistent", "test-client")
	assert.NilError(t, err)
	assert.Assert(t, got == nil)

	got, err = rs.GetClientCursor("test-channel", "nonexistent")
	assert.NilError(t, err)
	assert.Assert(t, got == nil)

	cursor2 := &ClientCursor{
		Channel:         "test-channel",
		ClientID:        "test-client",
		LastTimestampMs: 1234567899999,
	}
	err = rs.SetClientCursor(cursor2)
	assert.NilError(t, err)

	got, err = rs.GetClientCursor("test-channel", "test-client")
	assert.NilError(t, err)
	assert.Assert(t, got != nil)
	assert.Equal(t, got.LastTimestampMs, int64(1234567899999))
}
