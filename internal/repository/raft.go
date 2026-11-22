package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	goraft "github.com/disaster37/go-raft"
	"github.com/disaster37/go-raft/config"
	"github.com/disaster37/go-raft/discovery"
	goraftfsm "github.com/disaster37/go-raft/fsm"
	raftk8s "github.com/disaster37/go-raft/k8s"
	"github.com/disaster37/go-raft/pki"
	"github.com/disaster37/go-raft/storage"
	"github.com/disaster37/go-raft/transport"
	hraft "github.com/hashicorp/raft"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"github.com/webcenter-fr/gohookbridge/pkg/uuid"
	bolt "go.etcd.io/bbolt"
	"k8s.io/client-go/kubernetes"
)

// RaftStore is the Raft+BoltDB implementation of domain.Repository.
var _ domain.Repository = (*RaftStore)(nil)

// AtRestAAD is the additional authenticated data bound to every at-rest
// ciphertext (F27).
const AtRestAAD = "gohookbridge.at-rest.v1"

// RaftPeer and PeerResolver are re-exported so callers do not need to import
// go-raft/discovery directly.
type RaftPeer = discovery.Peer
type PeerResolver = discovery.PeerResolver

type RaftStore struct {
	node   *goraft.Node
	fsm    *compatFSM
	sealer storage.Sealer
}

type RaftConfig struct {
	Dir                      string
	NodeID                   string
	BindAddr                 string
	AdvertiseAddr            string
	Peers                    []string // legacy "id=addr" entries
	BootstrapPath            string
	Replicas                 int
	StatefulSetName          string
	HeadlessService          string
	Namespace                string
	ClusterDomain            string
	RecoveryMode             bool
	SingleNodeRecovery       bool
	NoSnapshotRestore        bool
	PerformanceMultiplier    float64
	ApplyTimeout             time.Duration
	Resolver                 discovery.PeerResolver
	CustomListener           net.Listener // optional pre-bound listener for race-free tests
	TLSEnabled               bool
	TLSDir                   string
	TLSValidity              time.Duration // 0 -> 8760h (365d) via config.ApplyDefaults
	TLSSafetyMargin          time.Duration // 0 -> 7d (pki.DefaultSafetyMargin) via config.ApplyDefaults
	TLSCACertPath            string
	TLSCertPath              string
	TLSKeyPath               string
	TLSCASecret              string
	TLSCABootstrap           bool
	TLSClientAuth            bool // used when TLSClientAuthSet is true; otherwise defaults to true
	TLSClientAuthSet         bool
	TLSSecretPollTimeout     time.Duration
	TLSKeyringRotation       *bool         // nil = auto per mode (true when TLSEnabled && !manualTLS; false for manual certs)
	TLSRotationPeriod        time.Duration // 0 -> 24h via config.ApplyDefaults
	TLSRotationCheckInterval time.Duration // 0 -> 30s via config.ApplyDefaults
	TLSReloadPollInterval    time.Duration // 0 -> 5s via config.ApplyDefaults
	BootstrapProvider        pki.BootstrapProvider
	GenerationFencer         goraft.GenerationFencer
	ReplicaCountAuthorizer   goraft.ReplicaCountAuthorizer
	Clientset                kubernetes.Interface // optional K8s client for single-node Secret CA bootstrap
	AtRest                   storage.Sealer
	AtRestKeyFile            string
	AutopilotEnabled         *bool // nil = auto (enabled when multi-node, disabled when single-node)
}

