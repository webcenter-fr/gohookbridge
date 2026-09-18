package server

import (
	"crypto/tls"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
	"github.com/webcenter-fr/gohookbridge/gohookbridge/store"
)

// buildRaftTLSConfig builds the raft transport tls.Config from flags. It
// returns nil when raft TLS is disabled. In auto mode the internal CA is
// shared via a K8s Secret; the bootstrap node is ordinal 0 of the StatefulSet
// (or forced with --raft-tls-ca-bootstrap).
func buildRaftTLSConfig(c *cli.Context, discovery store.RaftDiscoveryConfig, hostname string) (*tls.Config, error) {
	isMultiNode := c.Int("raft-replicas") > 1 || len(c.StringSlice("raft-peers")) > 1
	if !c.Bool("raft-tls-enabled") {
		if isMultiNode {
			log.Printf("WARNING: raft TLS is disabled on a multi-node deployment; Raft traffic (password hashes, session secret, encryption keys) is sent in cleartext (CWE-319/311)")
		}
		return nil, nil
	}

	tlsDir := c.String("raft-tls-dir")
	if tlsDir == "" {
		tlsDir = filepath.Join(c.String("raft-dir"), "tls")
	}

	caBootstrap := c.Bool("raft-tls-ca-bootstrap") || (isMultiNode && strings.HasSuffix(hostname, "-0")) || !isMultiNode

	commonName := c.String("raft-node-id")
	if commonName == "" {
		if self, err := store.NewPeerResolver(&discovery).Self(); err == nil {
			commonName = self.ID
		}
	}

	dnsNames, ipAddrs := store.PodSANs(&discovery, hostname)

	clientset, err := newK8sClientset()
	if err != nil {
		log.Printf("WARNING: raft TLS: no Kubernetes clientset (%v); CA Secret sharing disabled", err)
		clientset = nil
	}

	return store.LoadOrBuildRaftTLS(&store.RaftTLSConfig{
		Enabled:           true,
		Dir:               tlsDir,
		Validity:          c.Duration("raft-tls-validity"),
		CACertPath:        c.String("raft-tls-ca-cert"),
		CertPath:          c.String("raft-tls-cert"),
		KeyPath:           c.String("raft-tls-key"),
		CASecret:          c.String("raft-tls-ca-secret"),
		CABootstrap:       caBootstrap,
		ClientAuth:        c.Bool("raft-tls-client-auth"),
		SecretPollTimeout: c.Duration("raft-leader-wait-timeout"),
	}, isMultiNode, clientset, effectiveNamespace(c), dnsNames, ipAddrs, commonName, log.Default())
}

// effectiveNamespace returns the raft namespace: the explicit flag, else the
// POD_NAMESPACE environment variable injected by the Kubernetes downward API.
func effectiveNamespace(c *cli.Context) string {
	if ns := c.String("raft-namespace"); ns != "" {
		return ns
	}
	return os.Getenv("POD_NAMESPACE")
}
