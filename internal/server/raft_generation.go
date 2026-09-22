package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// raftGenerationKey is the key inside the raft CA Secret that carries the
// cluster configuration generation.
const raftGenerationKey = "generation"

// localGenerationFile is the per-node file that records the last cluster
// generation this node synchronized with.
const localGenerationFile = "generation"

// raftGenerationStore reads and increments the cluster-wide Raft configuration
// generation shared through the CA Secret. Bumping the generation fences off
// nodes that still hold a pre-recovery configuration: on startup they see a
// newer generation, clear their stale Raft state, and rejoin the bootstrap
// node via the normal join loop.
type raftGenerationStore struct {
	client    kubernetes.Interface
	namespace string
	secret    string
}

// newRaftGenerationStore builds a generation store for the raft CA Secret. It
// returns nil when no Kubernetes clientset is available (local development),
// in which case generation fencing is disabled.
func newRaftGenerationStore(namespace, secret string) *raftGenerationStore {
	if namespace == "" || secret == "" {
		return nil
	}
	clientset, err := newK8sClientset()
	if err != nil {
		return nil
	}
	return &raftGenerationStore{client: clientset, namespace: namespace, secret: secret}
}

// read returns the current generation. The second result is false when the
// Secret (or the key) does not exist yet, which is treated as generation 0.
func (g *raftGenerationStore) read(ctx context.Context) (uint64, bool) {
	if g == nil || g.client == nil {
		return 0, false
	}
	secret, err := g.client.CoreV1().Secrets(g.namespace).Get(ctx, g.secret, metav1.GetOptions{})
	if err != nil {
		return 0, false
	}
	raw, ok := secret.Data[raftGenerationKey]
	if !ok {
		return 0, false
	}
	gen, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return gen, true
}

// bump increments the generation in the CA Secret and returns the new value.
// It is called only by the bootstrap node during a recovery.
func (g *raftGenerationStore) bump(ctx context.Context) (uint64, error) {
	if g == nil || g.client == nil {
		return 0, errors.New("raft generation store unavailable")
	}
	secrets := g.client.CoreV1().Secrets(g.namespace)
	secret, err := secrets.Get(ctx, g.secret, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("read raft generation secret: %w", err)
	}
	current := uint64(0)
	if raw, ok := secret.Data[raftGenerationKey]; ok {
		if parsed, parseErr := strconv.ParseUint(string(raw), 10, 64); parseErr == nil {
			current = parsed
		}
	}
	next := current + 1
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[raftGenerationKey] = []byte(strconv.FormatUint(next, 10))
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return 0, fmt.Errorf("update raft generation secret: %w", err)
	}
	return next, nil
}

// readLocalGeneration returns the generation this node last synchronized with.
// The second result is false when the file does not exist (first start).
func readLocalGeneration(raftDir string) (uint64, bool) {
	raw, err := os.ReadFile(filepath.Join(raftDir, localGenerationFile))
	if err != nil {
		return 0, false
	}
	gen, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return gen, true
}

// writeLocalGeneration records the synchronized generation on disk.
func writeLocalGeneration(raftDir string, gen uint64) error {
	if err := os.MkdirAll(raftDir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(raftDir, localGenerationFile), []byte(strconv.FormatUint(gen, 10)), 0o600)
}
