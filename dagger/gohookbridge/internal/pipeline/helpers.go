// Package pipeline holds the pure, engine-independent logic of the
// gohookbridge Dagger module: version normalization, helm values rendering,
// report assembly, and the embedded smoke script. It deliberately imports no
// Dagger packages, so it can be unit-tested with a plain `go test` and no
// engine session (the generated internal/dagger package panics at init
// outside a Dagger session, which makes tests in package main impossible).
package pipeline

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReportSection is one titled Markdown section of the validation report.
type ReportSection struct {
	Title string
	Body  string
}

// smokeSessionSecret is the throwaway bootstrap session secret of the
// ephemeral smoke cluster (never used outside the throwaway deployment).
const smokeSessionSecret = "6f1c0d2e9a4b7385c6d1f0a9e2b4c7d85316af9e04b2c7d1a8e5f3b6c9d0e7a1" //nolint:gosec // G101 false positive: throwaway value, not a real credential.

// TrimVersionTag strips one leading "v" and surrounding whitespace from a
// version string (e.g. "v1.2.3" -> "1.2.3", "  dev  " -> "dev").
func TrimVersionTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// ResolveVersion returns TrimVersionTag(version), or "dev" when the result is
// empty. This is the module-side defensive guard; the caller (LLM) resolves
// the real version and passes it in.
func ResolveVersion(version string) string {
	if trimmed := TrimVersionTag(version); trimmed != "" {
		return trimmed
	}
	return "dev"
}

// RenderHelmValues renders the helm values-override map to YAML for the smoke
// deployment (single replica, raft TLS off, image pulled from the in-pipeline
// registry, minimal bootstrap admin user). See deploy.go:writeHelmValues.
func RenderHelmValues(repository string, version string, channelID string) (string, error) {
	values := map[string]any{
		"fullnameOverride": "gohookbridge",
		"server": map[string]any{
			"replicas": 1,
			"image": map[string]any{
				"repository": repository,
				"tag":        version,
			},
			"raft": map[string]any{
				"tls": map[string]any{
					"enabled": false,
				},
			},
			"bootstrap": map[string]any{
				"enabled": true,
				"config": map[string]any{
					"global": map[string]any{
						"server": map[string]any{
							"session_secret": smokeSessionSecret,
							"cors_origin":    "*",
							"max_body_size":  26214400,
						},
					},
					"users": []map[string]any{{
						"username": "admin",
						"password": "DaggerSmoke!Pipeline2026",
						"roles":    []string{"admin"},
						"channels": []string{"*"},
					}},
					"channels": []map[string]any{{
						"id":          channelID,
						"description": "Dagger pipeline smoke-test channel",
					}},
				},
			},
		},
	}

	rendered, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal helm values: %w", err)
	}
	return string(rendered), nil
}

// BuildReport assembles the final Markdown report from the given sections,
// preserving their order. Pure: renders only each section's title and body,
// so secret values must never be placed in a section.
func BuildReport(sections ...ReportSection) string {
	var b strings.Builder
	b.WriteString("# Gohookbridge Dagger validation report\n")
	for _, section := range sections {
		b.WriteString("\n## " + section.Title + "\n\n")
		b.WriteString(strings.TrimSpace(section.Body))
		b.WriteString("\n")
	}
	return b.String()
}

