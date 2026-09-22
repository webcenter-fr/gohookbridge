package server

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLocalGenerationRoundTrip(t *testing.T) {
	dir := t.TempDir()

	_, ok := readLocalGeneration(dir)
	assert.Assert(t, !ok)

	assert.NilError(t, writeLocalGeneration(dir, 7))
	gen, ok := readLocalGeneration(dir)
	assert.Assert(t, ok)
	assert.Equal(t, gen, uint64(7))
}

func TestRaftGenerationStore(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gohookbridge-raft-ca", Namespace: "gohookbridge"},
		Data:       map[string][]byte{raftGenerationKey: []byte("2")},
	})
	store := &raftGenerationStore{
		client:    client,
		namespace: "gohookbridge",
		secret:    "gohookbridge-raft-ca",
	}

	gen, ok := store.read(context.Background())
	assert.Assert(t, ok)
	assert.Equal(t, gen, uint64(2))

	next, err := store.bump(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, next, uint64(3))

	gen, ok = store.read(context.Background())
	assert.Assert(t, ok)
	assert.Equal(t, gen, uint64(3))

	missing := &raftGenerationStore{client: client, namespace: "gohookbridge", secret: "absent"}
	_, ok = missing.read(context.Background())
	assert.Assert(t, !ok)
	_, err = missing.bump(context.Background())
	assert.Assert(t, err != nil)

	var nilStore *raftGenerationStore
	_, ok = nilStore.read(context.Background())
	assert.Assert(t, !ok)
	gen, err = nilStore.bump(context.Background())
	assert.ErrorContains(t, err, "unavailable")
	assert.Equal(t, gen, uint64(0))
}
