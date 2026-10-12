package server

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/webcenter-fr/gohookbridge/internal/repository"
)

// loadAtRestKeeper loads the at-rest encryption key set. Production/multi-node
// must supply --at-rest-key-file. A single-node dev deployment with no key file
// auto-generates one under the Raft directory (with a warning); production
// records are always encrypted.
func loadAtRestKeeper(keyFile string, replicas int, raftDir string) (*repository.CryptoKeeper, error) {
	if keyFile == "" {
		if replicas > 1 {
			fmt.Fprintf(os.Stderr, "WARNING: at-rest encryption disabled on a multi-node deployment (set --at-rest-key-file)\n")
			return nil, nil
		}
		keyFile = filepath.Join(raftDir, "at-rest-key.json")
		if _, err := os.Stat(keyFile); os.IsNotExist(err) {
			if err := repository.GenerateKeyFile(keyFile); err != nil {
				return nil, fmt.Errorf("generate dev at-rest key: %w", err)
			}
			fmt.Fprintf(os.Stderr, "WARNING: generated a dev at-rest encryption key at %s; provision a real KEK in production\n", keyFile)
		}
	}
	keeper, err := repository.LoadCryptoKeeper(keyFile)
	if err != nil {
		return nil, fmt.Errorf("at-rest key file: %w", err)
	}
	return keeper, nil
}
