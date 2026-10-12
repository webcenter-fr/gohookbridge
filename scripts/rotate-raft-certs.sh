#!/usr/bin/env bash
#
# rotate-raft-certs.sh — offline Raft/NATS TLS rotation with a dual-trust
# transition (F14).
#
# Steps performed/documented:
#   1. Generate a NEW CA + per-pod leaves (scripts/generate-raft-certs.sh).
#   2. Update the node-set Secret with the NEW leafs while the CA bundle is
#      "newCA||oldCA" so peers trust both during the rollout.
#   3. Rolling-restart the StatefulSet (one pod at a time).
#   4. After the rollout, strip the old CA so only newCA remains.
#
# pkg/tlsreload hot-reloads the mounted files without a restart once the
# Secrets are updated, so the restart in step 3 may be a no-op for certificate
# pickup (it is still required for the JWT/session material).
set -euo pipefail

OUT=""
OLD_CA=""
NAME=""
REPLICAS=""
HEADLESS=""
NAMESPACE=""
CLUSTER_DOMAIN="cluster.local"
VALIDITY_DAYS="365"

usage() {
  cat >&2 <<'EOF'
Usage: rotate-raft-certs.sh --out DIR --name NAME --replicas N --headless HEADLESS \
         --namespace NS [--old-ca PATH] [--cluster-domain CLUSTER.LOCAL] [--validity-days 365]
EOF
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --out) OUT="$2"; shift 2 ;;
    --old-ca) OLD_CA="$2"; shift 2 ;;
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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"$SCRIPT_DIR/generate-raft-certs.sh" \
  --out "$OUT" --name "$NAME" --replicas "$REPLICAS" \
  --headless "$HEADLESS" --namespace "$NAMESPACE" \
  --cluster-domain "$CLUSTER_DOMAIN" --validity-days "$VALIDITY_DAYS"

if [ -n "$OLD_CA" ] && [ -f "$OLD_CA" ]; then
  cat "$OLD_CA" >> "$OUT/nodes/ca.crt"
  echo "Dual-trust CA bundle written: $OUT/nodes/ca.crt (newCA||oldCA)"
fi

cat <<EOF

# Rotation procedure (offline):
#   1) kubectl -n $NAMESPACE delete secret $NAME-server-tls-nodes
#      kubectl -n $NAMESPACE create secret generic $NAME-server-tls-nodes --from-file=$OUT/nodes
#      (with the dual-trust ca.crt bundle while the rollout is in progress)
#   2) kubectl -n $NAMESPACE rollout restart statefulset/$NAME-server
#      kubectl -n $NAMESPACE rollout status  statefulset/$NAME-server
#   3) After every pod has the new leaf, replace ca.crt with $OUT/ca.crt
#      only (strip oldCA) and update the secret again.
#   4) Destroy $OUT/ca.key once the rotation is complete - it must never be
#      stored anywhere but the operator's offline directory.
EOF
