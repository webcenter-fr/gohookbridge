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
	// from inside the k3s cluster (bound as the service alias "registry").
	localRegistryHost = "registry:5000"
	// pushRegistryHost is the address of the same in-pipeline registry as
	// seen by the Dagger engine when publishing the built image. The engine
	// only speaks plain HTTP to loopback registry hosts (containerd
	// MatchLocalhost) or to registries declared in its engine config, and the
	// module must not require engine configuration — so the push goes through
	// "localhost:5000", which the RegistryService binding aliases to the same
	// registry:2 service the cluster pulls from as "registry:5000". The
	// repository path is identical, so both sides address
	// gohookbridge:<version>.
	pushRegistryHost = "localhost:5000"
	// localImageRef is where the freshly built image is published so the
	// ephemeral cluster can pull it (GHCR packages are private by default).
	localImageRef = localRegistryHost + "/gohookbridge"
	// localPushRef is the ref the engine publishes to (see pushRegistryHost).
	localPushRef = pushRegistryHost + "/gohookbridge"
)

// registriesMirror is the k3s containerd mirror config that resolves the
// "registry:5000" image prefix to the plain-HTTP in-pipeline registry.
const registriesMirror = `mirrors:
  "registry:5000":
    endpoint:
      - "http://registry:5000"
`

// k3sHolderScript moves every process out of the container's root cgroup into
// a dedicated "holder" child before k3s starts. The engine starts the
// container with all processes (init, k3s) in the root cgroup, and cgroup v2
// marks children of a *populated* cgroup as "domain invalid": they cannot
// receive domain controllers (cpu/memory/io), so runc's pod-sandbox cgroup
// creation fatals with `cannot enter cgroupv2 ... with domain controllers --
// it is in an invalid state` (opencontainers/cgroups fs2.CreateCgroupPath;
// containerd sets CPU shares on every pause container). An empty root makes
// every later child a proper "domain" cgroup, so the whole kubelet/runc
// cgroup setup behaves as on a normal host. Processes spawned afterwards
// inherit the holder.
const k3sHolderScript = `mkdir -p /sys/fs/cgroup/holder
for pid in $(cat /sys/fs/cgroup/cgroup.procs); do
  echo "$pid" > /sys/fs/cgroup/holder/cgroup.procs 2>/dev/null || true
done
`

// k3sExtraArgs are the k3s server flags shared by the generation run and the
// long-lived service:
//   - --snapshotter native: the engine's executor runs on an overlayfs-backed
//     root where the kernel rejects nested overlay mounts (otherwise k3s fatals
//     with "overlayfs snapshotter cannot be enabled").
//   - --kubelet-arg cgroups-per-qos=false + enforce-node-allocatable=: keeps
//     kubelet from creating its own node-allocatable cgroups in this
//     unprivileged nested environment (kubelet only accepts
//     cgroups-per-qos=false together with an empty enforce-node-allocatable,
//     which k3s defaults to "pods"; verified against the kubelet source of the
//     pinned k3s v1.33.6, pkg/kubelet/cm/container_manager_linux.go). The
//     single-replica smoke cluster does not need QoS classes.
const k3sExtraArgs = `--snapshotter native --kubelet-arg cgroups-per-qos=false --kubelet-arg enforce-node-allocatable=`

// k3sGenScript boots k3s once so its CA, serving certificates, client
// certificates and kubeconfig are written into the container layer (a running
// service's runtime filesystem cannot be read back otherwise), then shuts k3s
// down cleanly. The later service start reuses the generated material. On
// timeout the last k3s log lines are dumped to make the failure actionable.
const k3sGenScript = `set -eu
` + k3sHolderScript + `
k3s server --disable traefik --disable servicelb ` + k3sExtraArgs + ` >/tmp/k3s-gen.log 2>&1 &
pid=$!
i=0
while [ ! -s /etc/rancher/k3s/k3s.yaml ]; do
  i=$((i + 1))
  if [ "$i" -gt 240 ]; then
    echo "k3s did not write /etc/rancher/k3s/k3s.yaml in time; last k3s log lines:" >&2
    tail -30 /tmp/k3s-gen.log >&2 || true
    kill -9 "$pid" 2>/dev/null || true
    exit 1
  fi
  sleep 0.5
done
kill -TERM "$pid" 2>/dev/null || true
wait "$pid" || true
`

