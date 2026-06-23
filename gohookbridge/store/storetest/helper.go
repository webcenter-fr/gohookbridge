// Package storetest provides test helpers for the store package.
package storetest

import (
	"testing"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
)

func NewRaftStore(t *testing.T) *store.RaftStore {
	t.Helper()
	rs, err := store.NewRaftStore(store.RaftConfig{
		Dir:      t.TempDir(),
		NodeID:   "test-node",
		BindAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rs.Shutdown() })
	return rs
}

func NewRaftStoreWithConfig(t *testing.T, cfg store.RaftConfig) *store.RaftStore {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "test-node"
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1:0"
	}
	rs, err := store.NewRaftStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rs.Shutdown() })
	return rs
}

func DefaultGlobalConfig() *store.GlobalConfig {
	return &store.GlobalConfig{
		Server: store.ServerConfig{
			MaxBodySize: 26214400,
			CORSOrigin:  "*",
		},
		Defaults: store.DefaultChannelConfig{},
	}
}

func SetupCORSOrigin(rs *store.RaftStore, corsOrigin string) {
	globalCfg := DefaultGlobalConfig()
	globalCfg.Server.CORSOrigin = corsOrigin
	rs.UpdateGlobalConfig(globalCfg) //nolint:errcheck
}

func SetupProtectedChannels(t *testing.T, channels map[string][]string) *store.ProtectedChannels {
	t.Helper()
	rs := NewRaftStore(t)
	SetupCORSOrigin(rs, "*")
	for channel, allowedKeys := range channels {
		p := &store.Channel{
			ID:                channel,
			EncryptionMode:    "e2e",
			EncryptionPubKeys: allowedKeys,
		}
		if err := rs.CreateChannel(p); err != nil {
			t.Fatal(err)
		}
	}
	return store.NewProtectedChannels(rs)
}
