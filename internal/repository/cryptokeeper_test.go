package repository

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gotest.tools/v3/assert"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	assert.NilError(t, err)
	return k
}

func keeperWith(t *testing.T, primary string, kids ...string) *CryptoKeeper {
	t.Helper()
	keys := make(map[string][]byte, len(kids))
	for _, kid := range kids {
		keys[kid] = randomKey(t)
	}
	return NewCryptoKeeper(primary, keys)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	keeper := keeperWith(t, "k1", "k1")

	ev, err := keeper.Encrypt([]byte("super-secret"))
	assert.NilError(t, err)
	assert.Equal(t, ev.Alg, "A256GCM")
	assert.Equal(t, ev.KID, "k1")
	assert.Assert(t, ev.Ct != "")
	assert.Assert(t, !bytes.Contains([]byte(ev.Ct), []byte("super-secret")))

	plain, err := keeper.Decrypt(ev)
	assert.NilError(t, err)
	assert.Equal(t, string(plain), "super-secret")
}

// F27: legacy plaintext records keep working and are sealed by the migration.
func TestMigrationFromPlaintext(t *testing.T) {
	rs := newTestRaftStore(t)
	rs.keeper = keeperWith(t, "k1", "k1")
	ctx := context.Background()

	legacy := domain.Channel{ID: "legacy", WebhookSecret: "plaintext-secret"}
	raw, err := json.Marshal(legacy)
	assert.NilError(t, err)
	assert.NilError(t, rs.applyCommand("set", "/channels/legacy/", raw))

	got, err := rs.GetChannel(ctx, "legacy")
	assert.NilError(t, err)
	assert.Equal(t, got.WebhookSecret, "plaintext-secret", "plaintext must be readable before migration")

	assert.NilError(t, rs.MigrateAtRest(ctx))

	stored, err := getFSMValue(rs.db, "/channels/legacy/")
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Contains(stored, []byte("plaintext-secret")), "migration must seal the plaintext value")

	got, err = rs.GetChannel(ctx, "legacy")
	assert.NilError(t, err)
	assert.Equal(t, got.WebhookSecret, "plaintext-secret")
}

// F27: values encrypted under an old key remain readable after a rotation.
func TestKeyRotation(t *testing.T) {
	oldKeeper := keeperWith(t, "k1", "k1")
	ev, err := oldKeeper.Encrypt([]byte("rotated-secret"))
	assert.NilError(t, err)

	// New primary k2, old k1 retained for decryption.
	newKeeper := NewCryptoKeeper("k2", map[string][]byte{
		"k1": oldKeeper.keys["k1"],
		"k2": randomKey(t),
	})
	plain, err := newKeeper.Decrypt(ev)
	assert.NilError(t, err)
	assert.Equal(t, string(plain), "rotated-secret")

	ev2, err := newKeeper.Encrypt([]byte("new"))
	assert.NilError(t, err)
	assert.Equal(t, ev2.KID, "k2")
}

// F27: snapshots must never contain plaintext secrets.
func TestSnapshotEncrypted(t *testing.T) {
	rs := newTestRaftStore(t)
	rs.keeper = keeperWith(t, "k1", "k1")
	ctx := context.Background()

	assert.NilError(t, rs.CreateChannel(ctx, &domain.Channel{
		ID:            "snap",
		WebhookSecret: "snapshot-plaintext-secret",
	}))

	snap, err := rs.fsm.Snapshot()
	assert.NilError(t, err)
	sink := &mockSnapshotSink{Buffer: &bytes.Buffer{}}
	assert.NilError(t, snap.Persist(sink))
	assert.Assert(t, !bytes.Contains(sink.Bytes(), []byte("snapshot-plaintext-secret")),
		"snapshot leaked a plaintext secret")
}

// F27: data files and directories use restrictive modes.
func TestDataFilesAndSnapshotsRestrictiveModes(t *testing.T) {
	dir := t.TempDir()
	rs, err := NewRaftStore(RaftConfig{Dir: dir, NodeID: "modes", BindAddr: freeTCPAddr(t)})
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rs.Shutdown() })

	info, err := os.Stat(dir)
	assert.NilError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "raft dir must be 0700")

	dbInfo, err := os.Stat(filepath.Join(dir, "modes.db"))
	assert.NilError(t, err)
	assert.Equal(t, os.FileMode(0o600), dbInfo.Mode().Perm(), "bolt file must be 0600")
}
