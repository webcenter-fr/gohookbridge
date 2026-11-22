package repository

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/disaster37/go-raft/pki"
	"github.com/disaster37/go-raft/storage"
	"github.com/disaster37/go-raft/testutil"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

func TestStepDown_SingleNodeNoop(t *testing.T) {
	rs := newTestRaftStore(t)
	assert.NilError(t, rs.StepDown(context.Background()))
}

func TestReconcileMembership(t *testing.T) {
	nodes := buildPlainClusterNodes(t, 3)
	leader := joinAll(t, nodes)

	// Add a fourth (not running) voter: AddVoter only records the config
	// entry and the 3 running nodes still satisfy the old quorum.
	desired := []RaftPeer{
		{ID: "node-0", Address: nodes[0].addr},
		{ID: "node-1", Address: nodes[1].addr},
		{ID: "node-2", Address: nodes[2].addr},
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
	return newTestRaftStoreWithConfig(t, RaftConfig{})
}

// newTestRaftStoreWithConfig builds a single-node store from cfg, filling the
// same defaults as the storetest helper (temp dir, listener, fast timings).
func newTestRaftStoreWithConfig(t *testing.T, cfg RaftConfig) *RaftStore {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "test-node"
	}
	if cfg.BindAddr == "" && cfg.CustomListener == nil {
		ln := testutil.FreeListener(t)
		cfg.CustomListener = ln
		cfg.BindAddr = testutil.FreeAddrFromListener(t, ln)
	}
	if cfg.PerformanceMultiplier == 0 {
		cfg.PerformanceMultiplier = 0.2
	}
	rs, err := NewRaftStore(cfg)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rs.Shutdown() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assert.NilError(t, rs.WaitForLeader(ctx))
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

	err = rs.CreateChannel(context.Background(), &domain.Channel{ID: "test"})
	assert.NilError(t, err)
	hasData, err = rs.HasData()
	assert.NilError(t, err)
	assert.Assert(t, hasData)
}

func TestRaftStore_CreateProject(t *testing.T) {
	rs := newTestRaftStore(t)

	p := &domain.Channel{ID: "proj1"}
	err := rs.CreateChannel(context.Background(), p)
	assert.NilError(t, err)

	got, err := rs.GetChannel(context.Background(), "proj1")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "proj1")

	err = rs.CreateChannel(context.Background(), &domain.Channel{ID: "proj1"})
	assert.ErrorContains(t, err, "already exists")
}

func TestRaftStore_UpdateProject(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(context.Background(), &domain.Channel{ID: "proj1"})
	assert.NilError(t, err)

	err = rs.UpdateChannel(context.Background(), &domain.Channel{ID: "proj1", MaxBodySize: 999})
	assert.NilError(t, err)

	got, err := rs.GetChannel(context.Background(), "proj1")
	assert.NilError(t, err)
	assert.Equal(t, got.MaxBodySize, 999)
}

func TestRaftStore_DeleteProject(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.CreateChannel(context.Background(), &domain.Channel{ID: "proj1"})
	assert.NilError(t, err)

	err = rs.DeleteChannel(context.Background(), "proj1")
	assert.NilError(t, err)

	_, err = rs.GetChannel(context.Background(), "proj1")
	assert.ErrorContains(t, err, "not found")
}

func TestRaftStore_ListProjects(t *testing.T) {
	rs := newTestRaftStore(t)

	ids := []string{"p1", "p2", "p3"}
	for _, id := range ids {
		err := rs.CreateChannel(context.Background(), &domain.Channel{ID: id})
		assert.NilError(t, err)
	}

	channels, err := rs.ListChannels(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(channels), 3)
}

