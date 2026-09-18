package server

import (
	"context"
	"fmt"
	"log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// newK8sClientset builds a Kubernetes clientset from the in-cluster config,
// falling back to the default kubeconfig (local development). Callers tolerate
// a nil clientset: Secret-based CA sharing is then disabled.
func newK8sClientset() (kubernetes.Interface, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		restCfg, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("cannot load in-cluster or kubeconfig: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}
	return clientset, nil
}

// statefulSetReplicaReader reports the desired replica count of the Raft
// StatefulSet. It is used to decide whether an existing multi-voter Raft
// configuration may be collapsed to the surviving node: only an explicit
// scale-down to one replica (spec.replicas == 1) authorizes that destructive
// recovery. A nil reader (no in-cluster config) never authorizes recovery.
type statefulSetReplicaReader struct {
	client    kubernetes.Interface
	namespace string
	name      string
}

// newStatefulSetReplicaReader builds a reader for the given StatefulSet. It
// returns nil when the namespace/name are unset or no Kubernetes clientset is
// available (e.g. local development), so callers can skip the check.
func newStatefulSetReplicaReader(namespace, name string) *statefulSetReplicaReader {
	if namespace == "" || name == "" {
		return nil
	}
	clientset, err := newK8sClientset()
	if err != nil {
		log.Printf("WARNING: single-node raft recovery: no Kubernetes clientset (%v); automatic quorum collapse disabled", err)
		return nil
	}
	return &statefulSetReplicaReader{client: clientset, namespace: namespace, name: name}
}

// replicas returns spec.replicas. The second result is false when the
// StatefulSet cannot be read (missing RBAC, object gone, API error); callers
// must then not take any recovery action. A nil *int32 means 1 (Kubernetes
// default).
func (r *statefulSetReplicaReader) replicas(ctx context.Context) (int32, bool) {
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
