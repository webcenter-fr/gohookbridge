#!/usr/bin/env bash
#
# generate-release-notes.sh — deterministic, reviewable release notes.
#
# Replaces the previous LLM-based generator: the output is derived purely from
# the git history between the previous tag and TAG, grouped by conventional
# commit type. It never sends repository content to a third party and can be
# reviewed before publication.
#
# Usage:
#   TAG=v1.2.3 scripts/generate-release-notes.sh            # print notes
#   TAG=v1.2.3 scripts/generate-release-notes.sh --publish  # also `gh release edit`
set -euo pipefail

PUBLISH=false
[ "${1:-}" = "--publish" ] && PUBLISH=true

if [ -z "${TAG:-}" ]; then
  TAG=$(git tag -l | tail -1)
  if [ -z "$TAG" ]; then
    echo "TAG is required and no git tag was found" >&2
    exit 1
  fi
  echo "Using latest tag: ${TAG}" >&2
fi

tag_commit="$(git rev-list -n 1 "${TAG}^{commit}")"
previous_tag="$(git describe --tags --abbrev=0 --match '*.[0-9]*.[0-9]*' "${tag_commit}^" 2>/dev/null || true)"
range="${TAG}"
if [ -n "${previous_tag}" ]; then
  range="${previous_tag}..${TAG}"
fi

generate_notes() {
  echo "## ${TAG}"
  echo
  section "Features"      '^feat(\([^)]*\))?!?:'
  section "Bug Fixes"     '^fix(\([^)]*\))?!?:'
  section "Security"      '^security(\([^)]*\))?!?:|^fix\(security\)'
  section "Performance"   '^perf(\([^)]*\))?!?:'
  section "Documentation" '^docs(\([^)]*\))?!?:'
  section "Maintenance"   '^(refactor|chore|build|ci|test)(\([^)]*\))?!?:'

  local breaking
  breaking="$(git log --no-merges --pretty='%s%n%b' "${range}" | grep -Ei 'breaking change|breaking-change|!:' || true)"
  if [ -n "$breaking" ]; then
    echo "### Breaking Changes"
    echo
    echo "${breaking}" | sed 's/^/- /'
    echo
  fi
}

section() {
  local title="$1" pattern="$2"
  local entries
  entries="$(git log --no-merges --pretty='%s' "${range}" | grep -E "${pattern}" | sed -E 's/^[a-z]+(\([^)]*\))?!?: //' || true)"
  if [ -n "$entries" ]; then
    echo "### ${title}"
    echo
    echo "${entries}" | sed 's/^/- /'
    echo
  fi
}

notes="$(generate_notes)"
printf '%s\n' "$notes"

if [ "$PUBLISH" = "true" ]; then
  file="$(mktemp)"
  trap 'rm -f "$file"' EXIT
  printf '%s\n' "$notes" > "$file"
  gh release edit "${TAG}" --notes-file "$file"
  echo "Release notes updated for ${TAG}" >&2
fi
