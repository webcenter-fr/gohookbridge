package repository

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disaster37/go-raft/storage"
	"github.com/disaster37/go-raft/testutil"
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

func keeperWith(t *testing.T, kids ...string) *storage.AESGCMKeeper {
	t.Helper()
	keys := make(map[string][]byte, len(kids))
	for _, kid := range kids {
		keys[kid] = randomKey(t)
	}
	primary := ""
	if len(kids) > 0 {
		primary = kids[0]
	}
	keeper, err := storage.NewAESGCMKeeper(primary, keys, AtRestAAD)
	assert.NilError(t, err)
	return keeper
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	keeper := keeperWith(t, "k1")

	ev, err := keeper.Seal([]byte("super-secret"))
	assert.NilError(t, err)
	assert.Equal(t, ev.Alg, "A256GCM")
	assert.Equal(t, ev.KID, "k1")
	assert.Assert(t, ev.Ct != "")
	assert.Assert(t, !strings.Contains(ev.Ct, "super-secret"))

	plain, err := keeper.Open(ev)
	assert.NilError(t, err)
	assert.Equal(t, string(plain), "super-secret")
}

// F27: legacy plaintext records keep working and are sealed by the migration.
func TestMigrationFromPlaintext(t *testing.T) {
	rs := newTestRaftStore(t)
	rs.sealer = keeperWith(t, "k1")
	ctx := context.Background()

	legacy := domain.Channel{ID: "legacy", WebhookSecret: "plaintext-secret"}
	raw, err := json.Marshal(legacy)
	assert.NilError(t, err)
	assert.NilError(t, rs.putValue(ctx, "/channels/legacy/", raw))

	got, err := rs.GetChannel(ctx, "legacy")
	assert.NilError(t, err)
	assert.Equal(t, got.WebhookSecret, "plaintext-secret", "plaintext must be readable before migration")

	assert.NilError(t, rs.MigrateAtRest(ctx))

	stored, err := rs.fsm.Get("/channels/legacy/")
	assert.NilError(t, err)
	assert.Assert(t, !bytes.Contains(stored, []byte("plaintext-secret")), "migration must seal the plaintext value")

	got, err = rs.GetChannel(ctx, "legacy")
	assert.NilError(t, err)
	assert.Equal(t, got.WebhookSecret, "plaintext-secret")
}

// F27: values encrypted under an old key remain readable after a rotation.
func TestKeyRotation(t *testing.T) {
	k1 := randomKey(t)
	oldKeeper, err := storage.NewAESGCMKeeper("k1", map[string][]byte{"k1": k1}, AtRestAAD)
	assert.NilError(t, err)
	ev, err := oldKeeper.Seal([]byte("rotated-secret"))
	assert.NilError(t, err)

	// New primary k2, old k1 retained for decryption.
	newKeeper, err := storage.NewAESGCMKeeper("k2", map[string][]byte{
		"k1": k1,
		"k2": randomKey(t),
	}, AtRestAAD)
	assert.NilError(t, err)
	plain, err := newKeeper.Open(ev)
	assert.NilError(t, err)
	assert.Equal(t, string(plain), "rotated-secret")

	ev2, err := newKeeper.Seal([]byte("new"))
	assert.NilError(t, err)
	assert.Equal(t, ev2.KID, "k2")
}

// F27: snapshots must never contain plaintext secrets.
func TestSnapshotEncrypted(t *testing.T) {
	rs := newTestRaftStore(t)
	rs.sealer = keeperWith(t, "k1")
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
	ln := testutil.FreeListener(t)
	rs, err := NewRaftStore(RaftConfig{
		Dir:            dir,
		NodeID:         "modes",
		BindAddr:       testutil.FreeAddrFromListener(t, ln),
		CustomListener: ln,
	})
	assert.NilError(t, err)
	t.Cleanup(func() { _ = rs.Shutdown() })

	info, err := os.Stat(dir)
	assert.NilError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "raft dir must be 0700")

	dbInfo, err := os.Stat(filepath.Join(dir, "modes.db"))
	assert.NilError(t, err)
	assert.Equal(t, os.FileMode(0o600), dbInfo.Mode().Perm(), "bolt file must be 0600")
}
