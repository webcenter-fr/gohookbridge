# Gohookbridge Quick Start

This guide covers two deployment modes:

- **Local** — run gohookbridge on your machine to relay webhooks to localhost
- **Kubernetes** — deploy gohookbridge server (public endpoint) and/or client inside a cluster

---

## Prerequisites

- Go 1.25+ (for local build)
- `make` (optional, used by the Makefile)
- `kubectl` and a cluster (for Kubernetes deployment)
- A webhook source (e.g., GitHub, GitLab) to send test events

---

## Local Quick Start

### 1. Build

```shell
git clone https://github.com/webcenter-fr/gohookbridge
cd gohookbridge

# Build the binary (requires Node.js 22+ for the Nuxt web build)
make build

# Verify
./bin/gohookbridge --help
```

`make build` runs `nuxt generate` for the admin UI, copies the static output into `gohookbridge/web/static/`, and embeds it into the Go binary. Node.js 22+ is required for this step.

Or install directly:

```shell
go install -v github.com/webcenter-fr/gohookbridge@latest
```

### 2. Run the client with smee.io

The simplest way to test gohookbridge locally is to use the free [smee.io](https://smee.io) relay service.

```shell
# 1. Get a smee.io channel URL
#    Visit https://smee.io/new in your browser, or use the gohookbridge client:
./bin/gohookbridge client --new-url

# 2. Start a local service to receive webhooks (example: a simple HTTP echo server)
python3 -m http.server 8080

# 3. In another terminal, start the gohookbridge client
./bin/gohookbridge client https://smee.io/YOUR_CHANNEL_ID http://localhost:8080
```

Webhooks sent to `https://smee.io/YOUR_CHANNEL_ID` will now be forwarded to `http://localhost:8080`.

### 3. Run your own gohookbridge server locally

Instead of relying on smee.io, you can run a local gohookbridge server:

```shell
# Start the server
./bin/gohookbridge server --address 0.0.0.0 --port 3333

# Generate a channel URL
curl http://localhost:3333/new
# → http://localhost:3333/NqybHcEi

# Start the client (in another terminal)
./bin/gohookbridge client http://localhost:3333/NqybHcEi http://localhost:8080

# Send a test webhook
curl -X POST http://localhost:3333/NqybHcEi \
  -H "Content-Type: application/json" \
  -d '{"test": "hello"}'
```

The client receives the webhook and forwards it to `http://localhost:8080`.

### 4. Save and replay payloads

```shell
./bin/gohookbridge client --saveDir /tmp/savedreplay \
  https://smee.io/YOUR_CHANNEL_ID http://localhost:8080

# Replayed payloads are saved as curl scripts in /tmp/savedreplay/
# Run a saved replay:
bash /tmp/savedreplay/1718123456.sh -t http://localhost:8080
```

### 5. Client with health endpoint (for containers/K8s)

```shell
./bin/gohookbridge client --health-port 8081 \
  https://smee.io/YOUR_CHANNEL_ID http://localhost:8080
```

Health check available at `http://localhost:8081/health`.

---

## Kubernetes Quick Start

### Kubernetes with Helm (recommended)

```shell
# Deploy the server
helm install gohookbridge oci://ghcr.io/webcenter-fr/gohookbridge \
  --namespace gohookbridge --create-namespace \
  --set server.enabled=true \
  --set server.publicURL=https://webhook.example.com

# Deploy server + client
helm install gohookbridge oci://ghcr.io/webcenter-fr/gohookbridge \
  --namespace gohookbridge --create-namespace \
  --set server.enabled=true \
  --set server.publicURL=https://webhook.example.com \
  --set client.enabled=true \
  --set client.channelURL=https://webhook.example.com/my-channel \
  --set client.targetURL=http://my-service:8080

# Verify
kubectl get pods -n gohookbridge
helm status gohookbridge -n gohookbridge
```

### High Availability with Helm (3 replicas)

The chart defaults to `server.replicas: 3` and deploys the server as a StatefulSet with a headless Service, per-pod Raft PVCs, and deterministic `--raft-peers` / `--nats-routes` derived from the replica count. Each pod binds `0.0.0.0:6001` and advertises its pod FQDN (`--raft-advertise-addr`), with DNS discovery driven by `--raft-replicas` / `--raft-statefulset-name` / `--raft-headless-service` / `--raft-namespace`. Raft mTLS is enabled by default (`server.raft.tls.enabled: true`), with the internal CA shared through the `<fullname>-raft-ca` Secret. For a home/lab cluster, keep the environment-specific values in a gitignored `helm/gohookbridge/values-home.yaml`:

```yaml
fullnameOverride: gohookbridge
server:
  replicas: 3
  publicURL: "https://gohookbridge-test.home.webcenter.fr"
  bootstrap:
    enabled: true
    config:
      global:
        server:
          behind_reverse_proxy: true
      users:
        - username: admin
          password: CHANGE_ME
          roles: [admin]
          channels: ["*"]
  ingress:
    enabled: true
    className: traefik
    hosts: ["gohookbridge-test.home.webcenter.fr"]
    tls:
      - hosts: ["gohookbridge-test.home.webcenter.fr"]
        secretName: gohookbridge-test-tls
```

```shell
helm upgrade --install gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge --create-namespace \
  --values helm/gohookbridge/values-home.yaml

# Verify the rendered peer/route strings before applying
helm template gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge \
  --values helm/gohookbridge/values-home.yaml \
  | grep -A1 'raft-advertise-addr\|raft-peers\|raft-replicas\|raft-tls-ca-secret\|nats-routes\|path: /readyz\|path: /startup'
```

Each pod gets its own Raft data volume via `volumeClaimTemplates`. Scaling the StatefulSet up or down (for example 3→2 or back) is reconciled by the leader's join loop, which only adds peers whose pod DNS currently resolves. Scaling all the way down to **one** replica is special: Raft can never commit the membership change that removes the two lost voters (no quorum), so the surviving ordinal-0 pod waits for `--raft-leader-wait-timeout`, verifies `spec.replicas == 1` through the Kubernetes API, restarts once, and forces the configuration to itself with `RecoverCluster` (replicated config is preserved). A recovery bumps a cluster *generation* kept in the raft CA Secret; when the other pods return they notice the new generation, clear their now-stale configuration, and rejoin, so scaling back to 3 re-adds them automatically. The chart grants the server ServiceAccount `get`/`update` on the CA Secret and `get` on its StatefulSet for these checks (`server.raft.tls.enabled`). If the cluster instead loses quorum *without* an explicit scale-down (for example two PVCs are deleted at once), delete the PVCs and use `--set server.raft.recoveryMode=true` as described below.

#### Migrating an existing cluster to DNS discovery

Older deployments stored resolved pod IPs in the Raft configuration, which
cannot be rewritten without quorum. Migrate once with a fresh bootstrap
(config data is re-created from `bootstrap.yaml`; webhooks are ephemeral):

```shell
export KUBECONFIG=/home/user/.kube/home
# 1. Stop the old cluster to prevent split-brain while resetting.
kubectl scale statefulset gohookbridge-server -n gohookbridge --replicas=0
# 2. Delete per-pod PVCs to clear the stale all-peers/IP config.
kubectl delete pvc -n gohookbridge -l app.kubernetes.io/component=server
# 3. Re-deploy with recovery mode (clears any residual raft state on first boot).
helm upgrade --install gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge --values helm/gohookbridge/values-home.yaml \
  --set server.raft.recoveryMode=true
# 4. Verify leader election on ordinal 0 and followers joining.
kubectl logs -n gohookbridge gohookbridge-server-0 | grep -i 'raft'
# 5. Remove recovery mode for steady state (config now stores DNS names).
helm upgrade --install gohookbridge ./helm/gohookbridge \
  --namespace gohookbridge --values helm/gohookbridge/values-home.yaml \
  --set server.raft.recoveryMode=false
```

After migration, pod restarts self-heal via DNS re-resolution and membership
reconciliation — no further recovery mode is needed.

### Server and client (Helm)

The server and client are deployed with the Helm chart (see the "Kubernetes
with Helm" and "High Availability with Helm" sections above). The chart renders
the server as a StatefulSet and exposes all configuration through values:

- `server.publicURL`, `server.ingress` — public endpoint + Ingress/TLS
- `server.bootstrap.config` — admin user, projects, and global settings
  (bootstrap.yaml content, stored in a Secret)
- `client.channelURL` / `client.targetURL` — client forwarding source/target

See [`helm/gohookbridge/values.yaml`](./helm/gohookbridge/values.yaml) for the
full option list, and the [README](./README.md#bootstrap-configuration) for the
`bootstrap.yaml` format.

### High Availability Server (multi-instance with Raft + NATS)

For production deployments with multiple replicas:

```yaml
# ha-server.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: gohookbridge-server
spec:
  serviceName: gohookbridge-server
  replicas: 3
  selector:
    matchLabels:
      app: gohookbridge-server
  template:
    metadata:
      labels:
        app: gohookbridge-server
    spec:
      containers:
      - image: ghcr.io/webcenter-fr/gohookbridge:main
        name: gohookbridge-server
        args:
        - server
        - --address=0.0.0.0
        - --port=3333
        - --public-url=https://webhook.example.com
        - --raft-dir=/data/raft
        - --raft-node-id=$(POD_NAME)
        # Bind on all interfaces; advertise the pod's stable DNS name.
        - --raft-bind-addr=0.0.0.0:6001
        - --raft-advertise-addr=$(POD_NAME).gohookbridge-server:6001
        - --raft-replicas=3
        - --raft-statefulset-name=gohookbridge-server
        - --raft-headless-service=gohookbridge-server
        - --raft-namespace=$(POD_NAMESPACE)
        - --raft-cluster-domain=cluster.local
        - --raft-leader-wait-timeout=60s
        - --raft-performance-multiplier=5.0
        - --raft-tls-enabled
        - --raft-tls-ca-secret=gohookbridge-raft-ca
        - --raft-peers=gohookbridge-server-0=gohookbridge-server-0.gohookbridge-server:6001,gohookbridge-server-1=gohookbridge-server-1.gohookbridge-server:6001,gohookbridge-server-2=gohookbridge-server-2.gohookbridge-server:6001
        - --nats-port=4222
        - --nats-cluster-port=6222
        - --nats-routes=nats://gohookbridge-server-0.gohookbridge-server:6222,nats://gohookbridge-server-1.gohookbridge-server:6222,nats://gohookbridge-server-2.gohookbridge-server:6222
        - --bootstrap-config-file=/etc/gohookbridge/bootstrap.yaml
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        ports:
        - containerPort: 3333
          name: http
        - containerPort: 6001
          name: raft
        - containerPort: 4222
          name: nats
        - containerPort: 6222
          name: nats-cluster
        volumeMounts:
        - mountPath: /data/raft
          name: raft-data
        - mountPath: /etc/gohookbridge
          name: bootstrap
      volumes:
      - name: bootstrap
        configMap:
          name: gohookbridge-bootstrap
  volumeClaimTemplates:
  - metadata:
      name: raft-data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 1Gi
---
apiVersion: v1
kind: Service
metadata:
  name: gohookbridge-server
spec:
  clusterIP: None
  # Required so each pod's DNS name resolves before its readiness probe passes
  # (Raft binds/advertises that name at startup).
  publishNotReadyAddresses: true
  ports:
  - name: raft
    port: 6001
  - name: nats-cluster
    port: 6222
  selector:
    app: gohookbridge-server
---
apiVersion: v1
kind: Service
metadata:
  name: gohookbridge-server-public
spec:
  ports:
  - name: http
    port: 80
    targetPort: 3333
  selector:
    app: gohookbridge-server
  type: ClusterIP
```

Apply the HA deployment:

```shell
kubectl apply -f ha-server.yaml
```

> The raw manifest above enables Raft mTLS, so the pod's ServiceAccount needs
> `create`/`get` on Secrets in the release namespace (the Helm chart creates
> this Role/RoleBinding automatically). Without it, ordinal 0 cannot share the
> internal CA and the cluster will not form.

**Port layout for HA deployments:**

| Port | Protocol | Purpose |
|------|----------|---------|
| 3333 | HTTP | Webhook ingestion + SSE + Admin UI |
| 6001 | TCP (Raft) | Configuration consensus |
| 4222 | TCP (NATS client) | In-process NATS client (localhost only) |
| 6222 | TCP (NATS cluster) | Inter-node NATS routes |

---

## Next Steps

- **Full documentation**: [README.md](./README.md)
- **Architecture**: [design.md](./design.md)
- **Security reference**: [SECURITY.md](./SECURITY.md)
- **System services**: [misc/README.md](./misc/README.md)
