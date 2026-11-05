#!/usr/bin/env bash
#
# verify-checksums.sh — verify downloaded tool/artifact checksums against a
# manifest of "<sha256>  <path>" lines (F17). Everything the CI downloads
# (Dagger, Node, GoReleaser, crane) must be pinned and verified.
set -euo pipefail

MANIFEST="${1:-}"
DIR="${2:-.}"

if [ -z "$MANIFEST" ]; then
  echo "usage: verify-checksums.sh MANIFEST [DIR]" >&2
  exit 2
fi
[ -f "$MANIFEST" ] || { echo "manifest not found: $MANIFEST" >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
  checker="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  checker="shasum -a 256"
else
  echo "no sha256 utility found" >&2
  exit 1
fi

failed=0
while read -r sum path; do
  [ -z "${sum:-}" ] && continue
  case "$sum" in \#*) continue ;; esac
  full="$DIR/$path"
  if [ ! -f "$full" ]; then
    echo "missing artifact: $full" >&2
    failed=1
    continue
  fi
  actual="$($checker "$full" | awk '{print $1}')"
  if [ "$actual" != "$sum" ]; then
    echo "checksum mismatch for $path: got $actual want $sum" >&2
    failed=1
  fi
done < "$MANIFEST"

[ "$failed" -eq 0 ] && echo "all checksums verified"
exit "$failed"