func TestRaftStore_GetGlobalConfig(t *testing.T) {
	rs := newTestRaftStore(t)

	cfg, err := rs.GetGlobalConfig(context.Background())
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

	newCfg := &domain.GlobalConfig{
		Server: domain.ServerConfig{
			MaxBodySize:        100,
			BehindReverseProxy: true,
			CORSOrigin:         "https://example.com",
			Footer:             "custom footer",
		},
		Defaults: domain.DefaultChannelConfig{
			WebhookSecret: "sig1",
			AllowedIPs:    []string{"10.0.0.0/8"},
		},
	}
	err := rs.UpdateGlobalConfig(context.Background(), newCfg)
	assert.NilError(t, err)

	cfg, err := rs.GetGlobalConfig(context.Background())
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

	u := &domain.User{
		ID:       "user1",
		Username: "testuser",
		Roles:    []string{"admin"},
		Channels: []string{"proj1"},
	}
	err := rs.CreateUser(context.Background(), u)
	assert.NilError(t, err)

	got, err := rs.GetUser(context.Background(), "user1")
	assert.NilError(t, err)
	assert.Equal(t, got.Username, "testuser")
	assert.DeepEqual(t, got.Roles, []string{"admin"})
	assert.DeepEqual(t, got.Channels, []string{"proj1"})

	got.Roles = []string{"channel_admin"}
	err = rs.UpdateUser(context.Background(), got)
	assert.NilError(t, err)

	updated, err := rs.GetUser(context.Background(), "user1")
	assert.NilError(t, err)
	assert.DeepEqual(t, updated.Roles, []string{"channel_admin"})

	err = rs.DeleteUser(context.Background(), "user1")
	assert.NilError(t, err)

	_, err = rs.GetUser(context.Background(), "user1")
	assert.ErrorContains(t, err, "not found")

	users, err := rs.ListUsers(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, len(users), 0)
}

func TestRaftStore_CreateUser_Duplicate(t *testing.T) {
	rs := newTestRaftStore(t)

	u := &domain.User{ID: "user1", Username: "testuser"}
	assert.NilError(t, rs.CreateUser(context.Background(), u))

	err := rs.CreateUser(context.Background(), &domain.User{ID: "user1", Username: "testuser"})
	assert.Assert(t, errors.Is(err, domain.ErrAlreadyExists))

	// Ensure the existing user was not overwritten.
	got, err := rs.GetUser(context.Background(), "user1")
	assert.NilError(t, err)
	assert.Equal(t, got.Username, "testuser")
}

func TestRaftStore_UpdateUser_Rename(t *testing.T) {
	rs := newTestRaftStore(t)

	assert.NilError(t, rs.CreateUser(context.Background(), &domain.User{
		ID:       "uid-1",
		Username: "oldname",
		Roles:    []string{"channel_viewer"},
	}))

	got, err := rs.GetUser(context.Background(), "uid-1")
	assert.NilError(t, err)
	got.Username = "newname"
	assert.NilError(t, rs.UpdateUser(context.Background(), got))

	_, err = rs.GetUserByUsername(context.Background(), "oldname")
	assert.Assert(t, errors.Is(err, domain.ErrNotFound))

	byName, err := rs.GetUserByUsername(context.Background(), "newname")
	assert.NilError(t, err)
	assert.Equal(t, byName.ID, "uid-1")

	updated, err := rs.GetUser(context.Background(), "uid-1")
	assert.NilError(t, err)
	assert.Equal(t, updated.Username, "newname")
}

func TestRaftStore_GetUserByUsername(t *testing.T) {
	rs := newTestRaftStore(t)

	u := &domain.User{
		ID:       "uid-1",
		Username: "johndoe",
		Roles:    []string{"admin"},
	}
	err := rs.CreateUser(context.Background(), u)
	assert.NilError(t, err)

	err = rs.CreateUser(context.Background(), &domain.User{
		ID:       "uid-2",
		Username: "janedoe",
		Roles:    []string{"channel_viewer"},
	})
	assert.NilError(t, err)

	got, err := rs.GetUserByUsername(context.Background(), "johndoe")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "uid-1")
	assert.Equal(t, got.Username, "johndoe")

	got, err = rs.GetUserByUsername(context.Background(), "janedoe")
	assert.NilError(t, err)
	assert.Equal(t, got.ID, "uid-2")

	_, err = rs.GetUserByUsername(context.Background(), "nonexistent")
	assert.ErrorContains(t, err, "not found")
}

