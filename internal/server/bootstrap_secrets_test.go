package server

import (
	"context"
	"testing"

	"github.com/webcenter-fr/gohookbridge/internal/repository"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func bootstrapCfgWithRef(ref *repository.SecretRef) *repository.BootstrapConfig {
	return &repository.BootstrapConfig{
		Auth: &repository.BootstrapAuth{
			OIDC: &repository.BootstrapOIDCAuth{
				Providers: []repository.BootstrapOIDCProvider{
					{
						ID:              "google",
						ClientID:        "client-id",
						ClientSecretRef: ref,
						IssuerURL:       "https://accounts.google.com",
					},
				},
			},
		},
	}
}

func TestHasBootstrapSecretRefs(t *testing.T) {
	assert.Equal(t, hasBootstrapSecretRefs(nil), false)
	assert.Equal(t, hasBootstrapSecretRefs(&repository.BootstrapConfig{}), false)
	assert.Equal(t, hasBootstrapSecretRefs(bootstrapCfgWithRef(nil)), false)
	assert.Equal(t, hasBootstrapSecretRefs(bootstrapCfgWithRef(&repository.SecretRef{Name: "s", Key: "k"})), true)
}

func TestResolveBootstrapSecretRefs(t *testing.T) {
	t.Run("resolves ref to value", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "oidc-secrets", Namespace: "gohookbridge"},
			Data:       map[string][]byte{"google-client-secret": []byte("resolved-secret")},
		})
		cfg := bootstrapCfgWithRef(&repository.SecretRef{Name: "oidc-secrets", Key: "google-client-secret"})

		err := resolveBootstrapSecretRefs(context.Background(), cfg, clientset, "gohookbridge")
		assert.NilError(t, err)

		p := cfg.Auth.OIDC.Providers[0]
		assert.Equal(t, p.ClientSecret, "resolved-secret")
		assert.Assert(t, p.ClientSecretRef == nil)
	})

	t.Run("missing name or key", func(t *testing.T) {
		cfg := bootstrapCfgWithRef(&repository.SecretRef{Name: "", Key: "k"})
		err := resolveBootstrapSecretRefs(context.Background(), cfg, fake.NewSimpleClientset(), "ns")
		assert.ErrorContains(t, err, "requires both name and key")
	})

	t.Run("missing secret", func(t *testing.T) {
		cfg := bootstrapCfgWithRef(&repository.SecretRef{Name: "absent", Key: "k"})
		err := resolveBootstrapSecretRefs(context.Background(), cfg, fake.NewSimpleClientset(), "ns")
		assert.ErrorContains(t, err, "read secret")
	})

	t.Run("missing key", func(t *testing.T) {
		clientset := fake.NewSimpleClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "oidc-secrets", Namespace: "ns"},
			Data:       map[string][]byte{"other": []byte("x")},
		})
		cfg := bootstrapCfgWithRef(&repository.SecretRef{Name: "oidc-secrets", Key: "absent"})
		err := resolveBootstrapSecretRefs(context.Background(), cfg, clientset, "ns")
		assert.ErrorContains(t, err, "has no key")
	})

	t.Run("nil clientset", func(t *testing.T) {
		cfg := bootstrapCfgWithRef(&repository.SecretRef{Name: "s", Key: "k"})
		err := resolveBootstrapSecretRefs(context.Background(), cfg, nil, "ns")
		assert.ErrorContains(t, err, "requires a Kubernetes clientset")
	})
}
