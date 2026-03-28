# hub-router

A lightweight, high-performance middleware that bridges an **online web server** and a **local processing server** through a secure async queue and long-polling. Drop it between your web app and your GPU/CPU-bound worker — no message broker required.

```
[Online Server]          [hub-router]          [Local Server]
   POST /request  ──→   enqueue(req)
   GET /result/id ──→   store.Get()  ←──────── GET /queue/pull
                                      ───────→  process(req)
   ←── 200 result ←──   wake waiter  ←──────── POST /queue/result
```

Or using the **sync fast path** (single round-trip when local is fast):

```
[Online Server]          [hub-router]          [Local Server]
   POST /request/sync ─→ enqueue + wait ←────── GET /queue/pull
   ←── 200 result     ←─ result arrived ←─────  POST /queue/result
   ←── 202 + id       ←─ (if timeout: fall back to async polling)
```

---

## Features

- **Two delivery modes** — async queue+poll, or sync fast-path that returns immediately like a normal HTTP proxy
- **Zero external dependencies** — in-memory queue and result store; no Redis, no Kafka
- **API key auth** — separate key sets for online and local servers, timing-safe comparison
- **Brute-force protection** — per-IP rate limiting (token bucket) and auth-failure lockout
- **Request pass-through** — forward exact headers and body from incoming requests unchanged
- **SDKs for Go, Python, Node.js** — single-file, copy-and-use, no package manager needed
- **Prometheus metrics** — built-in `/metrics` endpoint
- **Production-ready Docker** — distroless image, non-root, read-only filesystem

---

## Quick Start

### Run with Docker

```bash
docker run -p 8080:8080 \
  -e HR_ONLINE_API_KEYS=online-secret \
  -e HR_LOCAL_API_KEYS=local-secret \
  hub-router:latest
```

### Or with docker-compose

```bash
cp deploy/.env.example deploy/.env
# Edit deploy/.env — set HR_ONLINE_API_KEYS and HR_LOCAL_API_KEYS
docker-compose -f deploy/docker-compose.yml up -d
```

### Build and run locally

```bash
HR_ONLINE_API_KEYS=online-secret HR_LOCAL_API_KEYS=local-secret go run ./cmd/hub-router
# or
make run
```

---

## Integration in 60 seconds

### Online server (submits work, waits for results)

**Go** — copy [`sdks/go/hub_router.go`](sdks/go/hub_router.go) into your project:
```go
client := hubrouter.NewOnlineClient("http://hub:8080", "online-secret")

// Submit a payload and wait for the result (auto fast-path → long-poll)
result, err := client.Do(ctx, map[string]any{"query": "hello"}, nil)

// Or forward an incoming HTTP request verbatim
result, err := client.DoRequest(ctx, r)
```

**Python** — copy [`sdks/python/hub_router.py`](sdks/python/hub_router.py) into your project:
```python
client = OnlineClient("http://hub:8080", api_key="online-secret")

result = await client.do({"query": "hello"})

# Or forward an incoming request verbatim (FastAPI/Starlette)
result = await client.do_request(request)
```

**Node.js** — copy [`sdks/node/src/hub-router.ts`](sdks/node/src/hub-router.ts) into your project:
```typescript
const client = new OnlineClient("http://hub:8080", "online-secret");

const result = await client.do({ query: "hello" });

// Or forward an incoming request verbatim
const result = await client.doRequest(req);
```

### Local server (polls for work, returns results)

**Go:**
```go
worker := hubrouter.NewLocalClient("http://hub:8080", "local-secret",
    hubrouter.WithWorkers(4), hubrouter.WithBatchSize(20))

worker.Run(ctx, func(ctx context.Context, req *hubrouter.QueuedRequest) (*hubrouter.Result, error) {
    output := myModel.Infer(req.Payload)
    return &hubrouter.Result{RequestID: req.ID, Payload: output, StatusCode: 200}, nil
})
```

**Python:**
```python
worker = LocalClient("http://hub:8080", api_key="local-secret", workers=4)

async def process(req: QueuedRequest) -> Result:
    output = await my_model.infer(req.payload)
    return Result(request_id=req.id, payload=output)

await worker.run(process)
```

**Node.js:**
```typescript
const worker = new LocalClient("http://hub:8080", "local-secret", { workers: 4 });

worker.run(async (req) => {
    const output = await myModel.infer(req.payload);
    return { request_id: req.id, payload: output, status_code: 200 };
});
```

---

## Documentation

| Topic | Description |
|-------|-------------|
| [Architecture](docs/architecture.md) | System design, data flow, concurrency model |
| [API Reference](docs/api-reference.md) | All HTTP endpoints, request/response schemas |
| [Configuration](docs/configuration.md) | All `HR_*` environment variables |
| [SDK — Go](docs/sdk-go.md) | Go client (module or single-file) |
| [SDK — Python](docs/sdk-python.md) | Python async client |
| [SDK — Node.js](docs/sdk-node.md) | TypeScript/JavaScript client |
| [Deployment](docs/deployment.md) | Docker, docker-compose, production checklist |
| [Security](docs/security.md) | Rate limiting, lockout, hardening details |
| [Observability](docs/observability.md) | Terminal dashboard, stats endpoint, Prometheus metrics |

The raw HTTP API is also described in [`api/openapi.yaml`](api/openapi.yaml).

---

## Project Structure

```
hub-router/
├── cmd/hub-router/       # main entry point
├── internal/
│   ├── config/           # environment-based configuration
│   ├── handler/          # HTTP handlers (online + local side)
│   ├── middleware/        # auth, rate limit, lockout, body limit, secure headers
│   ├── model/            # shared data structs (QueuedRequest, Result, …)
│   ├── queue/            # in-memory FIFO queue with TTL
│   ├── server/           # route registration, middleware chain
│   └── store/            # race-free long-poll result store
├── pkg/client/           # Go SDK (module-aware, imports internal/model)
├── sdks/
│   ├── go/hub_router.go      # single-file Go SDK (copy into your project)
│   ├── python/hub_router.py  # single-file Python SDK
│   └── node/src/hub-router.ts # single-file TypeScript SDK
├── examples/
│   ├── online-server/    # example online integration
│   └── local-server/     # example local worker
├── deploy/
│   ├── Dockerfile
│   ├── docker-compose.yml
│   └── .env.example
└── api/openapi.yaml      # OpenAPI 3.1 spec
```

---

## Development

```bash
make build        # compile binary
make test         # run tests
make test-race    # run tests with Go race detector
make lint         # run golangci-lint
make docker-build # build Docker image
make docker-up    # start with docker-compose
```

---

## License

MIT
