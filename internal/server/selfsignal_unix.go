//go:build !windows

package server

import (
	"os"
	"syscall"
)

// terminateSelf asks the process to terminate with SIGTERM so the graceful
// shutdown path (raft step-down + HTTP server drain) runs, mirroring what a
// SIGTERM from Kubernetes does during a rolling restart. It is used by the
// single-replica recovery watcher to force a self-restart.
func terminateSelf() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}
