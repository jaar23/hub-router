# Python SDK

**File:** [`sdks/python/hub_router.py`](../sdks/python/hub_router.py)

Single-file, zero external dependencies. Requires **Python 3.9+**. Uses `asyncio.to_thread` + `urllib` for non-blocking HTTP — no `httpx`, no `aiohttp`, no `pip install`.

Alternatively, the full package (`sdks/python/hub_router/`) is available if you prefer a traditional package install.

---

## Setup

Copy `hub_router.py` into your project:

```bash
cp sdks/python/hub_router.py ./hub_router.py
```

Import it directly — no installation step:

```python
from hub_router import OnlineClient, LocalClient, QueuedRequest, Result
```

---

## OnlineClient

Use on the **web/API server side** to submit requests and wait for results.

### Create a client

```python
from hub_router import OnlineClient

client = OnlineClient(
    "http://hub-router:8080",
    api_key="online-api-key",
    long_poll_timeout=30.0,  # seconds (default)
    max_retries=10,           # max 204 responses (default)
)
```

### do — submit and wait (recommended)

`do` is the primary method. It tries the sync fast-path first and falls back to async polling transparently:

```python
result = await client.do({"query": "what is the capital of France?"})
print(result.payload)   # {"capital": "Paris"}
print(result.status_code)  # 200
```

With headers and a routing key:

```python
result = await client.do(
    payload={"query": "hello"},
    headers={"X-User-Id": "u123", "X-Tenant": "acme"},
    key="gpu",  # route to the "gpu" queue
)
```

#### Result helpers

```python
# is_error() — True when status_code >= 400 or error is non-empty
if result.is_error():
    raise result.err()  # returns an Exception, or None if no error

# err() — returns an Exception describing the failure, or None
exc = result.err()

# unmarshal() — return the payload as a Python object (already JSON-decoded)
data = result.unmarshal()

# unmarshal(cls) — instantiate a dataclass or namedtuple from the payload dict
@dataclass
class Answer:
    capital: str

answer = result.unmarshal(Answer)
print(answer.capital)  # Paris
```

### do_request — forward an incoming HTTP request

Accepts a **FastAPI or Starlette `Request`** object. The original headers and body are forwarded verbatim (hop-by-hop headers excluded):

```python
from fastapi import FastAPI, Request
from hub_router import OnlineClient

app = FastAPI()
client = OnlineClient("http://hub-router:8080", api_key="online-secret")

@app.post("/infer")
async def infer(request: Request):
    result = await client.do_request(request)
    return result.payload
```

Works with any request object that has:
- `.headers` — dict-like (`items()` method)
- `.body()` — async coroutine returning `bytes` (Starlette/FastAPI pattern)

### do_sync — explicit fast-path

```python
result, request_id = await client.do_sync({"query": "hello"})

if result is not None:
    print("fast path:", result.payload)
else:
    # slow path — local server is busy
    result = await client.wait_result(request_id)
    print("async path:", result.payload)
```

### submit + wait_result — manual async flow

```python
request_id = await client.submit(
    payload={"query": "hello"},
    headers={"X-User-Id": "u123"},
)

# ... store request_id somewhere ...

# Later (can be a different coroutine):
result = await client.wait_result(request_id)
```

### OnlineClient options

| Parameter | Default | Description |
|-----------|---------|-------------|
| `long_poll_timeout` | `30.0` | Per-poll wait in seconds for `wait_result` and `do_sync`. |
| `max_retries` | `10` | Max 204 (not-ready) responses before `wait_result` raises `ResultTimeoutError`. |

### Exceptions

| Exception | When raised |
|-----------|-------------|
| `QueueFullError` | Queue at capacity (503) |
| `RequestNotFoundError` | ID unknown or expired (404) |
| `ResultTimeoutError` | `max_retries` exhausted |
| `HubRouterError` | Any other HTTP error |

---

## LocalClient

Use on the **local processing server** to pull work and push results back.

### Create a client

```python
from hub_router import LocalClient

worker = LocalClient(
    "http://hub-router:8080",
    api_key="local-api-key",
    key="gpu",            # pull only from the "gpu" queue (default: "" → "default")
    batch_size=20,        # requests per pull (default: 10)
    poll_interval=0.5,   # seconds to sleep when queue empty (default: 1.0)
    workers=4,            # concurrent processor coroutines (default: 1)
)
```

### run — start the poll loop

```python
async def process(req: QueuedRequest) -> Result:
    # req.payload is whatever the online server sent
    answer = await my_model.infer(req.payload)
    return Result(
        request_id=req.id,
        payload={"answer": answer},
        status_code=200,
    )

# Block until stopped
await worker.run(process)
```

