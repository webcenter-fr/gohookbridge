// Package storetest provides test helpers for the store package.
package storetest

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
)

func NewRaftStore(t *testing.T) *store.RaftStore {
	t.Helper()
	return NewRaftStoreWithConfig(t, store.RaftConfig{})
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
		cfg.BindAddr = freeTCPAddr(t)
	}
	rs, err := store.NewRaftStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rs.Shutdown() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rs.WaitForLeader(ctx); err != nil {
		t.Fatalf("wait for raft leader: %v", err)
	}
	return rs
}

// freeTCPAddr returns a currently-free loopback host:port.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
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