// NewRaftStore constructs and starts a go-raft Node wrapped as a
// domain.Repository. It resolves this node's stable ID and whether it seeds the
// initial cluster configuration, opens the shared BoltDB store, creates the
// transport (plaintext or mTLS), and bootstraps the cluster with ONLY itself
// when it is the bootstrap node. Other nodes start empty and join via the
// leader's membership loop. The caller must call WaitForLeader before issuing
// writes.
func NewRaftStore(cfg RaftConfig) (*RaftStore, error) {
	// Fill defaults matching gohookbridge.
	if cfg.Dir == "" {
		cfg.Dir = "./raft-data"
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1:6001"
	}
	if cfg.ApplyTimeout <= 0 {
		cfg.ApplyTimeout = 5 * time.Second
	}
	// CRITICAL: config.DefaultConfig() in go-raft defaults PerformanceMultiplier
	// to 5.0; setting 1.0 when <= 0 preserves the CLI default while Helm
	// explicitly passes 5.0.
	if cfg.PerformanceMultiplier <= 0 {
		cfg.PerformanceMultiplier = 1.0
	}

	peerConfigs := make([]config.PeerConfig, 0, len(cfg.Peers))
	for _, p := range discovery.ParseLegacyPeers(cfg.Peers) {
		peerConfigs = append(peerConfigs, config.PeerConfig{ID: p.ID, Address: p.Address})
	}

	multiNode := cfg.Replicas > 1 || len(peerConfigs) > 1
	if cfg.Resolver != nil {
		if resolved, err := cfg.Resolver.Resolve(); err == nil && len(resolved) > 1 {
			multiNode = true
		}
	}

	if cfg.NodeID == "" && cfg.StatefulSetName == "" && len(peerConfigs) == 0 && cfg.Resolver == nil {
		cfg.NodeID = "node1"
	}

	clientAuth := true
	if cfg.TLSClientAuthSet {
		clientAuth = cfg.TLSClientAuth
	}

	manualTLS := cfg.TLSCACertPath != "" || cfg.TLSCertPath != "" || cfg.TLSKeyPath != ""
	keyringRotation := cfg.TLSEnabled && !manualTLS
	if cfg.TLSKeyringRotation != nil {
		keyringRotation = *cfg.TLSKeyringRotation
	}

	goraftCfg := config.FromGohookbridgeFlags(config.GohookbridgeFlags{
		Dir:                   cfg.Dir,
		NodeID:                cfg.NodeID,
		BindAddr:              cfg.BindAddr,
		AdvertiseAddr:         cfg.AdvertiseAddr,
		Peers:                 peerConfigs,
		Replicas:              cfg.Replicas,
		StatefulSetName:       cfg.StatefulSetName,
		HeadlessService:       cfg.HeadlessService,
		Namespace:             cfg.Namespace,
		ClusterDomain:         cfg.ClusterDomain,
		ApplyTimeout:          cfg.ApplyTimeout,
		PerformanceMultiplier: cfg.PerformanceMultiplier,
		TLSEnabled:            cfg.TLSEnabled,
		TLSDir:                cfg.TLSDir,
		TLSValidity:           cfg.TLSValidity, // 0 -> 8760h via ApplyDefaults
		TLSOrganization:       "gohookbridge-raft",
		CACertPath:            cfg.TLSCACertPath,
		CertPath:              cfg.TLSCertPath,
		KeyPath:               cfg.TLSKeyPath,
		ClientAuth:            clientAuth,
		AtRestKeyFile:         cfg.AtRestKeyFile,
		KeyringRotation:       keyringRotation,
		RotationPeriod:        cfg.TLSRotationPeriod, // 0 -> 24h via ApplyDefaults
	})
	goraftCfg.Storage.Backend = config.StorageBackendSharedBolt
	goraftCfg.Storage.AtRestKeyInfo = AtRestAAD
	goraftCfg.RecoveryMode = cfg.RecoveryMode
	goraftCfg.SingleNodeRecovery = cfg.SingleNodeRecovery
	goraftCfg.NoSnapshotRestoreOnStart = cfg.NoSnapshotRestore
	goraftCfg.TLS.CASecret = cfg.TLSCASecret
	goraftCfg.TLS.CABootstrap = cfg.TLSCABootstrap
	if cfg.TLSSecretPollTimeout > 0 {
		goraftCfg.TLS.SecretPollTimeout = cfg.TLSSecretPollTimeout
	}
	if cfg.TLSSafetyMargin > 0 {
		goraftCfg.TLS.SafetyMargin = cfg.TLSSafetyMargin // else 7d (pki.DefaultSafetyMargin)
	}
	if cfg.TLSRotationCheckInterval > 0 {
		goraftCfg.TLS.RotationCheckInterval = cfg.TLSRotationCheckInterval // else 30s
	}
	if cfg.TLSReloadPollInterval > 0 {
		goraftCfg.TLS.ReloadPollInterval = cfg.TLSReloadPollInterval // else 5s
	}
	if !cfg.TLSEnabled && multiNode {
		goraftCfg.AllowInsecureMultiNode = true
	}
	if cfg.CustomListener != nil {
		// Loopback multi-node tests pass config.Validate's isUnroutable check.
		goraftCfg.AllowInsecureMultiNode = true
	}
	if cfg.AutopilotEnabled != nil {
		goraftCfg.Autopilot.Enabled = *cfg.AutopilotEnabled
	} else {
		goraftCfg.Autopilot.Enabled = multiNode
	}

	// Wire the K8s Secret CA bootstrap provider for single-node Secret CA mode.
	if cfg.TLSEnabled && !multiNode && cfg.BootstrapProvider == nil && cfg.TLSCACertPath == "" && cfg.TLSCASecret != "" && cfg.Clientset != nil && cfg.Namespace != "" {
		tlsDir := cfg.TLSDir
		if tlsDir == "" {
			tlsDir = filepath.Join(cfg.Dir, "tls")
		}
		cfg.BootstrapProvider = raftk8s.NewSecretBootstrapProvider(raftk8s.SecretBootstrapConfig{
			Clientset:    cfg.Clientset,
			Namespace:    cfg.Namespace,
			SecretName:   cfg.TLSCASecret,
			CABootstrap:  cfg.TLSCABootstrap,
			CACommonName: "raft-ca",
			Organization: "gohookbridge-raft",
			PollTimeout:  cfg.TLSSecretPollTimeout,
			CacheDir:     tlsDir,
		})
	}

	// Resolve the at-rest sealer before constructing the node. A typed-nil
	// *storage.AESGCMKeeper wrapped in the storage.Sealer interface is non-nil,
	// so it would defeat the nil guards below and panic on Seal/Open (and defeat
	// go-raft's own nil-sealer fallback).
	sealer := cfg.AtRest
	if keeper, ok := sealer.(*storage.AESGCMKeeper); ok && keeper == nil {
		sealer = nil
	}
	if sealer == nil && cfg.AtRestKeyFile != "" {
		keeper, err := storage.LoadAESGCMKeeper(cfg.AtRestKeyFile, AtRestAAD)
		if err != nil {
			return nil, fmt.Errorf("load at-rest keeper: %w", err)
		}
		sealer = keeper
	}

	cfsm := newCompatFSM(nil)
	node, err := goraft.NewNode(context.Background(), &goraftCfg, goraft.NodeOptions{
		FSM:                    cfsm,
		Resolver:               cfg.Resolver,
		BootstrapProvider:      cfg.BootstrapProvider,
		GenerationFencer:       cfg.GenerationFencer,
		ReplicaCountAuthorizer: cfg.ReplicaCountAuthorizer,
		Sealer:                 sealer,
		CustomListener:         cfg.CustomListener,
	})
	if err != nil {
		return nil, err
	}
	cfsm.bindDB(func() *bolt.DB {
		if node.Storage() == nil {
			return nil
		}
		return node.Storage().BoltDB()
	})
	if err := node.Start(context.Background()); err != nil {
		return nil, err
	}

	return &RaftStore{node: node, fsm: cfsm, sealer: sealer}, nil
}

