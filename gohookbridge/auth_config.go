package gohookbridge

import (
	"fmt"
	"os"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
)

type AuthConfig = store.AuthConfig
type InternalConfig = store.InternalConfig
type InternalUser = store.InternalUser
type OIDCConfig = store.OIDCConfig
type OIDCProvider = store.OIDCProvider

func LoadAuthConfig(path string) (*AuthConfig, error) {
	if path == "" {
		return nil, nil
	}
	fmt.Fprintf(os.Stderr, "FATAL: --auth-config-file and GOSMEE_AUTH_CONFIG_FILE are removed.\n")
	fmt.Fprintf(os.Stderr, "Configure auth via bootstrap.yaml or the Admin UI at /admin.\n")
	fmt.Fprintf(os.Stderr, "See SECURITY.md and README.md for migration instructions.\n")
	os.Exit(1)
	return nil, nil
}