func TestClientCursorCRUD(t *testing.T) {
	rs := newTestRaftStore(t)

	cursor := &domain.ClientCursor{
		Channel:         "test-channel",
		ClientID:        "test-client",
		LastTimestampMs: 1234567890000,
	}
	err := rs.SetClientCursor(context.Background(), cursor)
	assert.NilError(t, err)

	got, err := rs.GetClientCursor(context.Background(), "test-channel", "test-client")
	assert.NilError(t, err)
	assert.Assert(t, got != nil)
	assert.Equal(t, got.Channel, "test-channel")
	assert.Equal(t, got.ClientID, "test-client")
	assert.Equal(t, got.LastTimestampMs, int64(1234567890000))

	got, err = rs.GetClientCursor(context.Background(), "nonexistent", "test-client")
	assert.Assert(t, errors.Is(err, domain.ErrNotFound))
	assert.Assert(t, got == nil)

	got, err = rs.GetClientCursor(context.Background(), "test-channel", "nonexistent")
	assert.Assert(t, errors.Is(err, domain.ErrNotFound))
	assert.Assert(t, got == nil)

	cursor2 := &domain.ClientCursor{
		Channel:         "test-channel",
		ClientID:        "test-client",
		LastTimestampMs: 1234567899999,
	}
	err = rs.SetClientCursor(context.Background(), cursor2)
	assert.NilError(t, err)

	got, err = rs.GetClientCursor(context.Background(), "test-channel", "test-client")
	assert.NilError(t, err)
	assert.Assert(t, got != nil)
	assert.Equal(t, got.LastTimestampMs, int64(1234567899999))
}

func TestInternalAuthEnabled_UnsetNil(t *testing.T) {
	rs := newTestRaftStore(t)

	flag, err := rs.InternalAuthEnabled(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, flag == nil)
}

func TestSetInternalAuthEnabled_RoundTrip(t *testing.T) {
	rs := newTestRaftStore(t)

	err := rs.SetInternalAuthEnabled(context.Background(), false)
	assert.NilError(t, err)

	flag, err := rs.InternalAuthEnabled(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, flag != nil)
	assert.Equal(t, *flag, false)

	err = rs.SetInternalAuthEnabled(context.Background(), true)
	assert.NilError(t, err)

	flag, err = rs.InternalAuthEnabled(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, flag != nil)
	assert.Equal(t, *flag, true)
}

// TestRaftStore_TLSModes exercises the three TLS activation modes.
func TestRaftStore_TLSModes(t *testing.T) {
	t.Run("manual mTLS", func(t *testing.T) {
		dir := t.TempDir()
		tlsDir := filepath.Join(dir, "tls")
		ca, err := pki.NewMintingCA("raft-ca", "gohookbridge-raft", 365*24*time.Hour)
		assert.NilError(t, err)
		writeClusterTLS(t, ca, tlsDir, "manual-node")

		ln := testutil.FreeListener(t)
		rs, err := NewRaftStore(RaftConfig{
			Dir:                   dir,
			NodeID:                "manual-node",
			BindAddr:              testutil.FreeAddrFromListener(t, ln),
			CustomListener:        ln,
			PerformanceMultiplier: 0.2,
			TLSEnabled:            true,
			TLSDir:                tlsDir,
			TLSCACertPath:         filepath.Join(tlsDir, "ca.crt"),
			TLSCertPath:           filepath.Join(tlsDir, "tls.crt"),
			TLSKeyPath:            filepath.Join(tlsDir, "tls.key"),
		})
		assert.NilError(t, err)
		t.Cleanup(func() { _ = rs.Shutdown() })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NilError(t, rs.WaitForLeader(ctx))
		assert.Assert(t, rs.TLSProvider() != nil)
		assert.Assert(t, rs.node.KeyringManager().CurrentKeyring() == nil, "manual mode must disable the keyring")
	})

	t.Run("single-node local auto-CA", func(t *testing.T) {
		ln := testutil.FreeListener(t)
		rs, err := NewRaftStore(RaftConfig{
			Dir:                   t.TempDir(),
			NodeID:                "auto-node",
			BindAddr:              testutil.FreeAddrFromListener(t, ln),
			CustomListener:        ln,
			PerformanceMultiplier: 0.2,
			TLSEnabled:            true,
		})
		assert.NilError(t, err)
		t.Cleanup(func() { _ = rs.Shutdown() })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NilError(t, rs.WaitForLeader(ctx))
		assert.Assert(t, rs.TLSProvider() != nil)
	})

	t.Run("multi-node without manual certs rejected", func(t *testing.T) {
		_, err := NewRaftStore(RaftConfig{
			Dir:                   t.TempDir(),
			NodeID:                "reject-node",
			BindAddr:              "127.0.0.1:0",
			Replicas:              3,
			PerformanceMultiplier: 0.2,
			TLSEnabled:            true,
		})
		assert.Assert(t, err != nil, "multi-node auto-CA must be rejected (F14)")
	})
}

