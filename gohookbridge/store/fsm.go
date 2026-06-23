package store

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hashicorp/raft"
	"go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

type fsmCommand struct {
	Op    string          `json:"op"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value,omitempty"`
}

type fsmBootstrapUser struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	Roles    []string `json:"roles"`
	Channels []string `json:"channels"`
}

type fsmBootstrapPayload struct {
	Global   *GlobalConfig      `json:"global,omitempty"`
	Users    []fsmBootstrapUser `json:"users,omitempty"`
	Channels []*Channel         `json:"channels,omitempty"`
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

	if cmd.Op != "bootstrap" {
		if err := validateKey(cmd.Key); err != nil {
			return err
		}
	}

	switch cmd.Op {
	case "set":
		return f.applySet(cmd.Key, []byte(cmd.Value))
	case "set-json":
		return f.applySet(cmd.Key, []byte(cmd.Value))
	case "delete":
		return f.applyDelete(cmd.Key)
	case "create-channel":
		return f.applyCreateChannel(cmd.Key, []byte(cmd.Value))
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
			return fmt.Errorf("channel %q already exists", strings.TrimPrefix(strings.TrimSuffix(key, "/"), "/channels/"))
		}
		return b.Put([]byte(key), value)
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
			hash, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("hash password for user %q: %w", u.Username, err)
			}
			user := User{
				ID:           u.Username,
				Username:     u.Username,
				PasswordHash: string(hash),
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
