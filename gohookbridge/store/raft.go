package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/raft"
	"go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

type RaftStore struct {
	raft *raft.Raft
	fsm  *FSM
	db   *bbolt.DB
}

type RaftConfig struct {
	Dir           string
	NodeID        string
	BindAddr      string
	Peers         []string
	BootstrapPath string
}

func NewRaftStore(cfg RaftConfig) (*RaftStore, error) {
	if cfg.Dir == "" {
		cfg.Dir = "./raft-data"
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "node1"
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1:6001"
	}

	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("create raft dir: %w", err)
	}

	db, err := newBoltDB(cfg.Dir, cfg.NodeID)
	if err != nil {
		return nil, fmt.Errorf("create bolt db: %w", err)
	}

	fsm := NewFSM(db)

	logStore := newBoltLogStore(db)
	stableStore := newBoltStableStore(db)

	snapStore, err := raft.NewFileSnapshotStore(cfg.Dir, 2, os.Stderr)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create snapshot store: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddr, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create transport: %w", err)
	}

	raftCfg := &raft.Config{
		LocalID:              raft.ServerID(cfg.NodeID),
		ProtocolVersion:      raft.ProtocolVersionMax,
		HeartbeatTimeout:     1 * time.Second,
		ElectionTimeout:      1 * time.Second,
		LeaderLeaseTimeout:   500 * time.Millisecond,
		CommitTimeout:        50 * time.Millisecond,
		MaxAppendEntries:     64,
		ShutdownOnRemove:     true,
		TrailingLogs:         10240,
		SnapshotInterval:     120 * time.Second,
		SnapshotThreshold:    8192,
		NoSnapshotRestoreOnStart: true,
	}

	r, err := raft.NewRaft(raftCfg, fsm, logStore, stableStore, snapStore, transport)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("new raft: %w", err)
	}

	rs := &RaftStore{
		raft: r,
		fsm:  fsm,
		db:   db,
	}

	if err := rs.bootstrap(cfg); err != nil {
		r.Shutdown()
		db.Close()
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	return rs, nil
}

func (rs *RaftStore) bootstrap(cfg RaftConfig) error {
	hasData, err := rs.HasData()
	if err != nil {
		return err
	}

	if !hasData {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raft.ServerID(cfg.NodeID),
					Address: raft.ServerAddress(cfg.BindAddr),
				},
			},
		}
		for _, peer := range cfg.Peers {
			parts := strings.SplitN(peer, "=", 2)
			if len(parts) == 2 {
				configuration.Servers = append(configuration.Servers, raft.Server{
					ID:      raft.ServerID(parts[0]),
					Address: raft.ServerAddress(parts[1]),
				})
			}
		}
		future := rs.raft.BootstrapCluster(configuration)
		if err := future.Error(); err != nil {
			return err
		}

		if err := rs.WaitForLeader(10 * time.Second); err != nil {
			return err
		}

		if cfg.BootstrapPath != "" && !hasData {
			bootstrapCfg, err := LoadBootstrap(cfg.BootstrapPath)
			if err != nil {
				return fmt.Errorf("load bootstrap: %w", err)
			}
			if err := rs.ApplyBootstrap(bootstrapCfg); err != nil {
				return fmt.Errorf("apply bootstrap: %w", err)
			}
		}
	} else if len(cfg.Peers) == 0 {
		_ = rs.WaitForLeader(10 * time.Second)
	}

	return nil
}

func (rs *RaftStore) IsLeader() bool {
	return rs.raft.State() == raft.Leader
}

func (rs *RaftStore) WaitForLeader(timeout time.Duration) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.After(timeout)

	for {
		select {
		case <-timer:
			return fmt.Errorf("timeout waiting for leader")
		case <-ticker.C:
			if rs.IsLeader() {
				return nil
			}
		}
	}
}

func (rs *RaftStore) Apply(cmd []byte) (interface{}, error) {
	if !rs.IsLeader() {
		return nil, fmt.Errorf("not the leader")
	}
	future := rs.raft.Apply(cmd, 10*time.Second)
	if err := future.Error(); err != nil {
		return nil, err
	}
	return future.Response(), nil
}

