package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"dagger/gohookbridge/internal/dagger"
)

const (
	// registryImage is the in-pipeline OCI registry the cluster pulls from.
	registryImage = "registry:2"
	// localRegistryHost is the address of the in-pipeline registry as seen
	// from inside the k3s cluster.
	localRegistryHost = "registry:5000"
	// localImageRef is where the freshly built image is published so the
	// ephemeral cluster can pull it (GHCR packages are private by default).
	localImageRef = localRegistryHost + "/gohookbridge"
)

// registriesMirror is the k3s containerd mirror config that resolves the
// "registry:5000" image prefix to the plain-HTTP in-pipeline registry.
const registriesMirror = `mirrors:
  "registry:5000":
    endpoint:
      - "http://registry:5000"
`

// k3sGenScript boots k3s once so its CA, serving certificates, client
// certificates and kubeconfig are written into the container layer (a running
// service's runtime filesystem cannot be read back otherwise), then shuts k3s
// down cleanly. The later service start reuses the generated material.
const k3sGenScript = `set -eu
k3s server --disable traefik --disable servicelb >/dev/null 2>&1 &
pid=$!
i=0
while [ ! -s /etc/rancher/k3s/k3s.yaml ]; do
  i=$((i + 1))
  if [ "$i" -gt 240 ]; then
    echo "k3s did not write /etc/rancher/k3s/k3s.yaml in time" >&2
    kill -9 "$pid" 2>/dev/null || true
    exit 1
  fi
  sleep 0.5
done
kill -TERM "$pid" 2>/dev/null || true
wait "$pid" || true
`

// k3sServiceArgs is the command used when the generated container runs as the
// long-lived k3s service (prepended with the image entrypoint /bin/k3s).
var k3sServiceArgs = []string{"server", "--disable", "traefik", "--disable", "servicelb"}

// Memoize the shared in-pipeline services so that every consumer (local
// push, helm, validation) binds the same running instance. Separate mutexes
// avoid a lock cycle: startK3s calls startRegistry.
var (
	registryLock   sync.Mutex
	registryMemo   *dagger.Service
	k3sLock        sync.Mutex
	k3sMemo        *dagger.Service
	kubeconfigMemo *dagger.File
)

// startRegistry starts an in-pipeline registry:2 service (hostname "registry",
// port 5000) that the k3s cluster pulls the freshly built image from. Avoids
// depending on private-by-default GHCR packages inside the ephemeral cluster.
// The service is memoized: every consumer binds the same instance.
func startRegistry(ctx context.Context) (*dagger.Service, error) {
	registryLock.Lock()
	defer registryLock.Unlock()
	if registryMemo != nil {
		return registryMemo, nil
	}

	svc := dag.Container().From(registryImage).
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.NetworkProtocolTcp,
			Description: "ephemeral OCI registry for the smoke cluster",
		}).
		AsService()
	started, err := svc.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("start in-pipeline registry %s: %w", registryImage, err)
	}
	registryMemo = started
	return registryMemo, nil
}

// startK3s starts a single-node k3s service (hostname "k3s") with a registries
// mirror for "registry:5000", and returns (service, kubeconfig) where the
// kubeconfig has server rewritten to "https://k3s:6443" and
// insecure-skip-tls-verify:true (ephemeral cluster only). The cluster is
// memoized: repeated calls return the same instance.
func startK3s(ctx context.Context) (*dagger.Service, *dagger.File, error) {
	k3sLock.Lock()
	defer k3sLock.Unlock()
	if k3sMemo != nil && kubeconfigMemo != nil {
		return k3sMemo, kubeconfigMemo, nil
	}

	registry, err := startRegistry(ctx)
	if err != nil {
		return nil, nil, err
	}

	base := dag.Container().From(k3sImage).
		WithFile("/etc/rancher/k3s/registries.yaml",
			dag.Directory().WithNewFile("registries.yaml", registriesMirror).File("registries.yaml")).
		WithExposedPort(6443, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.NetworkProtocolTcp,
			Description: "k3s Kubernetes API server",
		}).
		WithServiceBinding("registry", registry)

	// Generate the certificates and kubeconfig into the container layer, then
	// shut down so the service can boot on top of the generated material.
	generated := base.WithExec([]string{"sh", "-c", k3sGenScript}, dagger.ContainerWithExecOpts{
		InsecureRootCapabilities: true,
	})
	kubeconfigYAML, err := generated.File("/etc/rancher/k3s/k3s.yaml").Contents(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("extract k3s kubeconfig: %w", err)
	}
	rewritten, err := rewriteKubeconfig(kubeconfigYAML)
	if err != nil {
		return nil, nil, err
	}
	kubeconfig := dag.Directory().WithNewFile("kubeconfig.yaml", rewritten).File("kubeconfig.yaml")

	svc := generated.AsService(dagger.ContainerAsServiceOpts{
		Args:                     k3sServiceArgs,
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	})
	started, err := svc.Start(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("start single-node k3s service %s: %w", k3sImage, err)
	}
	k3sMemo = started
	kubeconfigMemo = kubeconfig
	return k3sMemo, kubeconfigMemo, nil
}

// rewriteKubeconfig points the single-cluster kubeconfig at the service
// hostname "k3s" and skips TLS verification (ephemeral cluster only; the
// serving certificate does not carry the service alias as a SAN).
func rewriteKubeconfig(kubeconfig string) (string, error) {
	lines := strings.Split(kubeconfig, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if !strings.HasPrefix(trimmed, "server:") {
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		lines[i] = indent + "server: https://k3s:6443\n" + indent + "insecure-skip-tls-verify: true"
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("rewrite k3s kubeconfig: no server field found")
}
