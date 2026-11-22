package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	goraftfsm "github.com/disaster37/go-raft/fsm"
	hraft "github.com/hashicorp/raft"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	bolt "go.etcd.io/bbolt"
	bberrors "go.etcd.io/bbolt/errors"
)

const fsmDataBucketName = "fsm_data"

// Legacy command payloads retained so pre-migration Raft logs replay cleanly
// and ApplyBootstrap remains atomic in a single bbolt transaction.
type legacyFSMCommand struct {
	Op    string          `json:"op"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value,omitempty"`
}

type fsmKV struct {
	Key    string `json:"key"`
	Value  []byte `json:"value,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

type fsmMultiPayload struct {
	Entries []fsmKV `json:"entries"`
}

type fsmBootstrapUser struct {
	Username     string   `json:"username"`
	PasswordHash string   `json:"password_hash"`
	Password     string   `json:"password,omitempty"`
	Roles        []string `json:"roles"`
	Channels     []string `json:"channels"`
}

type fsmBootstrapPayload struct {
	Global              *domain.GlobalConfig  `json:"global,omitempty"`
	Users               []fsmBootstrapUser    `json:"users,omitempty"`
	Channels            []*domain.Channel     `json:"channels,omitempty"`
	InternalAuthEnabled *bool                 `json:"internal_auth_enabled,omitempty"`
	OIDCProviders       []domain.OIDCProvider `json:"oidc_providers,omitempty"`
}

// compatFSM delegates KV operations and snapshots to go-raft's BoltKVFSM while
// enforcing key path validation, atomic bootstrap, and backward-compatible
// replay of pre-migration log entries and snapshots.
type compatFSM struct {
	mu   sync.Mutex
	dbFn func() *bolt.DB
	kv   *goraftfsm.BoltKVFSM
}

var _ hraft.FSM = (*compatFSM)(nil)

func newCompatFSM(db *bolt.DB) *compatFSM {
	f := &compatFSM{}
	if db != nil {
		f.dbFn = func() *bolt.DB { return db }
		f.kv = goraftfsm.NewBoltKVFSM(db)
	}
	return f
}

func (f *compatFSM) bindDB(fn func() *bolt.DB) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dbFn = fn
	f.kv = nil
}

func (f *compatFSM) ensureKV() (*goraftfsm.BoltKVFSM, *bolt.DB, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var db *bolt.DB
	if f.dbFn != nil {
		db = f.dbFn()
	}
	if db == nil {
		return nil, nil, fmt.Errorf("bolt FSM: nil database")
	}
	if f.kv == nil {
		f.kv = goraftfsm.NewBoltKVFSM(db)
	}
	return f.kv, db, nil
}

func (f *compatFSM) Get(key string) ([]byte, error) {
	kv, _, err := f.ensureKV()
	if err != nil {
		return nil, err
	}
	val, ok, err := kv.Get("", key)
	if err != nil || !ok {
		return nil, err
	}
	return val, nil
}

func (f *compatFSM) ListPrefix(prefix string) (map[string][]byte, error) {
	kv, _, err := f.ensureKV()
	if err != nil {
		return nil, err
	}
	return kv.ListPrefix("", prefix)
}

func (f *compatFSM) HasData() (bool, error) {
	kv, _, err := f.ensureKV()
	if err != nil {
		return false, err
	}
	return kv.HasData()
}

func (f *compatFSM) Apply(log *hraft.Log) interface{} {
	kv, db, err := f.ensureKV()
	if err != nil {
		return err
	}

	// Probe whether this entry uses go-raft's CommandCodec ("kind" > 0) or a
	// legacy/bootstrap command ("op" != "").
	var probe struct {
		Kind uint8  `json:"kind"`
		Op   string `json:"op"`
	}
	if err := json.Unmarshal(log.Data, &probe); err != nil {
		return err
	}
	if probe.Kind != 0 {
		if err := validateCodecKeys(log.Data); err != nil {
			return err
		}
		return kv.Apply(log)
	}
	return f.applyLegacyOrBootstrap(kv, db, log.Data)
}

func validateCodecKeys(raw []byte) error {
	cmd, err := (goraftfsm.CommandCodec{}).Decode(raw)
	if err != nil {
		return err
	}
	switch cmd.Kind {
	case goraftfsm.KindSet, goraftfsm.KindSetJSON, goraftfsm.KindDelete, goraftfsm.KindCreate:
		var entry goraftfsm.KVEntry
		if err := json.Unmarshal(cmd.Payload, &entry); err != nil {
			return err
		}
		return validateKey(entry.Key)
	case goraftfsm.KindSetMulti:
		var payload goraftfsm.SetMultiPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return err
		}
		for _, e := range payload.Entries {
			if err := validateKey(e.Key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *compatFSM) applyLegacyOrBootstrap(kv *goraftfsm.BoltKVFSM, db *bolt.DB, raw []byte) interface{} {
	var cmd legacyFSMCommand
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return err
	}
	if cmd.Op != "bootstrap" && cmd.Op != "set-multi" {
		if err := validateKey(cmd.Key); err != nil {
			return err
		}
	}
	encodeAndApply := func(kind uint8, payload any) interface{} {
		data, err := (goraftfsm.CommandCodec{}).Encode(kind, payload)
		if err != nil {
			return err
		}
		return kv.Apply(&hraft.Log{Data: data})
	}
	switch cmd.Op {
	case "set", "set-json":
		return encodeAndApply(goraftfsm.KindSet, goraftfsm.KVEntry{Key: cmd.Key, Value: []byte(cmd.Value)})
	case "delete":
		return encodeAndApply(goraftfsm.KindDelete, goraftfsm.KVEntry{Key: cmd.Key})
	case "set-multi":
		var p fsmMultiPayload
		if err := json.Unmarshal(cmd.Value, &p); err != nil {
			return err
		}
		entries := make([]goraftfsm.KVEntry, 0, len(p.Entries))
		for _, e := range p.Entries {
			if err := validateKey(e.Key); err != nil {
				return err
			}
			entries = append(entries, goraftfsm.KVEntry{Key: e.Key, Value: e.Value, Delete: e.Delete})
		}
		return encodeAndApply(goraftfsm.KindSetMulti, goraftfsm.SetMultiPayload{Entries: entries})
	case "create-channel":
		resp := encodeAndApply(goraftfsm.KindCreate, goraftfsm.KVEntry{Key: cmd.Key, Value: []byte(cmd.Value)})
		if respErr, ok := resp.(error); ok && errors.Is(respErr, goraftfsm.ErrConflict) {
			return fmt.Errorf("%w: channel %q", domain.ErrAlreadyExists, strings.TrimPrefix(strings.TrimSuffix(cmd.Key, "/"), "/channels/"))
		}
		return resp
	case "create-user":
		var u domain.User
		if err := json.Unmarshal(cmd.Value, &u); err != nil {
			return err
		}
		if u.Username == "" {
			return fmt.Errorf("create user: username required")
		}
		resp := encodeAndApply(goraftfsm.KindSetMulti, goraftfsm.SetMultiPayload{Entries: []goraftfsm.KVEntry{
			{Key: cmd.Key, Value: []byte(cmd.Value), Create: true},
			{Key: usernameIndexKey(u.Username) + "/", Value: usernameIndexValue(u.ID)},
		}})
		if respErr, ok := resp.(error); ok && errors.Is(respErr, goraftfsm.ErrConflict) {
			return fmt.Errorf("%w: user %q", domain.ErrAlreadyExists, strings.TrimPrefix(strings.TrimSuffix(cmd.Key, "/"), "/users/"))
		}
		return resp
	case "bootstrap":
		return applyBootstrapTx(db, cmd.Value)
	default:
		return fmt.Errorf("unknown operation: %s", cmd.Op)
	}
}

func (f *compatFSM) Snapshot() (hraft.FSMSnapshot, error) {
	kv, _, err := f.ensureKV()
	if err != nil {
		return nil, err
	}
	return kv.Snapshot()
}

func (f *compatFSM) Restore(rc io.ReadCloser) error {
	defer func() { _ = rc.Close() }()
	kv, db, err := f.ensureKV()
	if err != nil {
		return err
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return db.Update(func(tx *bolt.Tx) error {
			if err := tx.DeleteBucket([]byte(fsmDataBucketName)); err != nil && !errors.Is(err, bberrors.ErrBucketNotFound) {
				return err
			}
			_, err := tx.CreateBucket([]byte(fsmDataBucketName))
			return err
		})
	}
	// Detect legacy gohookbridge snapshot {"data": {"/k": "<b64>"}} vs
	// go-raft BoltKVFSM flat map {"/k": "<b64>"}.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return fmt.Errorf("invalid snapshot data: %w", err)
	}
	restoreBytes := data
	if nested, ok := rawMap["data"]; ok && len(rawMap) == 1 && len(bytes.TrimSpace(nested)) > 0 && bytes.TrimSpace(nested)[0] == '{' {
		restoreBytes = nested
	}
	if err := kv.Restore(io.NopCloser(bytes.NewReader(restoreBytes))); err != nil {
		return fmt.Errorf("invalid snapshot data: %w", err)
	}
	return nil
}

func validateKey(key string) error {
	if strings.Contains(key, "..") {
		return fmt.Errorf("invalid key: contains '..'")
	}
	if strings.HasPrefix(key, "/") {
		return nil
	}
	return fmt.Errorf("invalid key: must start with '/'")
}

// applyBootstrapTx applies a bootstrap payload inside one Bolt transaction,
// rejecting plaintext passwords (F15) and refusing to run when the FSM already
// holds data.
func applyBootstrapTx(db *bolt.DB, value []byte) error {
	var payload fsmBootstrapPayload
	if err := json.Unmarshal(value, &payload); err != nil {
		return err
	}

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		c := b.Cursor()
		if k, _ := c.First(); k != nil {
			return fmt.Errorf("cannot apply bootstrap: FSM already has data")
		}

		writeString := func(key, val string) error {
			return b.Put([]byte(key), []byte(val))
		}

		if payload.Global != nil {
			//nolint:gosec
			scVal, err := json.Marshal(payload.Global.Server)
			if err != nil {
				return fmt.Errorf("marshal server config: %w", err)
			}
			if err := writeString("/global/server/", string(scVal)); err != nil {
				return err
			}
			dcVal, err := json.Marshal(payload.Global.Defaults)
			if err != nil {
				return fmt.Errorf("marshal defaults: %w", err)
			}
			if err := writeString("/global/defaults/", string(dcVal)); err != nil {
				return err
			}
		}

		for _, u := range payload.Users {
			if u.Username == "" {
				return fmt.Errorf("username required")
			}
			if u.Password != "" {
				return fmt.Errorf("bootstrap user %q: plaintext password must be hashed before replication", u.Username)
			}
			if u.PasswordHash == "" {
				return fmt.Errorf("bootstrap user %q: password_hash required", u.Username)
			}
			user := domain.User{
				ID:           u.Username,
				Username:     u.Username,
				PasswordHash: u.PasswordHash,
				Roles:        u.Roles,
				Channels:     u.Channels,
			}
			userVal, err := json.Marshal(user)
			if err != nil {
				return err
			}
			if err := writeString("/users/"+u.Username+"/", string(userVal)); err != nil {
				return err
			}
			if err := writeString(usernameIndexKey(u.Username)+"/", string(usernameIndexValue(u.Username))); err != nil {
				return err
			}
		}

		for _, p := range payload.Channels {
			if p.ID == "" {
				return fmt.Errorf("channel ID required")
			}
			pVal, err := json.Marshal(p)
			if err != nil {
				return err
			}
			if err := writeString("/channels/"+p.ID+"/", string(pVal)); err != nil {
				return err
			}
		}

		if payload.InternalAuthEnabled != nil {
			iaVal, err := json.Marshal(*payload.InternalAuthEnabled)
			if err != nil {
				return fmt.Errorf("marshal internal auth flag: %w", err)
			}
			if err := writeString("/global/auth/internal_enabled", string(iaVal)); err != nil {
				return err
			}
		}
		if len(payload.OIDCProviders) > 0 {
			//nolint:gosec // OIDC client secrets are intentionally persisted in Raft
			provVal, err := json.Marshal(payload.OIDCProviders)
			if err != nil {
				return fmt.Errorf("marshal oidc providers: %w", err)
			}
			if err := writeString("/global/auth/oidc_providers", string(provVal)); err != nil {
				return err
			}
		}

		return nil
	})
}
