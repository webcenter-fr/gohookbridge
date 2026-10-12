#!/usr/bin/env bash
#
# generate-raft-certs.sh — offline generation of the Raft/NATS/leader-forward
# TLS material for a multi-node gohookbridge deployment.
#
# The CA private key (ca.key) is written to the output directory ONLY and must
# be kept offline: it is never stored in a cluster Secret and never mounted by
# server pods (F14).
#
# Emits:
#   DIR/ca.crt, DIR/ca.key                       (CA; keep ca.key offline)
#   DIR/<ordinal>/tls.crt, DIR/<ordinal>/tls.key (per-pod leaf + key)
#   DIR/nodes/ca.crt, DIR/nodes/<ordinal>.crt, DIR/nodes/<ordinal>.key
#                                                (input for the node-set Secret)
#
# and prints the exact `kubectl create secret` commands for the pre-created
# Secrets consumed by the chart.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: generate-raft-certs.sh --out DIR --name NAME --replicas N --headless HEADLESS \
         --namespace NS [--cluster-domain CLUSTER.LOCAL] [--validity-days 365]
EOF
  exit 2
}

OUT=""
NAME=""
REPLICAS=""
HEADLESS=""
NAMESPACE=""
CLUSTER_DOMAIN="cluster.local"
VALIDITY_DAYS="365"

while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --replicas) REPLICAS="$2"; shift 2 ;;
    --headless) HEADLESS="$2"; shift 2 ;;
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --cluster-domain) CLUSTER_DOMAIN="$2"; shift 2 ;;
    --validity-days) VALIDITY_DAYS="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done

[ -n "$OUT" ] && [ -n "$NAME" ] && [ -n "$REPLICAS" ] && [ -n "$HEADLESS" ] && [ -n "$NAMESPACE" ] || usage
case "$REPLICAS" in
  ''|*[!0-9]*) echo "--replicas must be a positive integer" >&2; exit 2 ;;
esac
[ "$REPLICAS" -ge 1 ] || { echo "--replicas must be >= 1" >&2; exit 2; }

command -v openssl >/dev/null 2>&1 || { echo "openssl is required" >&2; exit 1; }

umask 077
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

# --- CA (keep ca.key offline) ---
openssl genrsa -out "$OUT/ca.key" 4096 >/dev/null 2>&1
openssl req -x509 -new -nodes -key "$OUT/ca.key" -sha256 -days "$VALIDITY_DAYS" \
  -subj "/CN=$NAME-raft-ca" -out "$OUT/ca.crt" >/dev/null 2>&1

NODES_DIR="$OUT/nodes"
mkdir -p "$NODES_DIR"
cp "$OUT/ca.crt" "$NODES_DIR/ca.crt"

for i in $(seq 0 $((REPLICAS - 1))); do
  dir="$OUT/$i"
  mkdir -p "$dir"
  openssl genrsa -out "$dir/tls.key" 2048 >/dev/null 2>&1

  svc="$HEADLESS.$NAMESPACE.svc"
  [ -n "$CLUSTER_DOMAIN" ] && svc="$svc.$CLUSTER_DOMAIN"
  cat > "$dir/leaf.cnf" <<EOF
[req]
distinguished_name = dn
req_extensions = req_ext
prompt = no
[dn]
CN = $NAME-server-$i
[req_ext]
subjectAltName = @alt
[alt]
DNS.1 = $NAME-server-$i.$svc
DNS.2 = $svc
EOF

  openssl req -new -key "$dir/tls.key" -out "$dir/tls.csr" -config "$dir/leaf.cnf" >/dev/null 2>&1
  openssl x509 -req -in "$dir/tls.csr" -CA "$OUT/ca.crt" -CAkey "$OUT/ca.key" -CAcreateserial \
    -out "$dir/tls.crt" -days "$VALIDITY_DAYS" -sha256 -extensions req_ext -extfile "$dir/leaf.cnf" >/dev/null 2>&1
  rm -f "$dir/tls.csr" "$dir/leaf.cnf"

  cp "$dir/tls.crt" "$NODES_DIR/$i.crt"
  cp "$dir/tls.key" "$NODES_DIR/$i.key"
done
rm -f "$OUT/ca.srl"

cat <<EOF

# Offline material written to $OUT (keep $OUT/ca.key offline!).

# 1) Public CA Secret (ca.crt only):
kubectl -n $NAMESPACE create secret generic $NAME-server-tls-ca \\
  --from-file=ca.crt=$OUT/ca.crt

# 2) Node-set Secret (ca.crt + <ordinal>.crt/.key), mounted by all server pods:
kubectl -n $NAMESPACE create secret generic $NAME-server-tls-nodes \\
  --from-file=$NODES_DIR

# The chart mounts $NAME-server-tls-nodes at /etc/gohookbridge/tls and projects
# each pod's own \$(POD_NAME).crt/.key. Perform a rolling restart after
# creating or rotating the Secrets.
EOF
