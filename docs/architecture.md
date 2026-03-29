# Architecture

## Overview

hub-router sits between two independent systems:

- **Online server** — a web-facing application that receives requests from end users or external clients and needs results from compute-intensive processing.
- **Local server** — a machine (often behind NAT, on-premises, or GPU-equipped) that does the heavy lifting: ML inference, data processing, video encoding, etc.

hub-router never modifies request or result payloads — it is a transparent broker. All business logic lives in your servers.

---

## Data Flow

### Async Flow (two round-trips)

```
[Online Server]           [hub-router]              [Local Server]
       │                       │                          │
       │ POST /request         │                          │
       │──────────────────────>│ enqueue(req)             │
       │ 202 {id, wait_ms}     │                          │
       │<──────────────────────│                          │
       │                       │                          │
       │ GET /result/{id}      │       GET /queue/pull    │
       │──────────────────────>│<─────────────────────────│
       │   [long-poll blocks]  │ [{id, payload, headers}] │
       │                       │─────────────────────────>│
       │                       │         process(req)     │
       │                       │   POST /queue/result     │
       │                       │<─────────────────────────│
       │ 200 {payload, ...}    │                          │
       │<──────────────────────│ wake long-poll waiter    │
```

### Sync Fast Path (single round-trip when local is fast)

```
[Online Server]           [hub-router]              [Local Server]
       │                       │                          │
       │ POST /request/sync    │                          │
       │──────────────────────>│ RegisterPending(id)      │
       │                       │ enqueue(req)             │
       │                       │ store.Get(ctx, id) ─┐    │
       │                       │    [waiting...]     │    │
       │                       │         GET /queue/pull  │
       │                       │<─────────────────────────│
       │                       │──────────────────────────│ process
       │                       │   POST /queue/result     │
       │                       │<─────────────────────────│
       │                       │ waiter wakes ───────┘    │
       │ 200 {result payload}  │                          │
       │<──────────────────────│                          │
       │
       │  (if local server slow / unavailable)
       │ 202 {id, wait_ms}    ← timeout reached
       │<──────────────────────│
       │  → caller continues with GET /result/{id}
```

The sync endpoint calls `RegisterPending` **before** `Enqueue`. This ordering guarantees that even if the local server processes the request before the waiting goroutine reaches `store.Get`, the result is never lost — `Get` re-checks the results map after registering its waiter channel.

---

## Component Map

```
cmd/hub-router/main.go
│
└── internal/server/server.go          ← middleware chain + route registration
    ├── middleware/secureheaders.go     ← HSTS, CSP, X-Frame-Options, …
    ├── middleware/ratelimit.go         ← per-IP token-bucket limiter
    ├── middleware/bodylimit.go         ← max body size enforcement
    ├── middleware/auth.go              ← API key validation (timing-safe)
    ├── middleware/lockout.go           ← per-IP brute-force lockout
    │
    ├── handler/online.go              ← POST /request, POST /request/sync, GET /result/{id}
    └── handler/local.go               ← GET /queue/pull, POST /queue/result
        │
        ├── internal/queue/keyed.go    ← KeyedQueue: one MemoryQueue per routing key
        ├── internal/queue/memory.go   ← buffered-channel FIFO queue with TTL
        └── internal/store/memory.go   ← mutex-protected result store, long-poll channels
```

---

## Key-Based Routing

`KeyedQueue` (in `internal/queue/keyed.go`) wraps a map of `string → *MemoryQueue`. Each routing key gets its own independent FIFO channel with the same `maxSize` cap.

- The `"default"` key queue is pre-created at startup so it is always present in `/debug/stats`.
- Additional per-key queues are created on the first `Enqueue` call for that key (double-checked locking).
- `Enqueue(ctx, key, req)` and `Dequeue(ctx, key, n)` normalise an empty key to `"default"`.
- `StatsAll()` returns a `map[string]QueueStats` snapshot — one entry per active key.
- Workers isolate their work by passing `?key=K` to `GET /queue/pull`; they only receive requests enqueued under that key.

This design means that a spike in `"gpu"` requests never causes HOL-blocking for `"cpu"` workers, and each key's depth, throughput, and drop counters are tracked independently.

---

## Request Lifecycle