// sealString encrypts a sensitive string for persistence (no-op for the empty
// string or when at-rest encryption is disabled).
func (rs *RaftStore) sealString(plain string) (string, error) {
	if rs.sealer == nil || plain == "" {
		return plain, nil
	}
	return rs.sealer.SealString(plain)
}

// openString decrypts a persisted sensitive string. A value that is not an
// EncryptedValue envelope is treated as legacy plaintext and returned as-is
// (lazy migration).
func (rs *RaftStore) openString(stored string) (string, error) {
	if rs.sealer == nil || stored == "" {
		return stored, nil
	}
	return rs.sealer.OpenString(stored)
}

func (rs *RaftStore) sealChannel(p *domain.Channel) error {
	if rs.sealer == nil {
		return nil
	}
	var err error
	if p.WebhookSecret, err = rs.sealString(p.WebhookSecret); err != nil {
		return err
	}
	if p.EncryptionKey, err = rs.sealString(p.EncryptionKey); err != nil {
		return err
	}
	if p.EncryptionPrivateKey, err = rs.sealString(p.EncryptionPrivateKey); err != nil {
		return err
	}
	for i := range p.WebhookSignatures { //nolint:staticcheck // seal deprecated legacy field if present
		if p.WebhookSignatures[i], err = rs.sealString(p.WebhookSignatures[i]); err != nil { //nolint:staticcheck // seal deprecated legacy field if present
			return err
		}
	}
	return nil
}

func (rs *RaftStore) openChannel(p *domain.Channel) error {
	if rs.sealer == nil {
		return nil
	}
	var err error
	if p.WebhookSecret, err = rs.openString(p.WebhookSecret); err != nil {
		return err
	}
	if p.EncryptionKey, err = rs.openString(p.EncryptionKey); err != nil {
		return err
	}
	if p.EncryptionPrivateKey, err = rs.openString(p.EncryptionPrivateKey); err != nil {
		return err
	}
	for i := range p.WebhookSignatures { //nolint:staticcheck // open deprecated legacy field if present
		if p.WebhookSignatures[i], err = rs.openString(p.WebhookSignatures[i]); err != nil { //nolint:staticcheck // open deprecated legacy field if present
			return err
		}
	}
	return nil
}

func (rs *RaftStore) sealConfig(cfg *domain.GlobalConfig) error {
	if rs.sealer == nil {
		return nil
	}
	var err error
	if cfg.Server.SessionSecret, err = rs.sealString(cfg.Server.SessionSecret); err != nil {
		return err
	}
	cfg.Defaults.WebhookSecret, err = rs.sealString(cfg.Defaults.WebhookSecret)
	return err
}

func (rs *RaftStore) openConfig(cfg *domain.GlobalConfig) error {
	if rs.sealer == nil {
		return nil
	}
	var err error
	if cfg.Server.SessionSecret, err = rs.openString(cfg.Server.SessionSecret); err != nil {
		return err
	}
	cfg.Defaults.WebhookSecret, err = rs.openString(cfg.Defaults.WebhookSecret)
	return err
}

