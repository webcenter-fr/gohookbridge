package store

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/raft"
	"go.etcd.io/bbolt"
)

const (
	logsBucketName    = "logs"
	stableBucketName  = "stable"
	fsmDataBucketName = "fsm_data"
)

func newBoltDB(dir, nodeID string) (*bbolt.DB, error) {
	path := fmt.Sprintf("%s/%s.db", dir, nodeID)
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, name := range []string{logsBucketName, stableBucketName, fsmDataBucketName} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type boltLogStore struct {
	db *bbolt.DB
}

func newBoltLogStore(db *bbolt.DB) *boltLogStore {
	return &boltLogStore{db: db}
}

func (b *boltLogStore) FirstIndex() (uint64, error) {
	var idx uint64
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(logsBucketName))
		if bucket == nil {
			return nil
		}
		c := bucket.Cursor()
		k, _ := c.First()
		if k != nil {
			idx = btoi(k)
		}
		return nil
	})
	return idx, err
}

func (b *boltLogStore) LastIndex() (uint64, error) {
	var idx uint64
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(logsBucketName))
		if bucket == nil {
			return nil
		}
		c := bucket.Cursor()
		k, _ := c.Last()
		if k != nil {
			idx = btoi(k)
		}
		return nil
	})
	return idx, err
}

func (b *boltLogStore) GetLog(idx uint64, log *raft.Log) error {
	return b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(logsBucketName))
		if bucket == nil {
			return raft.ErrLogNotFound
		}
		val := bucket.Get(itob(idx))
		if val == nil {
			return raft.ErrLogNotFound
		}
		return decodeLog(val, log)
	})
}

func (b *boltLogStore) StoreLog(log *raft.Log) error {
	return b.StoreLogs([]*raft.Log{log})
}

func (b *boltLogStore) StoreLogs(logs []*raft.Log) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(logsBucketName))
		if bucket == nil {
			return fmt.Errorf("logs bucket not found")
		}
		for _, log := range logs {
			val, err := encodeLog(log)
			if err != nil {
				return err
			}
			if err := bucket.Put(itob(log.Index), val); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *boltLogStore) DeleteRange(low, high uint64) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(logsBucketName))
		if bucket == nil {
			return nil
		}
		for i := low; i <= high; i++ {
			if err := bucket.Delete(itob(i)); err != nil {
				return err
			}
		}
		return nil
	})
}

type boltStableStore struct {
	db *bbolt.DB
}

func newBoltStableStore(db *bbolt.DB) *boltStableStore {
	return &boltStableStore{db: db}
}

func (b *boltStableStore) Set(key []byte, val []byte) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(stableBucketName))
		if bucket == nil {
			return fmt.Errorf("stable bucket not found")
		}
		return bucket.Put(key, val)
	})
}

func (b *boltStableStore) Get(key []byte) ([]byte, error) {
	var val []byte
	err := b.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(stableBucketName))
		if bucket == nil {
			return nil
		}
		v := bucket.Get(key)
		if v != nil {
			val = make([]byte, len(v))
			copy(val, v)
		}
		return nil
	})
	if val == nil {
		return nil, fmt.Errorf("not found")
	}
	return val, err
}

func (b *boltStableStore) SetUint64(key []byte, val uint64) error {
	return b.Set(key, itob(val))
}

func (b *boltStableStore) GetUint64(key []byte) (uint64, error) {
	val, err := b.Get(key)
	if err != nil {
		return 0, err
	}
	return btoi(val), nil
}

func itob(v uint64) []byte {
	b := make([]byte, 8)
	//nolint:gosec
	b[0] = byte(v >> 56)
	//nolint:gosec
	b[1] = byte(v >> 48)
	//nolint:gosec
	b[2] = byte(v >> 40)
	//nolint:gosec
	b[3] = byte(v >> 32)
	//nolint:gosec
	b[4] = byte(v >> 24)
	//nolint:gosec
	b[5] = byte(v >> 16)
	//nolint:gosec
	b[6] = byte(v >> 8)
	//nolint:gosec
	b[7] = byte(v)
	return b
}

func btoi(b []byte) uint64 {
	return uint64(b[7]) | uint64(b[6])<<8 | uint64(b[5])<<16 | uint64(b[4])<<24 |
		uint64(b[3])<<32 | uint64(b[2])<<40 | uint64(b[1])<<48 | uint64(b[0])<<56
}

func encodeLog(log *raft.Log) ([]byte, error) {
	return json.Marshal(log)
}

func decodeLog(data []byte, log *raft.Log) error {
	return json.Unmarshal(data, log)
}

// FSM data access helpers.
func getFSMValue(db *bbolt.DB, key string) ([]byte, error) {
	var val []byte
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v != nil {
			val = make([]byte, len(v))
			copy(val, v)
		}
		return nil
	})
	return val, err
}

func setFSMValue(db *bbolt.DB, key string, value []byte) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", fsmDataBucketName)
		}
		return b.Put([]byte(key), value)
	})
}

func deleteFSMValue(db *bbolt.DB, key string) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

func listFSMKeys(db *bbolt.DB, prefix string) ([]string, error) {
	var keys []string
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.Seek([]byte(prefix)); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == prefix; k, _ = c.Next() {
			keys = append(keys, string(k))
		}
		return nil
	})
	return keys, err
}

func iterateFSM(db *bbolt.DB, fn func(key, value []byte) error) error {
	return db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		return b.ForEach(fn)
	})
}

// Snapshot wraps BoltDB snapshot operations.
type Snapshot struct {
	db *bbolt.DB
}

func (s *Snapshot) Persist(sink raft.SnapshotSink) error {
	snapshot := make(map[string][]byte)
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(fsmDataBucketName))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			val := make([]byte, len(v))
			copy(val, v)
			snapshot[string(k)] = val
			return nil
		})
	})
	if err != nil {
		_ = sink.Cancel()
		return err
	}

	enc := map[string]map[string][]byte{"data": snapshot}
	b, err := json.Marshal(enc)
	if err != nil {
		_ = sink.Cancel()
		return err
	}
	if _, err := sink.Write(b); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *Snapshot) Release() {}
