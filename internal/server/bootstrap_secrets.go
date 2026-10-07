package server

import (
	"context"
	"fmt"

	"github.com/webcenter-fr/gohookbridge/internal/repository"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// hasBootstrapSecretRefs reports whether any bootstrap OIDC provider carries an
// unresolved client_secret_ref.
func hasBootstrapSecretRefs(cfg *repository.BootstrapConfig) bool {
	if cfg == nil || cfg.Auth == nil || cfg.Auth.OIDC == nil {
		return false
	}
	for _, p := range cfg.Auth.OIDC.Providers {
		if p.ClientSecretRef != nil {
			return true
		}
	}
	return false
}

// resolveBootstrapSecretRefs replaces each OIDC client_secret_ref with the
// value read from the referenced Kubernetes Secret (release namespace).
func resolveBootstrapSecretRefs(ctx context.Context, cfg *repository.BootstrapConfig, clientset kubernetes.Interface, namespace string) error {
	if cfg == nil || cfg.Auth == nil || cfg.Auth.OIDC == nil {
		return nil
	}
	for i := range cfg.Auth.OIDC.Providers {
		p := &cfg.Auth.OIDC.Providers[i]
		if p.ClientSecretRef == nil {
			continue
		}
		if p.ClientSecretRef.Name == "" || p.ClientSecretRef.Key == "" {
			return fmt.Errorf("oidc provider %q: client_secret_ref requires both name and key", p.ID)
		}
		if clientset == nil {
			return fmt.Errorf("oidc provider %q: client_secret_ref requires a Kubernetes clientset", p.ID)
		}
		secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, p.ClientSecretRef.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("oidc provider %q: read secret %q: %w", p.ID, p.ClientSecretRef.Name, err)
		}
		val, ok := secret.Data[p.ClientSecretRef.Key]
		if !ok {
			return fmt.Errorf("oidc provider %q: secret %q has no key %q", p.ID, p.ClientSecretRef.Name, p.ClientSecretRef.Key)
		}
		p.ClientSecret = string(val)
		p.ClientSecretRef = nil
	}
	return nil
}
