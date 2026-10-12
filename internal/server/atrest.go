package server

import (
	"fmt"
	"os"

	"github.com/webcenter-fr/gohookbridge/internal/repository"
)

// loadAtRestKeeper loads the at-rest encryption key set. When no key file is
// configured, at-rest encryption is disabled (a warning is emitted); the
// chart/production deployments must supply --at-rest-key-file.
func loadAtRestKeeper(keyFile string, replicas int) (*repository.CryptoKeeper, error) {
	if keyFile == "" {
		fmt.Fprintf(os.Stderr, "WARNING: at-rest encryption disabled (no --at-rest-key-file configured)\n")
		return nil, nil
	}
	keeper, err := repository.LoadCryptoKeeper(keyFile)
	if err != nil {
		return nil, fmt.Errorf("at-rest key file: %w", err)
	}
	if replicas > 1 {
		fmt.Fprintf(os.Stdout, "at-rest encryption enabled with primary key %s\n", keeper.PrimaryKID())
	}
	return keeper, nil
}
