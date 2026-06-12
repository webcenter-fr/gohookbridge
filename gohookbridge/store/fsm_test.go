package store

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/hashicorp/raft"
	"gotest.tools/v3/assert"
)

func newTestFSM(t *testing.T) *FSM {
	t.Helper()
	db, err := newBoltDB(t.TempDir(), "test-fsm")
	assert.NilError(t, err)
	t.Cleanup(func() { db.Close() })
	return NewFSM(db)
}

type mockSnapshotSink struct {
	*bytes.Buffer
	closed bool
}

func (m *mockSnapshotSink) ID() string {
	return "mock"
}

func (m *mockSnapshotSink) Cancel() error {
	return nil
}

func (m *mockSnapshotSink) Close() error {
	m.closed = true
	return nil
}

func marshalCmd(t *testing.T, op, key string, value interface{}) []byte {
	t.Helper()
	cmd := fsmCommand{Op: op, Key: key}
	if value != nil {
		raw, err := json.Marshal(value)
		assert.NilError(t, err)
		cmd.Value = json.RawMessage(raw)
	}
	b, err := json.Marshal(cmd)
	assert.NilError(t, err)
	return b
}

func mustGetValue(t *testing.T, fsm *FSM, key string) []byte {
	t.Helper()
	val, err := getFSMValue(fsm.db, key)
	assert.NilError(t, err)
	return val
}

func applyOK(t *testing.T, fsm *FSM, cmdBytes []byte) {
	t.Helper()
	result := fsm.Apply(&raft.Log{Data: cmdBytes})
	if result != nil {
		err, ok := result.(error)
		assert.Assert(t, ok, "Apply returned non-error: %v", result)
		assert.NilError(t, err)
	}
}

func TestFSM_ApplySet(t *testing.T) {
	fsm := newTestFSM(t)

	cmdBytes := marshalCmd(t, "set", "/foo", "bar")
	applyOK(t, fsm, cmdBytes)

	val := mustGetValue(t, fsm, "/foo")
	assert.Equal(t, string(val), `"bar"`)
}

func TestFSM_ApplySetJSON(t *testing.T) {
	fsm := newTestFSM(t)

	jsonVal := map[string]interface{}{"name": "alice", "count": 42}
	cmdBytes := marshalCmd(t, "set-json", "/user", jsonVal)
	applyOK(t, fsm, cmdBytes)

	val := mustGetValue(t, fsm, "/user")
	var actual map[string]interface{}
	assert.NilError(t, json.Unmarshal(val, &actual))
	assert.Equal(t, actual["name"], "alice")
	assert.Equal(t, actual["count"], float64(42))
}

func TestFSM_ApplyDelete(t *testing.T) {
	fsm := newTestFSM(t)

	setBytes := marshalCmd(t, "set", "/key", "value")
	applyOK(t, fsm, setBytes)

	val := mustGetValue(t, fsm, "/key")
	assert.Assert(t, len(val) > 0)

	delBytes := marshalCmd(t, "delete", "/key", nil)
	applyOK(t, fsm, delBytes)

	val, err := getFSMValue(fsm.db, "/key")
	assert.NilError(t, err)
	assert.Assert(t, val == nil)
}

func TestFSM_Snapshot(t *testing.T) {
	fsm := newTestFSM(t)

	for i := 0; i < 3; i++ {
		key := "/key"
		if i > 0 {
			key += string(rune('0' + i))
		}
		cmdBytes := marshalCmd(t, "set", key, "v")
		applyOK(t, fsm, cmdBytes)
	}

	snap, err := fsm.Snapshot()
	assert.NilError(t, err)
	defer snap.Release()

	sink := &mockSnapshotSink{Buffer: new(bytes.Buffer)}
	assert.NilError(t, snap.Persist(sink))
	assert.Assert(t, sink.closed)
	assert.Assert(t, sink.Len() > 0)

	var decoded struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	assert.NilError(t, json.Unmarshal(sink.Bytes(), &decoded))
	assert.Equal(t, len(decoded.Data), 3)
}

func TestFSM_Restore(t *testing.T) {
	snapshot := map[string]map[string][]byte{
		"data": {
			"/alpha": []byte(`"1"`),
			"/beta":  []byte(`"2"`),
		},
	}
	payload, err := json.Marshal(snapshot)
	assert.NilError(t, err)

	fsm := newTestFSM(t)

	rc := io.NopCloser(bytes.NewReader(payload))
	assert.NilError(t, fsm.Restore(rc))

	val, err := getFSMValue(fsm.db, "/alpha")
	assert.NilError(t, err)
	assert.Equal(t, string(val), `"1"`)

	val, err = getFSMValue(fsm.db, "/beta")
	assert.NilError(t, err)
	assert.Equal(t, string(val), `"2"`)
}

func TestFSM_SnapshotRestore_Roundtrip(t *testing.T) {
	fsm := newTestFSM(t)

	for i := 0; i < 20; i++ {
		key := "/key"
		if i > 0 {
			key += string(rune('a' + i))
		}
		cmdBytes := marshalCmd(t, "set", key, i)
		applyOK(t, fsm, cmdBytes)
	}

	snap, err := fsm.Snapshot()
	assert.NilError(t, err)
	defer snap.Release()

	sink := &mockSnapshotSink{Buffer: new(bytes.Buffer)}
	assert.NilError(t, snap.Persist(sink))

	restored := newTestFSM(t)
	rc := io.NopCloser(bytes.NewReader(sink.Bytes()))
	assert.NilError(t, restored.Restore(rc))

	for i := 0; i < 20; i++ {
		key := "/key"
		if i > 0 {
			key += string(rune('a' + i))
		}
		val, err := getFSMValue(restored.db, key)
		assert.NilError(t, err)
		assert.Assert(t, val != nil, "key %s not found after restore", key)
	}
}

func TestFSM_KeyValidation(t *testing.T) {
	fsm := newTestFSM(t)

	tests := []struct {
		key     string
		wantErr string
	}{
		{"/valid", ""},
		{"/valid/path", ""},
		{"/path/with..dots", "contains '..'"},
		{"relative", "must start with '/'"},
		{"", "must start with '/'"},
		{"/../escape", "contains '..'"},
		{"/a..b/c", "contains '..'"},
		{"..", "contains '..'"},
	}

	for _, tc := range tests {
		cmdBytes := marshalCmd(t, "set", tc.key, "x")
		result := fsm.Apply(&raft.Log{Data: cmdBytes})
		if tc.wantErr == "" {
			assert.Assert(t, result == nil, "expected no error for key %q, got %v", tc.key, result)
		} else {
			err, ok := result.(error)
			assert.Assert(t, ok, "expected error for key %q", tc.key)
			assert.ErrorContains(t, err, tc.wantErr)
		}
	}
}

func TestFSM_Restore_Empty(t *testing.T) {
	fsm := newTestFSM(t)

	setBytes := marshalCmd(t, "set", "/foo", "bar")
	applyOK(t, fsm, setBytes)

	val := mustGetValue(t, fsm, "/foo")
	assert.Assert(t, len(val) > 0)

	rc := io.NopCloser(bytes.NewReader([]byte{}))
	assert.NilError(t, fsm.Restore(rc))

	val, err := getFSMValue(fsm.db, "/foo")
	assert.NilError(t, err)
	assert.Assert(t, val == nil, "expected all data to be cleared")
}

func TestFSM_Restore_Corrupted(t *testing.T) {
	fsm := newTestFSM(t)

	rc := io.NopCloser(bytes.NewReader([]byte("{invalid json")))
	err := fsm.Restore(rc)
	assert.ErrorContains(t, err, "invalid snapshot data")
}
