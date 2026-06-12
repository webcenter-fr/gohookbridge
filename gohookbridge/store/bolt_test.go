package store

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/hashicorp/raft"
	"go.etcd.io/bbolt"
	"gotest.tools/v3/assert"
)

func newTestBoltDB(t *testing.T) *bbolt.DB {
	t.Helper()
	db, err := newBoltDB(t.TempDir(), "test")
	assert.NilError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewBoltDB(t *testing.T) {
	db := newTestBoltDB(t)
	err := db.View(func(tx *bbolt.Tx) error {
		for _, name := range []string{logsBucketName, stableBucketName, fsmDataBucketName} {
			b := tx.Bucket([]byte(name))
			assert.Assert(t, b != nil, "expected bucket %s to exist", name)
		}
		return nil
	})
	assert.NilError(t, err)
}

func TestBoltLogStore_FirstIndex(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)
	idx, err := store.FirstIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(0), idx)
}

func TestBoltLogStore_LastIndex(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)
	idx, err := store.LastIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(0), idx)
}

func TestBoltLogStore_StoreGetLog(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)

	log := &raft.Log{
		Index: 42,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("hello"),
	}

	err := store.StoreLog(log)
	assert.NilError(t, err)

	var got raft.Log
	err = store.GetLog(42, &got)
	assert.NilError(t, err)
	assert.Equal(t, log.Index, got.Index)
	assert.Equal(t, log.Term, got.Term)
	assert.Equal(t, log.Type, got.Type)
	assert.Assert(t, bytes.Equal(log.Data, got.Data))
}

func TestBoltLogStore_StoreLogs(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)

	var logs []*raft.Log
	for i := uint64(1); i <= 10; i++ {
		logs = append(logs, &raft.Log{
			Index: i,
			Term:  1,
			Type:  raft.LogCommand,
			Data:  []byte("data"),
		})
	}

	err := store.StoreLogs(logs)
	assert.NilError(t, err)

	first, err := store.FirstIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(1), first)

	last, err := store.LastIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(10), last)

	for i := uint64(1); i <= 10; i++ {
		var got raft.Log
		err := store.GetLog(i, &got)
		assert.NilError(t, err)
		assert.Equal(t, i, got.Index)
	}
}

func TestBoltLogStore_DeleteRange(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)

	var logs []*raft.Log
	for i := uint64(1); i <= 10; i++ {
		logs = append(logs, &raft.Log{
			Index: i,
			Term:  1,
			Type:  raft.LogCommand,
			Data:  []byte("data"),
		})
	}
	err := store.StoreLogs(logs)
	assert.NilError(t, err)

	err = store.DeleteRange(1, 5)
	assert.NilError(t, err)

	first, err := store.FirstIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(6), first)

	last, err := store.LastIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(10), last)

	var missing raft.Log
	err = store.GetLog(1, &missing)
	assert.Equal(t, raft.ErrLogNotFound, err)

	for i := uint64(6); i <= 10; i++ {
		var got raft.Log
		err := store.GetLog(i, &got)
		assert.NilError(t, err)
		assert.Equal(t, i, got.Index)
	}
}

func TestBoltStableStore_SetGet(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltStableStore(db)

	err := store.Set([]byte("key1"), []byte("value1"))
	assert.NilError(t, err)

	val, err := store.Get([]byte("key1"))
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal([]byte("value1"), val))

	_, err = store.Get([]byte("nonexistent"))
	assert.ErrorContains(t, err, "not found")
}

func TestBoltStableStore_SetGetUint64(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltStableStore(db)

	err := store.SetUint64([]byte("counter"), 99)
	assert.NilError(t, err)

	val, err := store.GetUint64([]byte("counter"))
	assert.NilError(t, err)
	assert.Equal(t, uint64(99), val)

	_, err = store.GetUint64([]byte("missing"))
	assert.ErrorContains(t, err, "not found")
}

