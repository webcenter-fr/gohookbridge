//go:build windows

package server

import "os"

// terminateSelf exits the process: Windows has no SIGTERM, and the container
// supervisor (Kubernetes restartPolicy: Always) restarts the pod, which is the
// recovery path the single-replica watcher relies on.
func terminateSelf() {
	os.Exit(1)
}
