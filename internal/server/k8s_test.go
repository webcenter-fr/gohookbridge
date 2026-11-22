package server

import (
	"context"
	"flag"
	"testing"

	raftk8s "github.com/disaster37/go-raft/k8s"
	"github.com/urfave/cli/v2"
	"github.com/webcenter-fr/gohookbridge/internal/app"
	"gotest.tools/v3/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newTestCLIContext builds a cli.Context with the server flags and their
// defaults, optionally overridden by args.
func newTestCLIContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()
	cliApp := cli.NewApp()
	cliApp.Flags = app.ServerFlags
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	for _, f := range cliApp.Flags {
		assert.NilError(t, f.Apply(set))
	}
	assert.NilError(t, set.Parse(args))
	return cli.NewContext(cliApp, set, nil)
}

func TestEffectiveNamespace(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "from-env")
	assert.Equal(t, effectiveNamespace(newTestCLIContext(t)), "from-env")
	assert.Equal(t, effectiveNamespace(newTestCLIContext(t, "--raft-namespace", "explicit")), "explicit")
}

func TestStatefulSetReplicaReader(t *testing.T) {
	replicas := int32(1)
	reader := &replicaReaderAdapter{
		client: fake.NewSimpleClientset(&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "gohookbridge-server", Namespace: "gohookbridge"},
			Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		}),
		namespace: "gohookbridge",
		name:      "gohookbridge-server",
	}

	got, ok := reader.Replicas(context.Background())
	assert.Assert(t, ok)
	assert.Equal(t, got, int32(1))

	// Spec.Replicas == nil defaults to (1, true) per the StatefulSet spec.
	nilReplicas := &replicaReaderAdapter{
		client: fake.NewSimpleClientset(&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "nil-replicas", Namespace: "gohookbridge"},
		}),
		namespace: "gohookbridge",
		name:      "nil-replicas",
	}
	got, ok = nilReplicas.Replicas(context.Background())
	assert.Assert(t, ok)
	assert.Equal(t, got, int32(1))

	missing := &replicaReaderAdapter{
		client:    reader.client,
		namespace: "gohookbridge",
		name:      "absent",
	}
	got, ok = missing.Replicas(context.Background())
	assert.Assert(t, !ok)
	assert.Equal(t, got, int32(0))

	var nilReader *replicaReaderAdapter
	got, ok = nilReader.Replicas(context.Background())
	assert.Assert(t, !ok)
	assert.Equal(t, got, int32(0))
}

func TestSoftGenerationFencer(t *testing.T) {
	const ns, secret = "gohookbridge", "raft-ca"

	t.Run("read missing secret returns false", func(t *testing.T) {
		fencer := &softGenerationFencer{store: raftk8s.NewGenerationStore(fake.NewSimpleClientset(), ns, secret)}
		gen, ok := fencer.ReadClusterGeneration(context.Background())
		assert.Assert(t, !ok)
		assert.Equal(t, gen, uint64(0))
	})

	t.Run("bump increments the generation", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secret, Namespace: ns},
		})
		fencer := &softGenerationFencer{store: raftk8s.NewGenerationStore(clientset, ns, secret)}
		gen, err := fencer.BumpClusterGeneration(context.Background())
		assert.NilError(t, err)
		assert.Equal(t, gen, uint64(1))

		read, ok := fencer.ReadClusterGeneration(context.Background())
		assert.Assert(t, ok)
		assert.Equal(t, read, uint64(1))
	})

	t.Run("rbac-forbidden bump returns nil error with warning", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secret, Namespace: ns},
		})
		clientset.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, &forbiddenError{}
		})
		fencer := &softGenerationFencer{store: raftk8s.NewGenerationStore(clientset, ns, secret)}
		gen, err := fencer.BumpClusterGeneration(context.Background())
		assert.NilError(t, err, "a forbidden bump must not abort startup")
		assert.Equal(t, gen, uint64(0))
	})

	t.Run("nil fencer is inert", func(t *testing.T) {
		var fencer *softGenerationFencer
		gen, ok := fencer.ReadClusterGeneration(context.Background())
		assert.Assert(t, !ok)
		assert.Equal(t, gen, uint64(0))
		bumped, err := fencer.BumpClusterGeneration(context.Background())
		assert.NilError(t, err)
		assert.Equal(t, bumped, uint64(0))
	})
}

// forbiddenError mimics a Kubernetes RBAC Forbidden error.
type forbiddenError struct{}

func (e *forbiddenError) Error() string { return "secrets is forbidden: User cannot update resource" }