func TestFSMDataAccess(t *testing.T) {
	db := newTestBoltDB(t)

	err := setFSMValue(db, "/key1", []byte("val1"))
	assert.NilError(t, err)
	err = setFSMValue(db, "/key2", []byte("val2"))
	assert.NilError(t, err)
	err = setFSMValue(db, "/key3", []byte("val3"))
	assert.NilError(t, err)

	val, err := getFSMValue(db, "/key1")
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal([]byte("val1"), val))

	val, err = getFSMValue(db, "/nonexistent")
	assert.NilError(t, err)
	assert.Assert(t, val == nil)

	keys, err := listFSMKeys(db, "/key")
	assert.NilError(t, err)
	assert.Equal(t, 3, len(keys))
	assert.Equal(t, "/key1", keys[0])
	assert.Equal(t, "/key2", keys[1])
	assert.Equal(t, "/key3", keys[2])

	keys, err = listFSMKeys(db, "/nonexistent")
	assert.NilError(t, err)
	assert.Assert(t, len(keys) == 0)

	err = deleteFSMValue(db, "/key1")
	assert.NilError(t, err)

	val, err = getFSMValue(db, "/key1")
	assert.NilError(t, err)
	assert.Assert(t, val == nil)

	keys, err = listFSMKeys(db, "/key")
	assert.NilError(t, err)
	assert.Equal(t, 2, len(keys))

	err = iterateFSM(db, func(k, v []byte) error {
		return nil
	})
	assert.NilError(t, err)

	var count int
	err = iterateFSM(db, func(k, v []byte) error {
		count++
		return nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, count)
}

func TestStoreSnapshot(t *testing.T) {
	db := newTestBoltDB(t)

	err := setFSMValue(db, "/key1", []byte("val1"))
	assert.NilError(t, err)
	err = setFSMValue(db, "/key2", []byte("val2"))
	assert.NilError(t, err)

	snapshot := &StoreSnapshot{db: db}

	var buf bytes.Buffer
	sink := &mockSnapshotSink{Buffer: &buf}

	err = snapshot.Persist(sink)
	assert.NilError(t, err)
	assert.Assert(t, sink.closed)

	var decoded struct {
		Data map[string][]byte `json:"data"`
	}
	err = json.Unmarshal(buf.Bytes(), &decoded)
	assert.NilError(t, err)
	assert.Equal(t, 2, len(decoded.Data))
	assert.Assert(t, bytes.Equal([]byte("val1"), decoded.Data["/key1"]))
	assert.Assert(t, bytes.Equal([]byte("val2"), decoded.Data["/key2"]))
}

func TestEncodeDecodeLog(t *testing.T) {
	log := &raft.Log{
		Index: 1,
		Term:  2,
		Type:  raft.LogCommand,
		Data:  []byte("test data"),
	}

	data, err := encodeLog(log)
	assert.NilError(t, err)

	var decoded raft.Log
	err = decodeLog(data, &decoded)
	assert.NilError(t, err)
	assert.Equal(t, log.Index, decoded.Index)
	assert.Equal(t, log.Term, decoded.Term)
	assert.Equal(t, log.Type, decoded.Type)
	assert.Assert(t, bytes.Equal(log.Data, decoded.Data))
}

func TestItobBtoi(t *testing.T) {
	values := []uint64{0, 1, 255, 256, 65535, 65536, 1<<32 - 1, 1 << 32, 1<<64 - 1}
	for _, v := range values {
		b := itob(v)
		assert.Equal(t, 8, len(b), "itob length mismatch for %d", v)
		result := btoi(b)
		assert.Equal(t, v, result, "btoi(itob(%d)) round-trip failed", v)
	}
}

func TestGetLogNotFound(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)

	var log raft.Log
	err := store.GetLog(999, &log)
	assert.Equal(t, raft.ErrLogNotFound, err)
}

func TestDeleteRangeNonExistent(t *testing.T) {
	db := newTestBoltDB(t)
	store := newBoltLogStore(db)

	err := store.DeleteRange(1, 10)
	assert.NilError(t, err)

	idx, err := store.FirstIndex()
	assert.NilError(t, err)
	assert.Equal(t, uint64(0), idx)
}

func TestStoreSnapshotRelease(t *testing.T) {
	db := newTestBoltDB(t)
	snapshot := &StoreSnapshot{db: db}
	snapshot.Release()
}