// applyResponse converts the FSM's Apply response into an error. The FSM
// returns an error value for rejected mutations (e.g. duplicate channel); only
// surfacing it lets callers translate it back to domain.ErrAlreadyExists.
func applyResponse(resp interface{}) error {
	if resp == nil {
		return nil
	}
	if err, ok := resp.(error); ok && err != nil {
		return err
	}
	return nil
}

// putValue writes a key via a replicated set command. The request context is
// detached from cancellation: the legacy repository never honored ctx
// cancellation for writes (e.g. the SSE handler persists a client cursor after
// the client disconnects), so preserving that behavior avoids a regression.
func (rs *RaftStore) putValue(ctx context.Context, key string, val []byte) error {
	resp, err := rs.node.ApplyCommand(context.WithoutCancel(ctx), goraftfsm.KindSet, goraftfsm.KVEntry{Key: key, Value: val})
	if err != nil {
		return err
	}
	return applyResponse(resp)
}

// createValue writes a key only when absent, mapping the FSM conflict to
// domain.ErrAlreadyExists.
func (rs *RaftStore) createValue(ctx context.Context, key string, val []byte, entity, id string) error {
	resp, err := rs.node.ApplyCommand(context.WithoutCancel(ctx), goraftfsm.KindCreate, goraftfsm.KVEntry{Key: key, Value: val})
	if err != nil {
		return err
	}
	if respErr, ok := resp.(error); ok && errors.Is(respErr, goraftfsm.ErrConflict) {
		return fmt.Errorf("%w: %s %q", domain.ErrAlreadyExists, entity, id)
	}
	return applyResponse(resp)
}

// deleteValue removes a key via a replicated delete command.
func (rs *RaftStore) deleteValue(ctx context.Context, key string) error {
	resp, err := rs.node.ApplyCommand(context.WithoutCancel(ctx), goraftfsm.KindDelete, goraftfsm.KVEntry{Key: key})
	if err != nil {
		return err
	}
	return applyResponse(resp)
}

// applyMulti applies several key mutations as a single Raft entry (one FSM
// transaction), so multi-record writes are atomic across the cluster.
func (rs *RaftStore) applyMulti(ctx context.Context, entries []goraftfsm.KVEntry) error {
	return rs.node.ApplyMulti(context.WithoutCancel(ctx), entries)
}

// applyRaw submits a raw FSM command (e.g. the legacy bootstrap payload).
func (rs *RaftStore) applyRaw(ctx context.Context, data []byte) error {
	resp, err := rs.node.Apply(context.WithoutCancel(ctx), data)
	if err != nil {
		return err
	}
	return applyResponse(resp)
}

// getValue reads a key from the FSM (nil when absent).
func (rs *RaftStore) getValue(key string) ([]byte, error) {
	return rs.fsm.Get(key)
}

// listKeys returns the keys under prefix.
func (rs *RaftStore) listKeys(prefix string) ([]string, error) {
	entries, err := rs.fsm.ListPrefix(prefix)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	return keys, nil
}

// Lifecycle and health delegates.

func (rs *RaftStore) WaitForLeader(ctx context.Context) error {
	return rs.node.WaitForLeader(ctx)
}

func (rs *RaftStore) WaitForSelfLeadership(ctx context.Context) error {
	return rs.node.WaitForSelfLeadership(ctx)
}

func (rs *RaftStore) WaitForCleanState(ctx context.Context) error {
	return rs.node.WaitForCleanState(ctx)
}

func (rs *RaftStore) IsLeader() bool { return rs.node.IsLeader() }

func (rs *RaftStore) IsCleanState() bool { return rs.node.IsCleanState() }

func (rs *RaftStore) IsStarted() bool { return rs.node.IsStarted() }

func (rs *RaftStore) IsVoter() bool { return rs.node.IsVoter() }

func (rs *RaftStore) LeaderAddress() string { return rs.node.LeaderAddress() }

func (rs *RaftStore) GetConfiguration() (hraft.Configuration, error) {
	return rs.node.GetConfiguration()
}

func (rs *RaftStore) AddVoter(id, address string, timeout time.Duration) error {
	return rs.node.AddVoter(id, address, timeout)
}

func (rs *RaftStore) RemoveServer(id string, timeout time.Duration) error {
	return rs.node.RemoveServer(id, timeout)
}

func (rs *RaftStore) ReconcileMembership(desired []RaftPeer, timeout time.Duration) (added, updated, removed []string, err error) {
	return rs.node.ReconcileMembership(desired, timeout)
}

func (rs *RaftStore) StepDown(ctx context.Context) error { return rs.node.StepDown(ctx) }

func (rs *RaftStore) TLSProvider() *transport.DynamicTLSProvider { return rs.node.TLSProvider() }

func (rs *RaftStore) HasData() (bool, error) { return rs.fsm.HasData() }

