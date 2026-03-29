# Node.js / TypeScript SDK

**File:** [`sdks/node/src/hub-router.ts`](../sdks/node/src/hub-router.ts)

Single-file TypeScript client. No `npm install` required. Uses the native **Web Fetch API** — available in Node.js 18+, Deno, Cloudflare Workers, and modern browsers.

---

## Setup

### Option 1 — Copy the single file (zero dependencies)

```bash
cp sdks/node/src/hub-router.ts ./src/hub-router.ts
```

Import directly:

```typescript
import { OnlineClient, LocalClient } from "./hub-router.js";
```

No `package.json` changes needed. No `npm install`. TypeScript types are inlined.

### Option 2 — npm package

If hub-router is in your monorepo or installed as a package:

```typescript
import { OnlineClient, LocalClient } from "hub-router";
```

---

## OnlineClient

Use on the **web/API server side** to submit requests and wait for results.

### Create a client

```typescript
import { OnlineClient } from "./hub-router.js";

const client = new OnlineClient(
  "http://hub-router:8080",
  "online-api-key",
  {
    longPollTimeout: 30_000, // ms (default)
    maxRetries: 10,          // max 204 responses (default)
  }
);
```

### do — submit and wait (recommended)

`do` is the primary method. Tries the sync fast-path first and falls back to async polling transparently:

```typescript
const result = await client.do({ query: "what is the capital of France?" });
console.log(result.payload);     // { capital: "Paris" }
console.log(result.status_code); // 200
```

With headers and a routing key:

```typescript
const result = await client.do(
  { query: "hello" },
  { "X-User-Id": "u123", "X-Tenant": "acme" },
  { key: "gpu" }  // route to the "gpu" queue
);
```

#### Result helpers

```typescript
// isError() — true when status_code >= 400 or error is non-empty
if (result.isError()) {
  throw result.err();  // returns an Error, or null if no error
}

// err() — returns an Error describing the failure, or null
const err = result.err();

// unmarshal<T>() — cast/parse the payload to a typed value
interface Answer { capital: string }
const data = result.unmarshal<Answer>();
console.log(data.capital); // Paris
```

### doRequest — forward an incoming HTTP request

Accepts a Web API **`Request`** object. The original headers and body are forwarded verbatim (hop-by-hop headers excluded):

```typescript
// Express.js
import express from "express";
const app = express();

app.post("/api/infer", async (req, res) => {
  // Wrap Node.js IncomingMessage as Web API Request
  const webReq = new Request(`http://localhost${req.url}`, {
    method: req.method,
    headers: req.headers as HeadersInit,
    body: req,
    duplex: "half",
  } as RequestInit);

  const result = await client.doRequest(webReq);
  res.status(result.status_code).json(result.payload);
});
```

```typescript
// Hono / Bun / Cloudflare Workers — native Request available
app.post("/api/infer", async (c) => {
  const result = await client.doRequest(c.req.raw);
  return c.json(result.payload, result.status_code);
});
```

### doSync — explicit fast-path

```typescript
const response = await client.doSync({ query: "hello" });

if ("result" in response) {
  // Fast path: result arrived in the same request
  console.log("fast:", response.result.payload);
} else {
  // Slow path: local server is busy
  const result = await client.waitResult(response.requestId);
  console.log("async:", result.payload);
}
```

### submit + waitResult — manual async flow

```typescript
const requestId = await client.submit(
  { query: "hello" },
  { "X-User-Id": "u123" }
);

// ... store requestId somewhere ...

// Later:
const result = await client.waitResult(requestId);
```

### OnlineClient options

| Option | Default | Description |
|--------|---------|-------------|
| `longPollTimeout` | `30_000` | Per-poll wait in ms for `waitResult` and `doSync`. |
| `maxRetries` | `10` | Max 204 (not-ready) responses before `waitResult` throws `ResultTimeoutError`. |

### Errors

| Class | When thrown |
|-------|-------------|
| `QueueFullError` | Queue at capacity (503) |
| `RequestNotFoundError` | ID unknown or expired (404) |
| `ResultTimeoutError` | `maxRetries` exhausted |
| `HubRouterError` | Any other HTTP error (base class) |

---

## LocalClient

Use on the **local processing server** to pull work and push results back.

### Create a client

```typescript
import { LocalClient } from "./hub-router.js";

const worker = new LocalClient(
  "http://hub-router:8080",
  "local-api-key",
  {
    key: "gpu",         // pull only from the "gpu" queue (default: "" → "default")
    batchSize: 20,      // requests per pull (default: 10)
    pollInterval: 500,  // ms to sleep when queue empty (default: 1000)
    workers: 4,         // concurrent processor callbacks (default: 1)
  }
);
```

### run — start the poll loop

```typescript
worker.run(async (req) => {
  // req.payload is whatever the online server sent
  const answer = await myModel.infer(req.payload);
  return {
    request_id: req.id,
    payload: { answer },
    status_code: 200,
  };
});
// Note: no await — runs in background alongside your server
```

Run as the sole task (blocking):

```typescript
await worker.run(processor); // blocks until stop() is called
```

Stop gracefully:

```typescript
worker.stop(); // drains in-flight requests, then run() resolves
```

### LocalClient options

| Option | Default | Description |
|--------|---------|-------------|
| `key` | `""` (→ `"default"`) | Pull only from the named queue. |
| `batchSize` | `10` | Requests fetched per `GET /queue/pull`. |
| `pollInterval` | `1000` | Milliseconds to sleep when queue is empty. |
| `workers` | `1` | Max concurrent processor calls (integer semaphore). |

### Low-level methods

```typescript
// Pull a batch manually
const requests = await worker.pullBatch(); // QueuedRequest[]

