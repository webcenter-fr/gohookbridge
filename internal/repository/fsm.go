package repository

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hashicorp/raft"
	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"go.etcd.io/bbolt"
)

type fsmCommand struct {
	Op    string          `json:"op"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value,omitempty"`
}

type fsmBootstrapUser struct {
	Username string `json:"username"`
	// PasswordHash is the bcrypt hash computed before replication; plaintext
	// passwords must never enter the Raft log (CWE-312).
	PasswordHash string `json:"password_hash"`
	// Password is rejected by the FSM if present (defense in depth).
	Password string   `json:"password,omitempty"`
	Roles    []string `json:"roles"`
	Channels []string `json:"channels"`
}

// fsmMultiPayload carries the key mutations of a transactional "set-multi".
type fsmMultiPayload struct {
	Entries []fsmKV `json:"entries"`
}

type fsmBootstrapPayload struct {
	Global              *domain.GlobalConfig  `json:"global,omitempty"`
	Users               []fsmBootstrapUser    `json:"users,omitempty"`
	Channels            []*domain.Channel     `json:"channels,omitempty"`
	InternalAuthEnabled *bool                 `json:"internal_auth_enabled,omitempty"`
	OIDCProviders       []domain.OIDCProvider `json:"oidc_providers,omitempty"`
}

type FSM struct {
	db *bbolt.DB
}

func NewFSM(db *bbolt.DB) *FSM {
	return &FSM{db: db}
}

func (f *FSM) Apply(log *raft.Log) interface{} {
	var cmd fsmCommand
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		return err
	}

	if cmd.Op != "bootstrap" && cmd.Op != "set-multi" {
		if err := validateKey(cmd.Key); err != nil {
			return err
		}
	}

	switch cmd.Op {
	case "set":
		return f.applySet(cmd.Key, []byte(cmd.Value))
	case "set-json":
		return f.applySet(cmd.Key, []byte(cmd.Value))
	case "set-multi":
		return f.applySetMulti([]byte(cmd.Value))
	case "delete":
		return f.applyDelete(cmd.Key)
	case "create-channel":
		return f.applyCreateChannel(cmd.Key, []byte(cmd.Value))
	case "create-user":
		return f.applyCreateUser(cmd.Key, []byte(cmd.Value))
	case "bootstrap":
		return f.applyBootstrap([]byte(cmd.Value))
	default:
		return fmt.Errorf("unknown operation: %s", cmd.Op)
	}
}

func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	return &Snapshot{db: f.db}, nil
}

func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return f.clearAll()
	}

	var snapshot struct {
		Data map[string][]byte `json:"data"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("invalid snapshot data: %w", err)
	}

	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		// Clear existing data
		if err := clearBucket(b); err != nil {
			return err
		}
		for k, v := range snapshot.Data {
			if err := b.Put([]byte(k), v); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f *FSM) applyCreateChannel(key string, value []byte) error {
	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		existing := b.Get([]byte(key))
		if existing != nil {
			return fmt.Errorf("%w: channel %q", domain.ErrAlreadyExists, strings.TrimPrefix(strings.TrimSuffix(key, "/"), "/channels/"))
		}
		return b.Put([]byte(key), value)
	})
}

func (f *FSM) applyCreateUser(key string, value []byte) error {
	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		if b.Get([]byte(key)) != nil {
			return fmt.Errorf("%w: user %q", domain.ErrAlreadyExists, strings.TrimPrefix(strings.TrimSuffix(key, "/"), "/users/"))
		}
		if err := b.Put([]byte(key), value); err != nil {
			return err
		}
		// The username index is written in the same transaction as the user so
		// a crash can never leave the index pointing at a missing user.
		var u domain.User
		if err := json.Unmarshal(value, &u); err != nil {
			return err
		}
		if u.Username == "" {
			return fmt.Errorf("create user: username required")
		}
		return b.Put([]byte(usernameIndexKey(u.Username)+"/"), usernameIndexValue(u.ID))
	})
}

// applySetMulti applies a batch of key mutations in a single Bolt transaction,
// so a failed/partial write cannot leave the FSM inconsistent.
func (f *FSM) applySetMulti(value []byte) error {
	var payload fsmMultiPayload
	if err := json.Unmarshal(value, &payload); err != nil {
		return err
	}
	for _, e := range payload.Entries {
		if err := validateKey(e.Key); err != nil {
			return err
		}
	}
	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		for _, e := range payload.Entries {
			if e.Delete {
				if err := b.Delete([]byte(e.Key)); err != nil {
					return err
				}
				continue
			}
			if err := b.Put([]byte(e.Key), e.Value); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f *FSM) applyBootstrap(value []byte) error {
	var payload fsmBootstrapPayload
	if err := json.Unmarshal(value, &payload); err != nil {
		return err
	}

	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		if err := b.ForEach(func(_, _ []byte) error {
			return nil // just check if data exists
		}); err != nil {
			return err
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

func (f *FSM) applySet(key string, value []byte) error {
	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		return b.Put([]byte(key), value)
	})
}

func (f *FSM) applyDelete(key string) error {
	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

func (f *FSM) clearAll() error {
	return f.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		return clearBucket(b)
	})
}

func clearBucket(b *bbolt.Bucket) error {
	keys := make([][]byte, 0)
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		keys = append(keys, k)
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
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
