package server

import (
	"context"
	"log"
	"os"

	raftk8s "github.com/disaster37/go-raft/k8s"
	"github.com/urfave/cli/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// newK8sClientset builds a Kubernetes clientset from the in-cluster config,
// falling back to the default kubeconfig (local development). Callers tolerate
// a nil clientset: Secret-based CA sharing is then disabled.
func newK8sClientset() (kubernetes.Interface, error) {
	return raftk8s.NewClientset()
}

// effectiveNamespace returns the raft namespace: the explicit flag, else the
// POD_NAMESPACE environment variable injected by the Kubernetes downward API.
func effectiveNamespace(c *cli.Context) string {
	if ns := c.String("raft-namespace"); ns != "" {
		return ns
	}
	return os.Getenv("POD_NAMESPACE")
}

// replicaReaderAdapter wraps go-raft's StatefulSetReplicaReader so that a
// StatefulSet existing with Spec.Replicas == nil defaults to (1, true) per the
// Kubernetes StatefulSet spec.
type replicaReaderAdapter struct {
	client    kubernetes.Interface
	namespace string
	name      string
}

// newStatefulSetReplicaReader builds a reader for the given StatefulSet. It
// returns nil when the namespace/name are unset or no Kubernetes clientset is
// available (e.g. local development), so callers can skip the check.
func newStatefulSetReplicaReader(namespace, name string) *replicaReaderAdapter {
	if namespace == "" || name == "" {
		return nil
	}
	clientset, err := newK8sClientset()
	if err != nil {
		log.Printf("WARNING: single-node raft recovery: no Kubernetes clientset (%v); automatic quorum collapse disabled", err)
		return nil
	}
	return &replicaReaderAdapter{client: clientset, namespace: namespace, name: name}
}

// Replicas returns spec.replicas. The second result is false when the
// StatefulSet cannot be read (missing RBAC, object gone, API error); callers
// must then not take any recovery action. A nil *int32 means 1 (Kubernetes
// default).
func (r *replicaReaderAdapter) Replicas(ctx context.Context) (int32, bool) {
	if r == nil || r.client == nil {
		return 0, false
	}
	sts, err := r.client.AppsV1().StatefulSets(r.namespace).Get(ctx, r.name, metav1.GetOptions{})
	if err != nil {
		return 0, false
	}
	if sts.Spec.Replicas == nil {
		return 1, true
	}
	return *sts.Spec.Replicas, true
}

// softGenerationFencer wraps go-raft's GenerationStore so a missing Secret
// update RBAC logs a warning instead of failing pod startup (F14).
type softGenerationFencer struct {
	store *raftk8s.GenerationStore
}

// newSoftGenerationFencer builds a fencer for the raft CA Secret. It returns
// nil when no Kubernetes clientset is available (local development), in which
// case generation fencing is disabled.
func newSoftGenerationFencer(clientset kubernetes.Interface, namespace, secret string) *softGenerationFencer {
	if clientset == nil || namespace == "" || secret == "" {
		return nil
	}
	return &softGenerationFencer{store: raftk8s.NewGenerationStore(clientset, namespace, secret)}
}

func (g *softGenerationFencer) ReadClusterGeneration(ctx context.Context) (uint64, bool) {
	if g == nil || g.store == nil {
		return 0, false
	}
	return g.store.ReadClusterGeneration(ctx)
}

func (g *softGenerationFencer) BumpClusterGeneration(ctx context.Context) (uint64, error) {
	if g == nil || g.store == nil {
		return 0, nil
	}
	current, err := g.store.BumpClusterGeneration(ctx)
	if err != nil {
		log.Printf("WARNING: raft generation bump failed (%v); stale nodes may not rejoin cleanly", err)
	}
	return current, nil
}
