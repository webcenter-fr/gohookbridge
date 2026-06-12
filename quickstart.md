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

# Build the binary
make build

# Verify
./bin/gohookbridge --help
```

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

### Server Deployment

Deploy the gohookbridge server to receive webhooks from external sources:

```shell
# 1. Edit the public URL in misc/gohookbridge-server-deployment.yaml
#    Replace https://yourserver.example.com with your actual domain

# 2. Apply the deployment
kubectl apply -f misc/gohookbridge-server-deployment.yaml

# 3. Expose the service externally (example with an Ingress)
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gohookbridge-server
spec:
  rules:
  - host: webhook.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: gohookbridge-server
            port:
              number: 80
EOF

# 4. Verify the server is running
kubectl get pods -l app=gohookbridge-server
kubectl logs deployment/gohookbridge-server

# 5. Generate a channel and send a test webhook
CHANNEL=$(curl -s https://webhook.example.com/new)
curl -X POST "https://webhook.example.com/${CHANNEL##*/}" \
  -H "Content-Type: application/json" \
  -d '{"event": "test"}'
```

#### Bootstrap configuration

On first boot, you can initialize the Raft store with an admin user, projects, and global settings:

```shell
# Create a bootstrap ConfigMap
kubectl create configmap gohookbridge-bootstrap --from-file=bootstrap.yaml

# Edit the deployment to add:
#   --bootstrap-config-file /etc/gohookbridge/bootstrap.yaml
# and mount the ConfigMap at /etc/gohookbridge/
```

See the [README](./README.md#bootstrap-configuration) for the `bootstrap.yaml` format.

### Client Deployment

Deploy the gohookbridge client to relay webhooks from a server to an internal service:

```shell
# 1. Edit misc/gohookbridge-client-deployment.yaml
#    Replace the arguments:
#      - "https://yourserver.example.com/your-channel"  → your gohookbridge server or smee.io URL
#      - "http://your-internal-service.namespace:8080"   → your internal service URL

# 2. Apply the deployment
kubectl apply -f misc/gohookbridge-client-deployment.yaml

# 3. Verify
kubectl get pods -l app=gohookbridge-client
kubectl logs deployment/gohookbridge-client
```

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
        - --raft-bind-addr=0.0.0.0:6001
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
