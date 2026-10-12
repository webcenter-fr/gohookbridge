#!/usr/bin/env bash
#
# check-go-mod.sh — guard go.mod against legacy/forbidden dependencies (F31).
# The CI supply-chain gate runs this on every change to go.mod/go.sum.
set -euo pipefail

GOMOD="${1:-go.mod}"

forbidden=(
  "github.com/mgutz/ansi"            # replace with github.com/fatih/color
  "github.com/mitchellh/mapstructure" # replace with typed decoding
  "gopkg.in/cenkalti/backoff.v1"     # upgrade to github.com/cenkalti/backoff/v4
  "github.com/disaster37/goca"       # removed with the auto-CA (manual certs, F14)
)

status=0
for dep in "${forbidden[@]}"; do
  if grep -q "$dep" "$GOMOD"; then
    echo "forbidden/legacy dependency in $GOMOD: $dep" >&2
    status=1
  fi
done

if grep -qE '^\s*replace\s' "$GOMOD"; then
  echo "go.mod contains replace directives; review them before release" >&2
fi

[ "$status" -eq 0 ] && echo "go.mod OK"
exit "$status"
