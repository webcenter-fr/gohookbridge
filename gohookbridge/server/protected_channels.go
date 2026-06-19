package server

import (
	"fmt"
	"os"

	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
)

type ProtectedChannels = store.ProtectedChannels

func LoadProtectedChannels(path string) (*ProtectedChannels, error) {
	if path == "" {
		return &ProtectedChannels{}, nil
	}
	fmt.Fprintf(os.Stderr, "FATAL: --encrypted-channels-file and GOSMEE_ENCRYPTED_CHANNELS_FILE are removed.\n")
	fmt.Fprintf(os.Stderr, "Configure encrypted channels per-project via bootstrap.yaml or Admin UI.\n")
	fmt.Fprintf(os.Stderr, "See SECURITY.md and README.md for migration instructions.\n")
	os.Exit(1)
	return nil, nil
}