// Push a result manually
await worker.pushResult({
  request_id: req.id,
  payload: { answer: 42 },
  status_code: 200,
});
```

---

## TypeScript Types

```typescript
interface QueuedRequest {
  id: string;                          // UUIDv7 correlation ID
  key?: string;                        // routing key (absent = "default")
  payload: unknown;                    // JSON from online server
  headers: Record<string, string>;     // forwarded headers
  enqueued_at: string;                 // ISO 8601
  expires_at: string;                  // ISO 8601
}

// Result is a class — use new Result(data) or receive from client.do()
class Result {
  request_id: string;
  payload: unknown;                    // JSON result from local server
  status_code: number;
  error?: string;
  completed_at?: string;               // ISO 8601

  isError(): boolean;                  // true if status_code >= 400 or error non-empty
  err(): Error | null;                 // Error when isError(), else null
  unmarshal<T>(): T;                   // cast payload to T (already JSON-parsed)
}

interface ResultData {                 // plain object shape accepted by pushResult()
  request_id: string;
  payload: unknown;
  status_code: number;
  error?: string;
  completed_at?: string;
}

type ProcessorFn = (req: QueuedRequest) => Promise<ResultData>;
```

---

## Full Examples

### Express.js online server

```typescript
// online-server.ts
import express from "express";
import { OnlineClient } from "./hub-router.js";

const app = express();
const client = new OnlineClient(
  process.env.HUB_URL!,
  process.env.ONLINE_API_KEY!
);

app.post("/api/infer", express.json(), async (req, res) => {
  try {
    // Forward payload + headers from incoming request
    const result = await client.do(req.body, {
      "x-user-id": req.headers["x-user-id"] as string ?? "",
    });
    res.status(result.status_code).json(result.payload);
  } catch (err) {
    res.status(502).json({ error: String(err) });
  }
});

app.listen(3000, () => console.log("Online server on :3000"));
```

### Local worker server

```typescript
// local-worker.ts
import { LocalClient, QueuedRequest, Result } from "./hub-router.js";

const worker = new LocalClient(
  process.env.HUB_URL!,
  process.env.LOCAL_API_KEY!,
  { workers: 4, batchSize: 20 }
);

async function process(req: QueuedRequest): Promise<Result> {
  const userId = req.headers["x-user-id"] ?? "anonymous";
  console.log(`Processing ${req.id} from user ${userId}`);

  // Simulate work
  await new Promise(r => setTimeout(r, 100));

  return {
    request_id: req.id,
    payload: { answer: 42, user: userId },
    status_code: 200,
  };
}

console.log("Worker starting...");
worker.run(process); // fire-and-forget

// Graceful shutdown
process.on("SIGTERM", () => {
  worker.stop();
});
```

### Hono (Cloudflare Workers / Bun)

```typescript
// worker.ts
import { Hono } from "hono";
import { OnlineClient } from "./hub-router.js";

const app = new Hono();
const client = new OnlineClient(
  "https://hub.example.com",
  "online-api-key"
);

app.post("/infer", async (c) => {
  // doRequest accepts the native Web API Request — zero adaptation needed
  const result = await client.doRequest(c.req.raw);
  return c.json(result.payload, result.status_code as StatusCode);
});

export default app;
```

---

## Error Handling

```typescript
import {
  OnlineClient,
  HubRouterError,
  QueueFullError,
  RequestNotFoundError,
  ResultTimeoutError,
} from "./hub-router.js";

const client = new OnlineClient("http://hub:8080", "secret");

try {
  const result = await client.do({ query: "hello" });
  console.log(result.payload);
} catch (err) {
  if (err instanceof QueueFullError) {
    console.error("Queue is full — retry later");
  } else if (err instanceof RequestNotFoundError) {
    console.error("Request expired");
  } else if (err instanceof ResultTimeoutError) {
    console.error("Local server timed out — increase maxRetries");
  } else if (err instanceof HubRouterError) {
    console.error("Hub-router error:", err.message);
  } else {
    throw err;
  }
}
```

---

## Using as JavaScript (no TypeScript)

Compile `hub-router.ts` once with `tsc` or use a bundler, then import the `.js` output:

```bash
npx tsc sdks/node/src/hub-router.ts --target ES2020 --module ESNext --outDir ./dist
```

Or use it directly in Bun which runs TypeScript natively:

```bash
bun run server.ts  # imports hub-router.ts directly
```