### Enqueue phase (online side)
1. Middleware chain validates the request (auth, rate limit, body size).
2. Handler parses JSON body into `SubmitRequest{payload, headers, key}`.
3. A **UUIDv7** correlation ID is generated (time-sortable, globally unique).
4. A `QueuedRequest` is built with `EnqueuedAt = now`, `ExpiresAt = now + HR_REQUEST_TTL`.
5. For sync requests: `store.RegisterPending(id)` is called first.
6. `queue.Enqueue(ctx, key, req)` routes the request to the per-key `MemoryQueue` (empty key → `"default"`). If the channel is full, `503 QUEUE_FULL` is returned immediately.

### Poll phase (local side)
1. The local server calls `GET /queue/pull?batch=N&key=K` in a tight loop.
2. The `key` query parameter selects the per-key queue (empty or absent → `"default"`).
3. The handler drains up to `N` items from that queue's channel in a non-blocking loop.
4. Expired items (where `time.Now() > ExpiresAt`) are silently discarded during dequeue.
5. Remaining items are returned as a JSON array.

### Result phase (local → online)
1. Local server posts `POST /queue/result` with the `Result` JSON.
2. Handler calls `store.Put(result)` which:
   a. Writes the result to the results map.
   b. Signals all waiter channels registered for that ID.
3. Any goroutine in `store.Get(ctx, id)` wakes up and reads the result.

### Long-poll (`store.Get`) implementation
```
Get(ctx, id):
  1. Lock mutex; check results map → if found, return immediately.
  2. Create waiter channel; register it under id.
  3. Unlock mutex.
  4. select { case <-waiter; case <-ctx.Done() }
  5. Lock mutex; re-check results map (double-check eliminates lost-wakeup race).
  6. Return result or ErrNotFound.
```

The double-check in step 5 handles the race where `Put` runs between steps 1 and 3.

---

## Concurrency Model

| Component | Thread-safety mechanism |
|-----------|------------------------|
| `KeyedQueue` | `sync.RWMutex` guards the key→queue map; per-key queues created with double-checked locking |
| `MemoryQueue` | Buffered `chan *QueuedRequest` — channel operations are inherently safe |
| `MemoryStore` | `sync.RWMutex` — short critical sections; waiters registered under lock |
| `RateLimiter` | `sync.Mutex` per IP bucket; lazy cleanup goroutine |
| `AuthLockout` | `sync.Mutex` per IP record; sliding window reset |
| HTTP handlers | Stateless; shared state only via queue/store interfaces |

Background goroutines (started at server init, stopped via `context.WithCancel`):
- `store.sweepLoop` — evicts expired results every minute
- `rateLimiter.cleanupLoop` — evicts stale per-IP buckets
- `lockout.cleanupLoop` — evicts stale per-IP lockout records

---

## In-Memory Only

hub-router deliberately uses **no external storage**. All state (queue, results, waiter channels) lives in RAM. Consequences:

- **Restart loses pending requests** — submit a new request after restart.
- **No cross-instance sharing** — run one hub-router instance per isolated pipeline. For multi-instance HA, use a load balancer with sticky sessions or run separate hub-router instances per local server.
- **Size limits matter** — tune `HR_QUEUE_MAX_SIZE`, `HR_REQUEST_TTL`, and `HR_RESULT_TTL` to match your workload and available memory.

---

## Payload and Headers

hub-router treats `payload` as opaque JSON. It never reads, validates, or transforms the payload contents. The local server receives exactly what the online server sent.

The `headers` field in `QueuedRequest` carries metadata from the original request. When using `DoRequest` / `do_request` / `doRequest`, these are the verbatim HTTP headers from the incoming request, minus hop-by-hop headers (`Content-Length`, `Host`, `Transfer-Encoding`, `Connection`, `Keep-Alive`, `Proxy-*`, `Te`, `Trailers`, `Upgrade`).

This lets the local server make auth decisions, routing decisions, or logging based on the original caller's headers (`Authorization`, `X-User-Id`, `Content-Type`, etc.).

---

## Timeout Relationships

```
HR_LONGPOLL_TIMEOUT (30s default)
  │
  ├── GET /result/{id}?timeout=30s   — client waits up to this long per poll
  ├── POST /request/sync?timeout=30s — server waits up to this long for result
  │
  └── Must be < HR_WRITE_TIMEOUT (35s default)
          The HTTP write deadline must exceed the long-poll timeout to avoid
          the server closing the TCP connection while still waiting.
```

When tuning, always ensure: `HR_LONGPOLL_TIMEOUT < HR_WRITE_TIMEOUT`.
