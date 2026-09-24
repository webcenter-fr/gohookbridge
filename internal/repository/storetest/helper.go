// Package storetest provides test helpers for the repository package.
package storetest

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/internal/repository"
	"github.com/webcenter-fr/gohookbridge/internal/service"
)

func NewRaftStore(t *testing.T) *repository.RaftStore {
	t.Helper()
	return NewRaftStoreWithConfig(t, repository.RaftConfig{})
}

func NewRaftStoreWithConfig(t *testing.T, cfg repository.RaftConfig) *repository.RaftStore {
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
	rs, err := repository.NewRaftStore(cfg)
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

func DefaultGlobalConfig() *domain.GlobalConfig {
	return &domain.GlobalConfig{
		Server: domain.ServerConfig{
			MaxBodySize: 26214400,
			CORSOrigin:  "*",
		},
		Defaults: domain.DefaultChannelConfig{},
	}
}

func SetupCORSOrigin(rs *repository.RaftStore, corsOrigin string) {
	globalCfg := DefaultGlobalConfig()
	globalCfg.Server.CORSOrigin = corsOrigin
	rs.UpdateGlobalConfig(context.Background(), globalCfg) //nolint:errcheck
}

func SetupProtectedChannels(t *testing.T, channels map[string][]string) *service.ProtectedChannels {
	t.Helper()
	rs := NewRaftStore(t)
	SetupCORSOrigin(rs, "*")
	for channel, allowedKeys := range channels {
		p := &domain.Channel{
			ID:                channel,
			EncryptionMode:    "e2e",
			EncryptionPubKeys: allowedKeys,
		}
		if err := rs.CreateChannel(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	return service.NewService(rs, nil).NewProtectedChannels(context.Background())
}