func (rs *RaftStore) Close() error { return rs.node.Close() }

func (rs *RaftStore) Shutdown() error { return rs.node.Shutdown() }

// CRUD methods (domain.Repository).

func (rs *RaftStore) GetChannel(ctx context.Context, id string) (*domain.Channel, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: channel ID required", domain.ErrInvalidArgument)
	}
	val, err := rs.getValue("/channels/" + id + "/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("%w: channel %q", domain.ErrNotFound, id)
	}
	var ch domain.Channel
	if err := json.Unmarshal(val, &ch); err != nil {
		return nil, err
	}
	if err := rs.openChannel(&ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

func (rs *RaftStore) ListChannels(ctx context.Context) ([]*domain.Channel, error) {
	keys, err := rs.listKeys("/channels/")
	if err != nil {
		return nil, err
	}
	var channels []*domain.Channel
	seen := make(map[string]bool)
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/channels/"), "/")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		id := parts[0]
		if seen[id] {
			continue
		}
		seen[id] = true
		val, err := rs.getValue("/channels/" + id + "/")
		if err != nil {
			return nil, err
		}
		if val == nil {
			continue
		}
		var ch domain.Channel
		if err := json.Unmarshal(val, &ch); err != nil {
			return nil, fmt.Errorf("unmarshal channel %q: %w", id, err)
		}
		if err := rs.openChannel(&ch); err != nil {
			return nil, err
		}
		domain.MigrateChannel(&ch)
		channels = append(channels, &ch)
	}
	return channels, nil
}

func (rs *RaftStore) CreateChannel(ctx context.Context, p *domain.Channel) error {
	if p.ID == "" {
		return fmt.Errorf("%w: channel ID required", domain.ErrInvalidArgument)
	}
	sealed := *p
	if err := rs.sealChannel(&sealed); err != nil {
		return err
	}
	val, err := json.Marshal(&sealed)
	if err != nil {
		return err
	}
	return rs.createValue(ctx, "/channels/"+p.ID+"/", val, "channel", p.ID)
}

func (rs *RaftStore) UpdateChannel(ctx context.Context, p *domain.Channel) error {
	if p.ID == "" {
		return fmt.Errorf("%w: channel ID required", domain.ErrInvalidArgument)
	}
	sealed := *p
	if err := rs.sealChannel(&sealed); err != nil {
		return err
	}
	val, err := json.Marshal(&sealed)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/channels/"+p.ID+"/", val)
}

func (rs *RaftStore) DeleteChannel(ctx context.Context, id string) error {
	return rs.deleteValue(ctx, "/channels/"+id+"/")
}

func (rs *RaftStore) GetGlobalConfig(ctx context.Context) (*domain.GlobalConfig, error) {
	cfg := domain.DefaultGlobalConfig()

	val, err := rs.getValue("/global/server/")
	if err != nil {
		return nil, err
	}
	if val != nil {
		var sc domain.ServerConfig
		if err := json.Unmarshal(val, &sc); err != nil {
			return nil, fmt.Errorf("unmarshal server config: %w", err)
		}
		cfg.Server = sc
	}

	val, err = rs.getValue("/global/defaults/")
	if err != nil {
		return nil, err
	}
	if val != nil {
		var dc domain.DefaultChannelConfig
		if err := json.Unmarshal(val, &dc); err != nil {
			return nil, fmt.Errorf("unmarshal defaults config: %w", err)
		}
		cfg.Defaults = dc
	}

	if err := rs.openConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (rs *RaftStore) UpdateGlobalConfig(ctx context.Context, cfg *domain.GlobalConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: config required", domain.ErrInvalidArgument)
	}
	sealed := *cfg
	if err := rs.sealConfig(&sealed); err != nil {
		return err
	}
	//nolint:gosec
	scVal, err := json.Marshal(sealed.Server)
	if err != nil {
		return err
	}
	dcVal, err := json.Marshal(sealed.Defaults)
	if err != nil {
		return err
	}
	// Both records are written in a single transaction so the server and
	// defaults can never diverge.
	return rs.applyMulti(ctx, []goraftfsm.KVEntry{
		{Key: "/global/server/", Value: scVal},
		{Key: "/global/defaults/", Value: dcVal},
	})
}