// TestRaftStore_CertRenewalLifecycle proves the single-node renewal/rotation
// behaviors (leaf re-issue within the safety margin, manual FileWatcher hot
// reload, and in-band phased keyring rotation).
func TestRaftStore_CertRenewalLifecycle(t *testing.T) {
	t.Run("leaf_reissued_within_safety_margin", func(t *testing.T) {
		dir := t.TempDir()
		tlsDir := filepath.Join(dir, "tls")
		assert.NilError(t, os.MkdirAll(tlsDir, 0o700))
		ca, err := pki.NewMintingCA("raft-ca", "gohookbridge-raft", 365*24*time.Hour)
		assert.NilError(t, err)
		caCertPEM, caKeyPEM, err := ca.EncodePEM()
		assert.NilError(t, err)
		assert.NilError(t, os.WriteFile(filepath.Join(tlsDir, "ca.crt"), caCertPEM, 0o600))
		assert.NilError(t, os.WriteFile(filepath.Join(tlsDir, "ca.key"), caKeyPEM, 0o600))

		// Pre-mint a leaf that expires within 48h; with a 1h safety margin it is
		// immediately reusable, but a 7d margin forces re-issue.
		certPEM, keyPEM, err := pki.IssueOrReuseNodeCert(tlsDir, ca, "renew-node", "gohookbridge-raft", []string{"renew-node", "localhost"}, nil, 48*time.Hour, time.Hour)
		assert.NilError(t, err)
		reusedCert, reusedKey, err := pki.IssueOrReuseNodeCert(tlsDir, ca, "renew-node", "gohookbridge-raft", []string{"renew-node", "localhost"}, nil, 48*time.Hour, time.Hour)
		assert.NilError(t, err)
		assert.Equal(t, string(certPEM), string(reusedCert), "within the safety margin the leaf must be reused")
		assert.Equal(t, string(keyPEM), string(reusedKey))

		before, err := pki.ParsePEMCert(certPEM)
		assert.NilError(t, err)

		ln := testutil.FreeListener(t)
		rs, err := NewRaftStore(RaftConfig{
			Dir:                   dir,
			NodeID:                "renew-node",
			BindAddr:              testutil.FreeAddrFromListener(t, ln),
			CustomListener:        ln,
			PerformanceMultiplier: 0.2,
			TLSEnabled:            true,
			TLSDir:                tlsDir,
			TLSValidity:           30 * 24 * time.Hour,
			TLSSafetyMargin:       7 * 24 * time.Hour,
		})
		assert.NilError(t, err)
		t.Cleanup(func() { _ = rs.Shutdown() })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NilError(t, rs.WaitForLeader(ctx))

		served, err := rs.TLSProvider().GetCertificate(nil)
		assert.NilError(t, err)
		assert.Assert(t, served != nil && len(served.Certificate) > 0)
		after, err := x509.ParseCertificate(served.Certificate[0])
		assert.NilError(t, err)
		assert.Assert(t, after.SerialNumber.Cmp(before.SerialNumber) != 0, "leaf must be re-issued within the 7d safety margin")
		assert.Assert(t, after.NotAfter.After(time.Now().Add(29*24*time.Hour)), "re-issued leaf must carry the full validity")
	})

	t.Run("manual_certs_filewatcher_hot_reload", func(t *testing.T) {
		dir := t.TempDir()
		tlsDir := filepath.Join(dir, "tls")
		ca1, err := pki.NewMintingCA("raft-ca-1", "gohookbridge-raft", 365*24*time.Hour)
		assert.NilError(t, err)
		writeClusterTLS(t, ca1, tlsDir, "hot-node")
		cert1PEM := readFile(t, filepath.Join(tlsDir, "tls.crt"))
		cert1, err := pki.ParsePEMCert(cert1PEM)
		assert.NilError(t, err)

		ln := testutil.FreeListener(t)
		rs, err := NewRaftStore(RaftConfig{
			Dir:                   dir,
			NodeID:                "hot-node",
			BindAddr:              testutil.FreeAddrFromListener(t, ln),
			CustomListener:        ln,
			PerformanceMultiplier: 0.2,
			TLSEnabled:            true,
			TLSDir:                tlsDir,
			TLSCACertPath:         filepath.Join(tlsDir, "ca.crt"),
			TLSCertPath:           filepath.Join(tlsDir, "tls.crt"),
			TLSKeyPath:            filepath.Join(tlsDir, "tls.key"),
			TLSReloadPollInterval: 25 * time.Millisecond,
		})
		assert.NilError(t, err)
		t.Cleanup(func() { _ = rs.Shutdown() })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NilError(t, rs.WaitForLeader(ctx))

		assert.Assert(t, rs.node.KeyringManager().CurrentKeyring() == nil, "manual mode must disable the keyring")

		// Corrupt the key file: the watcher must keep serving cert1.
		assert.NilError(t, os.WriteFile(filepath.Join(tlsDir, "tls.key"), []byte("corrupt"), 0o600))
		time.Sleep(80 * time.Millisecond)
		served, err := rs.TLSProvider().GetCertificate(nil)
		assert.NilError(t, err)
		servedCert, err := x509.ParseCertificate(served.Certificate[0])
		assert.NilError(t, err)
		assert.Equal(t, servedCert.SerialNumber.String(), cert1.SerialNumber.String(), "a corrupt replacement must be rejected")

		// Mint ca2 + cert2 and write dual-trust ca2||ca1.
		ca2, err := pki.NewMintingCA("raft-ca-2", "gohookbridge-raft", 365*24*time.Hour)
		assert.NilError(t, err)
		ca2Cert, _, err := ca2.EncodePEM()
		assert.NilError(t, err)
		dualTrust := append(append([]byte{}, ca2Cert...), readFile(t, filepath.Join(tlsDir, "ca.crt"))...)
		assert.NilError(t, os.WriteFile(filepath.Join(tlsDir, "ca.crt"), dualTrust, 0o600))
		cert2PEM, key2PEM, err := ca2.IssuePeerCertificate("hot-node", "gohookbridge-raft", []string{"localhost"}, nil, time.Hour)
		assert.NilError(t, err)
		assert.NilError(t, os.WriteFile(filepath.Join(tlsDir, "tls.crt"), cert2PEM, 0o600))
		assert.NilError(t, os.WriteFile(filepath.Join(tlsDir, "tls.key"), key2PEM, 0o600))
		cert2, err := pki.ParsePEMCert(cert2PEM)
		assert.NilError(t, err)

		deadline := time.Now().Add(2 * time.Second)
		hotSwapped := false
		for time.Now().Before(deadline) {
			served, err := rs.TLSProvider().GetCertificate(nil)
			if err == nil && len(served.Certificate) > 0 {
				if parsed, perr := x509.ParseCertificate(served.Certificate[0]); perr == nil && parsed.SerialNumber.String() == cert2.SerialNumber.String() {
					hotSwapped = true
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		assert.Assert(t, hotSwapped, "valid replacement must hot-swap without restart")
	})

	t.Run("single_node_local_ca_phased_keyring_rotation", func(t *testing.T) {
		sealer, err := storage.NewAESGCMKeeper("k1", map[string][]byte{"k1": randomKey(t)}, AtRestAAD)
		assert.NilError(t, err)

		ln := testutil.FreeListener(t)
		rs, err := NewRaftStore(RaftConfig{
			Dir:                      t.TempDir(),
			NodeID:                   "rotate-node",
			BindAddr:                 testutil.FreeAddrFromListener(t, ln),
			CustomListener:           ln,
			PerformanceMultiplier:    0.2,
			TLSEnabled:               true,
			AtRest:                   sealer,
			TLSRotationPeriod:        150 * time.Millisecond,
			TLSRotationCheckInterval: 40 * time.Millisecond,
		})
		assert.NilError(t, err)
		t.Cleanup(func() { _ = rs.Shutdown() })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		assert.NilError(t, rs.WaitForLeader(ctx))

		// Wait for Term 1 to be seeded.
		var initialKeyID string
		var initialSerial string
		seedDeadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(seedDeadline) {
			if kr := rs.node.KeyringManager().CurrentKeyring(); kr != nil && len(kr.Keys) > 0 {
				initialKeyID = kr.ActiveKeyID
				if active := kr.GetActive(); active != nil {
					if cert, perr := active.ParseCert(); perr == nil {
						initialSerial = cert.SerialNumber.String()
					}
				}
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		assert.Assert(t, initialKeyID != "", "keyring Term 1 must be seeded")

		// Wait for two rotations (Term >= 3) and a new active key.
		rotateDeadline := time.Now().Add(15 * time.Second)
		rotated := false
		for time.Now().Before(rotateDeadline) {
			kr := rs.node.KeyringManager().CurrentKeyring()
			if kr != nil && kr.Term >= 3 && kr.ActiveKeyID != initialKeyID {
				rotated = true
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		assert.Assert(t, rotated, "phased keyring rotation must advance the term and active key")

		served, err := rs.TLSProvider().GetCertificate(nil)
		assert.NilError(t, err)
		servedCert, err := x509.ParseCertificate(served.Certificate[0])
		assert.NilError(t, err)
		assert.Assert(t, servedCert.SerialNumber.String() != initialSerial, "leaf must be re-minted after CA rotation")

		// Writes keep succeeding after rotation.
		assert.NilError(t, rs.CreateChannel(ctx, &domain.Channel{ID: "post-rotation"}))
	})
}

// TestRaftStore_TypedNilSealer is a regression guard: a typed-nil
// *storage.AESGCMKeeper stored in the storage.Sealer interface is non-nil, so
// without normalization every rs.sealer == nil guard would evaluate false and
// Seal/Open would panic on a nil receiver.
func TestRaftStore_TypedNilSealer(t *testing.T) {
	rs := newTestRaftStoreWithConfig(t, RaftConfig{AtRest: (*storage.AESGCMKeeper)(nil)})
	ctx := context.Background()

	assert.Assert(t, rs.sealer == nil, "a typed-nil sealer must be normalized to a nil interface")

	// A sensitive field must round-trip as plaintext (no sealer configured).
	assert.NilError(t, rs.CreateChannel(ctx, &domain.Channel{ID: "typed-nil", WebhookSecret: "plain-secret"}))

	got, err := rs.GetChannel(ctx, "typed-nil")
	assert.NilError(t, err)
	assert.Equal(t, got.WebhookSecret, "plain-secret")

	stored, err := rs.fsm.Get("/channels/typed-nil/")
	assert.NilError(t, err)
	assert.Assert(t, bytes.Contains(stored, []byte("plain-secret")), "without a sealer the value must be stored as plaintext")
}

// TestRaftStore_AtRestKeyFileFallback proves the AtRestKeyFile fallback loads a
// keeper even when AtRest is a typed nil, so sensitive values are sealed on
// write and opened on read.
func TestRaftStore_AtRestKeyFileFallback(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "at-rest-key.json")
	assert.NilError(t, storage.GenerateKeyFile(keyFile))

	rs := newTestRaftStoreWithConfig(t, RaftConfig{
		AtRest:        (*storage.AESGCMKeeper)(nil),
		AtRestKeyFile: keyFile,
	})
	ctx := context.Background()

	assert.Assert(t, rs.sealer != nil, "the AtRestKeyFile fallback must load a keeper")

	assert.NilError(t, rs.CreateChannel(ctx, &domain.Channel{ID: "sealed", WebhookSecret: "sensitive-value"}))

	got, err := rs.GetChannel(ctx, "sealed")
	assert.NilError(t, err)
	assert.Equal(t, got.WebhookSecret, "sensitive-value", "the value must be opened on read")

	stored, err := rs.fsm.Get("/channels/sealed/")
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Contains(stored, []byte("sensitive-value")), "the value must be sealed at rest")
}
