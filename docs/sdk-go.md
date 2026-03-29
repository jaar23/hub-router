# Go SDK

Two options depending on your setup:

| Option | File | When to use |
|--------|------|-------------|
| **Single-file** | `sdks/go/hub_router.go` | Copy into your project — no `go get` required, stdlib only |
| **Module client** | `pkg/client/` | Use when hub-router is already in your Go module |

Both expose the same API.

---

## Single-File Setup (Recommended)

Copy `sdks/go/hub_router.go` into your project and update the package name:

```bash
cp hub_router.go ./internal/hubrouter/hub_router.go
# or just place it next to your main.go and use the same package
```

Change the first line to match your package:
```go
package main  // or whatever package you need
```

**No `go get` required.** The file uses only the Go standard library.

---

## OnlineClient

Use on the **web/API server side** to submit requests and wait for results.

### Create a client

```go
import "your-project/hubrouter"  // wherever you placed hub_router.go

client := hubrouter.NewOnlineClient(
    "http://hub-router:8080",
    "online-api-key",
    // optional functional options:
    hubrouter.WithLongPollTimeout(15 * time.Second),
    hubrouter.WithMaxRetries(5),
    hubrouter.WithHTTPClient(customHTTPClient),
)
```

### Do — submit and wait (recommended)

`Do` is the primary method. It tries the sync fast-path first and falls back to async polling transparently:

```go
payload := map[string]any{"query": "what is the capital of France?"}
result, err := client.Do(ctx, payload, nil)
if err != nil {
    log.Printf("error: %v", err)
    return
}

// Check for application-level errors
if result.IsError() {
    log.Fatal(result.Err())
}

// Unmarshal the payload into a typed struct
var answer struct{ Capital string }
_ = result.Unmarshal(&answer)
fmt.Println(answer.Capital) // Paris
```

Route to a named queue with `WithKey`:

```go
result, err := client.Do(ctx, payload, nil, hubrouter.WithKey("gpu"))
```

### DoRequest — forward an incoming HTTP request

Pass an incoming `*http.Request` directly. The original headers and body are forwarded verbatim to the local server (hop-by-hop headers excluded):

```go
func handler(w http.ResponseWriter, r *http.Request) {
    result, err := client.DoRequest(r.Context(), r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(result.StatusCode)
    w.Write(result.Payload)
}
```

### DoSync — explicit sync fast-path

```go
result, requestID, err := client.DoSync(ctx, payload, headers)
if err != nil {
    // handle error
}
if result != nil {
    // fast path: result arrived in the same request
    fmt.Println("fast:", result.Payload)
} else {
    // slow path: local server is busy — poll for result
    result, err = client.WaitResult(ctx, requestID)
}
```

### Submit + WaitResult — manual async flow

```go
id, err := client.Submit(ctx, payload, map[string]string{
    "X-User-Id": "u123",
    "Content-Type": "application/json",
})
if err != nil { ... }

// Later (can be in a different goroutine, after storing id):
result, err := client.WaitResult(ctx, id)
```

### OnlineClient options

| Option | Default | Description |
|--------|---------|-------------|
| `WithLongPollTimeout(d)` | `30s` | Per-poll wait duration for `WaitResult` and `DoSync`. |
| `WithMaxRetries(n)` | `10` | Max 204 (not-ready) responses before `WaitResult` returns an error. |
| `WithHTTPClient(hc)` | 60s timeout | Custom `*http.Client` (TLS, proxy, tracing, etc.). |

---

## LocalClient

Use on the **local processing server** to pull work and push results back.

### Create a client

```go
worker := hubrouter.NewLocalClient(
    "http://hub-router:8080",
    "local-api-key",
    hubrouter.WithLocalKey("gpu"),          // pull only from the "gpu" queue
    hubrouter.WithBatchSize(20),
    hubrouter.WithWorkers(4),
    hubrouter.WithPollInterval(500 * time.Millisecond),
)
```

### Run the poll loop

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Handle SIGINT/SIGTERM for graceful shutdown
go func() {
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    <-c
    cancel()
}()