func (rs *RaftStore) GetUser(ctx context.Context, id string) (*domain.User, error) {
	val, err := rs.getValue("/users/" + id + "/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("%w: user %q", domain.ErrNotFound, id)
	}
	var u domain.User
	if err := json.Unmarshal(val, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

type usernameIndex struct {
	UserID string `json:"user_id"`
}

func usernameIndexKey(username string) string {
	return "/users/by-username/" + username
}

func usernameIndexValue(userID string) []byte {
	idx := usernameIndex{UserID: userID}
	val, err := json.Marshal(idx)
	if err != nil {
		return nil
	}
	return val
}

func (rs *RaftStore) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	idxVal, err := rs.getValue("/users/by-username/" + username + "/")
	if err != nil || idxVal == nil {
		return nil, fmt.Errorf("%w: user %q", domain.ErrNotFound, username)
	}
	var idx usernameIndex
	if err := json.Unmarshal(idxVal, &idx); err != nil {
		return nil, fmt.Errorf("%w: user %q", domain.ErrNotFound, username)
	}
	return rs.GetUser(ctx, idx.UserID)
}

// GetUserByExternalIdentity resolves the user bound to an OIDC (provider,
// issuer, subject) tuple by scanning the user set. issuer must already be
// normalized (no trailing slash) by the caller.
func (rs *RaftStore) GetUserByExternalIdentity(ctx context.Context, providerID, issuer, subject string) (*domain.User, error) {
	if providerID == "" || issuer == "" || subject == "" {
		return nil, fmt.Errorf("%w: external identity incomplete", domain.ErrInvalidArgument)
	}
	users, err := rs.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		for _, id := range u.ExternalIdentities {
			if id.ProviderID == providerID && id.Issuer == issuer && id.Subject == subject {
				return u, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: no user for external identity %s/%s/%s", domain.ErrNotFound, providerID, issuer, subject)
}

func (rs *RaftStore) ListUsers(ctx context.Context) ([]*domain.User, error) {
	keys, err := rs.listKeys("/users/")
	if err != nil {
		return nil, err
	}
	var users []*domain.User
	seen := make(map[string]bool)
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/users/"), "/")
		if len(parts) < 1 || parts[0] == "" || parts[0] == "by-username" {
			continue
		}
		id := parts[0]
		if seen[id] {
			continue
		}
		seen[id] = true
		val, err := rs.getValue("/users/" + id + "/")
		if err != nil || val == nil {
			continue
		}
		var u domain.User
		if err := json.Unmarshal(val, &u); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

func (rs *RaftStore) CreateUser(ctx context.Context, u *domain.User) error {
	if u.ID == "" {
		u.ID = u.Username
	}
	if u.ID == "" {
		return fmt.Errorf("%w: user ID required", domain.ErrInvalidArgument)
	}
	val, err := json.Marshal(u)
	if err != nil {
		return err
	}
	err = rs.applyMulti(ctx, []goraftfsm.KVEntry{
		{Key: "/users/" + u.ID + "/", Value: val, Create: true},
		{Key: usernameIndexKey(u.Username) + "/", Value: usernameIndexValue(u.ID)},
	})
	if err != nil && errors.Is(err, goraftfsm.ErrConflict) {
		return fmt.Errorf("%w: user %q", domain.ErrAlreadyExists, u.ID)
	}
	return err
}

func (rs *RaftStore) UpdateUser(ctx context.Context, u *domain.User) error {
	old, err := rs.GetUser(ctx, u.ID)
	oldUsername := ""
	if err == nil && old.Username != u.Username && old.Username != "" {
		oldUsername = old.Username
	}
	val, err := json.Marshal(u)
	if err != nil {
		return err
	}
	entries := []goraftfsm.KVEntry{
		{Key: "/users/" + u.ID + "/", Value: val},
		{Key: usernameIndexKey(u.Username) + "/", Value: usernameIndexValue(u.ID)},
	}
	if oldUsername != "" {
		entries = append(entries, goraftfsm.KVEntry{Key: "/users/by-username/" + oldUsername + "/", Delete: true})
	}
	return rs.applyMulti(ctx, entries)
}

func (rs *RaftStore) DeleteUser(ctx context.Context, id string) error {
	u, err := rs.GetUser(ctx, id)
	if err != nil {
		return err
	}
	return rs.applyMulti(ctx, []goraftfsm.KVEntry{
		{Key: "/users/" + id + "/", Delete: true},
		{Key: "/users/by-username/" + u.Username + "/", Delete: true},
	})
}

func (rs *RaftStore) GetSetupModeEndTime(ctx context.Context) time.Time {
	val, err := rs.getValue("/meta/setup_end")
	if err != nil || val == nil {
		return time.Time{}
	}
	var t time.Time
	if err := json.Unmarshal(val, &t); err != nil {
		return time.Time{}
	}
	return t
}

func (rs *RaftStore) SetSetupModeEndTime(ctx context.Context, t time.Time) error {
	val, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/meta/setup_end", val)
}

// MigrateAtRest re-writes every sensitive record through the configured
// sealer, sealing any legacy plaintext values and re-sealing under the current
// primary key (lazy migration + key rotation).
func (rs *RaftStore) MigrateAtRest(ctx context.Context) error {
	if rs.sealer == nil {
		return fmt.Errorf("at-rest encryption is not configured")
	}
	channels, err := rs.ListChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if err := rs.UpdateChannel(ctx, ch); err != nil {
			return err
		}
	}
	cfg, err := rs.GetGlobalConfig(ctx)
	if err != nil {
		return err
	}
	if err := rs.UpdateGlobalConfig(ctx, cfg); err != nil {
		return err
	}
	providers, err := rs.OIDCProviders(ctx)
	if err != nil {
		return err
	}
	if len(providers) > 0 {
		if err := rs.SetOIDCProviders(ctx, providers); err != nil {
			return err
		}
	}
	return nil
}

// GetAuthEpoch returns the persisted session/auth epoch (0 when unset).
func (rs *RaftStore) GetAuthEpoch(ctx context.Context) (int64, error) {
	val, err := rs.getValue("/meta/auth_epoch")
	if err != nil {
		return 0, err
	}
	if val == nil {
		return 0, nil
	}
	var epoch int64
	if err := json.Unmarshal(val, &epoch); err != nil {
		return 0, err
	}
	return epoch, nil
}

// BumpAuthEpoch increments the session/auth epoch, invalidating every
// outstanding session across the cluster.
func (rs *RaftStore) BumpAuthEpoch(ctx context.Context) error {
	current, err := rs.GetAuthEpoch(ctx)
	if err != nil {
		return err
	}
	val, err := json.Marshal(current + 1)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/meta/auth_epoch", val)
}

func (rs *RaftStore) OIDCProviders(ctx context.Context) ([]domain.OIDCProvider, error) {
	val, err := rs.getValue("/global/auth/oidc_providers")
	if err != nil || val == nil {
		return nil, err
	}
	var providers []domain.OIDCProvider
	if err := json.Unmarshal(val, &providers); err != nil {
		return nil, err
	}
	for i := range providers {
		opened, err := rs.openString(providers[i].ClientSecret)
		if err != nil {
			return nil, err
		}
		providers[i].ClientSecret = opened
	}
	return providers, nil
}

func (rs *RaftStore) SetOIDCProviders(ctx context.Context, providers []domain.OIDCProvider) error {
	sealed := make([]domain.OIDCProvider, len(providers))
	copy(sealed, providers)
	for i := range sealed {
		s, err := rs.sealString(sealed[i].ClientSecret)
		if err != nil {
			return err
		}
		sealed[i].ClientSecret = s
	}
	//nolint:gosec
	val, err := json.Marshal(sealed)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/global/auth/oidc_providers", val)
}

func (rs *RaftStore) InternalAuthEnabled(ctx context.Context) (*bool, error) {
	val, err := rs.getValue("/global/auth/internal_enabled")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	var enabled bool
	if err := json.Unmarshal(val, &enabled); err != nil {
		return nil, err
	}
	return &enabled, nil
}

func (rs *RaftStore) SetInternalAuthEnabled(ctx context.Context, enabled bool) error {
	val, err := json.Marshal(enabled)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/global/auth/internal_enabled", val)
}

func (rs *RaftStore) GetRole(ctx context.Context, name string) (*domain.Role, error) {
	val, err := rs.getValue("/rbac/roles/" + name + "/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		for _, r := range domain.DefaultRoles {
			if r.Name == name {
				return &r, nil
			}
		}
		return nil, fmt.Errorf("%w: role %q", domain.ErrNotFound, name)
	}
	var r domain.Role
	if err := json.Unmarshal(val, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (rs *RaftStore) ListRoles(ctx context.Context) ([]domain.Role, error) {
	roles := make([]domain.Role, 0, len(domain.DefaultRoles))
	roles = append(roles, domain.DefaultRoles...)

	keys, err := rs.listKeys("/rbac/roles/")
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/rbac/roles/"), "/")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		name := parts[0]
		if isDefaultRole(name) {
			continue
		}
		val, err := rs.getValue(key)
		if err != nil || val == nil {
			continue
		}
		var r domain.Role
		if err := json.Unmarshal(val, &r); err != nil {
			continue
		}
		roles = append(roles, r)
	}
	return roles, nil
}

func (rs *RaftStore) CreateRole(ctx context.Context, r domain.Role) error {
	if isDefaultRole(r.Name) {
		return fmt.Errorf("%w: role %q", domain.ErrAlreadyExists, r.Name)
	}
	val, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/rbac/roles/"+r.Name+"/", val)
}

func isDefaultRole(name string) bool {
	for _, r := range domain.DefaultRoles {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (rs *RaftStore) GetClientCursor(ctx context.Context, channel, clientID string) (*domain.ClientCursor, error) {
	key := "/cursors/" + channel + "/" + clientID + "/"
	val, err := rs.getValue(key)
	if err != nil || val == nil {
		return nil, fmt.Errorf("%w: client cursor %s/%s", domain.ErrNotFound, channel, clientID)
	}
	var c domain.ClientCursor
	if err := json.Unmarshal(val, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (rs *RaftStore) SetClientCursor(ctx context.Context, cursor *domain.ClientCursor) error {
	key := "/cursors/" + cursor.Channel + "/" + cursor.ClientID + "/"
	val, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, key, val)
}

func (rs *RaftStore) CreateRoleMapping(ctx context.Context, m *domain.RoleMapping) error {
	// Check for existing identical mapping to make this idempotent
	existing, _ := rs.ListRoleMappings(ctx)
	for _, e := range existing {
		if e.Type == m.Type && e.Subject == m.Subject && e.Role == m.Role && e.ChannelScope == m.ChannelScope {
			return nil // already exists, idempotent
		}
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		return err
	}
	m.ID = id
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/rbac/mappings/"+m.ID+"/", val)
}

func (rs *RaftStore) ListRoleMappings(ctx context.Context) ([]domain.RoleMapping, error) {
	keys, err := rs.listKeys("/rbac/mappings/")
	if err != nil {
		return nil, err
	}
	mappings := make([]domain.RoleMapping, 0)
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/rbac/mappings/"), "/")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}
		val, err := rs.getValue(key)
		if err != nil || val == nil {
			continue
		}
		var m domain.RoleMapping
		if err := json.Unmarshal(val, &m); err != nil {
			continue
		}
		mappings = append(mappings, m)
	}
	return mappings, nil
}

func (rs *RaftStore) DeleteRoleMapping(ctx context.Context, id string) error {
	return rs.deleteValue(ctx, "/rbac/mappings/"+id+"/")
}

func (rs *RaftStore) GetUserRoleMappings(ctx context.Context, userID string) ([]domain.RoleMapping, error) {
	all, err := rs.ListRoleMappings(ctx)
	if err != nil {
		return nil, err
	}
	var result []domain.RoleMapping
	for _, m := range all {
		if m.Type == "user" && m.Subject == userID {
			result = append(result, m)
		}
	}
	return result, nil
}

func (rs *RaftStore) GetGroupRoleMappings(ctx context.Context, groupName string) ([]domain.RoleMapping, error) {
	all, err := rs.ListRoleMappings(ctx)
	if err != nil {
		return nil, err
	}
	var result []domain.RoleMapping
	for _, m := range all {
		if m.Type == "group" && m.Subject == groupName {
			result = append(result, m)
		}
	}
	return result, nil
}

func (rs *RaftStore) CreateChannelRoleMapping(ctx context.Context, m *domain.ChannelRoleMapping) error {
	// Check for existing identical mapping to make this idempotent
	existing, _ := rs.ListChannelRoleMappings(ctx, m.ChannelID)
	for _, e := range existing {
		if e.Type == m.Type && e.Subject == m.Subject && e.Role == m.Role {
			return nil // already exists, idempotent
		}
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		return err
	}
	m.ID = id
	if m.ChannelID == "" {
		return fmt.Errorf("%w: channel_id required", domain.ErrInvalidArgument)
	}
	val, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return rs.putValue(ctx, "/channels/"+m.ChannelID+"/acl/"+m.ID+"/", val)
}

func (rs *RaftStore) ListChannelRoleMappings(ctx context.Context, channelID string) ([]domain.ChannelRoleMapping, error) {
	keys, err := rs.listKeys("/channels/" + channelID + "/acl/")
	if err != nil {
		return nil, err
	}
	var mappings []domain.ChannelRoleMapping
	for _, key := range keys {
		val, err := rs.getValue(key)
		if err != nil || val == nil {
			continue
		}
		var m domain.ChannelRoleMapping
		if err := json.Unmarshal(val, &m); err != nil {
			continue
		}
		mappings = append(mappings, m)
	}
	if mappings == nil {
		mappings = make([]domain.ChannelRoleMapping, 0)
	}
	return mappings, nil
}

func (rs *RaftStore) DeleteChannelRoleMapping(ctx context.Context, channelID, entryID string) error {
	return rs.deleteValue(ctx, "/channels/"+channelID+"/acl/"+entryID+"/")
}

func (rs *RaftStore) GetUserChannelRoleMappings(ctx context.Context, userID string) ([]domain.ChannelRoleMapping, error) {
	channels, err := rs.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	var result []domain.ChannelRoleMapping
	for _, ch := range channels {
		acls, err := rs.ListChannelRoleMappings(ctx, ch.ID)
		if err != nil {
			continue
		}
		for _, a := range acls {
			if a.Type == "user" && a.Subject == userID {
				result = append(result, a)
			}
		}
	}
	return result, nil
}

func (rs *RaftStore) GetGroupChannelRoleMappings(ctx context.Context, groupName string) ([]domain.ChannelRoleMapping, error) {
	channels, err := rs.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	var result []domain.ChannelRoleMapping
	for _, ch := range channels {
		acls, err := rs.ListChannelRoleMappings(ctx, ch.ID)
		if err != nil {
			continue
		}
		for _, a := range acls {
			if a.Type == "group" && a.Subject == groupName {
				result = append(result, a)
			}
		}
	}
	return result, nil
}