Run alongside other async tasks (non-blocking):

```python
import asyncio

async def main():
    worker = LocalClient("http://hub:8080", api_key="local-key", workers=4)

    # Fire-and-forget — runs in background
    asyncio.create_task(worker.run(process))

    # Your server keeps running
    await start_web_server()

asyncio.run(main())
```

Stop gracefully:

```python
worker.stop()  # signals the loop to drain and exit
```

### LocalClient options

| Parameter | Default | Description |
|-----------|---------|-------------|
| `key` | `""` (→ `"default"`) | Pull only from the named queue. |
| `batch_size` | `10` | Requests fetched per `GET /queue/pull`. |
| `poll_interval` | `1.0` | Seconds to sleep when queue is empty. |
| `workers` | `1` | Max concurrent `ProcessorFunc` coroutines (asyncio semaphore). |

### Low-level methods

```python
# Pull a batch manually
requests = await worker.pull_batch()  # list[QueuedRequest]

# Push a result manually
await worker.push_result(Result(
    request_id=req.id,
    payload={"answer": 42},
    status_code=200,
))
```

---

## Data Classes

### QueuedRequest

```python
@dataclass
class QueuedRequest:
    id: str                           # UUIDv7 correlation ID
    payload: Any                      # JSON-decoded payload from online server
    key: str                          # routing key (empty = "default")
    headers: dict[str, str]           # forwarded headers
    enqueued_at: datetime | None
    expires_at: datetime | None
```

### Result

```python
@dataclass
class Result:
    request_id: str                   # must match QueuedRequest.id
    payload: Any = None               # JSON-serializable result
    status_code: int = 200
    error: str = ""
    completed_at: datetime | None = None  # auto-set if omitted

    def is_error(self) -> bool: ...
    """True when status_code >= 400 or error is non-empty."""

    def err(self) -> Exception | None: ...
    """Returns an Exception if is_error(), else None."""

    def unmarshal(self, cls=None) -> Any: ...
    """Return the decoded payload. If cls is given, instantiate it with payload dict as kwargs."""
```

---

## Full Example

### FastAPI online server

```python
# online_server.py
import asyncio
import os
from fastapi import FastAPI, Request
from hub_router import OnlineClient

app = FastAPI()
client = OnlineClient(
    os.environ["HUB_URL"],
    api_key=os.environ["ONLINE_API_KEY"],
)

@app.post("/api/infer")
async def infer(request: Request):
    """Forward request to local model server via hub-router."""
    result = await client.do_request(request)
    if result.error:
        return {"error": result.error}, result.status_code
    return result.payload
```

### Async local worker

```python
# local_worker.py
import asyncio
import os
from hub_router import LocalClient, QueuedRequest, Result

worker = LocalClient(
    os.environ["HUB_URL"],
    api_key=os.environ["LOCAL_API_KEY"],
    batch_size=20,
    workers=4,
)

async def process(req: QueuedRequest) -> Result:
    # Access original request headers
    user_id = req.headers.get("x-user-id", "anonymous")
    print(f"Processing request {req.id} from user {user_id}")

    # Your processing logic
    import asyncio
    await asyncio.sleep(0.1)  # simulate work
    answer = f"processed: {req.payload}"

    return Result(
        request_id=req.id,
        payload={"answer": answer, "user": user_id},
        status_code=200,
    )

async def main():
    print("Starting worker...")
    await worker.run(process)

if __name__ == "__main__":
    asyncio.run(main())
```

### Django async view

```python
# views.py
from django.http import JsonResponse
from hub_router import OnlineClient
import asyncio

client = OnlineClient("http://hub:8080", api_key="secret")

async def infer_view(request):
    body = await request.abody()   # Django 4.1+
    headers = {k: v for k, v in request.headers.items()}

    result = await client.do(body.decode(), headers)
    return JsonResponse(result.payload)
```

---

## Error Handling

```python
from hub_router import (
    OnlineClient,
    HubRouterError,
    QueueFullError,
    RequestNotFoundError,
    ResultTimeoutError,
)

client = OnlineClient("http://hub:8080", api_key="secret")

try:
    result = await client.do({"query": "hello"})
except QueueFullError:
    print("Hub-router queue is full — retry later")
except RequestNotFoundError:
    print("Request expired before result was ready")
except ResultTimeoutError:
    print("Local server took too long — increase max_retries")
except HubRouterError as e:
    print(f"Unexpected error: {e}")
```