// k3sServiceArgs is the argument list appended to "/bin/k3s" when the
// generated container runs as the long-lived k3s service; the service entry
// first runs k3sHolderScript through a shell, then execs k3s. The extra args
// must match k3sGenScript (see k3sExtraArgs).
var k3sServiceArgs = append([]string{
	"server",
	"--disable", "traefik",
	"--disable", "servicelb",
}, strings.Fields(k3sExtraArgs)...)

// k3sServiceCommand is the full shell command the service container runs.
var k3sServiceCommand = k3sHolderScript + "exec /bin/k3s " + strings.Join(k3sServiceArgs, " ")

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
		Args:                     []string{"/bin/sh", "-c", k3sServiceCommand},
		UseEntrypoint:            false,
		InsecureRootCapabilities: true,
	})
	started, err := svc.Start(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("start single-node k3s service %s: %w", k3sImage, err)
	}

	// Wait for the fresh service's API server to become ready before
	// returning: discovery briefly answers 503 while the service container
	// boots, and helm/kubectl run immediately after startK3s returns.
	ready := dag.Container().From(kubectlImage).
		WithFile("/kubeconfig.yaml", kubeconfig).
		WithEnvVariable("KUBECONFIG", "/kubeconfig.yaml").
		WithServiceBinding("k3s", started).
		WithExec([]string{"sh", "-c", `i=0
until kubectl get --raw /readyz >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 60 ]; then
    echo "k3s API server not ready after 120s; /readyz says:" >&2
    kubectl get --raw /readyz >&2 || true
    exit 1
  fi
  sleep 2
done
echo "k3s API server ready"`}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	readyCode, err := ready.ExitCode(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("wait for k3s API server: %w", err)
	}
	if readyCode != 0 {
		return nil, nil, fmt.Errorf("k3s API server not ready within 120s: %s",
			strings.TrimSpace(combined(ctx, ready)))
	}

	k3sMemo = started
	kubeconfigMemo = kubeconfig
	return k3sMemo, kubeconfigMemo, nil
}

// rewriteKubeconfig turns the k3s-generated kubeconfig into a self-contained
// one usable from the pipeline's helper containers (helm, kubectl): the server
// is pointed at the service hostname "k3s" with TLS verification skipped
// (ephemeral cluster only; the serving certificate does not carry the service
// alias as a SAN), and the cluster CA reference is dropped because helm
// rejects a kubeconfig that sets insecure-skip-tls-verify alongside a CA
// file/data. The pinned k3s image emits a fully inlined kubeconfig
// (certificate-authority-data / client-certificate-data / client-key-data),
// so no file references need resolving.
func rewriteKubeconfig(kubeconfig string) (string, error) {
	lines := strings.Split(kubeconfig, "\n")
	kept := make([]string, 0, len(lines))
	serverFound := false
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(trimmed)]
		switch {
		case strings.HasPrefix(trimmed, "server:"):
			kept = append(kept,
				indent+"server: https://k3s:6443",
				indent+"insecure-skip-tls-verify: true")
			serverFound = true
		case strings.HasPrefix(trimmed, "certificate-authority"):
			// Drop file-path and data forms alike: helm refuses the
			// insecure-flag + CA combination.
		default:
			kept = append(kept, line)
		}
	}
	if !serverFound {
		return "", fmt.Errorf("rewrite k3s kubeconfig: no server field found")
	}
	return strings.Join(kept, "\n"), nil
}
