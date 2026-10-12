#!/usr/bin/env bash
#
# verify-attestation.sh — verify a released artifact's SLSA provenance and
# cosign signature (F29). Operator-provisioned cosign public key.
set -euo pipefail

BLOB="${1:-}"
BUNDLE="${2:-}"
CERT_IDENTITY_REGEX="${3:-}"
OIDC_ISSUER="${4:-https://token.actions.githubusercontent.com}"

if [ -z "$BLOB" ] || [ -z "$BUNDLE" ]; then
  echo "usage: verify-attestation.sh BLOB BUNDLE [CERT_IDENTITY_REGEX] [OIDC_ISSUER]" >&2
  exit 2
fi

command -v cosign >/dev/null 2>&1 || { echo "cosign is required" >&2; exit 1; }

args=(verify-blob --bundle "$BUNDLE" --certificate-oidc-issuer "$OIDC_ISSUER")
if [ -n "$CERT_IDENTITY_REGEX" ]; then
  args+=(--certificate-identity-regexp "$CERT_IDENTITY_REGEX")
fi
cosign "${args[@]}" "$BLOB"

echo "attestation verified for $BLOB"