// SmokeScript is a POSIX sh script run inside an alpine container bound to the
// port-forward service. It checks /version, /health, / (UI), webhook POST
// (202) and SSE relay (connected/ready + bodyB round-trip) against
// http://pf:3333.
const SmokeScript = `#!/bin/sh
# Gohookbridge smoke validation, run inside an alpine container bound to the
# kubectl port-forward service (BASE_URL=http://pf:3333 by default).
set -eu

BASE_URL="${BASE_URL:?BASE_URL is required}"
CHANNEL_ID="${CHANNEL_ID:?CHANNEL_ID is required}"
EXPECTED_VERSION="${EXPECTED_VERSION:?EXPECTED_VERSION is required}"

apk add --no-cache curl >/dev/null 2>&1

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
HDRS="$WORK/headers"
BODY="$WORK/body"
SSE="$WORK/sse"

fail() { echo "FAIL: $*" >&2; exit 1; }

# 1. GET /version -> 200, JSON version == expected, X-Gohookbridge-Version header matches.
code=$(curl -sS -o "$BODY" -D "$HDRS" -w '%{http_code}' "$BASE_URL/version") \
  || fail "GET /version: request failed"
[ "$code" = "200" ] || fail "GET /version: status $code"
grep -Fq "\"version\":\"$EXPECTED_VERSION\"" "$BODY" \
  || fail "GET /version: body does not report $EXPECTED_VERSION: $(cat "$BODY")"
got_version=$(grep -i '^X-Gohookbridge-Version:' "$HDRS" | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r' || true)
[ "$got_version" = "$EXPECTED_VERSION" ] \
  || fail "GET /version: X-Gohookbridge-Version is '$got_version', want '$EXPECTED_VERSION'"
echo "ok: GET /version reports $EXPECTED_VERSION (JSON + header)"

# 2. GET /health -> 200.
code=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE_URL/health") || fail "GET /health: request failed"
[ "$code" = "200" ] || fail "GET /health: status $code"
echo "ok: GET /health -> 200"

# 3. GET / -> 200 (embedded SPA index.html served: UI available).
code=$(curl -sS -o "$BODY" -w '%{http_code}' "$BASE_URL/") || fail "GET /: request failed"
[ "$code" = "200" ] || fail "GET /: status $code"
grep -qi '</html>' "$BODY" || fail "GET /: index.html not served"
echo "ok: GET / serves the SPA"

# 4. POST /<channel> -> 202.
payload1='{"smoke":"dagger","n":1}'
code=$(curl -sS -o "$BODY" -X POST -H 'Content-Type: application/json' \
  -d "$payload1" -w '%{http_code}' "$BASE_URL/$CHANNEL_ID") || fail "POST /$CHANNEL_ID: request failed"
[ "$code" = "202" ] || fail "POST /$CHANNEL_ID: status $code: $(cat "$BODY")"
echo "ok: POST /$CHANNEL_ID -> 202"

# 5. SSE: expect {"message":"connected"}, then POST again and wait until an
#    event's base64 bodyB decodes to the posted payload.
curl -sS -N --max-time 60 "$BASE_URL/events/$CHANNEL_ID" > "$SSE" &
sse_pid=$!

i=0
until grep -Fq '{"message":"connected"}' "$SSE" 2>/dev/null; do
  i=$((i + 1))
  [ "$i" -le 30 ] || fail "SSE: no connected message after 30s: $(cat "$SSE")"
  sleep 1
done
echo "ok: SSE emitted connected"

payload2='{"smoke":"dagger","n":2}'
code=$(curl -sS -o "$BODY" -X POST -H 'Content-Type: application/json' \
  -d "$payload2" -w '%{http_code}' "$BASE_URL/$CHANNEL_ID") || fail "second POST /$CHANNEL_ID: request failed"
[ "$code" = "202" ] || fail "second POST /$CHANNEL_ID: status $code: $(cat "$BODY")"

found=0
i=0
while [ "$i" -lt 45 ]; do
  while IFS= read -r line; do
    case "$line" in
      *bodyB*) ;;
      *) continue ;;
    esac
    b64=$(printf '%s' "$line" | sed -n 's/.*"bodyB":"\([^"]*\)".*/\1/p')
    [ -n "$b64" ] || continue
    if decoded=$(printf '%s' "$b64" | base64 -d 2>/dev/null) && [ "$decoded" = "$payload2" ]; then
      found=1
      break
    fi
  done < "$SSE"
  [ "$found" -eq 1 ] && break
  i=$((i + 1))
  sleep 1
done
[ "$found" -eq 1 ] || fail "SSE: no relayed event bodyB matched the posted payload: $(cat "$SSE")"
echo "ok: SSE relayed webhook bodyB round-trip"

kill "$sse_pid" 2>/dev/null || true
wait "$sse_pid" 2>/dev/null || true
echo "all smoke checks passed"
`
