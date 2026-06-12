# Security

## Security Model and Trust Boundaries

gohookbridge is a **relay**, not a firewall. It forwards webhook payloads from a public ingress point to clients running in private networks — it does not inspect, filter, or sanitize payload content beyond the controls described in this document.

```text
internet → [gohookbridge server] → [/login] → [API /admin]
                                    ↓
                              [Raft cluster] → [BoltDB]
                                    ↓
                         SSE stream → [gohookbridge client] → local service
```

**What gohookbridge can protect:**

- Webhook authenticity (signature validation, IP allowlisting)
- Payload confidentiality on the relay stream (end-to-end encryption)
- Server availability (payload size and channel name limits)
- **API and admin access via RBAC/ACL (session authentication + permission model)**
- **Raft-cluster-stored configuration (no live flag/env-var re-reading)**

**What gohookbridge does not handle by itself:**

- TLS for the server — terminate TLS at a reverse proxy (nginx, Caddy, etc.)
- Sanitization of payload content delivered to local services

---

## Threat Model

| Threat | Relevant controls |
|---|---|---|
| Forged or tampered webhooks from untrusted senders | Signature validation, IP allowlisting |
| Eavesdropping on the SSE relay stream | End-to-end encryption |
| Unauthorized replay injection | Per-project or global replay token via Raft config |
| Payload-based resource exhaustion (DoS) | Per-project or global max_body_size, channel name length limit |
| Command injection via exec scripts | `--exec` hardening, signature validation, IP allowlisting |
| Unauthorized access to protected channels | Encrypted channels with public-key authentication |
| Unauthorized admin/API access | RBAC ACL + session authentication (HMAC-signed cookies) |
| Session cookie hijacking | Secure/HttpOnly cookie + HMAC-SHA256 signed tokens with 24h expiry |
| Raft log tampering | Raft consensus protocol + BoltDB integrity |
| Unauthorized config mutations | RBAC permission checks (global:write, users:write, rbac:write, project:write) |
| NATS cluster eavesdropping / unauthorized connection | Firewall NATS cluster port (`6222`) to peer nodes only; use secure overlay network |
| NATS ring buffer memory exhaustion | Configure `--nats-buffer-size` and `--nats-buffer-ttl` to limit memory consumption |

---

## Recommended Baseline

If you do nothing else, apply these controls before deploying gohookbridge in production, ordered by impact:

- [ ] Run gohookbridge server behind TLS (nginx, Caddy, or similar)
- [ ] Configure projects via Admin UI or `bootstrap.yaml` with webhook signatures and allowed IPs
- [ ] Set `max_body_size` globally or per-project via `bootstrap.yaml` or Admin UI
- [ ] Create an admin user in `bootstrap.yaml` on first boot
- [ ] Set `session_secret` in `bootstrap.yaml` global config
- [ ] Run as a non-root user with minimal filesystem permissions
- [ ] Enable encrypted channels for sensitive payloads per-project
- [ ] Set replay tokens per-project or globally via `bootstrap.yaml` or Admin UI
- [ ] Configure CORS origin globally via `bootstrap.yaml` or Admin UI
- [ ] If using `--exec`, validate and sanitize all payload fields in scripts before passing them to shell commands

---

## Protecting the Webhook Intake

IP allowlisting and signature validation are complementary controls. Use both where possible: IP restrictions are coarse-grained (network-level, easy to configure) and signatures are fine-grained (cryptographic, provider-verified). An attacker who spoofs a source IP still fails signature validation; an attacker who obtains a signature secret but sends from a blocked IP is still rejected.

### Restricting Webhook Sources by IP

If you know which IP ranges your webhooks will come from, restrict them with `allowed_ips` configured per-project or globally via `bootstrap.yaml` or Admin UI. Requests from other IPs receive a 403 and are logged. The restriction applies only to POST requests — the web UI remains open.

