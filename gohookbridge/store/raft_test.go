package store

import (
	"testing"

	"gotest.tools/v3/assert"
)

func newTestRaftStore(t *testing.T) *RaftStore {
	t.Helper()
	rs, err := NewRaftStore(RaftConfig{
		Dir:      t.TempDir(),
		NodeID:   "test-node",
		BindAddr: "127.0.0.1:0",
	})
	assert.NilError(t, err)
	t.Cleanup(func() { rs.Shutdown() })
	return rs
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

	err = rs.CreateChannel(&Channel{ID: "test", Name: "test"})
	assert.NilError(t, err)
	hasData, err = rs.HasData()
	assert.NilError(t, err)
	assert.Assert(t, hasData)
}

func TestRaftStore_CreateProject(t *testing.T) {
	rs := newTestRaftStore(t)

	p := &Channel{ID: "proj1", Name: "Project One"}
	err := rs.CreateChannel(p)
	assert.NilError(t, err)

	got, err := rs.GetChannel("proj1")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "proj1")
	assert.Equal(t, got.Name, "Project One")

	err = rs.CreateChannel(&Channel{ID: "proj1"})
	assert.ErrorContains(t, err, "already exists")
}

func TestRaftStore_UpdateProject(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(&Channel{ID: "proj1", Name: "Original"})
	assert.NilError(t, err)

	err = rs.UpdateChannel(&Channel{ID: "proj1", Name: "Updated", MaxBodySize: 999})
	assert.NilError(t, err)

	got, err := rs.GetChannel("proj1")
	assert.NilError(t, err)
	assert.Equal(t, got.Name, "Updated")
	assert.Equal(t, got.MaxBodySize, 999)
}

func TestRaftStore_DeleteProject(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(&Channel{ID: "proj1", Name: "To Delete"})
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
		err := rs.CreateChannel(&Channel{ID: id, Name: "Project " + id})
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
	assert.Assert(t, !cfg.Server.TrustProxy)
	assert.Equal(t, cfg.Server.Footer, "")
	assert.Equal(t, cfg.Server.SessionSecret, "")
	assert.Equal(t, cfg.Defaults.WebhookSecret, "")
	assert.Assert(t, len(cfg.Defaults.AllowedIPs) == 0)
	assert.Equal(t, cfg.Defaults.ReplayToken, "")
}

func TestRaftStore_UpdateGlobalConfig(t *testing.T) {
	rs := newTestRaftStore(t)

	newCfg := &GlobalConfig{
		Server: ServerConfig{
			MaxBodySize: 100,
			TrustProxy:  true,
			CORSOrigin:  "https://example.com",
			Footer:      "custom footer",
		},
		Defaults: DefaultChannelConfig{
			WebhookSecret: "sig1",
			AllowedIPs:    []string{"10.0.0.0/8"},
			ReplayToken:   "token123",
		},
	}
	err := rs.UpdateGlobalConfig(newCfg)
	assert.NilError(t, err)

	cfg, err := rs.GetGlobalConfig()
	assert.NilError(t, err)
	assert.Equal(t, cfg.Server.MaxBodySize, 100)
	assert.Assert(t, cfg.Server.TrustProxy)
	assert.Equal(t, cfg.Server.CORSOrigin, "https://example.com")
	assert.Equal(t, cfg.Server.Footer, "custom footer")
	assert.Equal(t, cfg.Defaults.WebhookSecret, "sig1")
	assert.DeepEqual(t, cfg.Defaults.AllowedIPs, []string{"10.0.0.0/8"})
	assert.Equal(t, cfg.Defaults.ReplayToken, "token123")
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
			ReplayToken:   "global-token",
		},
	})
	assert.NilError(t, err)

	resolved, err := rs.ResolveChannelConfig("nonexistent-project")
	assert.NilError(t, err)
	assert.Equal(t, resolved.ID, "nonexistent-project")
	assert.Equal(t, resolved.MaxBodySize, 100)
	assert.Equal(t, resolved.WebhookSecret, "global-sig")
	assert.DeepEqual(t, resolved.AllowedIPs, []string{"10.0.0.0/8"})
	assert.Equal(t, resolved.ReplayToken, "global-token")

	err = rs.CreateChannel(&Channel{ID: "minimal-project", Name: "Minimal"})
	assert.NilError(t, err)

	resolved, err = rs.ResolveChannelConfig("minimal-project")
	assert.NilError(t, err)
	assert.Equal(t, resolved.MaxBodySize, 100)
	assert.Equal(t, resolved.WebhookSecret, "global-sig")
	assert.DeepEqual(t, resolved.AllowedIPs, []string{"10.0.0.0/8"})
	assert.Equal(t, resolved.ReplayToken, "global-token")

	err = rs.UpdateChannel(&Channel{
		ID:            "minimal-project",
		Name:          "Minimal",
		MaxBodySize:   999,
		WebhookSecret: "project-sig",
	})
	assert.NilError(t, err)

	resolved, err = rs.ResolveChannelConfig("minimal-project")
	assert.NilError(t, err)
	assert.Equal(t, resolved.MaxBodySize, 999)
	assert.Equal(t, resolved.WebhookSecret, "project-sig")
	assert.DeepEqual(t, resolved.AllowedIPs, []string{"10.0.0.0/8"})
	assert.Equal(t, resolved.ReplayToken, "global-token")
}

func TestRaftStore_ValidateReplayToken(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(&Channel{
		ID:          "secured",
		ReplayToken: "secret-token",
	})
	assert.NilError(t, err)

	assert.Assert(t, rs.ValidateReplayToken("secured", "secret-token"))
	assert.Assert(t, !rs.ValidateReplayToken("secured", "wrong-token"))
	assert.Assert(t, !rs.ValidateReplayToken("secured", ""))

	assert.Assert(t, rs.ValidateReplayToken("nonexistent", ""))
	assert.Assert(t, rs.ValidateReplayToken("nonexistent", "anything"))

	future := rs.UpdateGlobalConfig(&GlobalConfig{
		Defaults: DefaultChannelConfig{
			ReplayToken: "global-secret",
		},
	})
	assert.NilError(t, future)

	err = rs.CreateChannel(&Channel{ID: "global-token-project"})
	assert.NilError(t, err)

	assert.Assert(t, rs.ValidateReplayToken("global-token-project", "global-secret"))
	assert.Assert(t, !rs.ValidateReplayToken("global-token-project", "wrong-token"))

	err = rs.UpdateChannel(&Channel{
		ID:          "global-token-project",
		ReplayToken: "project-override",
	})
	assert.NilError(t, err)

	assert.Assert(t, rs.ValidateReplayToken("global-token-project", "project-override"))
	assert.Assert(t, !rs.ValidateReplayToken("global-token-project", "global-secret"))
}
