# API Reference

Base URL: `http://<host>:<port>` (default port `8080`)

All endpoints return `application/json` bodies unless stated otherwise.
Error responses always use the [ErrorResponse](#errorresponse) schema.

---

## Authentication

| Key header | Used by | Environment variable |
|------------|---------|---------------------|
| `X-Online-API-Key` | Online servers | `HR_ONLINE_API_KEYS` (comma-separated) |
| `X-Local-API-Key`  | Local servers  | `HR_LOCAL_API_KEYS` (comma-separated) |
| `X-Admin-API-Key`  | Admin/ops      | `HR_ADMIN_API_KEY` (optional single key) |

Multiple keys are supported for zero-downtime key rotation. Keys are compared using `crypto/subtle.ConstantTimeCompare` to prevent timing attacks.

---

## Online Server Endpoints

Require `X-Online-API-Key`.

---

### POST /request

Enqueue a request and return immediately with a correlation ID. Use this when the result is not needed synchronously. Poll `GET /result/{id}` to retrieve the result later.

**Request body** (`application/json`):

```json
{
  "payload": <any JSON>,
  "headers": { "X-User-Id": "u123", "Content-Type": "application/json" }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `payload` | any JSON | yes | Opaque data forwarded unchanged to the local server. |
| `headers` | object | no | Key-value metadata forwarded with the request. Useful for passing auth or routing context. |

**Responses:**

| Status | Meaning | Body |
|--------|---------|------|
| `202 Accepted` | Request enqueued | `SubmitResponse` |
| `400 Bad Request` | Invalid JSON or missing payload | `ErrorResponse` |
| `401 Unauthorized` | Missing or invalid API key | `ErrorResponse` |
| `413 Content Too Large` | Body exceeds `HR_MAX_BODY_BYTES` | `ErrorResponse` |
| `429 Too Many Requests` | Rate limited or IP locked | `ErrorResponse` (with `Retry-After` header) |
| `503 Service Unavailable` | Queue at capacity | `ErrorResponse` |

**Example:**

```bash
curl -s -X POST http://localhost:8080/request \
  -H "X-Online-API-Key: online-secret" \
  -H "Content-Type: application/json" \
  -d '{"payload": {"query": "hello world"}}'
```

```json
{
  "id": "01960000-0000-7000-8000-000000000001",
  "estimated_wait_ms": 100
}
```

---

### POST /request/sync

Enqueue a request and wait for the result within the same HTTP connection. When the local server responds before the timeout, behaves like a regular reverse proxy (single round-trip). When slow, falls back gracefully to async with a correlation ID.

**Query parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `timeout` | string | `HR_LONGPOLL_TIMEOUT` | Maximum wait duration, e.g. `5s`, `30s`. Capped at server maximum. |

**Request body:** Same as `POST /request`.

**Responses:**

| Status | Meaning | Body |
|--------|---------|------|
| `200 OK` | Result ready — fast path | `Result` |
| `202 Accepted` | Timeout elapsed — use `id` to poll | `SubmitResponse` |
| `400 Bad Request` | Invalid JSON or missing payload | `ErrorResponse` |
| `401 Unauthorized` | Missing or invalid API key | `ErrorResponse` |
| `413 Content Too Large` | Body exceeds `HR_MAX_BODY_BYTES` | `ErrorResponse` |
| `429 Too Many Requests` | Rate limited or IP locked | `ErrorResponse` |
| `503 Service Unavailable` | Queue at capacity | `ErrorResponse` |

**Example — fast path (200):**

```bash
curl -s -X POST "http://localhost:8080/request/sync?timeout=10s" \
  -H "X-Online-API-Key: online-secret" \
  -H "Content-Type: application/json" \
  -d '{"payload": {"query": "hello"}}'
```

```json
{
  "request_id": "01960000-0000-7000-8000-000000000001",
  "payload": {"answer": 42},
  "status_code": 200,
  "completed_at": "2026-03-28T12:00:00.123Z"
}
```

**Example — slow path (202):**

```json
{
  "id": "01960000-0000-7000-8000-000000000001",
  "estimated_wait_ms": 3000
}
```

When you receive a `202`, continue with `GET /result/{id}` using the returned `id`.

---

### GET /result/{id}

Long-poll for the result of a previously submitted request. The server holds the connection open until the result arrives or the timeout elapses.

**Path parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | The correlation ID returned by `POST /request` or `POST /request/sync`. |

**Query parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `timeout` | string | `HR_LONGPOLL_TIMEOUT` | Max wait duration, e.g. `30s`. |

**Responses:**

| Status | Meaning | Body |
|--------|---------|------|
| `200 OK` | Result ready | `Result` |
| `204 No Content` | Timeout elapsed — retry with same ID | _(empty)_ |
| `401 Unauthorized` | Missing or invalid API key | `ErrorResponse` |
| `404 Not Found` | Unknown or expired request ID | `ErrorResponse` |
| `429 Too Many Requests` | Rate limited or IP locked | `ErrorResponse` |

**Polling loop:**

```
while true:
    response = GET /result/{id}?timeout=30s
    if response.status == 200: return response.body  # done
    if response.status == 204: continue               # not ready yet
    if response.status == 404: break                  # expired or invalid
```

**Example:**

```bash
curl -s "http://localhost:8080/result/01960000-0000-7000-8000-000000000001?timeout=30s" \
  -H "X-Online-API-Key: online-secret"
```

```json
{
  "request_id": "01960000-0000-7000-8000-000000000001",
  "payload": {"answer": 42},
  "status_code": 200,
  "completed_at": "2026-03-28T12:00:00.456Z"
}
```

---

## Local Server Endpoints

Require `X-Local-API-Key`.

---

### GET /queue/pull

Fetch a batch of pending requests from the queue. **Never blocks** — returns immediately even if the queue is empty.

The local server should call this in a tight loop. When the response is empty, sleep briefly before the next call to avoid busy-waiting.

**Query parameters:**

| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `batch` | integer | `HR_MAX_BATCH_SIZE` | `HR_MAX_BATCH_SIZE` | Number of requests to return. |

**Responses:**

| Status | Meaning | Body |
|--------|---------|------|
| `200 OK` | Batch (may be empty) | `PullResponse` |
| `401 Unauthorized` | Missing or invalid API key | `ErrorResponse` |
| `429 Too Many Requests` | Rate limited or IP locked | `ErrorResponse` |

**Example:**

```bash
curl -s "http://localhost:8080/queue/pull?batch=10" \
  -H "X-Local-API-Key: local-secret"
```

```json
{
  "requests": [
    {
      "id": "01960000-0000-7000-8000-000000000001",
      "payload": {"query": "hello world"},
      "headers": {"X-User-Id": "u123"},
      "enqueued_at": "2026-03-28T12:00:00.000Z",
      "expires_at": "2026-03-28T12:05:00.000Z"
    }
  ],
  "count": 1
}
```

---

### POST /queue/result

Push a completed result. hub-router stores it and wakes any online server waiting for this request ID.

**Request body** (`application/json`):

```json
{
  "request_id": "01960000-0000-7000-8000-000000000001",
  "payload": {"answer": 42},
  "status_code": 200,
  "error": "",
  "completed_at": "2026-03-28T12:00:00.456Z"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `request_id` | string | yes | The `id` from the `QueuedRequest`. |
| `payload` | any JSON | no | The result data. Forwarded unchanged to the online server. |
| `status_code` | integer | no | HTTP-like code indicating success/failure. Defaults to `200`. |
| `error` | string | no | Error message if processing failed. |
| `completed_at` | datetime | no | ISO 8601 timestamp. Set by hub-router if omitted. |

**Responses:**

| Status | Meaning | Body |
|--------|---------|------|
| `204 No Content` | Result accepted | _(empty)_ |
| `400 Bad Request` | Invalid JSON or missing `request_id` | `ErrorResponse` |
| `401 Unauthorized` | Missing or invalid API key | `ErrorResponse` |
| `404 Not Found` | Request ID unknown or expired | `ErrorResponse` |
| `429 Too Many Requests` | Rate limited or IP locked | `ErrorResponse` |

`404` means the request TTL elapsed before the result was pushed. The result is discarded. The online server's long-poll has already returned a 404 to the end user.

**Example:**

```bash
curl -s -X POST http://localhost:8080/queue/result \
  -H "X-Local-API-Key: local-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "01960000-0000-7000-8000-000000000001",
    "payload": {"answer": 42},
    "status_code": 200
  }'
```

→ `204 No Content`

---

## Admin Endpoints

---

### GET /debug/stats

Returns a detailed real-time snapshot of all observable hub-router state. Used by the built-in terminal dashboard (`-tui` flag) but also queryable directly.

**Headers:** `X-Admin-API-Key: <key>` (only if `HR_ADMIN_API_KEY` is configured)

**Response:**

```json
{
  "uptime_seconds": 3600,
  "queue": {
    "depth": 5,
    "capacity": 10000,
    "enqueued_total": 1234,
    "dequeued_total": 1190,
    "expired_total": 3,
    "dropped_total": 0
  },
  "store": {
    "results": 12,
    "active_waiters": 4
  },
  "security": {
    "rate_limit_enabled": true,
    "tracked_ips": 15,
    "throttled_ips": 2,
    "locked_ips": 1,
    "watched_ips": 3
  }
}
```

All counters (`enqueued_total`, `dequeued_total`, etc.) are cumulative since server start. `depth` and `active_waiters` are instantaneous values.

---

### GET /health

Returns the current health of hub-router. No authentication required (accessible to load balancers and orchestrators).

**Response:**

```json
{
  "status": "ok",
  "queue_depth": 3,
  "uptime_seconds": 86400
}
```

Used by the Docker `HEALTHCHECK` via the built-in `-healthcheck` flag.

---

### GET /metrics

Prometheus text-format metrics. Authentication required if `HR_ADMIN_API_KEY` is set.

**Headers:** `X-Admin-API-Key: <key>` (only if `HR_ADMIN_API_KEY` configured)

**Response:** Prometheus text format (`text/plain; version=0.0.4`).

```
# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 14
...
```

---

## Data Schemas

### SubmitResponse

Returned by `POST /request` (202) and `POST /request/sync` (202).

```json
{
  "id": "01960000-0000-7000-8000-000000000001",
  "estimated_wait_ms": 200
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | UUIDv7 correlation ID. Use this to poll `GET /result/{id}`. |
| `estimated_wait_ms` | integer | Rough estimate: `queue_depth × 100 ms`. |

### Result

Returned by `GET /result/{id}` (200) and `POST /request/sync` (200). Also sent by `POST /queue/result`.

```json
{
  "request_id": "01960000-0000-7000-8000-000000000001",
  "payload": {"answer": 42},
  "status_code": 200,
  "error": "",
  "completed_at": "2026-03-28T12:00:00.456Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `request_id` | string | Echoes the correlation ID. |
| `payload` | any JSON | Result data from the local server. |
| `status_code` | integer | Application-level status (200 = success, 500 = error, etc.). |
| `error` | string | Non-empty on processor error. |
| `completed_at` | datetime | When the local server finished processing. |

### QueuedRequest

Returned inside `PullResponse`.

```json
{
  "id": "01960000-0000-7000-8000-000000000001",
  "payload": {"query": "hello world"},
  "headers": {"X-User-Id": "u123", "Content-Type": "application/json"},
  "enqueued_at": "2026-03-28T12:00:00.000Z",
  "expires_at":  "2026-03-28T12:05:00.000Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | UUIDv7 — use as `request_id` when pushing result. |
| `payload` | any JSON | The original payload from the online server. |
| `headers` | object | Headers forwarded by the online server (or from `doRequest`). |
| `enqueued_at` | datetime | When the request entered the queue. |
| `expires_at` | datetime | After this time the request is silently dropped. |

### ErrorResponse

Returned on all 4xx and 5xx responses.

```json
{
  "code": "QUEUE_FULL",
  "message": "queue is at capacity",
  "request_id": "01960000-0000-7000-8000-000000000001"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `code` | string | Machine-readable error code (see table below). |
| `message` | string | Human-readable description. |
| `request_id` | string | Present only on request-specific errors. |

**Error codes:**

| Code | HTTP Status | Cause |
|------|-------------|-------|
| `INVALID_BODY` | 400 | Malformed JSON or wrong Content-Type. |
| `MISSING_PAYLOAD` | 400 | `payload` field absent in submit request. |
| `MISSING_ID` | 400 | Path `{id}` is blank. |
| `MISSING_REQUEST_ID` | 400 | `request_id` absent in result push. |
| `UNAUTHORIZED` | 401 | API key missing or invalid. |
| `PAYLOAD_TOO_LARGE` | 413 | Body exceeds `HR_MAX_BODY_BYTES`. |
| `RATE_LIMITED` | 429 | Per-IP token bucket exhausted. |
| `IP_LOCKED` | 429 | Too many consecutive auth failures for this IP. |
| `NOT_FOUND` | 404 | Request ID unknown, expired, or already delivered. |
| `QUEUE_FULL` | 503 | Queue at `HR_QUEUE_MAX_SIZE` capacity. |
| `INTERNAL_ERROR` | 500 | Unexpected server error. |

---

## Response Headers

Every response includes the following security headers:

| Header | Value |
|--------|-------|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Content-Security-Policy` | `default-src 'none'` |
| `Cache-Control` | `no-store` |
| `Server` | `hub-router` |

Rate-limited responses additionally include:

| Header | Description |
|--------|-------------|
| `Retry-After` | Seconds until the client may retry. |
