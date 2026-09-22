package server

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStatefulSetReplicaReader(t *testing.T) {
	replicas := int32(1)
	reader := &statefulSetReplicaReader{
		client: fake.NewSimpleClientset(&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "gohookbridge-server", Namespace: "gohookbridge"},
			Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		}),
		namespace: "gohookbridge",
		name:      "gohookbridge-server",
	}

	got, ok := reader.replicas(context.Background())
	assert.Assert(t, ok)
	assert.Equal(t, got, int32(1))

	missing := &statefulSetReplicaReader{
		client:    reader.client,
		namespace: "gohookbridge",
		name:      "absent",
	}
	got, ok = missing.replicas(context.Background())
	assert.Assert(t, !ok)
	assert.Equal(t, got, int32(0))

	var nilReader *statefulSetReplicaReader
	got, ok = nilReader.replicas(context.Background())
	assert.Assert(t, !ok)
	assert.Equal(t, got, int32(0))
}