err := worker.Run(ctx, func(ctx context.Context, req *hubrouter.QueuedRequest) (*hubrouter.Result, error) {
    // req.Payload is json.RawMessage
    var input struct{ Query string }
    json.Unmarshal(req.Payload, &input)

    // Do your processing
    answer, err := myModel.Infer(ctx, input.Query)
    if err != nil {
        return nil, err // hub-router will push a 500 result
    }

    output, _ := json.Marshal(map[string]string{"answer": answer})
    return &hubrouter.Result{
        RequestID:  req.ID,
        Payload:    output,
        StatusCode: 200,
    }, nil
})
```

`Run` blocks until `ctx` is cancelled. In-flight processors finish before it returns.

### LocalClient options

| Option | Default | Description |
|--------|---------|-------------|
| `WithLocalKey(k)` | `""` (→ `"default"`) | Pull only from the named queue. |
| `WithBatchSize(n)` | `10` | Requests fetched per `GET /queue/pull` call. |
| `WithWorkers(n)` | `1` | Max concurrent `ProcessorFunc` goroutines. |
| `WithPollInterval(d)` | `1s` | Sleep duration when the queue is empty. |
| `WithLocalHTTPClient(hc)` | 60s timeout | Custom `*http.Client`. |

### Low-level methods

```go
// Pull a batch manually
requests, err := worker.PullBatch(ctx)

// Push a result manually
err = worker.PushResult(ctx, &hubrouter.Result{
    RequestID:  req.ID,
    Payload:    output,
    StatusCode: 200,
})
```

---

## Data Types

### QueuedRequest

```go
type QueuedRequest struct {
    ID         string            // UUIDv7 correlation ID
    Key        string            // routing key (empty = "default")
    Payload    json.RawMessage   // opaque JSON from online server
    Headers    map[string]string // forwarded headers
    EnqueuedAt time.Time
    ExpiresAt  time.Time
}
```

### Result

```go
type Result struct {
    RequestID   string          // must match QueuedRequest.ID
    Payload     json.RawMessage // any JSON to return to online server
    StatusCode  int             // 200 = success, 500 = error, etc.
    Error       string          // non-empty on failure
    CompletedAt time.Time       // auto-set by SDK if zero
}
```

### Result helper methods

```go
// IsError returns true when StatusCode >= 400 or Error is non-empty.
func (r *Result) IsError() bool

// Err returns a non-nil error when IsError() is true, nil otherwise.
func (r *Result) Err() error

// Unmarshal JSON-decodes the Payload into v.
func (r *Result) Unmarshal(v any) error
```

**Usage:**

```go
result, err := client.Do(ctx, payload, nil, hubrouter.WithKey("gpu"))
if err != nil {
    return err  // network / hub-router error
}
if result.IsError() {
    return result.Err()  // application-level error from local server
}

var out MyOutput
if err := result.Unmarshal(&out); err != nil {
    return fmt.Errorf("decode result: %w", err)
}
```

---

## Full Example

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    hr "myapp/hubrouter" // or wherever you placed hub_router.go
)

func main() {
    // ── Local server ───────────────────────────────────────────────────────
    worker := hr.NewLocalClient(
        "http://hub-router:8080",
        os.Getenv("LOCAL_API_KEY"),
        hr.WithBatchSize(10),
        hr.WithWorkers(4),
    )

    ctx, cancel := context.WithCancel(context.Background())
    go func() {
        sig := make(chan os.Signal, 1)
        signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
        <-sig
        fmt.Println("shutting down...")
        cancel()
    }()

    // Run poll loop in background
    go func() {
        err := worker.Run(ctx, process)
        if err != nil && err != context.Canceled {
            log.Fatalf("worker error: %v", err)
        }
    }()

    // ── Online server ──────────────────────────────────────────────────────
    client := hr.NewOnlineClient(
        "http://hub-router:8080",
        os.Getenv("ONLINE_API_KEY"),
    )

    http.HandleFunc("/infer", func(w http.ResponseWriter, r *http.Request) {
        result, err := client.DoRequest(r.Context(), r)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadGateway)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.Write(result.Payload)
    })

    log.Fatal(http.ListenAndServe(":3000", nil))
}

func process(ctx context.Context, req *hr.QueuedRequest) (*hr.Result, error) {
    // Simulate work
    time.Sleep(50 * time.Millisecond)

    output, _ := json.Marshal(map[string]int{"answer": 42})
    return &hr.Result{
        RequestID:  req.ID,
        Payload:    output,
        StatusCode: 200,
    }, nil
}
```