The examples below use `--trust-proxy` because they assume gohookbridge sits behind a reverse proxy. **Only enable `--trust-proxy` when gohookbridge is reachable exclusively through a trusted proxy that overwrites the forwarded headers** (see the warning under [Trusting Proxy Headers Safely](#trusting-proxy-headers-safely) below). If gohookbridge is directly reachable, drop `--trust-proxy` from these commands so the allowlist is enforced against the real connection address.

```shell
# Accept webhooks from GitHub's ranges only
gohookbridge server --trust-proxy \
  --allowed-ips 192.30.252.0/22 \
  --allowed-ips 185.199.108.0/22 \
  --allowed-ips 140.82.112.0/20

# GitLab.com
gohookbridge server --trust-proxy \
  --allowed-ips 35.231.145.151 \
  --allowed-ips 34.74.90.64 \
  --allowed-ips 34.74.226.93

# Bitbucket Cloud
gohookbridge server --trust-proxy \
  --allowed-ips 34.199.54.113 \
  --allowed-ips 34.232.119.183 \
  --allowed-ips 34.236.25.177 \
  --allowed-ips 35.171.175.212
```

Use `--trust-proxy` when gohookbridge sits behind a reverse proxy so that `X-Forwarded-For` / `X-Real-IP` headers are used for the client IP. Both IPv4 and IPv6 addresses and CIDR ranges are supported. You can also set allowed IPs via the `GOSMEE_ALLOWED_IPS` environment variable (comma-separated) and enable proxy trust via `GOSMEE_TRUST_PROXY`.

Official IP range docs: [GitHub](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/about-githubs-ip-addresses) · [GitLab.com](https://docs.gitlab.com/ee/user/gitlab_com/index.html#ipv4-addresses) · [Bitbucket Cloud](https://support.atlassian.com/bitbucket-cloud/docs/what-are-the-bitbucket-cloud-ip-addresses-i-should-use-to-configure-my-corporate-firewall/)

### Trusting Proxy Headers Safely

When `trust_proxy: true` is set in global config (via `bootstrap.yaml` or Admin UI), gohookbridge derives the client IP from the `X-Forwarded-For` (first value) and `X-Real-IP` request headers instead of the network connection's source address. These headers are trivially set by any HTTP client.

> **Warning:** `--trust-proxy` makes `--allowed-ips` only as trustworthy as whatever sets those headers. If gohookbridge is reachable directly (not solely through your proxy), an attacker can send `X-Forwarded-For: <an-allowed-ip>` and bypass the allowlist entirely. The same spoofing also forges the client IP shown in logs.

Only enable `--trust-proxy` when **both** of these hold:

- **gohookbridge is not directly reachable.** Bind it to `localhost` (the default) or an internal interface, or firewall the listening port so the reverse proxy is the only path to it. Do not expose the gohookbridge port to the public internet while `--trust-proxy` is on.
- **The proxy overwrites the forwarded headers.** Configure your proxy to *replace* `X-Forwarded-For` / `X-Real-IP` with the real connection address rather than appending to or passing through client-supplied values. A proxy that appends (so a client-controlled value ends up first) is unsafe, because gohookbridge trusts the first `X-Forwarded-For` entry.

If you cannot guarantee both conditions, leave `--trust-proxy` off. Without it, gohookbridge uses the connection's source address, which cannot be spoofed by the client — though behind a proxy that means every request appears to come from the proxy, so `--allowed-ips` should then contain the proxy's address (or you should enforce source IPs at the proxy/firewall instead). Pair IP allowlisting with `--webhook-signature` so a spoofed or proxy-originated request still fails cryptographic validation.

### Validating Webhook Signatures

Signature validation ensures that incoming webhooks are genuinely from your provider and haven't been tampered with. Enable it by configuring `webhook_signatures` per-project or globally via `bootstrap.yaml` or Admin UI.

gohookbridge automatically detects the provider from the request headers and validates accordingly:

| Provider | Header validated |
|---|---|
| GitHub | `X-Hub-Signature-256` (HMAC-SHA256) |
| GitLab | `X-Gitlab-Token` (constant-time comparison) |
| Bitbucket Cloud/Server | `X-Hub-Signature` (HMAC-SHA256) |
| Gitea / Forgejo | `X-Gitea-Signature` (HMAC-SHA256) |

Requests with a missing or invalid signature are rejected with HTTP 401. When multiple secrets are configured, each is tried in turn — useful when migrating secrets or receiving webhooks from multiple sources. The overhead is negligible (~2 μs per request).

Secrets can also be set via `GOSMEE_WEBHOOK_SIGNATURE` (comma-separated).

---

## Protecting the Replay Endpoint

The `/replay/{channel}` endpoint re-sends a captured event to all SSE subscribers on that channel. This is useful for debugging and incident replay, but it is also a write path into your relay stream.

Without authentication, anyone who can reach the server can POST to `/replay/{channel}` and inject payloads into any channel they can name.

Set `replay_token` per-project or globally via `bootstrap.yaml` (or Admin UI) to require bearer-token authentication.

When set, replay requests must include:

```text
Authorization: Bearer <token>
```

Missing or incorrect tokens are rejected with HTTP 401.

The web UI Replay button will prompt you to enter the token when you first attempt to replay an event. The token is stored in your browser's sessionStorage for convenience during the session.

If no replay token is configured for the project or globally, the replay endpoint remains open for backward compatibility.

---

## Protecting the Relay Stream

Once a webhook passes ingress checks (IP allowlisting, signature validation), it travels over the SSE stream to the client. End-to-end encryption protects this leg of the relay from eavesdropping, even if the SSE connection itself is unencrypted.

TLS for the connection is a complement, not a substitute — enable it at your reverse proxy to protect the transport layer.

### How End-to-End Encryption Works

gohookbridge uses **NaCl `box`** (Curve25519 + XSalsa20-Poly1305). For each SSE message on a protected channel, the server generates a fresh ephemeral Curve25519 keypair and a random 24-byte nonce, then seals the payload with `box.Seal` addressed to the recipient's public key. This gives per-message forward secrecy: even if a key is later compromised, past messages cannot be decrypted. The server never has access to plaintext after encryption.

The wire format is a JSON envelope:

```json
{
  "encrypted": true,
  "version": 1,
  "epk": "<base64 ephemeral public key>",
  "nonce": "<base64 24-byte nonce>",
  "ciphertext": "<base64 ciphertext>"
}
```

On receipt, the client calls `box.Open` with its static private key and the ephemeral public key to recover the original payload.

### Setting Up a Client Keypair

Generate a keypair once and store it locally:

```shell
gohookbridge keygen --key-file ~/.config/gohookbridge/key.json
```

This writes a `0600`-mode JSON file and prints the public key to stdout in base64 URL-safe format — paste that value into the server's channels config. Then pass the key file when starting the client:

```shell
gohookbridge client --encryption-key-file ~/.config/gohookbridge/key.json <server-url> <local-url>
```

Keep the key file private. Anyone with the private key can decrypt messages addressed to that keypair.

### Configuring the Server

Configure protected channels per-project via `bootstrap.yaml` or Admin UI by setting `encryption_enabled: true` and `encryption_public_keys` on the project.

When a subscriber connects to a protected channel, the server checks their public key against the list. Unauthorized clients get a generic not-found response — no information about the channel is leaked. Channels not listed in the config remain normal plaintext channels.

### What Is and Isn't Encrypted

Encryption covers the **server-to-client SSE leg only**. Incoming webhook POST bodies arrive at the server in plaintext, as does all web UI traffic.

| Encrypted | Not encrypted |
|---|---|
| SSE payload delivery to authorized clients | Incoming webhook POST bodies |
| | Unlisted (plaintext) channels |
| | Web UI and `/new` endpoint |
| | TLS transport (use a reverse proxy) |

Encryption requires gohookbridge's own server — smee.io is not supported.

### Restricting SSE Cross-Origin Access

The SSE endpoint (`/events/{channel}`) sets `Access-Control-Allow-Origin`. By default, it is `*`.

That default means any website that knows a channel ID can open an `EventSource` connection and receive all payloads for that channel.

Configure `cors_origin` in global config via `bootstrap.yaml` or Admin UI to control this header:

```yaml
# bootstrap.yaml
global:
  server:
    cors_origin: "https://dashboard.example.com"
```

To omit the header entirely (same-origin browser access only), set `cors_origin: ""`.

The default `*` preserves backward compatibility for existing deployments.

---

## Resource Protection

These controls protect server availability against large or malformed payloads.

### Payload Size Limits

gohookbridge enforces a 25 MB limit on incoming webhook bodies by default, matching GitHub's maximum. Raise or lower it by setting `max_body_size` per-project or globally via `bootstrap.yaml` or Admin UI (in bytes):

```yaml
# bootstrap.yaml
global:
  server:
    max_body_size: 10485760  # 10 MB
```

On the client side, the SSE receive buffer defaults to 1 MB. If you're forwarding large payloads, increase it to match:

```shell
gohookbridge client --sse-buffer-size 5242880 <SMEE_URL> <TARGET_URL>  # 5 MB
```

Raising these limits increases memory consumption proportionally. A server with a very high `--max-body-size` is also a more attractive DoS target. If you run gohookbridge in Kubernetes, update the memory `requests` and `limits` in your deployment manifests when you change these values, or Pods may be OOMKilled under load.

### Channel Name Length Limit

Channel names are capped at 64 characters across all endpoints. This guards against resource exhaustion from pathologically long names — no configuration is needed.

---

## Safe Command Execution

The `--exec` flag runs a shell command for each incoming webhook, with the payload written to `$GOSMEE_PAYLOAD_FILE` and headers to `$GOSMEE_HEADERS_FILE`. If you've already enabled signature validation and IP allowlisting, the scripts are much safer — but the payload content itself is still untrusted until your script validates it.

**The risk:** if your server accepts webhooks from untrusted sources and your exec script passes payload fields directly to shell commands (e.g. `$(jq -r .field)`), an attacker can craft a payload that executes arbitrary code.

**Mitigations:**

- Use `--webhook-signature` to verify that webhooks are from a trusted provider before they reach your script.
- Use `--allowed-ips` to restrict which hosts can send webhooks at all.
- In your scripts, treat all payload values as untrusted input — validate and sanitize before passing to any shell command.
- Use `--exec-on-events` to limit execution to specific event types, reducing attack surface.

---

## Operational Security

### Rotating Webhook Secrets

Webhook secrets are stored in the Raft store (per-project or globally). Rotate them via the Admin UI or the Admin API at `/api/projects/{id}` — no restart needed. To rotate a secret, update the project's `webhook_signatures` with the new secret alongside the old one, then remove the old secret once all clients have migrated.

### Rotating Encryption Keys

Generate a new keypair with `gohookbridge keygen`, add the new public key to the project's `encryption_public_keys` in the Admin UI or API, and redistribute the new key file to clients out-of-band. Once all clients have switched to the new keypair, remove the old public key. There is no built-in key rotation — this process is manual.

### What to Monitor in Server Logs

- **HTTP 403** from POST requests — IP allowlist rejections. A spike may indicate a scan or a misconfigured provider IP range.
- **HTTP 401** from POST requests — signature validation failures. Could indicate a misconfigured secret, a replay attempt, or an active forgery attempt.
- **Large payload rejections** — repeated hits against `--max-body-size` may indicate a DoS attempt.

### Kubernetes Considerations

When changing `--max-body-size` or `--sse-buffer-size`, update the memory `requests` and `limits` in your deployment manifests proportionally. Pods that exceed their memory limit are OOMKilled without warning.

## Authentication and RBAC (ACL)

### Internal User Authentication

Users authenticate with username and password on the `/login` page. Passwords are stored as bcrypt hashes in the Raft store. Session tokens are HMAC-SHA256 signed cookies with 24-hour expiry, marked `HttpOnly`, `Secure`, and `SameSite=Lax`.

### OIDC Authentication

OIDC providers can be configured via the Admin API. After successful OIDC login, users are matched to internal user records by OIDC subject claims.

### RBAC Model

There are three default roles:

| Role | Permissions |
|---|---|
| `admin` | `*` (wildcard — all permissions) |
| `project_admin` | `project:write`, `project:read` |
| `project_viewer` | `project:read` |

Custom roles can be created via the Admin API. Permission names follow the pattern: `global:read`, `global:write`, `users:read`, `users:write`, `rbac:read`, `rbac:write`, `project:read`, `project:write`, `project:view`.

Users can be scoped to specific projects. A user with the `*` (admin) role and project scope `["*"]` has universal access.

### Session Management

- HMAC-SHA256 signed cookies prevent forgery
- 24-hour session expiry
- `HttpOnly` / `Secure` / `SameSite=Lax` cookie attributes
- Session secret is stored in the Raft global config; configure via `bootstrap.yaml` or generate automatically on first leader boot

## Bootstrap Configuration Security

The `bootstrap.yaml` file is read once on first boot (when the Raft FSM is empty):

```yaml
global:
  server:
    session_secret: "your-32-byte-hex-secret"
    cors_origin: "https://dashboard.example.com"
    max_body_size: 26214400
    trust_proxy: true
  defaults:
    webhook_signatures: ["global-webhook-secret"]
    allowed_ips: ["192.30.252.0/22"]
    replay_token: "global-replay-token"
users:
  - username: admin
    password: "your-strong-password"
    roles: ["admin"]
    projects: ["*"]
projects:
  - id: my-project
    name: My Project
    webhook_signatures: ["project-specific-secret"]
    allowed_ips: ["10.0.0.0/8"]
```

- Bootstrap file is read once, never again after initial boot
- File should have permissions `0600` when it contains passwords/secrets
- Session secret and OIDC client secrets should be pre-configured in bootstrap
- Delete or archive the bootstrap file after initial cluster setup

## Raft Cluster Security

Raft inter-node communication uses TCP (not TLS in the current implementation).
- Run Raft transport on private network interfaces only
- Firewall the Raft port (`--raft-bind-addr`) to cluster nodes only
- The Raft data directory (`--raft-dir`) contains all configuration including secrets — protect with filesystem permissions (`0700`)
- In multi-node clusters, ensure Raft peers are specified via `--raft-peers` on all nodes

## NATS Cluster Security

NATS inter-node cluster routes use TCP (not TLS in the current implementation).
- The NATS client port (`--nats-port`, default `4222`) binds to `127.0.0.1` and is for in-process use only — do not expose it externally.
- The NATS cluster port (`--nats-cluster-port`, default `6222`) binds to `0.0.0.0` for inter-node communication. Firewall this port to cluster peers only.
- NATS cluster routes are unencrypted. In production HA deployments, use a secure overlay network (VPC, VXLAN, WireGuard) or configure NATS TLS.
- The NATS ring buffer is in-memory only — no persistence. Upon node restart, the buffer starts empty.
- Protected channels with end-to-end encryption are not supported when NATS mode is enabled.

## Setup Mode

On first boot with no users, gohookbridge enters a **5-minute setup window** during which the Admin API is accessible without authentication. This allows the initial admin user to be created (either via `bootstrap.yaml` or the first API call). After the setup window expires, all API requests require a valid session.

To re-enter setup mode, stop the server, delete the Raft data directory, and restart.

---

## Reporting Vulnerabilities

Please report security issues by opening a [GitHub issue](https://github.com/webcenter-fr/gohookbridge/issues). For sensitive disclosures, use GitHub's private vulnerability reporting feature on the Security tab. RBAC permission bypass bugs (such as context key type mismatches) are considered critical severity.
