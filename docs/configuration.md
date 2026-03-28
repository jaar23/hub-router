# Configuration

All configuration is done through environment variables prefixed with `HR_`. No config file is required. See [`deploy/.env.example`](../deploy/.env.example) for a complete template.

---

## Required Variables

These two variables must be set or the server refuses to start.

| Variable | Description |
|----------|-------------|
| `HR_ONLINE_API_KEYS` | Comma-separated list of valid API keys for the online server side. Used in the `X-Online-API-Key` header. |
| `HR_LOCAL_API_KEYS` | Comma-separated list of valid API keys for the local server side. Used in the `X-Local-API-Key` header. |

**Example:**
```
HR_ONLINE_API_KEYS=key-abc123,key-xyz789
HR_LOCAL_API_KEYS=local-key-secret
```

Multiple keys allow zero-downtime key rotation — add the new key, roll out, remove the old key.

---

## Server

| Variable | Default | Description |
|----------|---------|-------------|
| `HR_HOST` | `0.0.0.0` | Bind address. Set to `127.0.0.1` to restrict to localhost. |
| `HR_PORT` | `8080` | TCP port to listen on. |
| `HR_READ_TIMEOUT` | `10s` | Maximum duration for reading the full request (headers + body). |
| `HR_WRITE_TIMEOUT` | `35s` | Maximum duration for writing the full response. **Must be greater than `HR_LONGPOLL_TIMEOUT`** (the long-poll blocks inside this window). |
| `HR_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown timeout. In-flight requests are given this long to complete before the process exits. |

**Duration format:** Go duration strings — `5s`, `1m`, `500ms`, `1h30m`, etc.

---

## Auth

| Variable | Default | Description |
|----------|---------|-------------|
| `HR_ADMIN_API_KEY` | _(empty)_ | Optional API key for `GET /metrics`. If empty, `/metrics` is unauthenticated. Set this in production. Sent as `X-Admin-API-Key` header. |

---

## Queue

| Variable | Default | Description |
|----------|---------|-------------|
| `HR_QUEUE_MAX_SIZE` | `10000` | Maximum number of pending requests in the queue. Submissions return `503` when this limit is reached. Tune based on expected burst traffic and available memory. |
| `HR_REQUEST_TTL` | `5m` | How long a queued request lives before being discarded. If the local server hasn't pulled and processed the request within this window, the online server receives `404` on its next poll. |
| `HR_MAX_BATCH_SIZE` | `100` | Maximum requests returned per `GET /queue/pull` call. Clients can request fewer via the `?batch=N` query parameter. |

---

## Result Store

| Variable | Default | Description |
|----------|---------|-------------|
| `HR_LONGPOLL_TIMEOUT` | `30s` | Default long-poll wait duration for `GET /result/{id}` and `POST /request/sync`. Clients may request a shorter timeout via the `?timeout=` query parameter but cannot exceed this value. Must be less than `HR_WRITE_TIMEOUT`. |
| `HR_RESULT_TTL` | `10m` | How long a completed result is held in memory before being purged. The online server must retrieve the result within this window after the local server pushes it. |

---

## Security

| Variable | Default | Description |
|----------|---------|-------------|
| `HR_RATE_LIMIT_RPS` | `50` | Sustained request rate allowed per source IP (requests per second, token-bucket algorithm). Set to `0` to disable rate limiting. |
| `HR_RATE_LIMIT_BURST` | `100` | Maximum burst size — how many requests an IP can send instantaneously before being rate-limited. Should be ≥ `HR_RATE_LIMIT_RPS`. |
| `HR_LOCKOUT_THRESHOLD` | `10` | Number of consecutive authentication failures before the source IP is locked out. |
| `HR_LOCKOUT_DURATION` | `15m` | How long a locked-out IP is blocked. After this duration, the failure counter resets and the IP may try again. |
| `HR_LOCKOUT_WINDOW` | `10m` | Sliding window for counting auth failures. Failures older than this window are not counted toward the threshold. |
| `HR_MAX_BODY_BYTES` | `1048576` | Maximum allowed request body size in bytes (default 1 MiB). Requests with a larger body receive `413 Content Too Large`. |

---

## Logging

| Variable | Default | Options | Description |
|----------|---------|---------|-------------|
| `HR_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` | Minimum log severity. Use `debug` during development to see per-request details. |
| `HR_LOG_FORMAT` | `json` | `json`, `text` | `json` for structured machine-parseable logs (recommended for production/log aggregators). `text` for human-readable output during development. |

---

## Complete Example

```env
# ── Required ──────────────────────────────────────────────────────────────────
HR_ONLINE_API_KEYS=online-prod-key-a,online-prod-key-b
HR_LOCAL_API_KEYS=local-prod-key-a

# ── Auth (admin) ──────────────────────────────────────────────────────────────
HR_ADMIN_API_KEY=admin-metrics-key

# ── Server ────────────────────────────────────────────────────────────────────
HR_HOST=0.0.0.0
HR_PORT=8080
HR_READ_TIMEOUT=10s
HR_WRITE_TIMEOUT=35s
HR_SHUTDOWN_TIMEOUT=15s

# ── Queue ─────────────────────────────────────────────────────────────────────
HR_QUEUE_MAX_SIZE=10000
HR_REQUEST_TTL=5m
HR_MAX_BATCH_SIZE=100

# ── Result store ──────────────────────────────────────────────────────────────
HR_LONGPOLL_TIMEOUT=30s
HR_RESULT_TTL=10m

# ── Security ──────────────────────────────────────────────────────────────────
HR_RATE_LIMIT_RPS=50
HR_RATE_LIMIT_BURST=100
HR_LOCKOUT_THRESHOLD=10
HR_LOCKOUT_DURATION=15m
HR_LOCKOUT_WINDOW=10m
HR_MAX_BODY_BYTES=1048576

# ── Logging ───────────────────────────────────────────────────────────────────
HR_LOG_LEVEL=info
HR_LOG_FORMAT=json
```

---

## Tuning Guidelines

### High-throughput workload
```env
HR_QUEUE_MAX_SIZE=50000
HR_MAX_BATCH_SIZE=200
HR_RATE_LIMIT_RPS=200
HR_RATE_LIMIT_BURST=500
```

### Long-running local jobs (e.g. video processing)
```env
HR_REQUEST_TTL=30m
HR_LONGPOLL_TIMEOUT=120s
HR_WRITE_TIMEOUT=125s
HR_RESULT_TTL=60m
```

### Strict security (restricted environment)
```env
HR_RATE_LIMIT_RPS=10
HR_RATE_LIMIT_BURST=20
HR_LOCKOUT_THRESHOLD=5
HR_LOCKOUT_DURATION=1h
HR_LOCKOUT_WINDOW=5m
HR_MAX_BODY_BYTES=65536
```

### Development
```env
HR_ONLINE_API_KEYS=dev-online
HR_LOCAL_API_KEYS=dev-local
HR_LOG_LEVEL=debug
HR_LOG_FORMAT=text
HR_RATE_LIMIT_RPS=0
```