func (rs *RaftStore) applyCommand(op, key string, value []byte) error {
	cmd := fsmCommand{
		Op:  op,
		Key: key,
	}
	if value != nil {
		cmd.Value = value
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = rs.Apply(data)
	return err
}

func (rs *RaftStore) HasData() (bool, error) {
	keys, err := listFSMKeys(rs.db, "/")
	if err != nil {
		return false, err
	}
	return len(keys) > 0, nil
}

func (rs *RaftStore) Shutdown() error {
	future := rs.raft.Shutdown()
	if err := future.Error(); err != nil {
		return err
	}
	return rs.db.Close()
}

func (rs *RaftStore) GetChannel(id string) (*Channel, error) {
	if id == "" {
		return nil, fmt.Errorf("channel ID required")
	}
	val, err := getFSMValue(rs.db, "/channels/"+id+"/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("channel %q not found", id)
	}
	var ch Channel
	if err := json.Unmarshal(val, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

func (rs *RaftStore) ListChannels() ([]*Channel, error) {
	keys, err := listFSMKeys(rs.db, "/channels/")
	if err != nil {
		return nil, err
	}
	var channels []*Channel
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
		val, err := getFSMValue(rs.db, "/channels/"+id+"/")
		if err != nil || val == nil {
			continue
		}
		var ch Channel
		if err := json.Unmarshal(val, &ch); err != nil {
			continue
		}
		migrateChannel(&ch)
		channels = append(channels, &ch)
	}
	return channels, nil
}

func (rs *RaftStore) CreateChannel(p *Channel) error {
	if p.ID == "" {
		return fmt.Errorf("channel ID required")
	}
	val, err := json.Marshal(p)
	if err != nil {
		return err
	}
	cmd := fsmCommand{
		Op:    "create-channel",
		Key:   "/channels/" + p.ID + "/",
		Value: val,
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	resp, err := rs.Apply(data)
	if err != nil {
		return err
	}
	if resp != nil {
		return fmt.Errorf("%v", resp)
	}
	return nil
}

func (rs *RaftStore) UpdateChannel(p *Channel) error {
	if p.ID == "" {
		return fmt.Errorf("channel ID required")
	}
	val, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/channels/"+p.ID+"/", val)
}

func (rs *RaftStore) DeleteChannel(id string) error {
	return rs.applyCommand("delete", "/channels/"+id+"/", nil)
}

func (rs *RaftStore) GetGlobalConfig() (*GlobalConfig, error) {
	cfg := defaultGlobalConfig()

	val, err := getFSMValue(rs.db, "/global/server/")
	if err != nil {
		return nil, err
	}
	if val != nil {
		var sc ServerConfig
		if err := json.Unmarshal(val, &sc); err == nil {
			cfg.Server = sc
		}
	}

	val, err = getFSMValue(rs.db, "/global/defaults/")
	if err != nil {
		return nil, err
	}
	if val != nil {
		var dc DefaultChannelConfig
		if err := json.Unmarshal(val, &dc); err == nil {
			cfg.Defaults = dc
		}
	}

	return cfg, nil
}

func (rs *RaftStore) UpdateGlobalConfig(cfg *GlobalConfig) error {
	if cfg == nil {
		return fmt.Errorf("config required")
	}
	scVal, err := json.Marshal(cfg.Server)
	if err != nil {
		return err
	}
	if err := rs.applyCommand("set", "/global/server/", scVal); err != nil {
		return err
	}
	dcVal, err := json.Marshal(cfg.Defaults)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/global/defaults/", dcVal)
}

func (rs *RaftStore) ResolveChannelConfig(id string) (*Channel, error) {
	ch, err := rs.GetChannel(id)
	if err != nil {
		global, _ := rs.GetGlobalConfig()
		return &Channel{
			ID:                id,
			MaxBodySize:       global.Server.MaxBodySize,
			WebhookSecret:     global.Defaults.WebhookSecret,
			AllowedIPs:        global.Defaults.AllowedIPs,
			ReplayToken:       global.Defaults.ReplayToken,
			MessageTTLSeconds: global.Defaults.MessageTTLSeconds,
		}, nil
	}
	global, _ := rs.GetGlobalConfig()
	return resolveChannelConfig(ch, global), nil
}

func (rs *RaftStore) GetUser(id string) (*User, error) {
	val, err := getFSMValue(rs.db, "/users/"+id+"/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, fmt.Errorf("user %q not found", id)
	}
	var u User
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
	val, _ := json.Marshal(idx)
	return val
}

func (rs *RaftStore) GetUserByUsername(username string) (*User, error) {
	idxVal, err := getFSMValue(rs.db, "/users/by-username/"+username+"/")
	if err != nil || idxVal == nil {
		return nil, fmt.Errorf("user %q not found", username)
	}
	var idx usernameIndex
	if err := json.Unmarshal(idxVal, &idx); err != nil {
		return nil, fmt.Errorf("user %q not found", username)
	}
	return rs.GetUser(idx.UserID)
}

func (rs *RaftStore) ListUsers() ([]*User, error) {
	keys, err := listFSMKeys(rs.db, "/users/")
	if err != nil {
		return nil, err
	}
	var users []*User
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
		val, err := getFSMValue(rs.db, "/users/"+id+"/")
		if err != nil || val == nil {
			continue
		}
		var u User
		if err := json.Unmarshal(val, &u); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

func (rs *RaftStore) CreateUser(u *User) error {
	if u.ID == "" {
		u.ID = u.Username
	}
	if u.ID == "" {
		return fmt.Errorf("user ID required")
	}
	val, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if err := rs.applyCommand("set", "/users/"+u.ID+"/", val); err != nil {
		return err
	}
	return rs.applyCommand("set", usernameIndexKey(u.Username)+"/", usernameIndexValue(u.ID))
}

func (rs *RaftStore) UpdateUser(u *User) error {
	old, err := rs.GetUser(u.ID)
	oldUsername := ""
	if err == nil && old.Username != u.Username && old.Username != "" {
		oldUsername = old.Username
	}
	if err := rs.CreateUser(u); err != nil {
		return err
	}
	if oldUsername != "" {
		return rs.applyCommand("delete", "/users/by-username/"+oldUsername+"/", nil)
	}
	return nil
}

func (rs *RaftStore) DeleteUser(id string) error {
	u, err := rs.GetUser(id)
	if err != nil {
		return err
	}
	if err := rs.applyCommand("delete", "/users/"+id+"/", nil); err != nil {
		return err
	}
	return rs.applyCommand("delete", "/users/by-username/"+u.Username+"/", nil)
}

func (rs *RaftStore) ValidateReplayToken(channelID, token string) bool {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, _ := rs.GetGlobalConfig()
		if global.Defaults.ReplayToken == "" {
			return true
		}
		return constantTimeCompare(token, global.Defaults.ReplayToken)
	}
	if p.ReplayToken == "" {
		global, _ := rs.GetGlobalConfig()
		if global.Defaults.ReplayToken == "" {
			return true
		}
		return constantTimeCompare(token, global.Defaults.ReplayToken)
	}
	return constantTimeCompare(token, p.ReplayToken)
}

func (rs *RaftStore) GetSetupModeEndTime() time.Time {
	val, err := getFSMValue(rs.db, "/meta/setup_end")
	if err != nil || val == nil {
		return time.Time{}
	}
	var t time.Time
	if err := json.Unmarshal(val, &t); err != nil {
		return time.Time{}
	}
	return t
}

func (rs *RaftStore) SetSetupModeEndTime(t time.Time) error {
	val, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/meta/setup_end", val)
}

func (rs *RaftStore) OIDCProviders() ([]OIDCProvider, error) {
	val, err := getFSMValue(rs.db, "/global/auth/oidc_providers")
	if err != nil || val == nil {
		return nil, nil
	}
	var providers []OIDCProvider
	if err := json.Unmarshal(val, &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

func (rs *RaftStore) SetOIDCProviders(providers []OIDCProvider) error {
	val, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	return rs.applyCommand("set-json", "/global/auth/oidc_providers", val)
}

func constantTimeCompare(a, b string) bool {
	aHash := sha256.Sum256([]byte(a))
	bHash := sha256.Sum256([]byte(b))
	result := 0
	for i := 0; i < len(aHash); i++ {
		result |= int(aHash[i]) ^ int(bHash[i])
	}
	return result == 0
}

func (rs *RaftStore) GetRole(name string) (*Role, error) {
	val, err := getFSMValue(rs.db, "/rbac/roles/"+name+"/")
	if err != nil {
		return nil, err
	}
	if val == nil {
		for _, r := range DefaultRoles {
			if r.Name == name {
				return &r, nil
			}
		}
		return nil, fmt.Errorf("role %q not found", name)
	}
	var r Role
	if err := json.Unmarshal(val, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (rs *RaftStore) ListRoles() ([]Role, error) {
	roles := make([]Role, len(DefaultRoles))
	copy(roles, DefaultRoles)

	keys, err := listFSMKeys(rs.db, "/rbac/roles/")
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
		val, err := getFSMValue(rs.db, key)
		if err != nil || val == nil {
			continue
		}
		var r Role
		if err := json.Unmarshal(val, &r); err != nil {
			continue
		}
		roles = append(roles, r)
	}
	return roles, nil
}

func (rs *RaftStore) CreateRole(r Role) error {
	if isDefaultRole(r.Name) {
		return fmt.Errorf("role %q already exists (default role)", r.Name)
	}
	val, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return rs.applyCommand("set", "/rbac/roles/"+r.Name+"/", val)
}

func isDefaultRole(name string) bool {
	for _, r := range DefaultRoles {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (rs *RaftStore) GetUserBinding(userID string) (*UserBinding, error) {
	u, err := rs.GetUser(userID)
	if err != nil {
		return nil, err
	}
	return &UserBinding{
		UserID:   u.ID,
		Roles:    u.Roles,
		Channels: u.Channels,
	}, nil
}

func (rs *RaftStore) UpdateUserBinding(binding *UserBinding) error {
	u, err := rs.GetUser(binding.UserID)
	if err != nil {
		return err
	}
	u.Roles = binding.Roles
	u.Channels = binding.Channels
	return rs.UpdateUser(u)
}

func (rs *RaftStore) ListBindings() ([]UserBinding, error) {
	users, err := rs.ListUsers()
	if err != nil {
		return nil, err
	}
	bindings := make([]UserBinding, 0, len(users))
	for _, u := range users {
		bindings = append(bindings, UserBinding{
			UserID:   u.ID,
			Roles:    u.Roles,
			Channels: u.Channels,
		})
	}
	return bindings, nil
}

func (rs *RaftStore) IsSetupMode() bool {
	users, err := rs.ListUsers()
	if err != nil || len(users) == 0 {
		return true
	}
	return false
}

func (rs *RaftStore) CreateDevAdmin(password string) error {
	if !rs.IsSetupMode() {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	user := &User{
		ID:           "admin",
		Username:     "admin",
		PasswordHash: string(hash),
		Roles:        []string{"admin"},
		Channels:     []string{"*"},
	}
	return rs.CreateUser(user)
}

func (rs *RaftStore) ResolveChannelWebhookSecret(channelID string) (string, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, _ := rs.GetGlobalConfig()
		return global.Defaults.WebhookSecret, nil
	}
	migrateChannel(p)
	if p.WebhookSecret != "" {
		return p.WebhookSecret, nil
	}
	global, _ := rs.GetGlobalConfig()
	return global.Defaults.WebhookSecret, nil
}

func (rs *RaftStore) ResolveChannelAllowedIPs(channelID string) ([]string, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, _ := rs.GetGlobalConfig()
		return global.Defaults.AllowedIPs, nil
	}
	if len(p.AllowedIPs) > 0 {
		return p.AllowedIPs, nil
	}
	global, _ := rs.GetGlobalConfig()
	return global.Defaults.AllowedIPs, nil
}

func (rs *RaftStore) ResolveChannelMaxBodySize(channelID string) (int, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		global, _ := rs.GetGlobalConfig()
		return global.Server.MaxBodySize, nil
	}
	if p.MaxBodySize > 0 {
		return p.MaxBodySize, nil
	}
	global, _ := rs.GetGlobalConfig()
	return global.Server.MaxBodySize, nil
}

func (rs *RaftStore) ResolveChannelEncryption(channelID string) (string, string, []string, error) {
	p, err := rs.GetChannel(channelID)
	if err != nil {
		return "", "", nil, nil
	}
	migrateChannel(p)
	return p.EncryptionMode, p.EncryptionKey, p.EncryptionPubKeys, nil
}

func (rs *RaftStore) SessionSecret() string {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return ""
	}
	if global.Server.SessionSecret != "" {
		return global.Server.SessionSecret
	}
	return ""
}

func (rs *RaftStore) SetSessionSecret(secret string) error {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		global = defaultGlobalConfig()
	}
	global.Server.SessionSecret = secret
	return rs.UpdateGlobalConfig(global)
}

var sha256Hash = sha256.Sum256

func (rs *RaftStore) ResolveCORSOrigin() string {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return "*"
	}
	return global.Server.CORSOrigin
}

func (rs *RaftStore) ResolveTrustProxy() bool {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return false
	}
	return global.Server.TrustProxy
}

func (rs *RaftStore) ResolveFooter() string {
	global, err := rs.GetGlobalConfig()
	if err != nil {
		return ""
	}
	return global.Server.Footer
}