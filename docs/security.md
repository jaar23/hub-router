# Security

hub-router implements multiple layers of defence. This document explains each layer, its configuration, and the threats it mitigates.

---

## Authentication

### API Keys

All endpoints are protected by API key authentication:

| Endpoint group | Header | Variable |
|---------------|--------|----------|
| Online side (`/request`, `/result`) | `X-Online-API-Key` | `HR_ONLINE_API_KEYS` |
| Local side (`/queue/pull`, `/queue/result`) | `X-Local-API-Key` | `HR_LOCAL_API_KEYS` |
| Admin (`/metrics`) | `X-Admin-API-Key` | `HR_ADMIN_API_KEY` |

Keys are compared using Go's `crypto/subtle.ConstantTimeCompare`, which runs in constant time regardless of where the strings differ — eliminating timing side-channels that could leak key information.

### Multiple Keys

Each variable accepts a comma-separated list of keys. All keys in the list are valid simultaneously:

```
HR_ONLINE_API_KEYS=old-key,new-key
```

This enables **zero-downtime key rotation**:
1. Add the new key to the list alongside the old one.
2. Roll out clients to use the new key.
3. Remove the old key once all clients are updated.

### Key Strength

Use cryptographically random keys of at least 32 characters:

```bash
openssl rand -hex 32   # generates 64-char hex string
```

---

## Rate Limiting

### Per-IP Token Bucket

Each source IP gets an independent token bucket. Requests consume tokens; tokens refill at `HR_RATE_LIMIT_RPS` per second. When a bucket is empty, the request is rejected with `429 Too Many Requests`.

| Variable | Default | Effect |
|----------|---------|--------|
| `HR_RATE_LIMIT_RPS` | `50` | Sustained request rate per IP (tokens/second refill rate). |
| `HR_RATE_LIMIT_BURST` | `100` | Burst capacity — tokens in a full bucket. |

Set `HR_RATE_LIMIT_RPS=0` to disable rate limiting entirely (development only).

**Response headers on 429 (RATE_LIMITED):**
```
HTTP/1.1 429 Too Many Requests
Retry-After: 2
Content-Type: application/json

{"code": "RATE_LIMITED", "message": "rate limit exceeded"}
```

### Tuning

| Scenario | Suggested values |
|----------|-----------------|
| Public API | `RPS=10`, `Burst=30` |
| Trusted backend | `RPS=100`, `Burst=300` |
| Single local server | `RPS=5`, `Burst=10` |

---

## Brute-Force Lockout

Rate limiting bounds request volume but doesn't stop a slow password-spraying attack. The lockout layer specifically targets repeated authentication failures.

### How It Works

1. Each source IP has an independent failure counter.
2. Every authentication failure increments the counter and records the timestamp.
3. When the counter reaches `HR_LOCKOUT_THRESHOLD` within the `HR_LOCKOUT_WINDOW`, the IP is locked out for `HR_LOCKOUT_DURATION`.
4. A successful authentication resets the counter to zero.
5. A lockout event is logged at `warn` level.

| Variable | Default | Description |
|----------|---------|-------------|
| `HR_LOCKOUT_THRESHOLD` | `10` | Consecutive failures before lockout. |
| `HR_LOCKOUT_DURATION` | `15m` | How long the IP stays blocked. |
| `HR_LOCKOUT_WINDOW` | `10m` | Sliding window for counting failures. |

**Response on 429 (IP_LOCKED):**
```json
{"code": "IP_LOCKED", "message": "IP temporarily blocked due to repeated auth failures"}
```

The lockout response is indistinguishable from the error for a wrong API key from the attacker's perspective (both return 429).

### Interaction with Rate Limiting

The middleware chain evaluates in this order:
1. Rate limiter (per-IP) — if the IP is being hammered, it's stopped here first.
2. Body limit — oversized payloads rejected before any auth logic.
3. Auth + lockout — key validated, failure counter updated.

An IP can be blocked by either layer. A genuinely malicious IP will usually hit the rate limiter first.

---

## Request Body Limit

