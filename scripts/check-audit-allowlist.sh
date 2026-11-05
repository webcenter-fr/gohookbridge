#!/usr/bin/env bash
#
# check-audit-allowlist.sh — fail when an npm audit allowlist entry has expired
# or is malformed. Allowlisted advisories must include a GHSA id, an ISO expiry
# date in the future, and a reason (F16).
set -euo pipefail

ALLOWLIST="${1:-web/.audit-allowlist.json}"

if [ ! -f "$ALLOWLIST" ]; then
  echo "no audit allowlist at $ALLOWLIST (nothing to check)"
  exit 0
fi

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

now="$(date -u +%s)"
jq -e '.allow | type == "array"' "$ALLOWLIST" >/dev/null || { echo "allowlist must contain an 'allow' array" >&2; exit 1; }

count="$(jq '.allow | length' "$ALLOWLIST")"
i=0
while [ "$i" -lt "$count" ]; do
  entry="$(jq -c ".allow[$i]" "$ALLOWLIST")"
  ghsa="$(jq -r '.ghsa // empty' <<<"$entry")"
  expires="$(jq -r '.expires // empty' <<<"$entry")"
  reason="$(jq -r '.reason // empty' <<<"$entry")"
  [ -n "$ghsa" ] || { echo "entry $i missing ghsa" >&2; exit 1; }
  [ -n "$expires" ] || { echo "entry $i missing expires" >&2; exit 1; }
  [ -n "$reason" ] || { echo "entry $i missing reason" >&2; exit 1; }
  exp_secs="$(date -u -d "$expires" +%s 2>/dev/null || echo 0)"
  if [ "$exp_secs" -eq 0 ]; then
    echo "entry $i has an invalid expiry date: $expires" >&2
    exit 1
  fi
  if [ "$exp_secs" -lt "$now" ]; then
    echo "allowlist entry $ghsa expired on $expires" >&2
    exit 1
  fi
  i=$((i + 1))
done

echo "audit allowlist OK ($count entries)"
