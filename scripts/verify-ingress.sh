#!/usr/bin/env bash
#
# verify-ingress.sh — verify that the PUBLIC edge only exposes the webhook
# listener and health, while the internal API is unreachable from the public
# host (F06). Run against the real edge from inside the cluster or from an
# allowed network.
set -eu

PUBLIC_URL=""
INTERNAL_HOST=""
EXPECT_INTERNAL_BLOCKED="false"
WEBHOOK_CHANNEL=""

usage() {
  cat >&2 <<'EOF'
Usage: verify-ingress.sh --public-url URL --internal-host HOST \
         [--expect-internal-blocked true|false] [--webhook-channel CHANNEL]
EOF
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --public-url) PUBLIC_URL="$2"; shift 2 ;;
    --internal-host) INTERNAL_HOST="$2"; shift 2 ;;
    --expect-internal-blocked) EXPECT_INTERNAL_BLOCKED="$2"; shift 2 ;;
    --webhook-channel) WEBHOOK_CHANNEL="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done

[ -n "$PUBLIC_URL" ] && [ -n "$INTERNAL_HOST" ] || usage

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

status_of() {
  # status_of URL [HOST]
  local url="$1" host="${2:-}"
  if [ -n "$host" ]; then
    curl -s -o /dev/null -w '%{http_code}' -H "Host: $host" "$url" || true
  else
    curl -s -o /dev/null -w '%{http_code}' "$url" || true
  fi
}

# 1) Public health must be reachable.
code="$(status_of "$PUBLIC_URL/health")"
[ "$code" = "200" ] || fail "public /health expected 200, got $code"
pass "public /health = 200"

# 2) The auth API must NOT be exposed on the public host.
code="$(status_of "$PUBLIC_URL/api/auth/methods")"
case "$code" in
  404|000) pass "public /api/auth/methods is not exposed ($code)" ;;
  *) fail "public /api/auth/methods returned $code (expected 404/000)" ;;
esac

# 3) The internal host must not be able to reach /api/*.
if [ "$EXPECT_INTERNAL_BLOCKED" = "true" ]; then
  code="$(status_of "$PUBLIC_URL/api/me" "$INTERNAL_HOST")"
  case "$code" in
    200) fail "internal host reached /api/me on the public URL ($code)" ;;
    *) pass "internal host cannot reach /api/me on the public URL ($code)" ;;
  esac
fi

# 4) A webhook POST must be accepted when a channel is provided.
if [ -n "$WEBHOOK_CHANNEL" ]; then
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' \
    -d '{"verify":"ingress"}' \
    "$PUBLIC_URL/$WEBHOOK_CHANNEL" || true)"
  [ "$code" = "202" ] || fail "webhook POST expected 202, got $code"
  pass "webhook POST = 202"
fi

echo "verify-ingress: OK"