| Variable | Default |
|----------|---------|
| `HR_MAX_BODY_BYTES` | `1048576` (1 MiB) |

Requests with a `Content-Length` larger than this limit are rejected immediately (before the body is read). Requests without `Content-Length` are capped via `http.MaxBytesReader`.

This prevents:
- Memory exhaustion from enormous payloads.
- Slow-loris style attacks that trickle in large bodies.

**Response on 413:**
```json
{"code": "PAYLOAD_TOO_LARGE", "message": "request body exceeds limit"}
```

---

## Secure Response Headers

Every response includes:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Prevents MIME-sniffing attacks in browsers. |
| `X-Frame-Options` | `DENY` | Blocks clickjacking via iframes. |
| `Content-Security-Policy` | `default-src 'none'` | No content rendering allowed (API-only service). |
| `Cache-Control` | `no-store` | Prevents caching of API responses (they contain sensitive data). |
| `Server` | `hub-router` | Generic server name — version not disclosed. |

---

## Container Hardening

The Docker image and compose file implement defence-in-depth at the container layer:

| Control | Detail |
|---------|--------|
| **Distroless base** | `gcr.io/distroless/static-debian12:nonroot` — no shell, no package manager, no OS tools. Attack surface is just the binary. |
| **Non-root user** | Runs as uid `65532` (`nonroot`). Cannot write to most filesystem paths. |
| **Read-only filesystem** | `read_only: true` — the container filesystem is immutable at runtime. Only `/tmp` (tmpfs) is writable. |
| **No privilege escalation** | `no-new-privileges: true` — the process cannot gain new capabilities via setuid/setgid bits. |
| **Resource limits** | CPU (1 core) and memory (256 MiB) limits prevent resource exhaustion from affecting the host. |
| **Static binary** | `CGO_ENABLED=0` — no dynamic linking, no libc dependency, no `LD_PRELOAD` attack surface. |

---

## Network Security

hub-router speaks **plain HTTP**. TLS termination should be handled upstream:

- **Cloud:** AWS ALB / GCP Load Balancer / Cloudflare
- **Self-hosted:** nginx, Caddy, Traefik, HAProxy

Keep hub-router on a private network — accessible only from your online server and local server. Expose only the single port (`8080`) through a load balancer or reverse proxy.

```
Internet
  │
  ▼
[TLS Terminator] → (private network) → [hub-router:8080]
                                              │
                             (private network) ← [Local Server]
```

---

## Threat Model

| Threat | Mitigation |
|--------|------------|
| Stolen API key | Short TTL rotation; constant-time comparison; separate keys per side |
| Brute-force key guessing | Per-IP lockout after threshold failures |
| DDoS / traffic flood | Per-IP rate limiting (token bucket) |
| Large payload memory exhaustion | Body size limit (HR_MAX_BODY_BYTES) |
| Request injection via timing | Constant-time key comparison (no early exit) |
| Container escape | Distroless, read-only FS, non-root, no-new-privileges |
| Queue overflow / resource exhaustion | Queue size limit (HR_QUEUE_MAX_SIZE), memory limits |
| Stale result accumulation | Automatic TTL sweep for results (HR_RESULT_TTL) |
| Request replay / correlation | UUIDv7 IDs (unique per request, time-sortable for forensics) |

---

## Security Checklist

- [ ] `HR_ONLINE_API_KEYS` — 32+ char random string, not a dictionary word
- [ ] `HR_LOCAL_API_KEYS` — different key from online keys (separate trust domain)
- [ ] `HR_ADMIN_API_KEY` — set in production to protect Prometheus metrics
- [ ] TLS termination upstream — do not expose port 8080 directly to the internet
- [ ] Network isolation — hub-router accessible only from trusted servers
- [ ] `HR_RATE_LIMIT_RPS` — calibrated to expected traffic (not disabled)
- [ ] `HR_MAX_BODY_BYTES` — set to the minimum your use case requires
- [ ] Log monitoring — watch for `IP_LOCKED` warn logs indicating attack attempts
- [ ] Key rotation — schedule regular rotation; use multi-key support for zero downtime
